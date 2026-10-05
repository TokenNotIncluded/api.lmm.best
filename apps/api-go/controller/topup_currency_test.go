package controller

import (
	"bytes"
	"encoding/json"
	"math"
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

func preservePaymentCreditAnchor(t *testing.T, credits string) {
	t.Helper()
	previous, priorErr := common.CreditsPerUSD()
	require.NoError(t, common.SetCreditsPerUSD(decimal.RequireFromString(credits)))
	t.Cleanup(func() {
		if priorErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditsPerUSD(previous))
		}
	})
}

func TestExplicitTopUpUnitsPreserveCreditsAcrossDisplayFXAndLegacyBonus(t *testing.T) {
	preserveChannelPricing(t)
	preservePaymentCreditAnchor(t, "3500000")
	priorQ := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = priorQ })
	operation_setting.USDExchangeRate = 7
	for _, display := range []string{"USD", "CNY", "TOKENS"} {
		operation_setting.GetGeneralSetting().QuotaDisplayType = display
		for _, tc := range []struct {
			amount float64
			unit   string
		}{{1, "USD"}, {7, "CNY"}, {3500000, "CREDIT"}, {7, "LEGACY"}} {
			amount, err := parseTopUpAmountWithUnit(tc.amount, tc.unit)
			require.NoError(t, err)
			require.Equal(t, "7", amount.String())
			_, _, credits, err := topUpOrderAmountsLegacyDecimal(amount)
			require.NoError(t, err)
			require.EqualValues(t, 3500000, credits)
		}
	}
	operation_setting.USDExchangeRate = 8
	operation_setting.TopUpPlatformUnitsPerCNY = 99
	usd, err := parseTopUpAmountWithUnit(1, "USD")
	require.NoError(t, err)
	require.Equal(t, "7", usd.String())
	cny, err := parseTopUpAmountWithUnit(7, "CNY")
	require.NoError(t, err)
	require.Equal(t, "6.125", cny.String())
	_, _, credits, err := topUpOrderAmountsLegacyDecimal(cny)
	require.NoError(t, err)
	require.EqualValues(t, 3062500, credits)
	// Legacy batch 7 keeps the same real USD value; only a CNY cash quote follows FX.
	require.Equal(t, "1", getStripePayMoneyForLegacyAmount(usd, "default").String())
	pancake, err := getWaffoPancakePayMoneyForLegacyCurrency(usd, "default", "CNY")
	require.NoError(t, err)
	require.Equal(t, "8", pancake.String())
}

func TestTopUpCurrencyRoundsFiatDownAndRejectsFractionalUnsafeCredits(t *testing.T) {
	preserveChannelPricing(t)
	preservePaymentCreditAnchor(t, "3500000")
	previousQ := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previousQ })
	operation_setting.USDExchangeRate = 7
	amount, err := parseTopUpAmountWithUnit(0.000001, "USD")
	require.NoError(t, err)
	_, _, credits, err := topUpOrderAmountsLegacyDecimal(amount)
	require.NoError(t, err)
	require.EqualValues(t, 3, credits)
	for _, tc := range []struct {
		amount float64
		unit   string
	}{{1.5, "CREDIT"}, {float64(common.MaxWalletQuota) + 1, "CREDIT"}, {math.MaxFloat64, "USD"}, {1, "EUR"}, {0, "USD"}} {
		_, err := parseTopUpAmountWithUnit(tc.amount, tc.unit)
		require.Error(t, err)
	}
}

func TestEpayQuoteExplicitLegacyIsIndependentOfDisplayAndReturnsCreditSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	confirmPaymentComplianceForTest(t)
	preserveChannelPricing(t)
	preservePaymentCreditAnchor(t, "3500000")
	setupTopupInfoUser(t, 401, "default")
	priorQ, priorMin := common.QuotaPerUnit, operation_setting.MinTopUp
	common.QuotaPerUnit = 500000
	operation_setting.MinTopUp = 1
	t.Cleanup(func() { common.QuotaPerUnit = priorQ; operation_setting.MinTopUp = priorMin })
	operation_setting.GetGeneralSetting().QuotaDisplayType = "TOKENS"
	operation_setting.USDExchangeRate = 7
	priorOptions := operation_setting.GetPaymentSetting().AmountOptions
	operation_setting.GetPaymentSetting().AmountOptions = []int{3500000}
	t.Cleanup(func() { operation_setting.GetPaymentSetting().AmountOptions = priorOptions })
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{3500000: 0.9}
	operation_setting.PayMethods = []map[string]string{{"name": "fixture", "type": "alipay"}}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1}`))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 401)
	c.Request = httptest.NewRequest(http.MethodPost, "/amount", bytes.NewBufferString(`{"amount":7,"amount_unit":"LEGACY","payment_method":"alipay"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	RequestAmount(c)
	var response struct {
		Message, Data, AmountUnit, SettlementCurrency string
		CreditedQuota                                 int64
	}
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	require.NoError(t, json.Unmarshal(raw["message"], &response.Message))
	require.Equal(t, "success", response.Message, w.Body.String())
	require.NoError(t, json.Unmarshal(raw["data"], &response.Data))
	require.Equal(t, "6.30", response.Data)
	require.NoError(t, json.Unmarshal(raw["amount_unit"], &response.AmountUnit))
	require.Equal(t, "LEGACY", response.AmountUnit)
	require.NoError(t, json.Unmarshal(raw["credited_quota"], &response.CreditedQuota))
	require.EqualValues(t, 3500000, response.CreditedQuota)
	require.NoError(t, json.Unmarshal(raw["settlement_currency"], &response.SettlementCurrency))
	require.Equal(t, "CNY", response.SettlementCurrency)
	infoW := httptest.NewRecorder()
	infoC, _ := gin.CreateTestContext(infoW)
	infoC.Set("id", 401)
	infoC.Request = httptest.NewRequest(http.MethodGet, "/topup/info", nil)
	GetTopUpInfo(infoC)
	var info struct {
		Data struct {
			AmountUnit     string             `json:"amount_unit"`
			Options        []int              `json:"amount_options"`
			LegacyUnit     string             `json:"legacy_amount_unit"`
			LegacyOptions  []float64          `json:"legacy_amount_options"`
			LegacyDiscount map[string]float64 `json:"legacy_discount"`
		}
	}
	require.NoError(t, json.Unmarshal(infoW.Body.Bytes(), &info))
	require.Equal(t, "CREDIT", info.Data.AmountUnit)
	require.Equal(t, []int{3500000}, info.Data.Options)
	require.Equal(t, "LEGACY", info.Data.LegacyUnit)
	require.Equal(t, []float64{7}, info.Data.LegacyOptions)
	require.Equal(t, map[string]float64{"7": 0.9}, info.Data.LegacyDiscount)

}

func TestEpayParsingRetainsExplicitUnitForJSONAndForm(t *testing.T) {
	for _, tc := range []struct{ body, contentType string }{{`{"amount":7,"amount_unit":"LEGACY","payment_method":"alipay"}`, "application/json"}, {"amount=7&amount_unit=LEGACY&payment_method=alipay", "application/x-www-form-urlencoded"}} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/checkout", bytes.NewBufferString(tc.body))
		c.Request.Header.Set("Content-Type", tc.contentType)
		var amount float64
		var method, discount, unit string
		require.NoError(t, parsePayRequest(c, &amount, &method, &discount, &unit))
		require.Equal(t, float64(7), amount)
		require.Equal(t, "LEGACY", unit)
		require.Equal(t, "alipay", method)
	}
}

func TestDiscountCodeValidationMatchesExplicitTopUpUnitsAndFractionalBatch(t *testing.T) {
	preserveChannelPricing(t)
	preservePaymentCreditAnchor(t, "3650000")
	setupTopupInfoUser(t, 402, "default")
	require.NoError(t, model.DB.AutoMigrate(&model.DiscountCode{}, &model.DiscountCodeReservation{}))
	priorQ := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = priorQ })
	operation_setting.USDExchangeRate = 7
	operation_setting.GetGeneralSetting().QuotaDisplayType = "TOKENS"
	code := model.DiscountCode{Code: "UNIT10", DiscountPercent: 10, MinAmount: 3500000, Status: model.DiscountCodeStatusEnabled}
	require.NoError(t, model.DB.Create(&code).Error)
	for _, tc := range []struct {
		amount, unit string
		want         bool
	}{
		{"7", "LEGACY", true}, {"7.3", "LEGACY", true}, {"6", "LEGACY", false},
		{"3500000", "CREDIT", true}, {"3500000", "", true}, {"1", "USD", true}, {"1.5", "CREDIT", false},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", 402)
		body := `{"code":"UNIT10","amount":` + tc.amount + `,"amount_unit":"` + tc.unit + `"}`
		c.Request = httptest.NewRequest(http.MethodPost, "/discount-code/validate", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		ValidateDiscountCode(c)
		var response struct {
			Success bool
			Data    struct {
				LegacyMinimum string `json:"legacy_min_amount"`
				MinimumUnit   string `json:"min_amount_unit"`
			}
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, tc.want, response.Success, body+" "+w.Body.String())
		if tc.want {
			require.Equal(t, "7", response.Data.LegacyMinimum)
			require.Equal(t, "CREDIT", response.Data.MinimumUnit)
		}
	}
}

func TestResolvedTopUpCreditsAreNotLimitedByLegacyBatchProjection(t *testing.T) {
	preserveChannelPricing(t)
	preservePaymentCreditAnchor(t, "3500000")
	priorQ := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = priorQ })
	for _, tc := range []struct {
		q, amount               float64
		credits, stored, micros int64
	}{{300000, 1000000, 1000000, 3, 3333333}, {5000000, 1, 1, 0, 0}} {
		common.QuotaPerUnit = tc.q
		resolved, err := resolveTopUpAmount(tc.amount, "CREDIT")
		require.NoError(t, err)
		stored, micros, credits, err := topUpOrderAmountsResolved(resolved)
		require.NoError(t, err)
		require.Equal(t, tc.credits, credits)
		require.Equal(t, tc.stored, stored)
		require.Equal(t, tc.micros, micros)
		operation_setting.GetGeneralSetting().QuotaDisplayType = "TOKENS"
		_, _, legacyCredits, err := topUpOrderAmountsDecimal(decimal.NewFromFloat(tc.amount))
		require.NoError(t, err)
		require.Equal(t, tc.credits, legacyCredits)
	}
}

func TestPaymentMethodCatalogDeclaresLimitsAndCanonicalBatchAliases(t *testing.T) {
	preserveChannelPricing(t)
	preservePaymentCreditAnchor(t, "3400000")
	priorQ := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = priorQ })
	operation_setting.GetGeneralSetting().QuotaDisplayType = "TOKENS"
	operation_setting.PayMethods = []map[string]string{{"name": "Fiat", "type": "custom", "min_topup": "1", "max_topup": "2.5", "settlement_currency": "USD"}}
	catalog := sanitizedPaymentMethods(operation_setting.PayMethods)
	require.Len(t, catalog, 1)
	require.Equal(t, "USD", catalog[0]["min_topup_unit"])
	require.Equal(t, "6.8", catalog[0]["legacy_min_topup"])
	require.Equal(t, "CREDIT", catalog[0]["max_topup_amount_unit"])
	require.Equal(t, "8500000", catalog[0]["max_topup_amount"])
	require.Equal(t, "17", catalog[0]["legacy_max_topup_amount"])
	auto := sanitizedPaymentMethods([]map[string]string{{"name": "Auto", "type": "stripe", "min_topup": "2"}})
	require.Len(t, auto, 1)
	require.Equal(t, "LEGACY", auto[0]["min_topup_unit"])
	require.Equal(t, "2", auto[0]["legacy_min_topup"])
}

func TestTopUpWireRejectsFractionalCreditBeforeFloatRounding(t *testing.T) {
	preserveChannelPricing(t)
	preservePaymentCreditAnchor(t, "3500000")
	for _, tc := range []struct {
		raw   string
		valid bool
	}{{"9007199254740991", true}, {"9007199254740991.1", false}, {"9007199254740992", false}, {"1.5", false}, {"1000000", true}} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/amount", bytes.NewBufferString(`{"amount":`+tc.raw+`,"amount_unit":"CREDIT"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		var req AmountRequest
		require.NoError(t, bindTopUpRequest(c, &req))
		amount, err := resolveTopUpRequestAmount(c, req.Amount, req.AmountUnit)
		if tc.valid {
			require.NoError(t, err)
			if tc.raw == "9007199254740991" {
				require.EqualValues(t, 9007199254740991, amount.CreditedQuota)
			}
		} else {
			require.Error(t, err)
		}
	}
}
