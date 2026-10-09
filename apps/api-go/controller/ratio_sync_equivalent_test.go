package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRatioSyncEquivalentExpressionsAreNotChanges(t *testing.T) {
	local := map[string]any{
		"billing_mode": map[string]any{"quote": "tiered_expr"},
		"billing_expr": map[string]any{"quote": `(tier("standard", p * 2 + cr * 0.1 + c * 10)) / (1)`},
		"model_ratio":  map[string]any{"quote": 999.0},
		"model_price":  map[string]any{"quote": 1.0},
	}
	upstream := map[string]any{
		"billing_mode": map[string]any{"quote": "tiered_expr"},
		"billing_expr": map[string]any{"quote": `tier("default", c * 10 + p * 2 + cr * 0.10)`},
		"model_ratio":  map[string]any{"quote": 2.0},
		"cache_ratio":  map[string]any{"quote": 0.05},
	}
	channels := []struct {
		name string
		data map[string]any
	}{{"reference", upstream}}
	require.Empty(t, buildDifferences(local, channels), "no artificial mode/expression diff is offered")
	upstream["billing_expr"] = map[string]any{"quote": `tier("default", p * 2 + cr * 0.2 + c * 10)`}
	differences := buildDifferences(local, channels)
	require.Len(t, differences, 1)
	require.Contains(t, differences["quote"], "billing_expr")
	require.Contains(t, differences["quote"], "billing_mode", "real price changes remain an atomic mode/expression pair")
}

func TestRatioSyncProtectsImageOutputAndBillingConditions(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
		blocked             bool
	}{
		{"image output removed", `tier("base", p * 5 + img * 8 + img_o * 30)`, `tier("base", p * 5 + img * 8)`, true},
		{"time condition removed", `hour("UTC") < 4 ? tier("peak", p * 0.3 + cr * 0.006 + c * 1.2) : tier("off", p * 0.15 + cr * 0.003 + c * 0.6)`, `tier("standard", p * 0.15 + cr * 0.003 + c * 0.6)`, true},
		{"timezone changed", `hour("UTC") < 4 ? p * 2 : p`, `hour("Asia/Shanghai") < 4 ? p * 2 : p`, true},
		{"threshold changed", `len <= 200000 ? p * 2 : p * 4`, `len <= 272000 ? p * 2 : p * 4`, true},
		{"header condition removed", `tier("base", p * 2) * (header("x-mode") == "fast" ? 2 : 1)`, `tier("base", p * 2)`, true},
		{"calendar removed", `date("Asia/Shanghai") == 20261001 ? p : p * 2`, `p * 2`, true},
		{"price changed within same conditions", `len <= 200000 ? p * 2 : p * 4`, `len <= 200000 ? p * 3 : p * 6`, false},
		{"tier label and term order only", `len <= 200000 ? tier("base", p * 2 + c * 10) : tier("long", p * 4 + c * 15)`, `len <= 200000 ? tier("renamed", c * 10 + p * 2) : tier("renamed-long", c * 15 + p * 4)`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := map[string]any{"billing_mode": map[string]any{"quote": "tiered_expr"}, "billing_expr": map[string]any{"quote": tc.before}}
			upstream := map[string]any{"billing_mode": map[string]any{"quote": "tiered_expr"}, "billing_expr": map[string]any{"quote": tc.after}}
			protectPricingSyncShapes(local, upstream)
			_, present := valueMap(upstream["billing_expr"])["quote"]
			require.Equal(t, !tc.blocked, present)
			if tc.blocked {
				require.NotEmpty(t, valueMap(upstream[syncSkippedModels])["quote"])
			}
		})
	}
}
