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
