package controller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestRatioSyncFixedDenominationKeepsFiveMillionCreditsAcrossFX(t *testing.T) {
	db := ratioSyncCurrencyFixture(t, 500000)
	oldFX := operation_setting.USDExchangeRate
	t.Cleanup(func() { operation_setting.USDExchangeRate = oldFX })
	payload := map[string]any{"success": true, "pricing_schema_version": 2, "pricing_currency": "USD", "data": []map[string]any{
		{"model_name": "fixed-token", "quota_type": 0, "input_price": 10, "output_price": 20},
		{"model_name": "fixed-call", "quota_type": 1, "model_price": 10},
	}}
	fetched := ratioSyncFetchCurrency(t, payload)
	update := ratioSyncCurrencySelection(t, fetched, "fixed-token", "fixed-call")
	update.Values[operation_setting.ToolPriceOptionKey] = `{"web_search":10}`
	_, _, err := model.UpdateUSDPriceConfig(update)
	require.NoError(t, err)
	stored := ratioSyncStoredOptions(t, db)
	base, err := model.GetUSDPriceConfig()
	require.NoError(t, err)
	for _, fx := range []float64{1, 6.7, 6.8, 13} {
		operation_setting.USDExchangeRate = fx
		current, err := model.GetUSDPriceConfig()
		require.NoError(t, err)
		require.Equal(t, base, current, "FX must not alter prices or their snapshot revision")
		require.Equal(t, stored, ratioSyncStoredOptions(t, db))
		ratio, exists, _ := ratio_setting.GetModelRatio("fixed-token")
		require.True(t, exists)
		require.Equal(t, 5.0, ratio)
		require.Equal(t, 5000000.0, ratio*1000000)
		for _, key := range []string{"ModelPrice", operation_setting.ToolPriceOptionKey} {
			var values map[string]float64
			require.NoError(t, json.Unmarshal([]byte(stored[key]), &values))
			name := "fixed-call"
			if key == operation_setting.ToolPriceOptionKey {
				name = "web_search"
			}
			require.Equal(t, 10.0, values[name])
			credits, err := common.USDToLegacyAmount(decimal.NewFromFloat(values[name]))
			require.NoError(t, err)
			require.Equal(t, "5000000", credits.Mul(decimal.NewFromInt(500000)).String())
		}
		dev, skipped, err := convertModelsDevCanonicalData(strings.NewReader(canonicalModelsDevFixture(`{"input":10,"output":20}`)), "vendor", 500000)
		require.NoError(t, err)
		require.Empty(t, skipped)
		expr := dev["billing_expr"].(map[string]any)["example"].(string)
		result, err := billingexpr.ComputeTieredQuota(&billingexpr.BillingSnapshot{ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), QuotaPerUnit: 500000, GroupRatio: 1, ExprVersion: 1}, billingexpr.TokenParams{P: 1000000, Len: 1000000})
		require.NoError(t, err)
		require.EqualValues(t, 5000000, result.ActualQuotaAfterGroup)
		or, err := convertOpenRouterToRatioData(strings.NewReader(`{"data":[{"id":"or","pricing":{"prompt":"0.00001","completion":"0.00002"}}]}`))
		require.NoError(t, err)
		require.Equal(t, 5.0, or["model_ratio"].(map[string]any)["or"])
	}
}
