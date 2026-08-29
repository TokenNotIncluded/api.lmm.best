package paymentpricing

import (
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
)

const (
	CurrencyCNY = "CNY"
	CurrencyUSD = "USD"
)

// Rates contains the two independent dimensions used by payment pricing.
// CNYPerUSD is a real fiat FX rate. PlatformUnitsPerCNY is the platform's
// recharge purchase ratio and must never be treated as fiat FX.
type Rates struct {
	CNYPerUSD           decimal.Decimal
	PlatformUnitsPerCNY decimal.Decimal
}

// CurrentRates reads the live operator configuration. It intentionally does
// not use display-currency settings or deprecated provider unit-price fields.
func CurrentRates() (Rates, error) {
	rates := Rates{
		CNYPerUSD:           decimal.NewFromFloat(operation_setting.USDExchangeRate),
		PlatformUnitsPerCNY: decimal.NewFromFloat(operation_setting.TopUpPlatformUnitsPerCNY),
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
	if !r.PlatformUnitsPerCNY.IsPositive() {
		return fmt.Errorf("platform units per CNY must be positive")
	}
	return nil
}

func (r Rates) PlatformUnitsPerUSD() (decimal.Decimal, error) {
	if err := r.Validate(); err != nil {
		return decimal.Zero, err
	}
	return r.CNYPerUSD.Mul(r.PlatformUnitsPerCNY), nil
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

// FiatForPlatformUnits quotes platform units in a real settlement currency.
// P platform units first become P/B CNY, then CNY is converted to the target.
func (r Rates) FiatForPlatformUnits(platformUnits decimal.Decimal, settlementCurrency string) (decimal.Decimal, error) {
	if err := r.Validate(); err != nil {
		return decimal.Zero, err
	}
	if platformUnits.IsNegative() {
		return decimal.Zero, fmt.Errorf("platform amount cannot be negative")
	}
	cnyAmount := platformUnits.Div(r.PlatformUnitsPerCNY)
	return r.ConvertFiat(cnyAmount, CurrencyCNY, settlementCurrency)
}

// PlatformUnitsForFiat converts real fiat into platform units. This is used
// when a fiat-priced subscription is paid from the platform wallet.
func (r Rates) PlatformUnitsForFiat(fiatAmount decimal.Decimal, fiatCurrency string) (decimal.Decimal, error) {
	if err := r.Validate(); err != nil {
		return decimal.Zero, err
	}
	cnyAmount, err := r.ConvertFiat(fiatAmount, fiatCurrency, CurrencyCNY)
	if err != nil {
		return decimal.Zero, err
	}
	return cnyAmount.Mul(r.PlatformUnitsPerCNY), nil
}

func normalizeCurrency(currency string) string {
	return strings.ToUpper(strings.TrimSpace(currency))
}
