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
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func canonicalCreditTestConfig(t *testing.T, quotaPerBatch int64) {
	t.Helper()
	preserveTopUpCreditMetadataConfig(t)
	confirmPaymentComplianceForTest(t)
	common.QuotaPerUnit = float64(quotaPerBatch)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(quotaPerBatch)))
	persistCreditDenominationFixture(t, model.DB)
	operation_setting.MinTopUp = 0
	setting.StripeMinTopUp, setting.WaffoMinTopUp, setting.WaffoPancakeMinTopUp = 0, 0, 0
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{}
	previousRatios := common.TopupGroupRatio2JSONString()
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1}`))
	t.Cleanup(func() { require.NoError(t, common.UpdateTopupGroupRatioByJSONString(previousRatios)) })
}

func canonicalCreditRequest(t *testing.T, handler gin.HandlerFunc, userID int, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/canonical", func(c *gin.Context) { c.Set("id", userID) }, RequireCanonicalTopUpCredit, handler)
	request := httptest.NewRequest(http.MethodPost, "/canonical", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestCanonicalTopUpCreditRejectsAmbiguousOrUnboundedJSON(t *testing.T) {
	canonicalCreditTestConfig(t, 500000)
	for _, body := range []string{
		`{"amount":1}`, `{"amount":1,"amount_unit":"LEGACY"}`, `{"amount":1,"amount_unit":"USD"}`,
		`{"amount":1,"amount_unit":"CNY"}`, `{"amount":1,"amount_unit":"credit"}`,
		`{"amount":"1","amount_unit":"CREDIT"}`, `{"amount":null,"amount_unit":"CREDIT"}`,
		`{"amount":0,"amount_unit":"CREDIT"}`, `{"amount":-1,"amount_unit":"CREDIT"}`,
		`{"amount":1.5,"amount_unit":"CREDIT"}`, `{"amount":1.0,"amount_unit":"CREDIT"}`,
		`{"amount":1e0,"amount_unit":"CREDIT"}`, `{"amount":1e1000000000,"amount_unit":"CREDIT"}`,
		`{"amount":9007199254740991.1,"amount_unit":"CREDIT"}`,
		`{"amount":9007199254740992,"amount_unit":"CREDIT"}`,
		`{"amount":99999999999999999999999999,"amount_unit":"CREDIT"}`,
	} {
		called := false
		response := canonicalCreditRequest(t, func(c *gin.Context) { called = true }, 0, body)
		require.Equal(t, http.StatusBadRequest, response.Code, body)
		require.False(t, called, body)
	}
}

func TestCanonicalTopUpCreditPreservesOneCreditAndSafeIntegerExtremes(t *testing.T) {
	for _, quotaPerBatch := range []int64{300000, 500000} {
		t.Run(fmt.Sprint(quotaPerBatch), func(t *testing.T) {
			canonicalCreditTestConfig(t, quotaPerBatch)
			for _, credits := range []int64{1, 4503599627370497, 9007199254740987, 9007199254740991} {
				response := canonicalCreditRequest(t, func(c *gin.Context) {
					resolved, err := resolveTopUpRequestAmount(c, 0, "LEGACY")
					require.NoError(t, err)
					require.Equal(t, credits, resolved.CreditedQuota)
					_, _, captured, err := topUpOrderAmountsResolved(resolved)
					require.NoError(t, err)
					require.Equal(t, credits, captured)
					c.JSON(http.StatusOK, gin.H{"credited_quota": captured})
				}, 0, fmt.Sprintf(`{"amount":%d,"amount_unit":"CREDIT"}`, credits))
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				var result struct {
					Credits int64 `json:"credited_quota"`
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
				require.Equal(t, credits, result.Credits)
			}
		})
	}
}

func TestCanonicalTopUpCreditUnavailableBasisFailsClosed(t *testing.T) {
	canonicalCreditTestConfig(t, 500000)
	common.ClearCreditsPerUSD()
	called := false
	response := canonicalCreditRequest(t, func(c *gin.Context) { called = true }, 0, `{"amount":1,"amount_unit":"CREDIT"}`)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.False(t, called)
	info := withTopUpPublicCreditMetadata(gin.H{"pay_methods": []map[string]string{}, "amount_options": []int{1}}).(gin.H)
	require.Equal(t, false, info["credit_metadata_available"])
	require.NotContains(t, info, "credit_metadata_version")
	require.NotContains(t, info, "credit_amount_options")
}

func TestCanonicalTopUpCreditCouponUsesOriginalRawQualification(t *testing.T) {
	canonicalCreditTestConfig(t, 300000)
	setupTopupInfoUser(t, 701, "default")
	require.NoError(t, model.DB.AutoMigrate(&model.DiscountCode{}, &model.DiscountCodeReservation{}))
	operation_setting.GetGeneralSetting().QuotaDisplayType = "TOKENS"
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{1: 0.9}
	operation_setting.PayMethods = []map[string]string{{"type": "custom-credit", "settlement_unit": "CNY", "unit_price": "300000"}}
	code := model.DiscountCode{Code: "RAW1", DiscountPercent: 10, MinAmount: 1, MaxUses: 1, Status: model.DiscountCodeStatusEnabled}
	require.NoError(t, model.DB.Create(&code).Error)
	body := `{"amount":1,"amount_unit":"CREDIT","code":"RAW1","discount_code":"RAW1","payment_method":"custom-credit"}`
	validation := canonicalCreditRequest(t, ValidateDiscountCode, 701, body)
	var validated struct{ Success bool }
	require.NoError(t, json.Unmarshal(validation.Body.Bytes(), &validated))
	require.True(t, validated.Success, validation.Body.String())
	quote := canonicalCreditRequest(t, RequestAmount, 701, body)
	var quoted struct {
		Message, Data string
		Credits       int64 `json:"credited_quota"`
	}
	require.NoError(t, json.Unmarshal(quote.Body.Bytes(), &quoted))
	require.Equal(t, "success", quoted.Message, quote.Body.String())
	require.Equal(t, "0.81", quoted.Data)
	require.EqualValues(t, 1, quoted.Credits)
	// The order reserves the same integer qualification used by preview and
	// quote, even though its compatibility projection is only three micros.
	resolved, err := resolveTopUpDecimalAmount(decimal.NewFromInt(1), "CREDIT")
	require.NoError(t, err)
	stored, micros, captured, err := topUpOrderAmountsResolved(resolved)
	require.NoError(t, err)
	require.EqualValues(t, 0, stored)
	require.EqualValues(t, 3, micros)
	order := model.TopUp{UserId: 701, Amount: stored, PlatformAmountMicros: micros, CreditedQuota: captured,
		Money: 0.81, ExpectedAmountMicros: 810000, SettlementCurrency: "CNY", PaymentProvider: model.PaymentProviderEpay,
		PaymentMethod: "custom-credit", TradeNo: "canonical-raw-one", Status: common.TopUpStatusPending,
		DiscountCodeId: code.Id, DiscountPercent: 10, DiscountQualifyingUnit: "CREDIT", DiscountQualifyingAmount: "1"}
	require.NoError(t, order.Insert())
	var reservations int64
	require.NoError(t, model.DB.Model(&model.DiscountCodeReservation{}).Where("top_up_trade_no = ?", order.TradeNo).Count(&reservations).Error)
	require.EqualValues(t, 1, reservations)
	settlement := model.ExternalTopUpSettlement{TradeNo: order.TradeNo, PaymentProvider: model.PaymentProviderEpay,
		PaymentMethod: "custom-credit", ProviderEventId: "raw-one-event", ProviderTransactionId: "raw-one-transaction",
		SettlementCurrency: "CNY", SettledAmountMicros: 810000}
	_, err = model.CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	_, err = model.CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	var user model.User
	require.NoError(t, model.DB.First(&user, 701).Error)
	require.EqualValues(t, 1, user.Quota)
	require.NoError(t, model.DB.First(&code, code.Id).Error)
	require.EqualValues(t, 1, code.UsedCount)
}

func TestCanonicalTopUpCreditRawUSDLimitsUseIntegerAuthority(t *testing.T) {
	canonicalCreditTestConfig(t, 300000)
	operation_setting.PayMethods = []map[string]string{{"type": "fixture", "min_topup": "0.000000285714285714", "max_topup": "0.000000571428571429"}}
	for _, tc := range []struct {
		credits int64
		want    bool
	}{{1, true}, {2, true}, {3, false}} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.Equal(t, tc.want, requirePaymentMethodCreditedQuotaWithinLimit(c, "fixture", tc.credits), fmt.Sprint(tc.credits))
	}
}

func TestCanonicalTopUpCreditProviderQuotesShareRawDiscountQualification(t *testing.T) {
	canonicalCreditTestConfig(t, 300000)
	setupTopupInfoUser(t, 703, "default")
	require.NoError(t, model.DB.AutoMigrate(&model.DiscountCode{}))
	operation_setting.USDExchangeRate = 8
	operation_setting.GetGeneralSetting().QuotaDisplayType = "TOKENS"
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{1000000: 0.9}
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}
	code := model.DiscountCode{Code: "PROVIDER_RAW", DiscountPercent: 10, MinAmount: 1000000, Status: model.DiscountCodeStatusEnabled}
	require.NoError(t, model.DB.Create(&code).Error)
	for _, tc := range []struct{ provider, currency, want string }{
		{"epay", "CNY", "1.85"}, {"stripe", "USD", "0.23"}, {"waffo", "USD", "0.23"},
		{"pancake USD", "USD", "0.23"}, {"pancake CNY", "CNY", "1.85"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			response := canonicalCreditRequest(t, func(c *gin.Context) {
				amount, err := resolveTopUpRequestAmount(c, 0, "CREDIT")
				require.NoError(t, err)
				var quote decimal.Decimal
				if tc.provider == "epay" {
					quote, _, err = quoteTopUpRequestWithDiscount(c, amount, "default", "alipay", "PROVIDER_RAW", 703)
				} else {
					quote, _, err = quoteStandardTopUpRequestWithDiscount(c, amount, "default", tc.currency, "PROVIDER_RAW", 703)
				}
				require.NoError(t, err)
				require.Equal(t, tc.want, quote.StringFixed(2))
				_, _, grant, err := topUpOrderAmountsResolved(amount)
				require.NoError(t, err)
				require.EqualValues(t, 1000000, grant)
				c.Status(http.StatusOK)
			}, 703, `{"amount":1000000,"amount_unit":"CREDIT"}`)
			require.Equal(t, http.StatusOK, response.Code)
		})
	}
}

func TestCanonicalTopUpCreditStandardCNYQuoteCheckoutAndFrozenGrant(t *testing.T) {
	for _, tc := range []struct {
		fx        float64
		credits   int64
		cash, usd string
	}{
		{7, 50000000, "100.00", "14.2857142857142857"},
		{7.2, 48611111, "100.00", "13.8888888571428571"},
	} {
		t.Run(fmt.Sprint(tc.fx), func(t *testing.T) {
			canonicalCreditTestConfig(t, 500000)
			setupTopupInfoUser(t, 702, "default")
			operation_setting.USDExchangeRate = tc.fx
			operation_setting.PayMethods = []map[string]string{{"name": "synthetic alipay", "type": "alipay"}}
			previousAddress, previousID, previousKey := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey
			operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = "http://127.0.0.1:1", "offline-fixture", "offline-fixture"
			t.Cleanup(func() {
				operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = previousAddress, previousID, previousKey
			})
			body := fmt.Sprintf(`{"amount":%d,"amount_unit":"CREDIT","payment_method":"alipay"}`, tc.credits)
			quote := canonicalCreditRequest(t, RequestAmount, 702, body)
			var quoted struct {
				Message, Data string
				Credits       int64 `json:"credited_quota"`
			}
			require.NoError(t, json.Unmarshal(quote.Body.Bytes(), &quoted))
			require.Equal(t, "success", quoted.Message, quote.Body.String())
			require.Equal(t, tc.cash, quoted.Data)
			require.Equal(t, tc.credits, quoted.Credits)
			// The Epay SDK constructs form parameters locally; no request is sent.
			checkout := canonicalCreditRequest(t, RequestEpay, 702, body)
			var opened struct {
				Message string
				Data    map[string]string
				Credits int64 `json:"credited_quota"`
			}
			require.NoError(t, json.Unmarshal(checkout.Body.Bytes(), &opened))
			require.Equal(t, "success", opened.Message, checkout.Body.String())
			require.Equal(t, tc.cash, opened.Data["money"])
			require.Equal(t, tc.credits, opened.Credits)
			var order model.TopUp
			require.NoError(t, model.DB.Where("trade_no = ?", opened.Data["out_trade_no"]).First(&order).Error)
			require.Equal(t, tc.credits, order.CreditedQuota)
			require.EqualValues(t, 100000000, order.ExpectedAmountMicros)
			operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = 9, 99
			// Synthetic transaction evidence exercises local grant/replay only.
			// It does not establish acceptance of a real provider payment.
			settlement := model.ExternalTopUpSettlement{TradeNo: order.TradeNo, PaymentProvider: model.PaymentProviderEpay,
				PaymentMethod: "alipay", ProviderEventId: "canonical-event", ProviderTransactionId: "canonical-transaction",
				SettlementCurrency: "CNY", SettledAmountMicros: 100000000}
			_, err := model.CompleteExternalTopUp(settlement)
			require.NoError(t, err)
			_, err = model.CompleteExternalTopUp(settlement)
			require.NoError(t, err)
			var user model.User
			require.NoError(t, model.DB.First(&user, 702).Error)
			require.EqualValues(t, tc.credits, user.Quota)
			usd, err := common.CreditsToUSD(int64(user.Quota))
			require.NoError(t, err)
			require.Equal(t, tc.usd, usd.String())
		})
	}
}
