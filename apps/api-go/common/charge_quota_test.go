package common

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestChargeQuotaRoundsOnlyFinalPositiveConsumption(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"0", 0}, {"0.00000001", 1}, {"10", 10}, {"10.4", 11}, {"10.9999", 11},
		{"5000", 5000}, {"2147483646.1", MaxQuota}, {"2147483647", MaxQuota},
	} {
		t.Run(tc.value, func(t *testing.T) {
			value := decimal.RequireFromString(tc.value)
			quota, err := ChargeQuotaFromDecimalStrict(value)
			require.NoError(t, err)
			require.Equal(t, tc.want, quota)
			delta := decimal.NewFromInt(int64(quota)).Sub(value)
			require.False(t, delta.IsNegative())
			require.True(t, delta.LessThan(decimal.NewFromInt(1)))
		})
	}
}

func TestChargeQuotaRejectsInvalidAndOverflowAmounts(t *testing.T) {
	for _, value := range []float64{-1, -.1, math.NaN(), math.Inf(1), math.Inf(-1), float64(MaxQuota) + .1} {
		_, err := ChargeQuotaFromFloatStrict(value)
		require.Error(t, err)
	}
	_, err := ChargeQuotaFromDecimalStrict(decimal.RequireFromString("2147483647.000000000000000001"))
	require.Error(t, err)
}

func TestChargeQuotaDoesNotChangeRechargeRefundAndTokenConversions(t *testing.T) {
	require.Equal(t, 10, QuotaFromDecimal(decimal.RequireFromString("10.4")))
	require.Equal(t, -10, QuotaFromDecimal(decimal.RequireFromString("-10.4")))
	require.Equal(t, 10, QuotaFromFloat(10.9))
	wallet, err := WalletQuotaFromDecimalStrict(decimal.RequireFromString("10.4"))
	require.NoError(t, err)
	require.Equal(t, 10, wallet)
	// USD 0.01 costs 5000 points; the fixed USD anchor is unchanged.
	quota, err := ChargeQuotaFromDecimalStrict(decimal.RequireFromString("0.01").Mul(decimal.NewFromInt(FixedCreditsPerUSD)))
	require.NoError(t, err)
	require.Equal(t, 5000, quota)
}
