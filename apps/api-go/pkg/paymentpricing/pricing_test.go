package paymentpricing

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestRatesUseDynamicFXWithoutChangingPlatformBase(t *testing.T) {
	for _, rawRate := range []string{"6.8", "7.2"} {
		r := Rates{
			CNYPerUSD:           decimal.RequireFromString(rawRate),
			PlatformUnitsPerCNY: decimal.NewFromInt(1),
		}
		platformAmount := decimal.RequireFromString(rawRate)

		usd, err := r.FiatForPlatformUnits(platformAmount, CurrencyUSD)
		require.NoError(t, err)
		require.True(t, usd.Equal(decimal.NewFromInt(1)), rawRate)

		cny, err := r.FiatForPlatformUnits(platformAmount, CurrencyCNY)
		require.NoError(t, err)
		require.True(t, cny.Equal(platformAmount), rawRate)

		fiatCNY, err := r.ConvertFiat(decimal.NewFromInt(1), CurrencyUSD, CurrencyCNY)
		require.NoError(t, err)
		require.True(t, fiatCNY.Equal(decimal.RequireFromString(rawRate)), rawRate)
	}
}

func TestRechargeRatioIsIndependentFromFiatFX(t *testing.T) {
	r := Rates{
		CNYPerUSD:           decimal.RequireFromString("7.2"),
		PlatformUnitsPerCNY: decimal.RequireFromString("1.25"),
	}

	platformPerUSD, err := r.PlatformUnitsPerUSD()
	require.NoError(t, err)
	require.True(t, platformPerUSD.Equal(decimal.NewFromInt(9)))

	usd, err := r.FiatForPlatformUnits(decimal.NewFromInt(9), CurrencyUSD)
	require.NoError(t, err)
	require.True(t, usd.Equal(decimal.NewFromInt(1)))

	walletUnits, err := r.PlatformUnitsForFiat(decimal.NewFromInt(1), CurrencyUSD)
	require.NoError(t, err)
	require.True(t, walletUnits.Equal(decimal.NewFromInt(9)))
}

func TestFiatConversionDoesNotApplyRechargeRatio(t *testing.T) {
	r := Rates{
		CNYPerUSD:           decimal.RequireFromString("7.2"),
		PlatformUnitsPerCNY: decimal.RequireFromString("1.25"),
	}

	cny, err := r.ConvertFiat(decimal.NewFromInt(2), CurrencyUSD, CurrencyCNY)
	require.NoError(t, err)
	require.True(t, cny.Equal(decimal.RequireFromString("14.4")))
}

func TestRatesRejectInvalidOrUnsupportedInputs(t *testing.T) {
	_, err := (Rates{}).FiatForPlatformUnits(decimal.NewFromInt(1), CurrencyUSD)
	require.Error(t, err)

	r := Rates{
		CNYPerUSD:           decimal.NewFromInt(7),
		PlatformUnitsPerCNY: decimal.NewFromInt(1),
	}
	_, err = r.ConvertFiat(decimal.NewFromInt(1), "EUR", CurrencyUSD)
	require.Error(t, err)
	_, err = r.PlatformUnitsForFiat(decimal.NewFromInt(-1), CurrencyUSD)
	require.Error(t, err)
}
