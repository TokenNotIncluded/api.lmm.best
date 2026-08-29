package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func preserveChannelPricing(t *testing.T) {
	t.Helper()
	originalPrice := operation_setting.Price
	originalUSDExchangeRate := operation_setting.USDExchangeRate
	originalPlatformUnitsPerCNY := operation_setting.TopUpPlatformUnitsPerCNY
	generalSetting := operation_setting.GetGeneralSetting()
	originalDisplayType := generalSetting.QuotaDisplayType
	originalCustomExchangeRate := generalSetting.CustomCurrencyExchangeRate
	originalMethods := operation_setting.PayMethods
	originalDiscounts := operation_setting.GetPaymentSetting().AmountDiscount
	originalRatios := common.TopupGroupRatio2JSONString()
	originalStripeUnitPrice := setting.StripeUnitPrice
	originalWaffoUnitPrice := setting.WaffoUnitPrice
	originalPancakeUnitPrice := setting.WaffoPancakeUnitPrice
	t.Cleanup(func() {
		operation_setting.Price = originalPrice
		operation_setting.USDExchangeRate = originalUSDExchangeRate
		operation_setting.TopUpPlatformUnitsPerCNY = originalPlatformUnitsPerCNY
		generalSetting.QuotaDisplayType = originalDisplayType
		generalSetting.CustomCurrencyExchangeRate = originalCustomExchangeRate
		operation_setting.PayMethods = originalMethods
		operation_setting.GetPaymentSetting().AmountDiscount = originalDiscounts
		setting.StripeUnitPrice = originalStripeUnitPrice
		setting.WaffoUnitPrice = originalWaffoUnitPrice
		setting.WaffoPancakeUnitPrice = originalPancakeUnitPrice
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(originalRatios))
	})
}

func setupTopupInfoUser(t *testing.T, id int, group string) {
	t.Helper()
	previousDB := model.DB
	previousDatabaseType := common.MainDatabaseType()
	previousRedisEnabled := common.RedisEnabled
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}))
	model.DB = db
	levelOne := model.TrustLevelMinUser + 1
	require.NoError(t, db.Create(&model.User{
		Id:                 id,
		Username:           "topup-info-user",
		Password:           "test-password",
		Role:               common.RoleCommonUser,
		Status:             common.UserStatusEnabled,
		Group:              group,
		TrustLevelOverride: &levelOne,
	}).Error)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousDatabaseType)
		common.RedisEnabled = previousRedisEnabled
	})
}

func TestIntegerPlatformAmountRejectsFractionalInput(t *testing.T) {
	amount, err := integerPlatformAmount(6.8)
	require.Error(t, err)
	require.Zero(t, amount)

	amount, err = integerPlatformAmount(68)
	require.NoError(t, err)
	require.EqualValues(t, 68, amount)
}

func TestDedicatedUSDGatewaysShareOneStandardQuote(t *testing.T) {
	preserveChannelPricing(t)
	operation_setting.USDExchangeRate = 6.8
	operation_setting.TopUpPlatformUnitsPerCNY = 1
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1}`))
	setting.StripeUnitPrice = 99
	setting.WaffoUnitPrice = 88
	setting.WaffoPancakeUnitPrice = 77

	require.InDelta(t, 10.0, getStripePayMoney(68, "default"), 0.000001)
	require.InDelta(t, 10.0, getWaffoPayMoney(68, "default"), 0.000001)
	require.True(t, getWaffoPancakePayMoneyDecimal(68, "default").Equal(decimal.RequireFromString("10.00")))
}

func TestDedicatedGatewayMetadataCannotLeakLegacyUnitPrice(t *testing.T) {
	preserveChannelPricing(t)
	operation_setting.USDExchangeRate = 6.8
	operation_setting.TopUpPlatformUnitsPerCNY = 1
	method := map[string]string{
		"type":                               "waffo_pancake",
		"unit_price":                         "6.8",
		"settlement_units_per_platform_unit": "6.8",
		"platform_units_per_usd":             "1",
		"settlement_units_per_usd":           "6.8",
	}

	setPaymentMethodStandardPricing(method, "USD")
	require.Equal(t, "USD", method["settlement_currency"])
	require.Equal(t, "6.8", method["platform_units_per_usd"])
	require.Equal(t, "1", method["settlement_units_per_usd"])
	require.NotContains(t, method, "unit_price")
	require.NotContains(t, method, "settlement_units_per_platform_unit")
}

func TestSettlementAmountUsesTwoRateCurrencyContract(t *testing.T) {
	platformAmount := decimal.RequireFromString("6.8")
	platformUnitsPerUSD := decimal.RequireFromString("6.8")

	usd, err := settlementAmountForPlatformAmount(platformAmount, payMethodSettlementPricing{
		platformUnitsPerUSD:   platformUnitsPerUSD,
		settlementUnitsPerUSD: decimal.NewFromInt(1),
	})
	require.NoError(t, err)
	require.True(t, usd.Equal(decimal.NewFromInt(1)), "6.8 platform units must settle as 1 USD")

	cny, err := settlementAmountForPlatformAmount(platformAmount, payMethodSettlementPricing{
		platformUnitsPerUSD:   platformUnitsPerUSD,
		settlementUnitsPerUSD: decimal.RequireFromString("6.8"),
	})
	require.NoError(t, err)
	require.True(t, cny.Equal(decimal.RequireFromString("6.8")), "6.8 platform units must settle as 6.8 CNY")
}

func TestQuoteTopUpSupportsExplicitFXAndLegacyDirectPricing(t *testing.T) {
	preserveChannelPricing(t)
	operation_setting.Price = 999 // The removed global fallback must not affect either quote.
	operation_setting.USDExchangeRate = 6.8
	operation_setting.GetGeneralSetting().QuotaDisplayType =
		operation_setting.QuotaDisplayTypeCNY
	operation_setting.PayMethods = []map[string]string{
		{"name": "USD gateway", "type": "usd", "settlement_unit": "USD", "platform_units_per_usd": "6.8", "settlement_units_per_usd": "1"},
		{"name": "CNY gateway", "type": "cny", "settlement_unit": "CNY", "platform_units_per_usd": "6.8", "settlement_units_per_usd": "6.8"},
		{"name": "CNY global platform rate", "type": "cny-global", "settlement_currency": "CNY", "settlement_units_per_usd": "6.8"},
		{"name": "LINUX DO Credit", "type": "epay", "settlement_unit": "LDC", "unit_price": "10", "topup_ratio": "0.5"},
	}
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1,"ldc":0.14}`))

	usd, err := quoteTopUp(68, "default", "usd")
	require.NoError(t, err)
	require.True(t, usd.Equal(decimal.RequireFromString("10.00")))

	cny, err := quoteTopUp(68, "default", "cny")
	require.NoError(t, err)
	require.True(t, cny.Equal(decimal.RequireFromString("68.00")))

	cnyGlobal, err := quoteTopUp(68, "default", "cny-global")
	require.NoError(t, err)
	require.True(t, cnyGlobal.Equal(decimal.RequireFromString("68.00")))

	legacyDirect, err := quoteTopUp(1, "default", "epay")
	require.NoError(t, err)
	require.True(t, legacyDirect.Equal(decimal.RequireFromString("5.00")))

	grouped, err := quoteTopUp(1, "ldc", "epay")
	require.NoError(t, err)
	require.True(t, grouped.Equal(decimal.RequireFromString("0.70")))
}

func TestConfiguredPlatformRateUsesCNYBaseIndependentlyOfDisplay(t *testing.T) {
	preserveChannelPricing(t)
	operation_setting.USDExchangeRate = 6.8
	operation_setting.TopUpPlatformUnitsPerCNY = 1
	generalSetting := operation_setting.GetGeneralSetting()
	generalSetting.QuotaDisplayType = operation_setting.QuotaDisplayTypeCustom
	generalSetting.CustomCurrencyExchangeRate = 0.92

	rate, err := configuredPlatformUnitsPerUSD()
	require.NoError(t, err)
	require.True(t, rate.Equal(decimal.RequireFromString("6.8")))

	operation_setting.TopUpPlatformUnitsPerCNY = 1.1
	rate, err = configuredPlatformUnitsPerUSD()
	require.NoError(t, err)
	require.True(t, rate.Equal(decimal.RequireFromString("7.48")))
}

func TestEpayAlwaysUsesCNYSettlementContract(t *testing.T) {
	preserveChannelPricing(t)
	operation_setting.USDExchangeRate = 6.8
	operation_setting.TopUpPlatformUnitsPerCNY = 1
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1}`))
	operation_setting.PayMethods = []map[string]string{
		{"name": "支付宝", "type": "alipay", "settlement_currency": "CNY", "unit_price": "0.1470588235"},
		{"name": "微信", "type": "wxpay"},
		{"name": "Custom Epay", "type": "epay-default"},
	}

	alipay, err := quoteTopUp(68, "default", "alipay")
	require.NoError(t, err)
	require.True(t, alipay.Equal(decimal.RequireFromString("68.00")))

	wxpay, err := quoteTopUp(68, "default", "wxpay")
	require.NoError(t, err)
	require.True(t, wxpay.Equal(decimal.RequireFromString("68.00")))

	customEpay, err := quoteTopUp(68, "default", "epay-default")
	require.NoError(t, err)
	require.True(t, customEpay.Equal(decimal.RequireFromString("68.00")))
	publicMethods := sanitizedPaymentMethods(operation_setting.PayMethods)
	require.Equal(t, "CNY", publicMethods[1]["settlement_currency"])
	require.Equal(t, "6.8", publicMethods[1]["platform_units_per_usd"])
	require.Equal(t, "6.8", publicMethods[1]["settlement_units_per_usd"])
	require.Equal(t, "CNY", publicMethods[2]["settlement_currency"])
	require.Equal(t, "6.8", publicMethods[2]["platform_units_per_usd"])
	require.Equal(t, "6.8", publicMethods[2]["settlement_units_per_usd"])

	operation_setting.PayMethods[0]["settlement_currency"] = "USD"
	migratedLegacy, err := quoteTopUp(68, "default", "alipay")
	require.NoError(t, err)
	require.True(t, migratedLegacy.Equal(decimal.RequireFromString("68.00")))
}

func TestQuoteTopUpRejectsAmbiguousOrInvalidPricing(t *testing.T) {
	preserveChannelPricing(t)
	operation_setting.PayMethods = []map[string]string{
		{"name": "missing pricing", "type": "missing-pricing", "settlement_unit": "CNY"},
		{"name": "missing settlement FX", "type": "missing-settlement-fx", "settlement_unit": "USD", "platform_units_per_usd": "6.8"},
		{"name": "zero platform FX", "type": "zero-platform-fx", "settlement_unit": "USD", "platform_units_per_usd": "0", "settlement_units_per_usd": "1"},
		{"name": "mixed pricing", "type": "mixed-pricing", "settlement_unit": "USD", "platform_units_per_usd": "6.8", "settlement_units_per_usd": "1", "unit_price": "1"},
		{"name": "conflicting direct", "type": "conflicting-direct", "settlement_unit": "USD", "unit_price": "1", "settlement_units_per_platform_unit": "2"},
		{"name": "missing unit", "type": "missing-unit", "unit_price": "10"},
		{"name": "invalid unit", "type": "invalid-unit", "settlement_unit": "L DC", "unit_price": "10"},
		{"name": "safe direct", "type": "safe-direct", "settlement_unit": ".LDC-1", "settlement_units_per_platform_unit": "10"},
		{"name": "invalid topup ratio", "type": "invalid-topup-ratio", "settlement_unit": "USD", "settlement_units_per_platform_unit": "1", "topup_ratio": "0"},
	}

	standardCNY, err := quoteTopUp(1, "default", "missing-pricing")
	require.NoError(t, err)
	require.True(t, standardCNY.Equal(decimal.RequireFromString("1.00")))

	for _, paymentMethod := range []string{
		"unknown", "missing-settlement-fx", "zero-platform-fx",
		"mixed-pricing", "conflicting-direct", "invalid-unit", "invalid-topup-ratio",
	} {
		_, err := quoteTopUp(1, "default", paymentMethod)
		require.Error(t, err, paymentMethod)
	}

	defaultCNYDirect, err := quoteTopUp(1, "default", "missing-unit")
	require.NoError(t, err)
	require.True(t, defaultCNYDirect.Equal(decimal.RequireFromString("10.00")))

	punctuated, err := quoteTopUp(1, "default", "safe-direct")
	require.NoError(t, err)
	require.True(t, punctuated.Equal(decimal.RequireFromString("10.00")))
}

func TestGetTopUpInfoPreservesSettlementMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	confirmPaymentComplianceForTest(t)
	preserveChannelPricing(t)
	setupTopupInfoUser(t, 301, "ldc")
	operation_setting.PayMethods = []map[string]string{
		{"name": "LINUX DO Credit", "type": "epay", "settlement_unit": "LDC", "unit_price": "10", "topup_ratio": "0.5"},
	}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1,"ldc":0.14}`))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 301)
	GetTopUpInfo(c)

	var response struct {
		Data struct {
			PayMethods      []map[string]string `json:"pay_methods"`
			TopupGroupRatio float64             `json:"topup_group_ratio"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.Data.PayMethods, 1)
	require.Equal(t, "LDC", response.Data.PayMethods[0]["settlement_unit"])
	require.Equal(t, "10", response.Data.PayMethods[0]["unit_price"])
	require.Equal(t, "10", response.Data.PayMethods[0]["settlement_units_per_platform_unit"])
	require.Equal(t, "0.5", response.Data.PayMethods[0]["topup_ratio"])
	require.InDelta(t, 0.14, response.Data.TopupGroupRatio, 0.000001)
}

func TestGetTopUpInfoPreservesCanonicalFXMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	confirmPaymentComplianceForTest(t)
	preserveChannelPricing(t)
	setupTopupInfoUser(t, 303, "default")
	operation_setting.USDExchangeRate = 6.8
	operation_setting.GetGeneralSetting().QuotaDisplayType =
		operation_setting.QuotaDisplayTypeCNY
	operation_setting.PayMethods = []map[string]string{
		{
			"name":                     "CNY gateway",
			"type":                     "cny",
			"settlement_currency":      "CNY",
			"settlement_units_per_usd": "6.8",
		},
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 303)
	GetTopUpInfo(c)

	var response struct {
		Data struct {
			PayMethods []map[string]string `json:"pay_methods"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.Data.PayMethods, 1)
	require.Equal(t, "CNY", response.Data.PayMethods[0]["settlement_currency"])
	require.Equal(t, "6.8", response.Data.PayMethods[0]["platform_units_per_usd"])
	require.Equal(t, "6.8", response.Data.PayMethods[0]["settlement_units_per_usd"])
}

func TestRequestAmountWithoutPaymentMethodDoesNotUseGlobalPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	preserveChannelPricing(t)
	setupTopupInfoUser(t, 302, "default")
	operation_setting.Price = 7.3
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1}`))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 302)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/amount", strings.NewReader(`{"amount":2}`))
	c.Request.Header.Set("Content-Type", "application/json")
	RequestAmount(c)

	var response struct {
		Message string `json:"message"`
		Data    string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, "error", response.Message)
	require.Equal(t, "payment_method is required", response.Data)
}

func TestValidateEpayCallbackRejectsSignedTypeOrMoneyMismatchAndIsIdempotent(t *testing.T) {
	pending := &model.TopUp{
		PaymentMethod:        "epay",
		PaymentProvider:      model.PaymentProviderEpay,
		Money:                1.4,
		ExpectedAmountMicros: 1_400_000,
		CreditedQuota:        2 * int64(common.QuotaPerUnit),
		SettlementCurrency:   "CNY",
		Status:               common.TopUpStatusPending,
	}

	shouldCredit, err := validateEpayCallback(pending, &epay.VerifyRes{Type: "wxpay", Money: "1.40"})
	require.Error(t, err)
	require.False(t, shouldCredit)

	shouldCredit, err = validateEpayCallback(pending, &epay.VerifyRes{Type: "epay", Money: "1.41"})
	require.Error(t, err)
	require.False(t, shouldCredit)

	shouldCredit, err = validateEpayCallback(pending, &epay.VerifyRes{Type: "epay", Money: "1.40"})
	require.NoError(t, err)
	require.True(t, shouldCredit)

	completed := *pending
	completed.Status = common.TopUpStatusSuccess
	shouldCredit, err = validateEpayCallback(&completed, &epay.VerifyRes{Type: "epay", Money: "1.40"})
	require.NoError(t, err)
	require.False(t, shouldCredit)
}
