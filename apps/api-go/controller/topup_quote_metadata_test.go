package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func setupSettlementQuoteUser(t *testing.T, id int) {
	t.Helper()
	previousDB, previousLog := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	t.Setenv("LOG_SQL_DSN", "")
	setupTopupInfoUser(t, id, "default")
	// Initialize dialect-aware columns against this disposable DB as startup does.
	require.NoError(t, model.InitLogDB(new(model.StartupMigrationSession)))
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousMainType)
		require.NoError(t, model.InitLogDB(new(model.StartupMigrationSession)))
		model.LOG_DB = previousLog
		common.SetLogDatabaseType(previousLogType)
	})
}

func TestTopUpSettlementQuoteCapturesActualSameCurrencyDiscounts(t *testing.T) {
	for _, tc := range []struct {
		name, method, currency string
		handler                gin.HandlerFunc
		quota                  int64
	}{
		{"ordinary CNY", "alipay", "CNY", RequestAmount, 50000000},
		{"Stripe USD", model.PaymentMethodStripe, "USD", RequestStripeAmount, 335974400},
		{"Waffo USD", model.PaymentMethodWaffo, "USD", RequestWaffoAmount, 335974400},
		{"Pancake CNY", model.PaymentMethodWaffoPancake, "CNY", RequestWaffoPancakeAmount, 50000000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publicTopUpCreditTestConfig(t, "100000")
			setupSettlementQuoteUser(t, 706)
			operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
			operation_setting.USDExchangeRate = 6.719488
			operation_setting.PayMethods = []map[string]string{{"type": tc.method}}
			operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{int(tc.quota): 0.9}
			require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 706).Update("setting", `{"settlement_currency":"CNY"}`).Error)
			body := fmt.Sprintf(`{"amount":%d,"amount_unit":"LEDGER_QUOTA","credit_metadata_version":2,"payment_method":%q}`, tc.quota, tc.method)
			response := publicCreditRequest(t, tc.handler, 706, body)
			require.Equal(t, http.StatusOK, response.Code)
			var decoded map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &decoded))
			require.Equal(t, "success", decoded["message"], response.Body.String())
			require.Equal(t, "90.00", decoded["data"])
			require.Equal(t, tc.currency, decoded["settlement_currency"])
			require.Equal(t, map[string]any{
				"schema_version": float64(1), "currency": tc.currency, "original_amount": "100.00", "paid_amount": "90.00",
				"savings_amount": "10.00", "discount_percent": "10.00", "basis": "amount_preset_and_code",
			}, decoded["settlement_quote"])
			if tc.method == model.PaymentMethodWaffoPancake {
				// Existing fields name a pre-coupon amount, already after a preset.
				// Keep their old meaning; the additive breakdown includes both.
				require.NotContains(t, decoded, "original_settlement_amount")
			}
		})
	}
}

func TestTopUpSettlementQuoteIncludesFeesAndOmitsNoActualSavings(t *testing.T) {
	for _, tc := range []struct {
		name, paymentRatio, groupRatio, paid, original, savings, percent string
		preset                                                           float64
	}{
		{"eligible with same payment fee", "1.05", "1", "94.50", "105.00", "10.50", "10.00", 0.9},
		{"no discount", "1", "1", "100.00", "", "", "", 1},
		{"payment fee without discount", "1.05", "1", "105.00", "", "", "", 1},
		{"preset surcharge", "1", "1", "110.00", "", "", "", 1.1},
		{"group pricing without discount", "1", "0.8", "80.00", "", "", "", 1},
		{"group pricing plus eligible preset", "1", "0.8", "72.00", "80.00", "8.00", "10.00", 0.9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publicTopUpCreditTestConfig(t, "100000")
			setupSettlementQuoteUser(t, 707)
			operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
			operation_setting.USDExchangeRate = 6.719488
			operation_setting.PayMethods = []map[string]string{{"type": "alipay", "topup_ratio": tc.paymentRatio}}
			require.NoError(t, common.UpdateTopupGroupRatioByJSONString(fmt.Sprintf(`{"default":%s}`, tc.groupRatio)))
			operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{50000000: tc.preset}
			response := publicCreditRequest(t, RequestAmount, 707, `{"amount":50000000,"amount_unit":"LEDGER_QUOTA","credit_metadata_version":2,"payment_method":"alipay"}`)
			var decoded map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &decoded))
			require.Equal(t, "success", decoded["message"], response.Body.String())
			require.Equal(t, tc.paid, decoded["data"])
			if tc.original == "" {
				require.NotContains(t, decoded, "settlement_quote")
			} else {
				quote := decoded["settlement_quote"].(map[string]any)
				require.Equal(t, tc.original, quote["original_amount"])
				require.Equal(t, tc.paid, quote["paid_amount"])
				require.Equal(t, tc.savings, quote["savings_amount"])
				require.Equal(t, tc.percent, quote["discount_percent"])
			}
		})
	}
}

func TestTopUpSettlementQuoteKeepsCouponEligibilityAndPancakeLegacyBasis(t *testing.T) {
	publicTopUpCreditTestConfig(t, "100000")
	setupSettlementQuoteUser(t, 708)
	require.NoError(t, model.DB.AutoMigrate(&model.DiscountCode{}))
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	operation_setting.USDExchangeRate = 6.719488
	operation_setting.PayMethods = []map[string]string{{"type": model.PaymentMethodWaffoPancake}}
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{50000000: 0.9}
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 708).Update("setting", `{"settlement_currency":"CNY"}`).Error)
	for _, code := range []model.DiscountCode{
		{Code: "ELIGIBLE_SAME_QUOTE", DiscountPercent: 10, MinAmount: 50000000, Status: model.DiscountCodeStatusEnabled},
		{Code: "INELIGIBLE_SAME_QUOTE", DiscountPercent: 10, MinAmount: 50000001, Status: model.DiscountCodeStatusEnabled},
	} {
		require.NoError(t, model.DB.Create(&code).Error)
	}
	for _, eligible := range []bool{true, false} {
		code := "INELIGIBLE_SAME_QUOTE"
		if eligible {
			code = "ELIGIBLE_SAME_QUOTE"
		}
		response := publicCreditRequest(t, RequestWaffoPancakeAmount, 708,
			fmt.Sprintf(`{"amount":50000000,"amount_unit":"LEDGER_QUOTA","credit_metadata_version":2,"discount_code":%q}`, code))
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &decoded))
		if !eligible {
			require.Equal(t, "error", decoded["message"])
			require.NotContains(t, decoded, "settlement_quote")
			continue
		}
		require.Equal(t, "81.00", decoded["data"])
		require.Equal(t, "90.00", decoded["original_settlement_amount"])
		require.Equal(t, "9.00", decoded["savings_settlement_amount"])
		quote := decoded["settlement_quote"].(map[string]any)
		require.Equal(t, "100.00", quote["original_amount"])
		require.Equal(t, "81.00", quote["paid_amount"])
		require.Equal(t, "19.00", quote["savings_amount"])
		require.Equal(t, "19.00", quote["discount_percent"])
	}
}

func TestTopUpSettlementQuoteRejectsMixedCurrenciesVATOrUncapturedBasis(t *testing.T) {
	for _, tc := range []struct{ name, capturedCurrency, responseCurrency, paid string }{
		{"different currency", "USD", "CNY", "90.00"},
		{"tax changes final amount", "CNY", "CNY", "99.00"},
		{"missing quote capture", "", "CNY", "90.00"},
		{"unknown currency", "CREDIT", "CREDIT", "90.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if tc.capturedCurrency != "" {
				recordTopUpQuoteBasis(c, decimal.NewFromInt(100), decimal.NewFromInt(90), tc.capturedCurrency)
			}
			response := gin.H{"message": "success", "data": tc.paid}
			addTopUpSettlementQuote(c, response, tc.responseCurrency)
			require.NotContains(t, response, "settlement_quote")
		})
	}
}

func TestTopUpSettlementQuotePreservesFinalRoundingAndSmallSavings(t *testing.T) {
	publicTopUpCreditTestConfig(t, "100000")
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{1: 0.9}
	original, paid := applyTopUpSettlementRatiosWithOriginal(decimal.RequireFromString("100.005"), decimal.NewFromInt(1), "default", decimal.NewFromInt(1))
	// Applying 0.9 after rounding the display original would charge 90.01.
	// The actual preexisting quote rounds only the final payable amount.
	require.Equal(t, "100.01", original.StringFixed(2))
	require.Equal(t, "90.00", paid.StringFixed(2))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	recordTopUpQuoteBasis(c, decimal.NewFromInt(1000), decimal.RequireFromString("999.99"), "USD")
	response := gin.H{"data": "999.99"}
	addTopUpSettlementQuote(c, response, "USD")
	quote := response["settlement_quote"].(gin.H)
	require.Equal(t, "0.01", quote["savings_amount"])
	require.Equal(t, "0.001", quote["discount_percent"])
}
