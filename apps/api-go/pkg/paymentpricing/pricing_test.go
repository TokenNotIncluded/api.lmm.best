package paymentpricing

import (
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func initializePaymentCreditAnchor(t *testing.T) {
	t.Helper()
	previous, previousErr := common.CreditsPerUSD()
	previousQPU := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	// Freeze the old real-price basis: 500000 * 7 CNY/USD * 1.25 = 4375000.
	require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(4375000)))
	t.Cleanup(func() {
		common.QuotaPerUnit = previousQPU
		if previousErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditsPerUSD(previous))
		}
	})
}

func requireDecimal(t *testing.T, actual decimal.Decimal, literal string) {
	t.Helper()
	require.True(t, actual.Equal(decimal.RequireFromString(literal)), "got %s, want %s", actual, literal)
}

func TestFrozenLegacyPriceIsIndependentOfLiveFXAndRechargeBonus(t *testing.T) {
	initializePaymentCreditAnchor(t)
	for _, tc := range []struct {
		fx, bonus, expectedCNY string
	}{
		{"7", "1.25", "7"},
		{"6.8", "1", "6.8"},
		{"7.2", "9", "7.2"},
		{"7.2", "0", "7.2"},
		{"7.2", "-1", "7.2"},
	} {
		t.Run(tc.fx+"/"+tc.bonus, func(t *testing.T) {
			r := Rates{CNYPerUSD: decimal.RequireFromString(tc.fx), PlatformUnitsPerCNY: decimal.RequireFromString(tc.bonus)}
			unitsPerUSD, err := r.PlatformUnitsPerUSD()
			require.NoError(t, err)
			requireDecimal(t, unitsPerUSD, "8.75")
			usd, err := r.FiatForPlatformUnits(decimal.RequireFromString("8.75"), CurrencyUSD)
			require.NoError(t, err)
			requireDecimal(t, usd, "1")
			cny, err := r.FiatForPlatformUnits(decimal.RequireFromString("8.75"), CurrencyCNY)
			require.NoError(t, err)
			requireDecimal(t, cny, tc.expectedCNY)
			units, err := r.PlatformUnitsForFiat(decimal.NewFromInt(1), CurrencyUSD)
			require.NoError(t, err)
			requireDecimal(t, units, "8.75")
			// A $1 wallet purchase always debits the same actual raw credits.
			requireDecimal(t, units.Mul(decimal.NewFromInt(500000)), "4375000")
			cnyUnits, err := r.PlatformUnitsForFiat(decimal.RequireFromString(tc.expectedCNY), CurrencyCNY)
			require.NoError(t, err)
			requireDecimal(t, cnyUnits, "8.75")
		})
	}
}

func TestFiatConversionUsesFXWithoutRechargeBonus(t *testing.T) {
	initializePaymentCreditAnchor(t)
	for _, bonus := range []string{"0", "1.25", "999", "-1"} {
		r := Rates{CNYPerUSD: decimal.RequireFromString("7.2"), PlatformUnitsPerCNY: decimal.RequireFromString(bonus)}
		cny, err := r.ConvertFiat(decimal.NewFromInt(2), CurrencyUSD, CurrencyCNY)
		require.NoError(t, err)
		requireDecimal(t, cny, "14.4")
		usd, err := r.ConvertFiat(decimal.RequireFromString("14.4"), " cny ", " usd ")
		require.NoError(t, err)
		requireDecimal(t, usd, "2")
		unchanged, err := r.ConvertFiat(decimal.RequireFromString("12.34"), CurrencyCNY, CurrencyCNY)
		require.NoError(t, err)
		requireDecimal(t, unchanged, "12.34")
	}
}

func TestCurrentRatesIgnoresInvalidBonusAndRejectsNonFiniteFX(t *testing.T) {
	initializePaymentCreditAnchor(t)
	previousFX, previousBonus := operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY
	t.Cleanup(func() {
		operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = previousFX, previousBonus
	})
	operation_setting.USDExchangeRate = 7.2
	for _, bonus := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		operation_setting.TopUpPlatformUnitsPerCNY = bonus
		rates, err := CurrentRates()
		require.NoError(t, err)
		units, err := rates.PlatformUnitsPerUSD()
		require.NoError(t, err)
		requireDecimal(t, units, "8.75")
	}
	for _, fx := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		operation_setting.USDExchangeRate = fx
		require.NotPanics(t, func() {
			_, err := CurrentRates()
			require.Error(t, err)
		})
	}
}

func TestFiatConversionDoesNotDependOnLegacyDisplayScale(t *testing.T) {
	initializePaymentCreditAnchor(t)
	r := Rates{CNYPerUSD: decimal.RequireFromString("7.2")}
	for _, invalidScale := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		common.QuotaPerUnit = invalidScale
		cny, err := r.ConvertFiat(decimal.NewFromInt(2), CurrencyUSD, CurrencyCNY)
		require.NoError(t, err)
		requireDecimal(t, cny, "14.4")
		require.NotPanics(t, func() {
			_, err := r.FiatForPlatformUnits(decimal.NewFromInt(1), CurrencyUSD)
			require.Error(t, err)
			_, err = r.PlatformUnitsForFiat(decimal.NewFromInt(1), CurrencyUSD)
			require.Error(t, err)
		})
	}
}

func TestPricingFailsClosedWithoutCreditAnchor(t *testing.T) {
	initializePaymentCreditAnchor(t)
	common.ClearCreditsPerUSD()
	r := Rates{CNYPerUSD: decimal.NewFromInt(7)}
	require.Error(t, r.Validate())
	_, err := r.PlatformUnitsPerUSD()
	require.Error(t, err)
	_, err = r.FiatForPlatformUnits(decimal.NewFromInt(1), CurrencyUSD)
	require.Error(t, err)
	_, err = r.PlatformUnitsForFiat(decimal.NewFromInt(1), CurrencyUSD)
	require.Error(t, err)
}

func TestRatesRejectInvalidCurrenciesAndNegativeAmounts(t *testing.T) {
	initializePaymentCreditAnchor(t)
	_, err := (Rates{}).FiatForPlatformUnits(decimal.NewFromInt(1), CurrencyUSD)
	require.Error(t, err)
	r := Rates{CNYPerUSD: decimal.NewFromInt(7)}
	_, err = r.ConvertFiat(decimal.NewFromInt(1), "EUR", CurrencyUSD)
	require.Error(t, err)
	_, err = r.ConvertFiat(decimal.NewFromInt(1), CurrencyUSD, "")
	require.Error(t, err)
	_, err = r.FiatForPlatformUnits(decimal.NewFromInt(-1), CurrencyUSD)
	require.Error(t, err)
	_, err = r.PlatformUnitsForFiat(decimal.NewFromInt(-1), CurrencyUSD)
	require.Error(t, err)
	_, err = r.PlatformUnitsForFiat(decimal.NewFromInt(1), "EUR")
	require.Error(t, err)
	zero, err := r.FiatForPlatformUnits(decimal.Zero, CurrencyUSD)
	require.NoError(t, err)
	requireDecimal(t, zero, "0")
}
