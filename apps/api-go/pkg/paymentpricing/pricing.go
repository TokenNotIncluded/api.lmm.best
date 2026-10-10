package paymentpricing

import (
	"fmt"
	"math"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
)

const (
	CurrencyCNY = "CNY"
	CurrencyUSD = "USD"
)

// Rates contains real fiat FX. Legacy batch units are converted through the
// immutable credit anchor. PlatformUnitsPerCNY remains for source compatibility
// only: recharge bonuses never enter fiat or wallet conversion.
type Rates struct {
	CNYPerUSD           decimal.Decimal
	PlatformUnitsPerCNY decimal.Decimal
}

// CurrentRates reads the live operator configuration. It intentionally does
// not use display-currency settings or deprecated provider unit-price fields.
func CurrentRates() (Rates, error) {
	fx := operation_setting.USDExchangeRate
	if math.IsNaN(fx) || math.IsInf(fx, 0) || fx <= 0 {
		return Rates{}, fmt.Errorf("CNY per USD rate must be positive and finite")
	}
	rates := Rates{
		CNYPerUSD: decimal.NewFromFloat(fx),
	}
	if err := rates.Validate(); err != nil {
		return Rates{}, err
	}
	return rates, nil
}

func (r Rates) Validate() error {
	if !r.CNYPerUSD.IsPositive() {
		return fmt.Errorf("CNY per USD rate must be positive")
	}
	_, err := common.CreditsPerUSD()
	return err
}

func (r Rates) PlatformUnitsPerUSD() (decimal.Decimal, error) {
	if err := r.Validate(); err != nil {
		return decimal.Zero, err
	}
	return common.LegacyPricingUnitsPerUSD()
}

// ConvertFiat converts a real ISO-fiat amount. It never applies the platform
// recharge purchase ratio. Only the configured USD/CNY pair is supported.
func (r Rates) ConvertFiat(amount decimal.Decimal, fromCurrency, toCurrency string) (decimal.Decimal, error) {
	if err := r.Validate(); err != nil {
		return decimal.Zero, err
	}
	if amount.IsNegative() {
		return decimal.Zero, fmt.Errorf("fiat amount cannot be negative")
	}
	from := normalizeCurrency(fromCurrency)
	to := normalizeCurrency(toCurrency)
	if from == "" || to == "" {
		return decimal.Zero, fmt.Errorf("fiat currency is required")
	}
	if from == to {
		if from != CurrencyUSD && from != CurrencyCNY {
			return decimal.Zero, fmt.Errorf("unsupported fiat currency %q", from)
		}
		return amount, nil
	}
	switch {
	case from == CurrencyUSD && to == CurrencyCNY:
		return amount.Mul(r.CNYPerUSD), nil
	case from == CurrencyCNY && to == CurrencyUSD:
		return amount.Div(r.CNYPerUSD), nil
	default:
		return decimal.Zero, fmt.Errorf("unsupported fiat conversion %s/%s", from, to)
	}
}

// FiatForPlatformUnits quotes legacy batch units in a real settlement currency.
// Units first become USD using K/QPU, then real FX converts USD to the target.
func (r Rates) FiatForPlatformUnits(platformUnits decimal.Decimal, settlementCurrency string) (decimal.Decimal, error) {
	if err := r.Validate(); err != nil {
		return decimal.Zero, err
	}
	if platformUnits.IsNegative() {
		return decimal.Zero, fmt.Errorf("platform amount cannot be negative")
	}
	usdAmount, err := common.LegacyAmountToUSD(platformUnits)
	if err != nil {
		return decimal.Zero, err
	}
	return r.ConvertFiat(usdAmount, CurrencyUSD, settlementCurrency)
}

// PlatformUnitsForFiat converts real fiat into legacy batch units. Multiplying
// the result by QPU yields the fixed K-based wallet debit, independent of bonus.
func (r Rates) PlatformUnitsForFiat(fiatAmount decimal.Decimal, fiatCurrency string) (decimal.Decimal, error) {
	if err := r.Validate(); err != nil {
		return decimal.Zero, err
	}
	usdAmount, err := r.ConvertFiat(fiatAmount, fiatCurrency, CurrencyUSD)
	if err != nil {
		return decimal.Zero, err
	}
	return common.USDToLegacyAmount(usdAmount)
}

func normalizeCurrency(currency string) string {
	return strings.ToUpper(strings.TrimSpace(currency))
}
