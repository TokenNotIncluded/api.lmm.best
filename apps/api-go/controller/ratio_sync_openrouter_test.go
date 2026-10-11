package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenRouterStructuredPricingDoesNotAbortOtherModels(t *testing.T) {
	for _, pricing := range []string{
		`{"prompt":"0.000002","completion":"0.000006","overrides":[{"min_prompt_tokens":272000,"prompt":"0.000004","completion":"0.000015"}]}`,
		`{"prompt":"0.000002","completion":"0.000006","future_dimension":{"price":"0.01"}}`,
		`{"prompt":["0.000002","0.000004"],"completion":"0.000006"}`,
		`{"prompt":"0.000002","completion":0.000006}`,
		`{"prompt":"0.000002","completion":"0.000006","input_cache_read":null}`,
	} {
		t.Run(pricing, func(t *testing.T) {
			result, err := convertOpenRouterToRatioDataWithAnchor(strings.NewReader(`{"data":[
				{"id":"structured","pricing":`+pricing+`},
				{"id":"valid","pricing":{"prompt":"0.000002","completion":"0.000006","input_cache_read":"0.000001"}},
				{"id":"free","pricing":{"prompt":"0","completion":"0"}}
			]}`), 500000)
			require.NoError(t, err)
			require.Equal(t, map[string]any{"valid": 1.0, "free": 0.0}, result["model_ratio"])
			require.Equal(t, map[string]any{"valid": 3.0}, result["completion_ratio"])
			require.Equal(t, map[string]any{"valid": 0.5}, result["cache_ratio"])
			require.Contains(t, valueMap(result[syncSkippedModels])["structured"], "unsupported pricing value")
			for _, field := range pricingSyncFields {
				require.NotContains(t, valueMap(result[field]), "structured", field)
			}
		})
	}
}

func TestOpenRouterInvalidPricingObjectsAreIsolated(t *testing.T) {
	for _, pricing := range []string{`null`, `[]`, `"0"`, `42`, `true`} {
		t.Run(pricing, func(t *testing.T) {
			result, err := convertOpenRouterToRatioDataWithAnchor(strings.NewReader(`{"data":[
				{"id":"invalid","pricing":`+pricing+`},
				{"id":"valid","pricing":{"prompt":"0.000002","completion":"0.000006"}}
			]}`), 500000)
			require.NoError(t, err)
			require.Equal(t, map[string]any{"valid": 1.0}, result["model_ratio"])
			require.Equal(t, map[string]any{"valid": 3.0}, result["completion_ratio"])
			require.Equal(t, "OpenRouter quote has an invalid pricing object", valueMap(result[syncSkippedModels])["invalid"])
		})
	}
	_, err := convertOpenRouterToRatioDataWithAnchor(strings.NewReader(`{"data":[`), 500000)
	require.ErrorContains(t, err, "failed to decode OpenRouter response")
}

func TestOpenRouterSyncReportsStructuredQuotesWithoutChangingPrices(t *testing.T) {
	db := ratioSyncCurrencyFixture(t, 500000)
	before := ratioSyncStoredOptions(t, db)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "Bearer synthetic-openrouter-sync-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"structured","pricing":{"prompt":"0.000002","completion":"0.00001","overrides":[{"min_prompt_tokens":272000,"prompt":"0.000004"}]}},
			{"id":"valid","pricing":{"prompt":"0.000002","completion":"0.000006"}}
		]}`))
	}))
	defer source.Close()
	baseURL := source.URL
	channel := model.Channel{Name: "openrouter-fixture", Type: 20, Status: 1, Key: "synthetic-openrouter-sync-key", BaseURL: &baseURL}
	require.NoError(t, db.Create(&channel).Error)
	w := ratioSyncRunHandler(t, http.MethodPost, "/api/ratio_sync/fetch", map[string]any{
		"timeout": 2, "upstreams": []map[string]any{{"id": channel.Id, "name": channel.Name, "base_url": source.URL, "endpoint": "openrouter"}},
	}, FetchUpstreamRatios)
	require.Equal(t, http.StatusOK, w.Code)
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Differences map[string]any `json:"differences"`
			Results     []struct {
				Status        string            `json:"status"`
				SkippedModels map[string]string `json:"skipped_models"`
			} `json:"test_results"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.True(t, result.Success, w.Body.String())
	require.Contains(t, result.Data.Differences, "valid")
	require.NotContains(t, result.Data.Differences, "structured")
	require.Len(t, result.Data.Results, 1)
	require.Equal(t, "success", result.Data.Results[0].Status)
	require.Contains(t, result.Data.Results[0].SkippedModels["structured"], "overrides")
	require.Equal(t, before, ratioSyncStoredOptions(t, db), "fetching automatic sync prices remains read-only")
}

func TestOpenRouterRejectsIncompletePriceRatios(t *testing.T) {
	ratioSyncCurrencyFixture(t, 500000)
	for _, prices := range []struct{ prompt, completion string }{
		{"0.000002", "1e308"},
		{"0.0000000001", "1e300"},
	} {
		t.Run(prices.completion, func(t *testing.T) {
			result, err := convertOpenRouterToRatioData(strings.NewReader(`{"data":[
				{"id":"valid","pricing":{"prompt":"0.000002","completion":"0.000006"}},
				{"id":"overflow","pricing":{"prompt":"` + prices.prompt + `","completion":"` + prices.completion + `","input_cache_read":"0.000001"}}
			]}`))
			require.NoError(t, err)
			for category, raw := range result {
				require.NotContains(t, raw.(map[string]any), "overflow", category)
			}
			require.Equal(t, map[string]any{
				"model_ratio":      map[string]any{"valid": 1.0},
				"completion_ratio": map[string]any{"valid": 3.0},
			}, result)
			_, err = json.Marshal(result)
			require.NoError(t, err)
		})
	}
}

func TestOpenRouterExplicitFreePricesRemainSupported(t *testing.T) {
	ratioSyncCurrencyFixture(t, 500000)
	result, err := convertOpenRouterToRatioData(strings.NewReader(`{"data":[{"id":"free","pricing":{"prompt":"0","completion":"0"}}]}`))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"model_ratio": map[string]any{"free": 0.0}}, result)
}

// Unknown prices must not become free quotes, and non-finite input must not
// reach the result where it would fail JSON serialization.
func TestOpenRouterRejectsUnusablePrices(t *testing.T) {
	ratioSyncCurrencyFixture(t, 500000)
	cases := []struct {
		name         string
		pricing      string
		wantRejected bool
	}{
		{
			name:         "missing completion price is not free",
			pricing:      `{"prompt":"0.000002"}`,
			wantRejected: true,
		},
		{
			name:         "non-numeric completion price is not free",
			pricing:      `{"prompt":"0","completion":"not-a-price"}`,
			wantRejected: true,
		},
		{
			name:         "NaN prompt price",
			pricing:      `{"prompt":"NaN","completion":"0.000006"}`,
			wantRejected: true,
		},
		{
			name:         "infinite completion price",
			pricing:      `{"prompt":"0.000002","completion":"+Inf"}`,
			wantRejected: true,
		},
		{
			name:         "infinite cache price cannot create a partial quote",
			pricing:      `{"prompt":"0.000002","completion":"0.000006","input_cache_read":"+Inf"}`,
			wantRejected: true,
		},
		{
			name:         "underflowed input price cannot become free",
			pricing:      `{"prompt":"1e-400","completion":"0"}`,
			wantRejected: true,
		},
		{
			name:         "underflowed cache price cannot become free",
			pricing:      `{"prompt":"0.000002","completion":"0.000006","input_cache_read":"1e-400"}`,
			wantRejected: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := convertOpenRouterToRatioData(strings.NewReader(
				`{"data":[{"id":"audit-model","pricing":` + testCase.pricing + `}]}`,
			))
			require.NoError(t, err)

			if testCase.wantRejected {
				require.Empty(t, result)
			}
			_, err = json.Marshal(result)
			require.NoError(t, err)
		})
	}
}

// A negative price is a dynamic-pricing sentinel, not a free quote.
func TestOpenRouterKeepsNegativeSentinelOutOfResults(t *testing.T) {
	ratioSyncCurrencyFixture(t, 500000)
	result, err := convertOpenRouterToRatioData(strings.NewReader(`{"data":[
		{"id":"dynamic","pricing":{"prompt":"-1","completion":"-1"}},
		{"id":"valid","pricing":{"prompt":"0.000002","completion":"0.000006"}}
	]}`))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"model_ratio":      map[string]any{"valid": 1.0},
		"completion_ratio": map[string]any{"valid": 3.0},
	}, result)
}

func TestOpenRouterUsesFixedCreditAnchorForUSDPrices(t *testing.T) {
	ratioSyncCurrencyFixture(t, 500000)
	result, err := convertOpenRouterToRatioData(strings.NewReader(`{"data":[
		{"id":"usd-model","pricing":{"prompt":"0.000002","completion":"0.000006","input_cache_read":"0.000001"}}
	]}`))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"model_ratio":      map[string]any{"usd-model": 1.0},
		"completion_ratio": map[string]any{"usd-model": 3.0},
		"cache_ratio":      map[string]any{"usd-model": 0.5},
	}, result, "absolute USD prices use the fixed credit anchor; relative token multipliers do not")
	_, err = json.Marshal(result)
	require.NoError(t, err)
}
