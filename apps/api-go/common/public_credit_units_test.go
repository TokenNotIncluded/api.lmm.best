package common

import (
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"testing"
)

func preservePublicCreditFixture(t *testing.T) {
	t.Helper()
	preserveCreditAnchor(t)
	previousPublic := publicCreditsPerUSD.Load()
	t.Cleanup(func() { publicCreditsPerUSD.Store(previousPublic) })
	require.NoError(t, SetCreditCurrencyBasis(decimal.NewFromInt(3359744), decimal.NewFromInt(500000)))
	ClearPublicCreditsPerUSD()
}

func TestPublicCreditsAreWalletIntegers(t *testing.T) {
	preservePublicCreditFixture(t)
	require.NoError(t, SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	require.NoError(t, SetPublicCreditsPerUSD(decimal.NewFromInt(500000)))
	for _, quota := range []int64{0, 1, -1, 500000, MaxWalletQuota} {
		projected, err := LedgerQuotaToPublicCredits(quota)
		require.NoError(t, err)
		require.True(t, decimal.NewFromInt(quota).Equal(projected))
		if quota >= 0 {
			amount, err := PublicCreditsToLedgerQuota(projected)
			require.NoError(t, err)
			require.Equal(t, quota, amount)
		}
	}
	usd, err := CreditsToUSD(500000)
	require.NoError(t, err)
	require.Equal(t, "1", usd.String())
	for _, invalid := range []string{"0", "100000", "200000", "3359744", "1.5", "9007199254740992"} {
		require.Error(t, SetPublicCreditsPerUSD(decimal.RequireFromString(invalid)))
	}
	units, err := CreditDenominationMetadata()
	require.NoError(t, err)
	require.Equal(t, "500000", units.LedgerQuotaPerUSDExact)
	require.Equal(t, "500000", units.PublicCreditsPerUSDExact)
	bad := units
	bad.PublicCreditsPerUSDExact = "100000"
	bad.PublicCreditsPerUSD = 100000
	_, err = bad.ProjectLedgerQuota(1)
	require.ErrorIs(t, err, ErrCreditUnitsUnavailable)
	for _, invalid := range []string{"-1", "0.000000000000000001", "9007199254740992", "1e100000"} {
		_, err := PublicCreditsToLedgerQuota(decimal.RequireFromString(invalid))
		require.Error(t, err)
	}
	ClearCreditsPerUSD()
	_, err = LedgerQuotaToPublicCredits(1)
	require.ErrorIs(t, err, ErrCreditUnitsUnavailable)
}
