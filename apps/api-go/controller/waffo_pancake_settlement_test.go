package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestWaffoPancakeExpectedQuoteJSONGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"legacy omitted pair", `{}`, true},
		{"exact quote", `{"settlement_currency":"CNY","settlement_amount":"9.90"}`, true},
		{"equivalent decimal", `{"settlement_currency":"CNY","settlement_amount":"9.9"}`, true},
		{"both null", `{"settlement_currency":null,"settlement_amount":null}`, false},
		{"currency null only", `{"settlement_currency":null}`, false},
		{"amount null only", `{"settlement_amount":null}`, false},
		{"currency omitted", `{"settlement_amount":"9.90"}`, false},
		{"amount omitted", `{"settlement_currency":"CNY"}`, false},
		{"currency null", `{"settlement_currency":null,"settlement_amount":"9.90"}`, false},
		{"amount null", `{"settlement_currency":"CNY","settlement_amount":null}`, false},
		{"empty strings", `{"settlement_currency":"","settlement_amount":""}`, false},
		{"stale currency", `{"settlement_currency":"USD","settlement_amount":"9.90"}`, false},
		{"stale amount", `{"settlement_currency":"CNY","settlement_amount":"9.91"}`, false},
		{"numeric amount", `{"settlement_currency":"CNY","settlement_amount":9.90}`, false},
		{"numeric currency", `{"settlement_currency":123,"settlement_amount":"9.90"}`, false},
		{"object amount", `{"settlement_currency":"CNY","settlement_amount":{}}`, false},
		{"array currency", `{"settlement_currency":[],"settlement_amount":"9.90"}`, false},
		{"exponent", `{"settlement_currency":"CNY","settlement_amount":"99e-1"}`, false},
		{"fractional cent", `{"settlement_currency":"CNY","settlement_amount":"9.901"}`, false},
		{"whitespace", `{"settlement_currency":"CNY","settlement_amount":" 9.90"}`, false},
		{"zero", `{"settlement_currency":"CNY","settlement_amount":"0"}`, false},
		{"negative", `{"settlement_currency":"CNY","settlement_amount":"-9.90"}`, false},
		{"oversized", `{"settlement_currency":"CNY","settlement_amount":"` + strings.Repeat("9", 129) + `"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Exercise both public DTOs: RawMessage distinguishes absent fields
			// from explicit null and defers wrong-type values to the quote guard.
			var subscription SubscriptionWaffoPancakePayRequest
			var topup WaffoPancakePayRequest
			require.NoError(t, json.Unmarshal([]byte(tc.body), &subscription))
			require.NoError(t, json.Unmarshal([]byte(tc.body), &topup))
			amount := decimal.RequireFromString("9.90")
			for _, quote := range [][2]json.RawMessage{
				{subscription.SettlementCurrency, subscription.SettlementAmount},
				{topup.SettlementCurrency, topup.SettlementAmount},
			} {
				response := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(response)
				require.Equal(t, tc.want, requireWaffoPancakeExpectedQuote(c, quote[0], quote[1], "CNY", amount))
				if tc.want {
					require.Empty(t, response.Body.String())
					continue
				}
				require.Equal(t, http.StatusOK, response.Code)
				var payload struct {
					Success bool   `json:"success"`
					Code    string `json:"code"`
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
				require.False(t, payload.Success)
				require.Equal(t, "SETTLEMENT_QUOTE_CHANGED", payload.Code)
			}
		})
	}
}

func TestWaffoPancakeSubscriptionFiatQuotes(t *testing.T) {
	preserveChannelPricing(t)
	operation_setting.TopUpPlatformUnitsPerCNY = 99 // Wallet purchase ratio must never affect a plan's fiat price.
	for _, tc := range []struct {
		name         string
		price        float64
		planCurrency string
		currency     string
		fx           float64
		want         string
	}{
		{"CNY stays CNY", 9.9, "CNY", "CNY", 6.6, "9.90"},
		{"CNY converts to USD", 9.9, "CNY", "USD", 6.6, "1.50"},
		{"empty plan currency means CNY", 9.9, "", "CNY", 6.6, "9.90"},
		{"USD converts to CNY", 1.5, "USD", "CNY", 6.6, "9.90"},
		{"exact cent", 0.01, "CNY", "CNY", 6.8, "0.01"},
		{"round converted amount", 1, "CNY", "USD", 6.8, "0.15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation_setting.USDExchangeRate = tc.fx
			plan := &model.SubscriptionPlan{PriceAmount: tc.price, Currency: tc.planCurrency, WaffoPancakeProductType: model.WaffoPancakeProductTypeOneTime}
			quote := subscriptionWaffoPancakeSettlementQuote(plan, tc.currency)
			require.True(t, quote.Available)
			require.Equal(t, tc.want, quote.Amount)
			require.Equal(t, tc.currency, quote.Currency)
			require.Equal(t, tc.price, plan.PriceAmount)
			require.Equal(t, tc.planCurrency, plan.Currency)
		})
	}
}

func TestWaffoPancakeSubscriptionQuoteUnavailableWithoutRelabeling(t *testing.T) {
	preserveChannelPricing(t)
	operation_setting.USDExchangeRate = 6.6
	operation_setting.TopUpPlatformUnitsPerCNY = 2
	plan := &model.SubscriptionPlan{PriceAmount: 9.9, Currency: "CNY", WaffoPancakeProductType: model.WaffoPancakeProductTypeSubscription}
	quote := subscriptionWaffoPancakeSettlementQuote(plan, "CNY")
	require.False(t, quote.Available)
	require.Equal(t, "9.90", quote.Amount)
	require.Equal(t, "CNY", quote.Currency)
	require.Equal(t, "unsupported_settlement_currency", quote.Reason)

	plan.Currency = "EUR"
	quote = subscriptionWaffoPancakeSettlementQuote(plan, "USD")
	require.False(t, quote.Available)
	require.Empty(t, quote.Amount)
	require.Equal(t, "USD", quote.Currency)
	require.Equal(t, "invalid_settlement_quote", quote.Reason)
	require.Equal(t, "EUR", plan.Currency)
}

func TestWaffoPancakeTopUpCurrencyPreservesPlatformCredits(t *testing.T) {
	preserveChannelPricing(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 100
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	operation_setting.USDExchangeRate = 6.6
	operation_setting.TopUpPlatformUnitsPerCNY = 2
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1}`))

	amount := decimal.RequireFromString("19.8")
	for _, tc := range []struct{ currency, want string }{{"CNY", "9.90"}, {"USD", "1.50"}} {
		quote, err := getWaffoPancakePayMoneyForCurrency(amount, "default", tc.currency)
		require.NoError(t, err)
		require.Equal(t, tc.want, quote.StringFixed(2))
		_, micros, quota, err := topUpOrderAmountsDecimal(amount)
		require.NoError(t, err)
		require.EqualValues(t, 19_800_000, micros)
		require.EqualValues(t, 1980, quota)
	}
	require.Equal(t, "1.50", getWaffoPancakePayMoneyForAmount(amount, "default").StringFixed(2))
	cent, err := getWaffoPancakePayMoneyForCurrency(decimal.RequireFromString("0.02"), "default", "CNY")
	require.NoError(t, err)
	require.Equal(t, "0.01", cent.StringFixed(2))
	_, err = getWaffoPancakePayMoneyForCurrency(amount, "default", "EUR")
	require.Error(t, err)
}

func TestWaffoPancakeSubscriptionCallbackUsesPersistedCurrencyAndAmount(t *testing.T) {
	preserveChannelPricing(t)
	operation_setting.USDExchangeRate = 100 // Deliberately different from checkout-time FX.
	operation_setting.TopUpPlatformUnitsPerCNY = 99
	plan := &model.SubscriptionPlan{Id: 42, PriceAmount: 500, Currency: "USD", WaffoPancakeProductId: "PROD_changed"}
	order := &model.SubscriptionOrder{PlanId: 42, ExpectedAmountMicros: 9_900_000, SettlementCurrency: "CNY", ProviderProductId: "PROD_original", ProviderStoreId: "STO_original"}
	event := &service.WaffoPancakeWebhookEvent{StoreID: "STO_original", Data: service.WaffoPancakeWebhookData{
		Amount: "9.90", Currency: "CNY", OrderMetadata: map[string]string{
			service.WaffoPancakeOrderMetadataProductID: "PROD_original",
			service.WaffoPancakeOrderMetadataPlanID:    "42",
		},
	}}
	require.NoError(t, validateWaffoPancakeSubscriptionEvent(event, order, plan))
	event.Data.Currency = "USD"
	require.ErrorContains(t, validateWaffoPancakeSubscriptionEvent(event, order, plan), "currency mismatch")
	event.Data.Currency = "CNY"
	event.Data.Amount = "500.00"
	require.ErrorContains(t, validateWaffoPancakeSubscriptionEvent(event, order, plan), "amount mismatch")
}
