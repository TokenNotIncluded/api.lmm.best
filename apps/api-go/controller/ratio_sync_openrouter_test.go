package controller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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

func TestOpenRouterUsesDurableCreditAnchorForUSDPrices(t *testing.T) {
	ratioSyncCurrencyFixture(t, 3000000)
	result, err := convertOpenRouterToRatioData(strings.NewReader(`{"data":[
		{"id":"usd-model","pricing":{"prompt":"0.000002","completion":"0.000006","input_cache_read":"0.000001"}}
	]}`))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"model_ratio":      map[string]any{"usd-model": 6.0},
		"completion_ratio": map[string]any{"usd-model": 3.0},
		"cache_ratio":      map[string]any{"usd-model": 0.5},
	}, result, "absolute USD prices use the local credit anchor; relative token multipliers do not")
	_, err = json.Marshal(result)
	require.NoError(t, err)
}
