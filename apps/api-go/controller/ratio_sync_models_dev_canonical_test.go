package controller

import (
	"encoding/json"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

const canonicalModelsDevTextModalities = `"modalities":{"input":["text","image","pdf"],"output":["text"]}`

func canonicalModelsDevFixture(cost string) string {
	return `{"vendor":{"id":"vendor","models":{"example":{` + canonicalModelsDevTextModalities + `,"cost":` + cost + `}}}}`
}

func TestModelsDevCanonicalSelectsOnlyExplicitProvider(t *testing.T) {
	fixture := `{
		"other":{"models":{"same":{"modalities":{"input":["text"],"output":["text"]},"cost":{"input":0.01,"output":0.02}},"other-only":{"cost":{"input":1,"output":2}}}},
		"vendor":{"id":"vendor","models":{"same":{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"cache_read":0.4,"cache_write":5}}}}
	}`
	data, skipped, err := convertModelsDevCanonicalData(strings.NewReader(fixture), "vendor", 3_500_000)
	require.NoError(t, err)
	require.Empty(t, skipped)
	require.Len(t, data, 10)
	require.Equal(t, map[string]any{"same": 14.0}, data["model_ratio"])
	require.Equal(t, map[string]any{"same": 5.0}, data["completion_ratio"])
	require.Equal(t, map[string]any{"same": 0.1}, data["cache_ratio"])
	require.Equal(t, map[string]any{"same": 1.25}, data["create_cache_ratio"])
	require.Equal(t, map[string]any{"same": "tiered_expr"}, data["billing_mode"])
	require.Equal(t, map[string]any{"same": `tier("base", p * 4 + c * 20 + cr * 0.4 + cc * 5)`}, data["billing_expr"])
	for field, raw := range data {
		require.NotContains(t, raw.(map[string]any), "other-only", field)
	}

	// Denomination changes only the calibrated input ratio, never monetary
	// coefficients or dimensionless cache/completion multipliers.
	second, _, err := convertModelsDevCanonicalData(strings.NewReader(fixture), "vendor", 4_375_000)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"same": 17.5}, second["model_ratio"])
	for _, field := range []string{"completion_ratio", "cache_ratio", "create_cache_ratio", "billing_expr"} {
		require.Equal(t, data[field], second[field], field)
	}
}

func TestModelsDevCanonicalPreservesContextBoundaryAndEveryCacheLane(t *testing.T) {
	// Actual models.dev gpt-5.6-sol cost structure. The legacy alias says
	// "200k", but the explicit threshold and official vector use 272000.
	fixture := canonicalModelsDevFixture(`{
		"input":4,"output":20,"cache_read":0.4,"cache_write":5,
		"tiers":[{"input":8,"output":30,"cache_read":0.8,"cache_write":10,"tier":{"type":"context","size":272000}}],
		"context_over_200k":{"input":8,"output":30,"cache_read":0.8,"cache_write":10}
	}`)
	data, skipped, err := convertModelsDevCanonicalData(strings.NewReader(fixture), "vendor", 3_500_000)
	require.NoError(t, err)
	require.Empty(t, skipped)
	expression := data["billing_expr"].(map[string]any)["example"].(string)
	for _, length := range []float64{200001, 271999, 272000, 272001, 1_040_000} {
		for _, lane := range []string{"p", "c", "cr", "cc"} {
			params := billingexpr.TokenParams{Len: length}
			rates := map[string]float64{"p": 4, "c": 20, "cr": 0.4, "cc": 5}
			if length > 272000 {
				rates = map[string]float64{"p": 8, "c": 30, "cr": 0.8, "cc": 10}
			}
			switch lane {
			case "p":
				params.P = 1000
			case "c":
				params.C = 1000
			case "cr":
				params.CR = 1000
			case "cc":
				params.CC = 1000
			}
			amount, trace, err := billingexpr.RunExpr(expression, params)
			require.NoError(t, err)
			require.Equal(t, rates[lane]*1000, amount, "length=%g lane=%s", length, lane)
			if length <= 272000 {
				require.Equal(t, "base", trace.MatchedTier)
			} else {
				require.Equal(t, "context_over_272000", trace.MatchedTier)
			}
		}
	}

	// Use the actual usage normalizer: cached tokens reduce billable p, but
	// do not reduce len and move the request into the cheaper context tier.
	usage := &dto.Usage{PromptTokens: 300000, CompletionTokens: 1000}
	usage.PromptTokensDetails.CachedTokens = 250000
	usage.PromptTokensDetails.CacheWriteTokens = 10000
	params := service.BuildTieredTokenParams(usage, false, map[string]bool{"p": true, "c": true, "cr": true, "cc": true})
	require.Equal(t, 300000.0, params.Len)
	require.Equal(t, 40000.0, params.P)
	amount, trace, err := billingexpr.RunExpr(expression, params)
	require.NoError(t, err)
	require.Equal(t, 650000.0, amount) // 40k*8 + 1k*30 + 250k*.8 + 10k*10.
	require.Equal(t, "context_over_272000", trace.MatchedTier)
}

func TestModelsDevCanonicalMultipleTiersKeepExactThresholds(t *testing.T) {
	fixture := canonicalModelsDevFixture(`{"input":1,"output":2,"cache_read":0.1,"tiers":[
		{"input":5,"output":10,"cache_read":0.5,"tier":{"type":"context","size":128000}},
		{"input":3,"output":6,"cache_read":0.3,"tier":{"type":"context","size":32000}}
	]}`)
	data, skipped, err := convertModelsDevCanonicalData(strings.NewReader(fixture), "vendor", 3_500_000)
	require.NoError(t, err)
	require.Empty(t, skipped)
	for _, test := range []struct{ length, expected float64 }{
		{32000, 100}, {32001, 300}, {128000, 300}, {128001, 500},
	} {
		value, _, err := billingexpr.RunExpr(data["billing_expr"].(map[string]any)["example"].(string), billingexpr.TokenParams{Len: test.length, P: 100})
		require.NoError(t, err)
		require.Equal(t, test.expected, value)
	}
}

func TestModelsDevCanonicalSkipsIncompleteNativeCacheTTLPrices(t *testing.T) {
	for _, fixture := range []string{
		`{"vendor":{"npm":"@ai-sdk/anthropic","models":{"example":{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"cache_read":0.4,"cache_write":5}}}}}`,
		`{"vendor":{"npm":"@ai-sdk/openai-compatible","models":{"example":{"family":"claude-opus",` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"cache_read":0.4,"cache_write":5}}}}}`,
		`{"vendor":{"npm":"@ai-sdk/anthropic","models":{"example":{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20}}}}}`,
	} {
		data, skipped, err := convertModelsDevCanonicalData(strings.NewReader(fixture), "vendor", 3_500_000)
		require.NoError(t, err)
		require.Contains(t, skipped["example"], "1-hour TTL price")
		for _, raw := range data {
			require.Empty(t, raw)
		}
	}
}

func TestModelsDevCanonicalNeverTurnsMissingOrUnsupportedPricesIntoZero(t *testing.T) {
	tests := []struct{ name, model, reason string }{
		{"conflicting model identity", `{"id":"different",` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20}}`, "model identity"},
		{"missing output", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4}}`, "missing output"},
		{"missing cost", `{` + canonicalModelsDevTextModalities + `}`, "missing or invalid token prices"},
		{"missing modalities", `{"cost":{"input":4,"output":20}}`, "modality metadata"},
		{"null output", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":null}}`, "output token price"},
		{"string price", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":"4","output":20}}`, "input token price"},
		{"negative sentinel", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":-1,"output":20}}`, "input token price"},
		{"invalid cache price", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"cache_write":null}}`, "cache_write token price"},
		{"audio prices", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"input_audio":32}}`, "unsupported price field"},
		{"unknown pricing", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"reasoning":30}}`, "unsupported price field"},
		{"image output", `{"modalities":{"input":["text","image"],"output":["image"]},"cost":{"input":5,"output":30}}`, "text-output"},
		{"audio input", `{"modalities":{"input":["text","audio"],"output":["text"]},"cost":{"input":4,"output":20}}`, "unsupported input modality"},
		{"unknown input", `{"modalities":{"input":["speech"],"output":["text"]},"cost":{"input":4,"output":20}}`, "unsupported input modality"},
		{"missing tier output", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"tiers":[{"input":8,"tier":{"type":"context","size":272000}}]}}`, "missing output"},
		{"dropped tier cache", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"cache_write":5,"tiers":[{"input":8,"output":30,"tier":{"type":"context","size":272000}}]}}`, "every base price lane"},
		{"unsupported tier type", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"tiers":[{"input":8,"output":30,"tier":{"type":"request","size":272000}}]}}`, "tier condition"},
		{"unknown tier field", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"tiers":[{"input":8,"output":30,"input_audio":2,"tier":{"type":"context","size":272000}}]}}`, "tier price field"},
		{"duplicate tiers", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"tiers":[{"input":8,"output":30,"tier":{"type":"context","size":272000}},{"input":12,"output":50,"tier":{"type":"context","size":272000}}]}}`, "duplicate context tier"},
		{"legacy alias without actual threshold", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"context_over_200k":{"input":8,"output":30}}}`, "one matching explicit context tier"},
		{"conflicting alias", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"output":20,"tiers":[{"input":8,"output":30,"tier":{"type":"context","size":272000}}],"context_over_200k":{"input":12,"output":40}}}`, "conflicts"},
		{"duplicate price field", `{` + canonicalModelsDevTextModalities + `,"cost":{"input":4,"input":1,"output":20}}`, "invalid token prices"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := `{"vendor":{"models":{"rejected":` + test.model + `,"good":{` + canonicalModelsDevTextModalities + `,"cost":{"input":1,"output":2}}}}}`
			data, skipped, err := convertModelsDevCanonicalData(strings.NewReader(fixture), "vendor", 3_500_000)
			require.NoError(t, err)
			require.Contains(t, skipped["rejected"], test.reason)
			require.Len(t, skipped, 1)
			for field, raw := range data {
				require.NotContains(t, raw.(map[string]any), "rejected", field)
			}
			require.Contains(t, data["billing_expr"], "good")
		})
	}
}

func TestModelsDevCanonicalExplicitZeroAndTinyPositiveRemainDistinct(t *testing.T) {
	for _, cost := range []string{
		`{"input":0,"output":0,"cache_read":0,"cache_write":0}`,
		`{"input":0e-400,"output":0e-400,"cache_read":0e-400,"cache_write":0e-400}`,
		`{"input":0,"output":20,"cache_read":0,"cache_write":5}`,
		`{"input":1e-20,"output":2e-20,"cache_read":1e-21,"cache_write":1.25e-20}`,
	} {
		data, skipped, err := convertModelsDevCanonicalData(strings.NewReader(canonicalModelsDevFixture(cost)), "vendor", 3_500_000)
		require.NoError(t, err)
		require.Empty(t, skipped)
		var prices map[string]float64
		require.NoError(t, json.Unmarshal([]byte(cost), &prices))
		expression := data["billing_expr"].(map[string]any)["example"].(string)
		for _, lane := range []struct {
			key    string
			params billingexpr.TokenParams
		}{
			{"input", billingexpr.TokenParams{P: 1000}},
			{"output", billingexpr.TokenParams{C: 1000}},
			{"cache_read", billingexpr.TokenParams{CR: 1000}},
			{"cache_write", billingexpr.TokenParams{CC: 1000}},
		} {
			value, _, err := billingexpr.RunExpr(expression, lane.params)
			require.NoError(t, err)
			require.Equal(t, prices[lane.key]*1000, value)
			if prices[lane.key] > 0 {
				require.Positive(t, value)
			}
		}
		if prices["input"] > 0 {
			require.Positive(t, data["model_ratio"].(map[string]any)["example"])
			require.Equal(t, 2.0, data["completion_ratio"].(map[string]any)["example"])
			require.Equal(t, 0.1, data["cache_ratio"].(map[string]any)["example"])
			require.Equal(t, 1.25, data["create_cache_ratio"].(map[string]any)["example"])
		} else {
			require.Equal(t, 0.0, data["model_ratio"].(map[string]any)["example"])
			require.NotContains(t, data["completion_ratio"], "example")
		}
		_, err = json.Marshal(data)
		require.NoError(t, err)
	}
}

func TestModelsDevCanonicalRejectsOverflowAndPositiveUnderflow(t *testing.T) {
	for _, cost := range []string{
		`{"input":1e309,"output":20}`,
		`{"input":1e-400,"output":0}`,
		`{"input":1e308,"output":20}`,
		`{"input":1e-300,"output":1e300}`,
		`{"input":1e300,"output":1e-300}`,
		`{"input":1,"output":2,"cache_read":1e-400}`,
		`{"input":1,"output":2,"tiers":[{"input":1e-400,"output":2,"tier":{"type":"context","size":272000}}]}`,
		`{"input":0e1000000000,"output":0}`,
	} {
		data, skipped, err := convertModelsDevCanonicalData(strings.NewReader(canonicalModelsDevFixture(cost)), "vendor", 3_500_000)
		require.NoError(t, err)
		require.NotEmpty(t, skipped["example"], cost)
		for _, raw := range data {
			require.Empty(t, raw)
		}
	}
	// A source rate may fit float64 while its calibrated credit rate does not.
	data, skipped, err := convertModelsDevCanonicalData(strings.NewReader(canonicalModelsDevFixture(`{"input":5e-324,"output":0}`)), "vendor", 1)
	require.NoError(t, err)
	require.Contains(t, skipped["example"], "calibrated ratio range")
	require.Empty(t, data["billing_expr"])
}

func TestModelsDevCanonicalLargeFiniteIntegerPricesCompile(t *testing.T) {
	for _, literal := range []string{"9223372036854775808", "10000000000000000000"} {
		data, skipped, err := convertModelsDevCanonicalData(strings.NewReader(canonicalModelsDevFixture(`{"input":`+literal+`,"output":0}`)), "vendor", 3_500_000)
		require.NoError(t, err)
		require.Empty(t, skipped)
		expression := data["billing_expr"].(map[string]any)["example"].(string)
		require.Contains(t, expression, literal+".0")
		amount, _, err := billingexpr.RunExpr(expression, billingexpr.TokenParams{P: 1})
		require.NoError(t, err)
		var expected float64
		require.NoError(t, json.Unmarshal([]byte(literal), &expected))
		require.Equal(t, expected, amount)
	}
}

func TestModelsDevCanonicalRejectsInvalidProviderOrDenomination(t *testing.T) {
	fixture := canonicalModelsDevFixture(`{"input":1,"output":2}`)
	for _, provider := range []string{"", " ", "missing", "vendor,other"} {
		_, _, err := convertModelsDevCanonicalData(strings.NewReader(fixture), provider, 3_500_000)
		require.Error(t, err)
	}
	for _, denomination := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1), 1 << 53} {
		_, _, err := convertModelsDevCanonicalData(strings.NewReader(fixture), "vendor", denomination)
		require.Error(t, err)
	}
	for _, malformed := range []string{
		`{"vendor":{"id":"other","models":{"m":{}}}}`,
		`{"vendor":{"models":{}}}`,
		`{"vendor":{"models":{"m":{}}},"vendor":{"models":{"n":{}}}}`,
		fixture + `{}`,
	} {
		_, _, err := convertModelsDevCanonicalData(strings.NewReader(malformed), "vendor", 3_500_000)
		require.Error(t, err)
	}
	_, _, err := convertModelsDevCanonicalData(nil, "vendor", 3_500_000)
	require.Error(t, err)
	_, _, err = convertModelsDevCanonicalData(io.LimitReader(strings.NewReader(strings.Repeat(" ", canonicalModelsDevMaxBytes+1)), canonicalModelsDevMaxBytes+1), "vendor", 3_500_000)
	require.ErrorContains(t, err, "size limit")
}
