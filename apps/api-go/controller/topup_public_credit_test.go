package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func publicCreditRequest(t *testing.T, handler gin.HandlerFunc, userID int, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/canonical/v2", func(c *gin.Context) { c.Set("id", userID) }, RequirePublicTopUpCredit, handler)
	request := httptest.NewRequest(http.MethodPost, "/canonical/v2", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func publicTopUpCreditTestConfig(t *testing.T, public string) {
	t.Helper()
	previousPublic, err := common.PublicCreditsPerUSD()
	previousLedger, ledgerErr := common.CreditsPerUSD()
	t.Cleanup(func() {
		if err != nil || (ledgerErr == nil && previousPublic.Equal(previousLedger)) {
			common.ClearPublicCreditsPerUSD()
		} else {
			require.NoError(t, common.SetPublicCreditsPerUSD(previousPublic))
		}
	})
	canonicalCreditTestConfig(t, 500000)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	require.NoError(t, common.SetPublicCreditsPerUSD(decimal.RequireFromString(public)))
	persistCreditDenominationFixture(t, model.DB)
}

func TestPublicTopUpCreditVersionedUnitsPreserveLegacyQuota(t *testing.T) {
	publicTopUpCreditTestConfig(t, "500000")
	for _, tc := range []struct {
		name, unit, version string
		amount, quota       int64
	}{
		{"old omitted version", "CREDIT", "", 100000, 100000},
		{"explicit old version", "CREDIT", `,"credit_metadata_version":1`, 100000, 100000},
		{"public one dollar", "CREDIT", `,"credit_metadata_version":2`, 500000, 500000},
		{"public one credit", "CREDIT", `,"credit_metadata_version":2`, 1, 1},
		{"authoritative existing preset", "LEDGER_QUOTA", `,"credit_metadata_version":2`, 5000000, 5000000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := canonicalCreditRequest
			guard := ""
			if strings.Contains(tc.version, ":2") {
				request = publicCreditRequest
				if tc.unit == "CREDIT" {
					guard = `,"expected_public_credits_per_usd_exact":"500000"`
				}
			}
			response := request(t, func(c *gin.Context) {
				amount, err := resolveTopUpRequestAmount(c, 0, "ignored")
				require.NoError(t, err)
				require.Equal(t, tc.quota, amount.CreditedQuota)
				_, _, frozen, err := topUpOrderAmountsResolved(amount)
				require.NoError(t, err)
				require.Equal(t, tc.quota, frozen)
				c.Status(http.StatusOK)
			}, 0, fmt.Sprintf(`{"amount":%d,"amount_unit":%q%s%s}`, tc.amount, tc.unit, tc.version, guard))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		})
	}
	// Non-versioned legacy endpoints retain their original raw quota boundary.
	amount, err := resolveTopUpDecimalAmount(decimal.NewFromInt(100000), "CREDIT")
	require.NoError(t, err)
	require.EqualValues(t, 100000, amount.CreditedQuota)
}

func TestPublicTopUpCreditRejectsUnknownUnitsVersionsAndUnsafeConversion(t *testing.T) {
	publicTopUpCreditTestConfig(t, "500000")
	for _, body := range []string{
		`{"amount":1,"amount_unit":"CREDIT"}`,
		`{"amount":1,"amount_unit":"LEDGER_QUOTA"}`,
		`{"amount":1,"amount_unit":"LEDGER_QUOTA","credit_metadata_version":1}`,
		`{"amount":1,"amount_unit":"USD","credit_metadata_version":2}`,
		`{"amount":1,"amount_unit":"DOGE","credit_metadata_version":2}`,
		`{"amount":1,"amount_unit":"credit","credit_metadata_version":2}`,
		`{"amount":1,"amount_unit":"CREDIT","credit_metadata_version":0}`,
		`{"amount":1,"amount_unit":"CREDIT","credit_metadata_version":3}`,
		`{"amount":1,"amount_unit":"CREDIT","credit_metadata_version":"2"}`,
		`{"amount":1,"amount_unit":"CREDIT","credit_metadata_version":2.0}`,
		`{"amount":1,"amount_unit":"CREDIT","credit_metadata_version":null}`,
		`{"amount":9007199254740992,"amount_unit":"CREDIT","credit_metadata_version":2,"expected_public_credits_per_usd_exact":"500000"}`,
	} {
		called := false
		response := publicCreditRequest(t, func(c *gin.Context) { called = true }, 0, body)
		require.Equal(t, http.StatusBadRequest, response.Code, body)
		require.False(t, called, body)
	}
	require.Error(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(100000000)))
	persistCreditDenominationFixture(t, model.DB)
	response := publicCreditRequest(t, func(c *gin.Context) { t.Fatal("a stale denomination guard cannot reach checkout") }, 0,
		`{"amount":1,"amount_unit":"CREDIT","credit_metadata_version":2,"expected_public_credits_per_usd_exact":"100000000"}`)
	require.Equal(t, http.StatusConflict, response.Code)
	response = publicCreditRequest(t, func(c *gin.Context) { t.Fatal("fractional dust cannot grant zero credits") }, 0,
		`{"amount":0.9,"amount_unit":"CREDIT","credit_metadata_version":2,"expected_public_credits_per_usd_exact":"500000"}`)
	require.Equal(t, http.StatusBadRequest, response.Code)
}

func TestPublicTopUpCreditExpectedDenominationRejectsStaleCheckout(t *testing.T) {
	publicTopUpCreditTestConfig(t, "500000")
	body := `{"amount":100000,"amount_unit":"CREDIT","credit_metadata_version":2,"expected_public_credits_per_usd_exact":"500000"}`
	response := publicCreditRequest(t, func(c *gin.Context) {
		amount, ok := canonicalTopUpCredit(c)
		require.True(t, ok)
		require.EqualValues(t, 100000, amount.CreditedQuota)
		// A rejected denomination change cannot alter the wallet integer grant.
		require.Error(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(200000)))
		persistCreditDenominationFixture(t, model.DB)
		c.JSON(http.StatusOK, withTopUpRequestCreditFields(c, gin.H{}, "CREDIT", amount.LegacyBatch, amount.CreditedQuota, "USD"))
	}, 0, body)
	require.Equal(t, http.StatusOK, response.Code)
	var first struct {
		PublicAmount string `json:"public_credit_amount"`
		PublicExact  string `json:"public_credits_per_usd_exact"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &first))
	require.Equal(t, "100000", first.PublicAmount)
	require.Equal(t, "500000", first.PublicExact)
	response = publicCreditRequest(t, func(c *gin.Context) { t.Fatal("stale public units must not reach checkout") }, 0, strings.Replace(body, `"500000"`, `"200000"`, 1))
	require.Equal(t, http.StatusConflict, response.Code)
	for _, guard := range []string{"", `,"expected_public_credits_per_usd_exact":200000`, `,"expected_public_credits_per_usd_exact":"0200000"`, `,"expected_public_credits_per_usd_exact":"2e5"`, `,"expected_public_credits_per_usd_exact":"200000.0"`} {
		response = publicCreditRequest(t, func(c *gin.Context) { t.Fatal("invalid or missing denomination guard reached checkout") }, 0,
			`{"amount":100000,"amount_unit":"CREDIT","credit_metadata_version":2`+guard+`}`)
		require.Equal(t, http.StatusBadRequest, response.Code, guard)
	}
}

func TestPublicTopUpCreditCannotUseLegacyCurrencyRoute(t *testing.T) {
	publicTopUpCreditTestConfig(t, "500000")
	for _, unit := range []string{"CREDIT", "LEDGER_QUOTA"} {
		response := canonicalCreditRequest(t, func(c *gin.Context) { t.Fatal("a public denomination request reached the version-1 handler") }, 0,
			fmt.Sprintf(`{"amount":100000,"amount_unit":%q,"credit_metadata_version":2}`, unit))
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	}
}

func TestPublicTopUpCreditCannotFallThroughLegacyAmountResolver(t *testing.T) {
	publicTopUpCreditTestConfig(t, "500000")
	for _, version := range []string{"2", "3", "null", `"2"`} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/amount", strings.NewReader(
			fmt.Sprintf(`{"amount":100000,"amount_unit":"CREDIT","credit_metadata_version":%s}`, version)))
		c.Request.Header.Set("Content-Type", "application/json")
		var request AmountRequest
		require.NoError(t, bindTopUpRequest(c, &request))
		_, err := resolveTopUpRequestAmount(c, request.Amount, request.AmountUnit)
		require.Error(t, err, version)
	}
	for _, version := range []string{"1", ""} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		field := ""
		if version != "" {
			field = `,"credit_metadata_version":` + version
		}
		c.Request = httptest.NewRequest(http.MethodPost, "/api/user/amount", strings.NewReader(
			fmt.Sprintf(`{"amount":100000,"amount_unit":"CREDIT"%s}`, field)))
		c.Request.Header.Set("Content-Type", "application/json")
		var request AmountRequest
		require.NoError(t, bindTopUpRequest(c, &request))
		amount, err := resolveTopUpRequestAmount(c, request.Amount, request.AmountUnit)
		require.NoError(t, err)
		require.EqualValues(t, 100000, amount.CreditedQuota)
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/user/amount", strings.NewReader("amount=100000&credit_metadata_version=2")),
		httptest.NewRequest(http.MethodGet, "/api/user/amount?amount=100000&credit_metadata_version=2", nil),
	} {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = request
		_, err := resolveTopUpRequestAmount(c, 100000, "CREDIT")
		require.Error(t, err)
	}
}

func TestPublicTopUpMetadataPairsDecimalProjectionWithLedgerSelection(t *testing.T) {
	publicTopUpCreditTestConfig(t, "500000")
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	operation_setting.GetPaymentSetting().AmountOptions = operation_setting.PaymentAmountOptions{"5000000", "10000000"}
	operation_setting.GetPaymentSetting().AmountDiscount = operation_setting.PaymentAmountDiscount{"5000000": 0.9}
	operation_setting.PayMethods = []map[string]string{{"name": "Custom", "type": "custom", "min_topup": "1", "max_topup": "2.5"}}
	methods := sanitizedPaymentMethods(operation_setting.PayMethods)
	require.Len(t, methods, 1)
	metadata, err := topUpCreditMetadata(methods)
	require.NoError(t, err)
	require.Equal(t, 1, metadata["credit_metadata_version"])
	require.Equal(t, 2, metadata["public_credit_metadata_version"])
	require.Equal(t, "LEDGER_QUOTA", metadata["legacy_credit_unit"])
	require.Equal(t, "500000", metadata["ledger_quota_per_usd_exact"])
	require.Equal(t, "500000", metadata["public_credits_per_usd_exact"])
	require.Equal(t, []int64{5000000, 10000000}, metadata["credit_amount_options"])
	require.Equal(t, metadata["credit_amount_options"], metadata["ledger_quota_amount_options"])
	require.Equal(t, map[string]float64{"5000000": 0.9}, metadata["ledger_quota_discount"])
	projection, err := common.LedgerQuotaToPublicCredits(5000000)
	require.NoError(t, err)
	require.True(t, projection.IsInteger(), "public credits are the same wallet integers")
	require.Equal(t, projection.String(), metadata["public_credit_amount_options"].([]string)[0])
	require.Equal(t, map[string]float64{projection.String(): 0.9}, metadata["public_credit_discount"])
	require.Equal(t, "500000", methods[0]["min_topup_credit"])
	require.Equal(t, methods[0]["min_topup_credit"], methods[0]["min_topup_ledger_quota"])
	require.Equal(t, "500000", methods[0]["min_topup_public_credit"])
	require.Equal(t, "1250000", methods[0]["max_topup_credit"])
	require.Equal(t, "1250000", methods[0]["max_topup_public_credit"])
	require.Equal(t, operation_setting.PaymentAmountOptions{"5000000", "10000000"}, operation_setting.GetPaymentSetting().AmountOptions)
	require.Equal(t, operation_setting.PaymentAmountDiscount{"5000000": 0.9}, operation_setting.GetPaymentSetting().AmountDiscount)
	fields := topUpCreditFields("LEDGER_QUOTA", decimal.NewFromInt(10), 5000000, "CNY")
	require.EqualValues(t, 5000000, fields["credited_quota"])
	require.EqualValues(t, 5000000, fields["credit_amount"])
	require.Equal(t, "LEDGER_QUOTA", fields["credit_amount_unit"])
	require.Equal(t, projection.String(), fields["public_credit_amount"])
	require.Equal(t, "CNY", fields["settlement_currency"])
}

func TestPublicTopUpQuoteCheckoutAndHistoricalRefundKeepFrozenMoney(t *testing.T) {
	runPublicTopUpQuoteCheckoutAndHistoricalRefund(t, false)
}

func TestPublicTopUpQuoteCheckoutAndHistoricalRefundKeepFrozenMoneyPostgres(t *testing.T) {
	runPublicTopUpQuoteCheckoutAndHistoricalRefund(t, true)
}

func runPublicTopUpQuoteCheckoutAndHistoricalRefund(t *testing.T, postgres bool) {
	t.Helper()
	publicTopUpCreditTestConfig(t, "500000")
	if postgres {
		openAssistantKeyPostgresHarness(t)
		persistCreditDenominationFixture(t, model.DB)
		levelOne := model.TrustLevelMinUser + 1
		require.NoError(t, model.DB.Create(&model.User{Id: 705, Username: "public-topup-user", Password: "not-used-in-test",
			Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "PUB705", TrustLevelOverride: &levelOne}).Error)
	} else {
		setupTopupInfoUser(t, 705, "default")
	}
	require.NoError(t, model.DB.AutoMigrate(&model.DiscountCode{}, &model.DiscountCodeReservation{}, &model.FinanceLedgerEntry{}))
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	operation_setting.USDExchangeRate = 7
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}
	operation_setting.GetPaymentSetting().AmountDiscount = operation_setting.PaymentAmountDiscount{"5000000": 0.9}
	code := model.DiscountCode{Code: "PUBLIC_CREDIT_SNAPSHOT", DiscountPercent: 10, MinAmount: 5000000, Status: model.DiscountCodeStatusEnabled}
	require.NoError(t, model.DB.Create(&code).Error)
	previousAddress, previousID, previousKey := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey
	operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = "http://127.0.0.1:1", "offline-fixture", "offline-fixture"
	t.Cleanup(func() {
		operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = previousAddress, previousID, previousKey
	})
	body := `{"amount":5000000,"amount_unit":"CREDIT","credit_metadata_version":2,"expected_public_credits_per_usd_exact":"500000","payment_method":"alipay","discount_code":"PUBLIC_CREDIT_SNAPSHOT"}`
	quote := publicCreditRequest(t, RequestAmount, 705, body)
	var quoted struct {
		Message, Data, CreditAmountUnit, PublicCreditAmount string
		CreditedQuota                                       int64 `json:"credited_quota"`
	}
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(quote.Body.Bytes(), &quoted))
	require.NoError(t, json.Unmarshal(quote.Body.Bytes(), &raw))
	require.Equal(t, "success", quoted.Message, quote.Body.String())
	require.Equal(t, "56.70", quoted.Data)
	require.EqualValues(t, 5000000, quoted.CreditedQuota)
	require.JSONEq(t, `"LEDGER_QUOTA"`, string(raw["credit_amount_unit"]))
	require.JSONEq(t, `"5000000"`, string(raw["public_credit_amount"]))
	// Epay constructs its signed redirect locally; no real provider is called.
	checkout := publicCreditRequest(t, RequestEpay, 705, body)
	var opened struct {
		Message string
		Data    map[string]string
	}
	require.NoError(t, json.Unmarshal(checkout.Body.Bytes(), &opened))
	require.Equal(t, "success", opened.Message, checkout.Body.String())
	require.Equal(t, "56.70", opened.Data["money"])
	var order model.TopUp
	require.NoError(t, model.DB.Where("trade_no = ?", opened.Data["out_trade_no"]).First(&order).Error)
	require.EqualValues(t, 5000000, order.CreditedQuota)
	require.EqualValues(t, 56700000, order.ExpectedAmountMicros)
	require.Equal(t, "5000000", order.DiscountQualifyingAmount)
	require.Equal(t, "CREDIT", order.DiscountQualifyingUnit, "historical coupon unit retains its ledger quota contract")
	require.Error(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(250000)))
	persistCreditDenominationFixture(t, model.DB)
	operation_setting.USDExchangeRate = 9
	operation_setting.GetPaymentSetting().AmountDiscount = operation_setting.PaymentAmountDiscount{}
	settlement := model.ExternalTopUpSettlement{TradeNo: order.TradeNo, PaymentProvider: model.PaymentProviderEpay,
		PaymentMethod: "alipay", ProviderEventId: "public-credit-event", ProviderTransactionId: "public-credit-transaction",
		SettlementCurrency: "CNY", SettledAmountMicros: 56700000}
	_, err := model.CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	_, err = model.CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	var user model.User
	require.NoError(t, model.DB.First(&user, 705).Error)
	require.EqualValues(t, 5000000, user.Quota)
	refund, err := model.ApplyPaymentRefund(order.TradeNo, false, 28350000, "CNY", "public-credit-refund", "alipay", model.PaymentProviderEpay, "fixture", 705)
	require.NoError(t, err)
	require.True(t, refund.Created)
	require.EqualValues(t, 2500000, refund.QuotaDebited)
	refund, err = model.ApplyPaymentRefund(order.TradeNo, false, 28350000, "CNY", "public-credit-refund", "alipay", model.PaymentProviderEpay, "fixture", 705)
	require.NoError(t, err)
	require.False(t, refund.Created)
	require.NoError(t, model.DB.First(&user, 705).Error)
	require.EqualValues(t, 2500000, user.Quota)
	require.NoError(t, model.DB.First(&order, order.Id).Error)
	require.EqualValues(t, 5000000, order.CreditedQuota)
	require.EqualValues(t, 56700000, order.ExpectedAmountMicros)
	require.EqualValues(t, 2500000, order.RefundedQuota)
}

func TestPublicTopUpCreditRejectsOtherNodeMutableDenomination(t *testing.T) {
	publicTopUpCreditTestConfig(t, "500000")
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.PublicCreditsPerUSDOptionKey).Update("value", "200000").Error)
	cached, err := common.PublicCreditsPerUSD()
	require.NoError(t, err)
	require.Equal(t, "500000", cached.String())
	response := publicCreditRequest(t, func(c *gin.Context) { t.Fatal("mutable durable denomination reached checkout") }, 0,
		`{"amount":100000,"amount_unit":"CREDIT","credit_metadata_version":2,"expected_public_credits_per_usd_exact":"500000"}`)
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	_, err = topUpCreditMetadata(nil)
	require.Error(t, err)
}

func TestPublicTopUpCreditInvalidDurableConfigurationDoesNotUseStaleCache(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"missing ledger basis", model.CreditsPerUSDOptionKey, ""},
		{"missing legacy scale", model.LegacyPricingQuotaPerUnitOptionKey, ""},
		{"missing active scale", "QuotaPerUnit", ""},
		{"ledger mismatch", model.CreditsPerUSDOptionKey, "3359745"},
		{"active scale mismatch", "QuotaPerUnit", "300000"},
		{"zero public denomination", model.PublicCreditsPerUSDOptionKey, "0"},
		{"fractional public denomination", model.PublicCreditsPerUSDOptionKey, "100000.5"},
		{"unsafe public denomination", model.PublicCreditsPerUSDOptionKey, "9007199254740992"},
		{"database failure", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publicTopUpCreditTestConfig(t, "500000")
			if tc.key == "" {
				require.NoError(t, model.DB.Migrator().DropTable(&model.Option{}))
			} else if tc.value == "" {
				require.NoError(t, model.DB.Delete(&model.Option{}, "key = ?", tc.key).Error)
			} else {
				require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", tc.key).Update("value", tc.value).Error)
			}
			response := publicCreditRequest(t, func(c *gin.Context) { t.Fatal("unverified durable basis must not reach a quote or order") }, 0,
				`{"amount":100000,"amount_unit":"LEDGER_QUOTA","credit_metadata_version":2}`)
			require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			info := withTopUpPublicCreditMetadata(gin.H{"pay_methods": []map[string]string{}}).(gin.H)
			require.Equal(t, false, info["credit_metadata_available"])
			require.NotContains(t, info, "public_credits_per_usd_exact")
			require.NotContains(t, info, "public_credit_amount_options")
		})
	}
}
