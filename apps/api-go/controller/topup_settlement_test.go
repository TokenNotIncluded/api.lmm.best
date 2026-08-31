package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
	"gorm.io/gorm"
)

func TestEPayLoggingDoesNotSerializeSensitiveRequestOrCallbackData(t *testing.T) {
	source, err := os.ReadFile("topup.go")
	require.NoError(t, err)
	text := string(source)
	for _, forbidden := range []string{
		"c.Request.RequestURI",
		"common.GetJsonString(params)",
		"common.GetJsonString(verifyInfo)",
		"common.GetJsonString(completed)",
		"params=%q",
		"verify_info=%q",
		"topup=%q",
	} {
		assert.NotContains(t, text, forbidden)
	}
}

func TestTopUpOrderAmountsPreservesRequestedTokenQuota(t *testing.T) {
	previousDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	previousQuotaPerUnit := common.QuotaPerUnit
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() {
		operation_setting.GetGeneralSetting().QuotaDisplayType = previousDisplayType
		common.QuotaPerUnit = previousQuotaPerUnit
	})

	storedAmount, creditedQuota := topUpOrderAmounts(1_250_000)
	assert.EqualValues(t, 2, storedAmount)
	assert.EqualValues(t, 1_250_000, creditedQuota)
}

func TestTopUpOrderAmountsUsesPlatformUnitsOutsideTokenMode(t *testing.T) {
	previousDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	previousQuotaPerUnit := common.QuotaPerUnit
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() {
		operation_setting.GetGeneralSetting().QuotaDisplayType = previousDisplayType
		common.QuotaPerUnit = previousQuotaPerUnit
	})

	storedAmount, creditedQuota := topUpOrderAmounts(25)
	assert.EqualValues(t, 25, storedAmount)
	assert.EqualValues(t, 12_500_000, creditedQuota)
}

func TestTopUpOrderAmountsRejectsInt64OverflowInTokenConversion(t *testing.T) {
	previousDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	previousQuotaPerUnit := common.QuotaPerUnit
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	common.QuotaPerUnit = math.SmallestNonzeroFloat64
	t.Cleanup(func() {
		operation_setting.GetGeneralSetting().QuotaDisplayType = previousDisplayType
		common.QuotaPerUnit = previousQuotaPerUnit
	})

	storedAmount, creditedQuota := topUpOrderAmounts(math.MaxInt64)
	assert.Zero(t, storedAmount)
	assert.Zero(t, creditedQuota)
}

func TestMonetaryMicrosConversionsAreExact(t *testing.T) {
	micros, err := monetaryStringToMicros("12.340001")
	require.NoError(t, err)
	assert.EqualValues(t, 12_340_001, micros)

	_, err = monetaryStringToMicros("12.3400001")
	require.Error(t, err)

	micros, err = minorCurrencyUnitsToMicros(1234, "usd")
	require.NoError(t, err)
	assert.EqualValues(t, 12_340_000, micros)

	micros, err = minorCurrencyUnitsToMicros(1234, "jpy")
	require.NoError(t, err)
	assert.EqualValues(t, 1_234_000_000, micros)
}

func TestStripeQuoteAndCheckoutLineItemUseSameCanonicalAmount(t *testing.T) {
	previousDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	previousQuotaPerUnit := common.QuotaPerUnit
	previousUnitPrice := setting.StripeUnitPrice
	previousDiscounts := operation_setting.GetPaymentSetting().AmountDiscount
	common.QuotaPerUnit = 500_000
	setting.StripeUnitPrice = 8
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{1_000_000: 0.8}
	t.Cleanup(func() {
		operation_setting.GetGeneralSetting().QuotaDisplayType = previousDisplayType
		common.QuotaPerUnit = previousQuotaPerUnit
		setting.StripeUnitPrice = previousUnitPrice
		operation_setting.GetPaymentSetting().AmountDiscount = previousDiscounts
	})

	for _, displayType := range []string{operation_setting.QuotaDisplayTypeUSD, operation_setting.QuotaDisplayTypeTokens} {
		operation_setting.GetGeneralSetting().QuotaDisplayType = displayType
		amount := int64(2)
		if displayType == operation_setting.QuotaDisplayTypeTokens {
			amount = 1_000_000
		}
		micros, err := stripeTopUpQuoteMicros(amount, "default")
		require.NoError(t, err)
		lineItem, currency, err := stripeTopUpLineItem(&stripe.Price{
			Currency: stripe.CurrencyUSD,
			Product:  &stripe.Product{ID: "prod_canonical"},
		}, micros)
		require.NoError(t, err)
		assert.Equal(t, "USD", currency)
		require.NotNil(t, lineItem.PriceData.UnitAmount)
		assert.EqualValues(t, micros, *lineItem.PriceData.UnitAmount*10_000)
		assert.EqualValues(t, 1, *lineItem.Quantity)
	}
}

func TestTopUpSelfRecordDoesNotExposeSettlementEvidence(t *testing.T) {
	record := newTopUpSelfRecord(&model.TopUp{
		Id: 1, UserId: 2, Amount: 3, Money: 4, TradeNo: "safe", PaymentMethod: "stripe",
		ProviderProductId: "prod_secret", ProviderStoreId: "store_secret",
		ProviderEventId: stringPointer("evt_secret"), ProviderTransactionId: stringPointer("pi_secret"),
		ExpectedAmountMicros: 4_000_000, SettledAmountMicros: 4_000_000, SettlementCurrency: "USD",
	})
	payload, err := json.Marshal(record)
	require.NoError(t, err)
	jsonText := string(payload)
	assert.Contains(t, jsonText, `"currency":"USD"`)
	for _, forbidden := range []string{"credited_quota", "expected_amount_micros", "settled_amount_micros", "settlement_currency", "provider_product_id", "provider_store_id", "provider_event_id", "provider_transaction_id"} {
		assert.NotContains(t, jsonText, forbidden)
	}
}

func TestTopUpAdminRecordProvidesCurrencyAlias(t *testing.T) {
	record := newTopUpAdminRecord(&model.TopUp{
		Id: 1, SettlementCurrency: "USD", ProviderTransactionId: stringPointer("pi_admin"),
	})
	payload, err := json.Marshal(record)
	require.NoError(t, err)
	jsonText := string(payload)
	assert.Contains(t, jsonText, `"currency":"USD"`)
	assert.Contains(t, jsonText, `"settlement_currency":"USD"`)
	assert.Contains(t, jsonText, `"provider_transaction_id":"pi_admin"`)
}

func configureNeutralTopUpInfoTest(t *testing.T) {
	t.Helper()
	preservePaymentGatewaySettings(t)
	paymentSetting := operation_setting.GetPaymentSetting()
	previousConfirmed := paymentSetting.ComplianceConfirmed
	previousTermsVersion := paymentSetting.ComplianceTermsVersion
	previousOptions := paymentSetting.AmountOptions
	previousDiscounts := paymentSetting.AmountDiscount
	previousMinimum := operation_setting.MinTopUp
	t.Cleanup(func() {
		paymentSetting.ComplianceConfirmed = previousConfirmed
		paymentSetting.ComplianceTermsVersion = previousTermsVersion
		paymentSetting.AmountOptions = previousOptions
		paymentSetting.AmountDiscount = previousDiscounts
		operation_setting.MinTopUp = previousMinimum
	})

	paymentSetting.ComplianceConfirmed = true
	paymentSetting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	paymentSetting.AmountOptions = []int{7, 14}
	paymentSetting.AmountDiscount = map[int]float64{14: 0.9}
	operation_setting.MinTopUp = 7
	operation_setting.PayAddress = "https://provider-secret.invalid"
	operation_setting.EpayId = "merchant-secret"
	operation_setting.EpayKey = "key-secret"
	operation_setting.PayMethods = []map[string]string{{
		"name": "LDC", "type": "epay", "product_id": "prod-secret",
	}}
}

func TestGetTopUpInfoReturnsNeutralDataWhenDeveloperAccessIsDenied(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	configureNeutralTopUpInfoTest(t)
	user := model.User{Username: "neutral-topup", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Set("id", user.Id)
	GetTopUpInfo(context)

	var payload struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	assert.Equal(t, false, payload.Data["developer_access_granted"])
	assert.Equal(t, true, payload.Data["activation_required"])
	assert.Equal(t, true, payload.Data["payment_available"])
	assert.Equal(t, true, payload.Data["enable_online_topup"])
	assert.EqualValues(t, 7, payload.Data["min_payment"])
	assert.Contains(t, payload.Data, "pay_methods")
	assert.Equal(t, []any{map[string]any{"name": "LDC", "type": "epay"}}, payload.Data["pay_methods"])
	for _, forbidden := range []string{
		"provider-secret.invalid", "merchant-secret", "key-secret", "prod-secret",
		"topup_group_ratio",
	} {
		assert.NotContains(t, strings.ToLower(response.Body.String()), forbidden)
	}
}

func TestGetTopUpInfoFailsClosedWhenDeveloperAccessCalculationFails(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	configureNeutralTopUpInfoTest(t)
	user := model.User{Username: "failed-topup-access", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Migrator().DropTable(&model.TopUp{}))

	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Set("id", user.Id)
	GetTopUpInfo(context)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	assert.Equal(t, false, payload["success"])
	assert.NotContains(t, payload, "data")
	assert.NotContains(t, strings.ToLower(response.Body.String()), "provider-secret")
	assert.NotContains(t, strings.ToLower(response.Body.String()), "prod-secret")
	assert.NotContains(t, strings.ToLower(response.Body.String()), "epay")
}

func TestGetTopUpInfoPreservesGrantedResponseAndAddsAccessDecision(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	configureNeutralTopUpInfoTest(t)
	levelOne := 1
	user := model.User{Username: "granted-topup", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, TrustLevelOverride: &levelOne}
	require.NoError(t, db.Create(&user).Error)

	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Set("id", user.Id)
	GetTopUpInfo(context)

	var payload struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	assert.Equal(t, true, payload.Data["developer_access_granted"])
	assert.Contains(t, payload.Data, "pay_methods")
	assert.Contains(t, payload.Data, "topup_group_ratio")
}

func TestGetTopUpInfoHidesPaymentMethodsForRestrictedAccount(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	configureNeutralTopUpInfoTest(t)
	levelOne := 1
	user := model.User{
		Username:                "restricted-topup",
		Password:                "password",
		Role:                    common.RoleCommonUser,
		Status:                  common.UserStatusEnabled,
		TrustLevelOverride:      &levelOne,
		PaymentRestrictionFlags: model.PaymentRestrictionLinuxDOHighScore,
	}
	require.NoError(t, db.Create(&user).Error)

	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Set("id", user.Id)
	GetTopUpInfo(context)

	var payload struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	assert.Equal(t, true, payload.Data["developer_access_granted"])
	assert.Equal(t, false, payload.Data["payment_available"])
	assert.Equal(t, false, payload.Data["enable_online_topup"])
	assert.Equal(t, false, payload.Data["enable_stripe_topup"])
	assert.Equal(t, []any{}, payload.Data["pay_methods"])
	assert.Equal(t, []any{}, payload.Data["amount_options"])
	assert.NotContains(t, response.Body.String(), "payment_restriction")
	assert.NotContains(t, response.Body.String(), "LDC")
}

func TestRequestCreemPayRejectsConfiguredProductWithoutCurrency(t *testing.T) {
	previousProducts := setting.CreemProducts
	setting.CreemProducts = `[{"productId":"prod_blank","price":12.34,"currency":"","quota":1000}]`
	t.Cleanup(func() { setting.CreemProducts = previousProducts })

	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Set("id", 7)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/user/creem/pay", bytes.NewBufferString(`{"product_id":"prod_blank","payment_method":"creem"}`))
	context.Request.Header.Set("Content-Type", "application/json")
	RequestCreemPay(context)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), "产品币种配置错误")
}

func TestRequestWaffoPayFailsWhenGroupLookupFails(t *testing.T) {
	previousDB := model.DB
	previousRedis := common.RedisEnabled
	previousEnabled := setting.WaffoEnabled
	common.RedisEnabled = false
	setting.WaffoEnabled = true
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Token{}))
	user := model.User{Username: "waffo-group-error", Password: "password", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	model.DB = db
	callbackName := "test:waffo_group_lookup_failure"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "group" {
			tx.AddError(fmt.Errorf("injected group lookup failure"))
		}
	}))
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(callbackName)
		model.DB = previousDB
		common.RedisEnabled = previousRedis
		setting.WaffoEnabled = previousEnabled
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Set("id", user.Id)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/user/waffo/pay", bytes.NewBufferString(`{"amount":10}`))
	context.Request.Header.Set("Content-Type", "application/json")
	RequestWaffoPay(context)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.True(t, strings.Contains(response.Body.String(), "获取用户分组失败") || strings.Contains(response.Body.String(), "支付配置错误"))
}

func stringPointer(value string) *string { return &value }
