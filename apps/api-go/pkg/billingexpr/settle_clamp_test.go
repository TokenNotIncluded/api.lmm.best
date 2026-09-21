package billingexpr_test

import (
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestComputeTieredQuota_ClampOnOverflow guards the billing-safety invariant
// that an oversized tiered settlement clamps to the single-request max instead
// of wrapping into a credit, and that the saturation event is surfaced on the
// result so callers can record it for admin auditing.
func TestComputeTieredQuota_ClampOnOverflow(t *testing.T) {
	// exprOutput = p * 1e12 = 1e21; quotaBeforeGroup = 1e21 / 1e6 * 5e5 = 5e20,
	// which far exceeds the supported single-request range and must saturate.
	exprStr := `tier("base", p * 1000000000000)`
	snap := &billingexpr.BillingSnapshot{
		BillingMode:  "tiered_expr",
		ExprString:   exprStr,
		ExprHash:     billingexpr.ExprHashString(exprStr),
		GroupRatio:   1.0,
		QuotaPerUnit: 500_000,
	}

	result, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 1_000_000_000})
	require.NoError(t, err)

	assert.Equal(t, common.MaxQuota, result.ActualQuotaAfterGroup, "oversized quota must clamp, never wrap negative")
	require.NotNil(t, result.Clamp, "clamp event must be surfaced so it can be audited")
	assert.Equal(t, common.QuotaClampOverflow, result.Clamp.Kind)
	assert.Equal(t, common.MaxQuota, result.Clamp.Clamped)
}

// TestComputeTieredQuota_NonFiniteGroupRatioFailsSafe verifies shared strict
// conversion handles non-finite tiered results without integer wraparound.
func TestComputeTieredQuota_NonFiniteGroupRatioFailsSafe(t *testing.T) {
	exprStr := `tier("base", p)`
	for _, tc := range []struct {
		name      string
		ratio     float64
		wantQuota int
		wantKind  common.QuotaClampKind
	}{
		{name: "positive infinity saturates", ratio: math.Inf(1), wantQuota: common.MaxQuota, wantKind: common.QuotaClampOverflow},
		{name: "NaN falls back to zero", ratio: math.NaN(), wantQuota: 0, wantKind: common.QuotaClampNaN},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := &billingexpr.BillingSnapshot{
				BillingMode:  "tiered_expr",
				ExprString:   exprStr,
				ExprHash:     billingexpr.ExprHashString(exprStr),
				GroupRatio:   tc.ratio,
				QuotaPerUnit: 500_000,
			}

			result, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 1_000})
			require.NoError(t, err)
			assert.Equal(t, tc.wantQuota, result.ActualQuotaAfterGroup)
			require.NotNil(t, result.Clamp)
			assert.Equal(t, tc.wantKind, result.Clamp.Kind)
		})
	}
}

// TestComputeTieredQuota_NoClampInRange confirms an in-range settlement leaves
// Clamp nil, so the audit path is a no-op in the common case.
func TestComputeTieredQuota_NoClampInRange(t *testing.T) {
	exprStr := `tier("base", p * 2 + c * 10)`
	snap := &billingexpr.BillingSnapshot{
		BillingMode:  "tiered_expr",
		ExprString:   exprStr,
		ExprHash:     billingexpr.ExprHashString(exprStr),
		GroupRatio:   1.0,
		QuotaPerUnit: 500_000,
	}

	result, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 1000, C: 500})
	require.NoError(t, err)
	assert.Nil(t, result.Clamp, "in-range settlement must not report a clamp")
}
