package billingexpr

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPricingEquivalent(t *testing.T) {
	for _, tc := range []struct {
		name, left, right string
		equal             bool
	}{
		{"USD identity wrapper", `(tier("standard", p * 2 + cr * 0.1 + c * 10)) / (1)`, `tier("standard", p * 2 + cr * 0.1 + c * 10)`, true},
		{"reordered terms and tier label", `tier("standard", p * 2 + cr * 0.1 + c * 10)`, `v1:tier("default", c * 10 + 0.10 * cr + 2 * p)`, true},
		{"constant scale and distribution", `(p * 4 + c * 20) / 2`, `2 * (p + c * 5)`, true},
		{"repeated terms", `p + p + c * 10`, `2 * p + c * 10`, true},
		{"additive zero", `p * 2 + 0`, `p * 2`, true},
		{"extreme intermediate overflow", `(p * 1e308) / 1e308`, `p`, false},
		{"extreme intermediate underflow", `(p * 1e-308) * 1e308`, `p`, false},
		{"same tiers", `(len <= 272000 ? tier("short", p * 2 + c * 10) : tier("long", p * 4 + c * 15)) * 1 / 1`, `len <= 272000 ? tier("default", c * 10 + p * 2) : tier("long_context", c * 15 + p * 4)`, true},
		{"real cache price change", `p * 2 + cr * 0.2 + c * 10`, `p * 2 + cr * 0.1 + c * 10`, false},
		{"tiny positive differs from free", `p * 0.00000000000001`, `p * 0`, false},
		{"real currency scale", `(p * 2 + c * 10) / 2`, `p * 2 + c * 10`, false},
		{"tier boundary", `len <= 200000 ? p * 2 : p * 4`, `len < 200000 ? p * 2 : p * 4`, false},
		{"changed threshold", `len <= 200000 ? p * 2 : p * 4`, `len <= 272000 ? p * 2 : p * 4`, false},
		{"timezone", `hour("UTC") < 4 ? p * 2 : p`, `hour("Asia/Shanghai") < 4 ? p * 2 : p`, false},
		{"request rule", `p * (param("service_tier") == "fast" ? 2 : 1)`, `p * (param("service_tier") == "fast" ? 3 : 1)`, false},
		{"normalization changes despite zero coefficient", `p * 2 + cr * 0`, `p * 2`, false},
		{"image output merged into completion", `c * 30 + img_o * 30`, `c * 30`, false},
		{"optional measurement not erased", `p * 2 + audio_s * 0`, `p * 2`, false},
		{"variable denominator cannot be cancelled", `(p / len) * len`, `p`, false},
		{"division by zero", `p / 0`, `p`, false},
		{"unsupported version", `v2:p * 2`, `p * 2`, false},
		{"malformed", `tier("x", p * 2`, `p * 2`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.equal, PricingEquivalent(tc.left, tc.right))
			require.Equal(t, tc.equal, PricingEquivalent(tc.right, tc.left), "comparison is symmetric")
		})
	}
}

func TestBillingConditionsPreservesDecisionsAndProbes(t *testing.T) {
	before, ok := BillingConditions(`len <= 272000 ? tier("short", p * 2) : tier("long", p * 4)`)
	require.True(t, ok)
	after, ok := BillingConditions(`len <= 272000 ? tier("renamed", p * 3) : tier("renamed-long", p * 6)`)
	require.True(t, ok)
	require.Equal(t, before, after, "ordinary price updates keep the same decisions")
	changed, ok := BillingConditions(`len <= 200000 ? p * 2 : p * 4`)
	require.True(t, ok)
	require.NotEqual(t, before, changed)
	flat, ok := BillingConditions(`tier("standard", p * 2)`)
	require.True(t, ok)
	require.Equal(t, "[]", flat)
}

func TestCalendarDateUsesFullYearAndRequestedTimezone(t *testing.T) {
	utc := time.Date(2026, 12, 31, 17, 0, 0, 0, time.UTC)
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	require.Equal(t, 20261231, calendarDate(utc))
	require.Equal(t, 20270101, calendarDate(utc.In(shanghai)))
	_, _, err = RunExpr(`tier("holiday", p * (date("Asia/Shanghai") >= 20260101 ? 0.15 : 0.3))`, TokenParams{P: 1000})
	require.NoError(t, err)
}
