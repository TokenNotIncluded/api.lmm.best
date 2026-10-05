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

func TestPublicCreditDenominationDoesNotChangeDollarLedger(t *testing.T) {
	preservePublicCreditFixture(t)
	beforeUSD, err := CreditsToUSD(3359744)
	require.NoError(t, err)
	beforePrice, err := LegacyPricingUnitsPerUSD()
	require.NoError(t, err)
	require.NoError(t, SetPublicCreditsPerUSD(decimal.NewFromInt(100000)))
	projected, err := LedgerQuotaToPublicCredits(3359744)
	require.NoError(t, err)
	require.Equal(t, "100000", projected.String())
	afterUSD, err := CreditsToUSD(3359744)
	require.NoError(t, err)
	require.True(t, beforeUSD.Equal(afterUSD))
	require.Equal(t, "1", afterUSD.String())
	afterPrice, err := LegacyPricingUnitsPerUSD()
	require.NoError(t, err)
	require.True(t, beforePrice.Equal(afterPrice))
	metadata, err := CreditDenominationMetadata()
	require.NoError(t, err)
	require.Equal(t, float64(3359744), metadata.LedgerQuotaPerUSD)
	require.Equal(t, float64(100000), metadata.PublicCreditsPerUSD)
	require.Equal(t, LedgerQuotaUnit, metadata.QuotaUnit)
	require.Equal(t, PublicCreditUnit, metadata.PublicCreditUnit)
	// A second denomination change changes display only, never an old quote.
	require.NoError(t, SetPublicCreditsPerUSD(decimal.NewFromInt(200000)))
	projected, err = LedgerQuotaToPublicCredits(3359744)
	require.NoError(t, err)
	require.Equal(t, "200000", projected.String())
	afterUSD, err = CreditsToUSD(3359744)
	require.NoError(t, err)
	require.True(t, beforeUSD.Equal(afterUSD))
}

func TestExplicitPublicCreditInputUsesLedgerUnitWithoutOverGrant(t *testing.T) {
	preservePublicCreditFixture(t)
	require.NoError(t, SetPublicCreditsPerUSD(decimal.NewFromInt(100000)))
	amount, err := PublicCreditsToLedgerQuota(decimal.NewFromInt(100000))
	require.NoError(t, err)
	require.Equal(t, int64(3359744), amount)
	amount, err = PublicCreditsToLedgerQuota(decimal.NewFromInt(1))
	require.NoError(t, err)
	require.Equal(t, int64(33), amount)
	projected, err := LedgerQuotaToPublicCredits(amount)
	require.NoError(t, err)
	require.True(t, projected.LessThanOrEqual(decimal.NewFromInt(1)))
	for _, invalid := range []string{"-1", "0.000000000000000001", "9007199254740991", "1e100000"} {
		_, err := PublicCreditsToLedgerQuota(decimal.RequireFromString(invalid))
		require.Error(t, err)
	}
	require.NoError(t, SetPublicCreditsPerUSD(decimal.NewFromInt(100000)))
	amount, err = PublicCreditsToLedgerQuota(decimal.Zero)
	require.NoError(t, err)
	require.Zero(t, amount)
}

func TestPublicCreditProjectionKeepsFractionalAndSignedAmounts(t *testing.T) {
	preservePublicCreditFixture(t)
	require.NoError(t, SetPublicCreditsPerUSD(decimal.NewFromInt(100000)))
	p, err := LedgerQuotaToPublicCredits(1)
	require.NoError(t, err)
	require.True(t, p.IsPositive())
	require.True(t, p.LessThan(decimal.NewFromInt(1)))
	n, err := LedgerQuotaToPublicCredits(-1)
	require.NoError(t, err)
	require.True(t, n.Equal(p.Neg()))
	_, err = LedgerQuotaToPublicCredits(MaxWalletQuota + 1)
	require.Error(t, err)
	for _, invalid := range []string{"0", "-1", "1.5", "9007199254740992"} {
		require.Error(t, SetPublicCreditsPerUSD(decimal.RequireFromString(invalid)))
	}
	ClearPublicCreditsPerUSD()
	fallback, err := PublicCreditsPerUSD()
	require.NoError(t, err)
	require.Equal(t, "3359744", fallback.String())
	ClearCreditsPerUSD()
	_, err = LedgerQuotaToPublicCredits(1)
	require.ErrorIs(t, err, ErrCreditUnitsUnavailable)
}

func TestCapturedPublicBasisStaysCoherentAcrossOptionChanges(t *testing.T) {
	preservePublicCreditFixture(t)
	require.NoError(t, SetPublicCreditsPerUSD(decimal.NewFromInt(100000)))
	units, err := CreditDenominationMetadata()
	require.NoError(t, err)
	require.NoError(t, SetPublicCreditsPerUSD(decimal.NewFromInt(200000)))
	amount, err := units.ProjectLedgerQuota(3359744)
	require.NoError(t, err)
	require.Equal(t, "100000", amount.String())
	quota, err := units.ResolvePublicCredits(decimal.NewFromInt(100000))
	require.NoError(t, err)
	require.Equal(t, int64(3359744), quota)
	current, err := LedgerQuotaToPublicCredits(3359744)
	require.NoError(t, err)
	require.Equal(t, "200000", current.String())
	bad := units
	bad.PublicCreditsPerUSDExact = "200000"
	_, err = bad.ProjectLedgerQuota(1)
	require.ErrorIs(t, err, ErrCreditUnitsUnavailable)
	bad = units
	bad.QuotaUnit = PublicCreditUnit
	_, err = bad.ResolvePublicCredits(decimal.NewFromInt(1))
	require.ErrorIs(t, err, ErrCreditUnitsUnavailable)
}
