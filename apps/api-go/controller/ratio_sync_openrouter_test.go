package controller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenRouterRejectsIncompletePriceRatios(t *testing.T) {
	for _, completion := range []string{"1e308", "1e300"} {
		t.Run(completion, func(t *testing.T) {
			result, err := convertOpenRouterToRatioData(strings.NewReader(`{"data":[
				{"id":"valid","pricing":{"prompt":"0.000002","completion":"0.000006"}},
				{"id":"overflow","pricing":{"prompt":"0.000002","completion":"` + completion + `","input_cache_read":"0.000001"}}
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
	result, err := convertOpenRouterToRatioData(strings.NewReader(`{"data":[{"id":"free","pricing":{"prompt":"0","completion":"0"}}]}`))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"model_ratio": map[string]any{"free": 0.0}}, result)
}

// Unknown prices must not become free quotes, and non-finite input must not
// reach the result where it would fail JSON serialization.
func TestOpenRouterRejectsUnusablePrices(t *testing.T) {
	cases := []struct {
		name          string
		pricing       string
		wantRejected  bool
		wantCacheOmit bool
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
			name:          "infinite cache price keeps the model without a cache ratio",
			pricing:       `{"prompt":"0.000002","completion":"0.000006","input_cache_read":"+Inf"}`,
			wantCacheOmit: true,
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
			if testCase.wantCacheOmit {
				require.Equal(t, map[string]any{
					"model_ratio":      map[string]any{"audit-model": 1.0},
					"completion_ratio": map[string]any{"audit-model": 3.0},
				}, result)
			}
			_, err = json.Marshal(result)
			require.NoError(t, err)
		})
	}
}

// A negative price is a dynamic-pricing sentinel, not a free quote.
func TestOpenRouterKeepsNegativeSentinelOutOfResults(t *testing.T) {
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
