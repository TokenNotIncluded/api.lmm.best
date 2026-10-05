package common

import (
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func preserveCreditAnchor(t *testing.T) {
	t.Helper()
	previous, err := CreditsPerUSD()
	t.Cleanup(func() {
		if err != nil {
			ClearCreditsPerUSD()
		} else {
			require.NoError(t, SetCreditsPerUSD(previous))
		}
	})
}

func TestCreditUnitsUnavailableAndInvalid(t *testing.T) {
	preserveCreditAnchor(t)
	ClearCreditsPerUSD()
	_, err := CreditsToUSD(500000)
	require.ErrorIs(t, err, ErrCreditUnitsUnavailable)
	_, err = FiatToCreditsDecimal(decimal.NewFromInt(1), "USD", decimal.NewFromInt(7))
	require.ErrorIs(t, err, ErrCreditUnitsUnavailable)
	for _, value := range []decimal.Decimal{decimal.Zero, decimal.NewFromInt(-1), decimal.NewFromInt(MaxWalletQuota).Add(decimal.NewFromInt(1))} {
		require.Error(t, SetCreditsPerUSD(value))
	}
}

func TestCreditUnitsTrueFiatAndLegacyBridge(t *testing.T) {
	preserveCreditAnchor(t)
	oldQuota := QuotaPerUnit
	t.Cleanup(func() { QuotaPerUnit = oldQuota })
	QuotaPerUnit = 500000
	require.NoError(t, SetCreditsPerUSD(decimal.NewFromInt(4375000)))
	scale, err := LegacyPricingUnitsPerUSD()
	require.NoError(t, err)
	require.Equal(t, "8.75", scale.String())
	price, err := LegacyAmountToUSD(decimal.NewFromInt(7))
	require.NoError(t, err)
	require.Equal(t, "0.8", price.String())
	legacy, err := USDToLegacyAmount(price)
	require.NoError(t, err)
	require.Equal(t, "7", legacy.String())
	for _, test := range []struct{ currency, fx, want string }{{"USD", "7", "4375000"}, {"CNY", "7", "625000"}, {"CNY", "8", "546875"}} {
		credits, err := FiatToCreditsDecimal(decimal.NewFromInt(1), test.currency, decimal.RequireFromString(test.fx))
		require.NoError(t, err)
		require.Equal(t, test.want, credits.String())
	}
	usd, err := CreditsToUSD(-4375000)
	require.NoError(t, err)
	require.Equal(t, "-1", usd.String(), "existing negative balances stay negative")
	_, err = FiatToCreditsDecimal(decimal.NewFromInt(1), "CNY", decimal.Zero)
	require.Error(t, err)
	_, err = FiatToCreditsDecimal(decimal.NewFromInt(1), "CREDIT", decimal.NewFromInt(7))
	require.Error(t, err, "credit is not a fiat payment currency")
	QuotaPerUnit = math.NaN()
	_, err = LegacyPricingUnitsPerUSD()
	require.Error(t, err)
}
