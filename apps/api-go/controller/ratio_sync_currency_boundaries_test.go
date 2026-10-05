package controller

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/stretchr/testify/require"
)

func TestRatioSyncCurrencyRejectsDeclaredInvalidUnits(t *testing.T) {
	for _, metadata := range []string{
		`"pricing_schema_version":3,"pricing_currency":"USD"`,
		`"pricing_schema_version":2,"schema_version":1,"pricing_currency":"USD"`,
		`"pricing_schema_version":2,"pricing_currency":"CNY"`,
		`"pricing_schema_version":2,"pricing_currency":"USD","currency":"legacy_pricing_unit"`,
		`"pricing_schema_version":2,"pricing_currency":"USD","pricing_storage_basis":"unknown"`,
		`"pricing_schema_version":2,"pricing_currency":"USD","credits_per_usd":0`,
		`"pricing_schema_version":1,"pricing_currency":"legacy_pricing_unit","credits_per_usd":3000000,"quota_per_unit":500000,"legacy_pricing_units_per_usd":1`,
		`"pricing_schema_version":1,"pricing_currency":"legacy_pricing_unit","credits_per_usd":3000000,"quota_per_unit":-500000`,
		`"pricing_currency":"USD","credits_per_usd":3000000,"quota_per_unit":500000`,
		`"pricing_schema_version":1,"pricing_currency":"USD","credits_per_usd":3000000,"quota_per_unit":500000`,
		`"pricing_schema_version":1,"storage_basis":"USD","credits_per_usd":3000000,"quota_per_unit":500000`,
	} {
		t.Run(metadata, func(t *testing.T) {
			_, err := decodeUpstreamPricingData([]byte(`{"success":true,`+metadata+`,"data":{"model_ratio":{"quote":2}}}`), 3359744)
			require.Error(t, err)
		})
	}
	for _, raw := range []string{
		`{"model_ratio":{"quote":-1}}`,
		`{"model_ratio":{"quote":1e999}}`,
		`{"model_ratio":{"quote":2},"completion_ratio":{"quote":-1}}`,
		`{"model_price":{"quote":-0.01}}`,
		`{"model_price":{"quote":1e-400}}`,
		`{"cache_ratio":{"quote":"0.4"}}`,
		`{"billing_expr":{"quote":"v1:tier(\"negative\", -p)"}}`,
	} {
		_, err := decodeUpstreamPricingData([]byte(`{"success":true,"data":`+raw+`}`), 3359744)
		require.Error(t, err, raw)
	}
}

func TestRatioSyncCurrencyRowInheritsExplicitLegacyBasis(t *testing.T) {
	// Row schema metadata must inherit the envelope's quota_per_unit alias,
	// rather than falling back to the target's different legacy scale.
	data, err := decodeUpstreamPricingData([]byte(`{"success":true,"pricing_schema_version":1,"pricing_currency":"legacy_pricing_unit","credits_per_usd":3000000,"quota_per_unit":500000,"data":[{"model_name":"quote","quota_type":1,"model_price":0.06,"pricing_schema_version":1}]}`), 3359744)
	require.NoError(t, err)
	require.Equal(t, 0.01, valueMap(data["model_price"])["quote"])
}

func TestRatioSyncCurrencyCanonicalAbsoluteRatesPreserveSettlement(t *testing.T) {
	data, err := decodeUpstreamPricingData([]byte(`{"success":true,"data":[{"model_name":"quote","quota_type":0,"pricing_schema_version":2,"pricing_currency":"USD","input_price":4,"output_price":20,"cache_read_price":0.4,"cache_write_price":5,"image_price":8,"audio_input_price":32,"audio_output_price":64}]}`), 3359744)
	require.NoError(t, err)
	for field, amount := range map[string]float64{
		"model_ratio": 13.438976, "completion_ratio": 5, "cache_ratio": 0.1,
		"create_cache_ratio": 1.25, "image_ratio": 2, "audio_ratio": 8, "audio_completion_ratio": 2,
	} {
		require.Equal(t, amount, valueMap(data[field])["quote"], field)
	}
	for _, prices := range []string{
		`"input_price":0,"output_price":20,"completion_ratio":5`,
		`"input_price":4,"output_price":20,"completion_ratio":4`,
		`"input_price":4,"output_price":20,"cache_read_price":-1`,
		`"input_price":4,"output_price":20,"audio_input_price":32,"audio_output_price":64,"audio_completion_ratio":16`,
		`"input_price":1e-400,"output_price":0,"completion_ratio":0`,
		`"input_price":4,"output_price":20,"cache_read_price":1e-400`,
	} {
		_, err := decodeUpstreamPricingData([]byte(`{"success":true,"data":[{"model_name":"quote","quota_type":0,"pricing_schema_version":2,"pricing_currency":"USD",`+prices+`}]}`), 3359744)
		require.Error(t, err, prices)
	}
	free, err := decodeUpstreamPricingData([]byte(`{"success":true,"data":{"model_price":{"quote":0e-400}}}`), 3359744)
	require.NoError(t, err)
	require.Equal(t, 0.0, valueMap(free["model_price"])["quote"])
}

func TestRatioSyncCurrencyTinyDifferencesRemainSelectable(t *testing.T) {
	require.False(t, nearlyEqual(0, math.SmallestNonzeroFloat64))
	require.False(t, nearlyEqual(1e-14, 2e-14))
	require.False(t, nearlyEqual(math.MaxFloat64, 1))
	require.True(t, nearlyEqual(4, math.Nextafter(4, math.Inf(1))))
	differences := buildDifferences(map[string]any{"model_price": map[string]any{"micro": 0.0}}, []struct {
		name string
		data map[string]any
	}{{name: "source", data: map[string]any{"model_price": map[string]any{"micro": 1e-14}}}})
	require.Equal(t, 1e-14, differences["micro"]["model_price"].Upstreams["source"])
}

func TestRatioSyncCurrencyPartialShapePreservesWholeLocalModel(t *testing.T) {
	old := `v1:len <= 272000 ? tier("short", (p*4+c*20+cr_text*.4+img*5+cr_img*.5+audio_s*.075)/1000000) : tier("long", (p*8+c*30+cr_text*.8+img*10+cr_img+audio_s*.15)/1000000)`
	local := map[string]any{
		billing_setting.BillingModeField: map[string]any{"quote": billing_setting.BillingModeTieredExpr},
		billing_setting.BillingExprField: map[string]any{"quote": old},
	}
	for _, expr := range []string{"", `v1:len <= 272000 ? tier("short", (p*4+c*20+cr_text*.4+audio_s*.075)/1000000) : tier("long", (p*8+c*30+cr_text*.8+audio_s*.15)/1000000)`} {
		upstream := map[string]any{
			"model_ratio": map[string]any{"quote": 13.438976}, "cache_ratio": map[string]any{"quote": 0.1},
			billing_setting.BillingModeField: map[string]any{"quote": billing_setting.BillingModeTieredExpr},
			billing_setting.BillingExprField: map[string]any{"quote": expr},
		}
		protectPricingSyncShapes(local, upstream)
		for _, field := range pricingSyncFields {
			require.NotContains(t, valueMap(upstream[field]), "quote", field)
		}
		require.NotEmpty(t, valueMap(upstream[syncSkippedModels])["quote"])
		require.Equal(t, old, valueMap(local[billing_setting.BillingExprField])["quote"])
	}
}

func TestRatioSyncCurrencyModelsDevRequiresProviderBeforeNetwork(t *testing.T) {
	ratioSyncCurrencyFixture(t, 3359744)
	for _, endpoint := range []string{"/api.json", "/api.json?provider=", "/api.json?provider=openai&provider=anthropic"} {
		w := ratioSyncRunHandler(t, http.MethodPost, "/api/ratio_sync/fetch", map[string]any{
			"upstreams": []map[string]any{{"name": "models.dev", "base_url": "https://models.dev", "endpoint": endpoint}},
		}, FetchUpstreamRatios)
		require.Equal(t, http.StatusOK, w.Code)
		var response struct {
			Success bool `json:"success"`
			Data    struct {
				Differences map[string]any                   `json:"differences"`
				Results     []struct{ Status, Error string } `json:"test_results"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.True(t, response.Success)
		require.Empty(t, response.Data.Differences)
		require.Len(t, response.Data.Results, 1)
		require.Equal(t, "error", response.Data.Results[0].Status)
		require.Equal(t, "models.dev requires an explicit provider query parameter", response.Data.Results[0].Error)
	}
}

func TestRatioSyncCurrencyOrphanBillingContractsAreNotSelectable(t *testing.T) {
	for _, maps := range []string{
		`"billing_mode":{"quote":"tiered_expr"}`,
		`"billing_expr":{"quote":"v1:tier(\"quote\",p*4)"}`,
	} {
		data, err := decodeUpstreamPricingData([]byte(`{"success":true,"data":{"model_ratio":{"quote":2},`+maps+`}}`), 3359744)
		require.NoError(t, err)
		for _, field := range pricingSyncFields {
			require.NotContains(t, valueMap(data[field]), "quote", field)
		}
		require.NotEmpty(t, valueMap(data[syncSkippedModels])["quote"])
	}
	old := `v1:tier("minute", audio_s*.05+cr_text*.4)`
	local := map[string]any{
		"billing_mode": map[string]any{"quote": "tiered_expr"}, "billing_expr": map[string]any{"quote": old},
	}
	inactive := map[string]any{
		"model_ratio": map[string]any{"quote": 2.0}, "billing_mode": map[string]any{"quote": "ratio"},
		"billing_expr": map[string]any{"quote": old},
	}
	protectPricingSyncShapes(local, inactive)
	require.NotContains(t, valueMap(inactive["model_ratio"]), "quote")
	require.NotContains(t, valueMap(inactive["billing_mode"]), "quote")
	require.NotEmpty(t, valueMap(inactive[syncSkippedModels])["quote"])
}

func TestRatioSyncCurrencyBridgeRejectsPositiveRoundingToFree(t *testing.T) {
	db := ratioSyncCurrencyFixture(t, 3359744)
	fetched := ratioSyncFetchCurrency(t, map[string]any{
		"success": true, "pricing_schema_version": 2, "pricing_currency": "USD",
		"data": map[string]any{"model_price": map[string]float64{"small": 1e-100, "free": 0}},
	})
	require.Equal(t, 1e-100, fetched.Data.Differences["small"]["model_price"].Upstreams["source"])
	before := ratioSyncStoredOptions(t, db)
	w := ratioSyncRunHandler(t, http.MethodPost, "/api/option/pricing/bulk", ratioSyncCurrencySelection(t, fetched, "small"), USDPriceOptionsBulk)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "outside the representable range")
	require.Equal(t, before, ratioSyncStoredOptions(t, db), "rejected positive price must leave every option unchanged")
	w = ratioSyncRunHandler(t, http.MethodPost, "/api/option/pricing/bulk", ratioSyncCurrencySelection(t, fetched, "free"), USDPriceOptionsBulk)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	config, err := model.GetUSDPriceConfig()
	require.NoError(t, err)
	var prices map[string]float64
	require.NoError(t, json.Unmarshal([]byte(config.Values["ModelPrice"]), &prices))
	require.Equal(t, 0.0, prices["free"])
	// A previously stored positive rate outside the bridge domain must make
	// export unavailable, rather than misrepresenting an existing price as free.
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "ModelPrice").Update("value", `{"small":1e-100}`).Error)
	_, err = model.GetUSDPriceConfig()
	require.ErrorContains(t, err, "outside the representable range")
}

func TestRatioSyncCurrencyInactiveExpressionNeverChangesActiveMode(t *testing.T) {
	db := ratioSyncCurrencyFixture(t, 3359744)
	oldExpr := `v1:tier("inactive_old", p*100)`
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"billing_setting.billing_mode": `{"quote":"ratio","keep-expr":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"quote":"v1:tier(\"inactive_old\", p*100)","keep-expr":"v1:tier(\"keep\", p*1.234500)"}`,
	}))
	fetched := ratioSyncFetchCurrency(t, map[string]any{
		"success": true, "data": map[string]any{
			"model_ratio": map[string]float64{"quote": 2}, "completion_ratio": map[string]float64{"quote": 5},
			"billing_mode": map[string]string{"quote": "ratio"},
			"billing_expr": map[string]string{"quote": `v1:tier("inactive_new", audio_s*.05+cr_text*.4)`},
		},
	})
	require.NotContains(t, fetched.Data.Differences["quote"], "billing_expr", "retained inactive source text must not become a selectable tariff")
	update := ratioSyncCurrencySelection(t, fetched, "quote")
	w := ratioSyncRunHandler(t, http.MethodPost, "/api/option/pricing/bulk", update, USDPriceOptionsBulk)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	stored := ratioSyncStoredOptions(t, db)
	var modes, exprs map[string]string
	require.NoError(t, json.Unmarshal([]byte(stored["billing_setting.billing_mode"]), &modes))
	require.NoError(t, json.Unmarshal([]byte(stored["billing_setting.billing_expr"]), &exprs))
	require.Equal(t, "ratio", modes["quote"])
	require.Equal(t, oldExpr, exprs["quote"], "the local inactive raw snapshot is preserved")
}

func TestRatioSyncCurrencyOpenRouterExtraChargesCannotBecomeFreeOrPartial(t *testing.T) {
	for _, quote := range []string{
		`"prompt":"0","completion":"0","request":"0.05"`,
		`"prompt":"0.000004","completion":"0.00002","image":"0.01"`,
		`"prompt":"0.000004","completion":"0.00002","audio":"0.01"`,
	} {
		data, err := convertOpenRouterToRatioDataWithAnchor(strings.NewReader(`{"data":[{"id":"quote","pricing":{`+quote+`}}]}`), 3359744)
		require.NoError(t, err)
		for _, field := range pricingSyncFields {
			require.NotContains(t, valueMap(data[field]), "quote", field)
		}
		require.NotEmpty(t, valueMap(data[syncSkippedModels])["quote"])
	}
	free, err := convertOpenRouterToRatioDataWithAnchor(strings.NewReader(`{"data":[{"id":"free","pricing":{"prompt":"0","completion":"0","request":"0","image":"0"}}]}`), 3359744)
	require.NoError(t, err)
	require.Equal(t, 0.0, valueMap(free["model_ratio"])["free"])
}

func TestRatioSyncCurrencyDuplicateRowsCannotBlendBillingContracts(t *testing.T) {
	_, err := decodeUpstreamPricingData([]byte(`{"success":true,"data":[{"model_name":"quote","billing_mode":"tiered_expr","billing_expr":"v1:tier(\"minute\",audio_s*.05)"},{"model_name":"quote","quota_type":0,"model_ratio":2,"completion_ratio":5}]}`), 3359744)
	require.ErrorContains(t, err, "duplicate upstream model pricing row")
}
