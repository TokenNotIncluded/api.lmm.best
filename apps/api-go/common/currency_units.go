package common

import (
	"errors"
	"math"
	"strings"
	"sync/atomic"

	"github.com/shopspring/decimal"
)

// Legacy quota integers are the wallet ledger. This anchor is initialized from the
// durable option once at startup, independently of subsequent exchange rates.
type creditCurrencyBasis struct{ usd, legacy decimal.Decimal }

var creditsPerUSD atomic.Pointer[creditCurrencyBasis]

var ErrCreditUnitsUnavailable = errors.New("credit currency units are unavailable")

func ValidateCreditsPerUSD(value decimal.Decimal) error {
	if !value.IsPositive() || value.GreaterThan(decimal.NewFromInt(MaxWalletQuota)) {
		return errors.New("credits per USD must be positive and within the wallet domain")
	}
	return nil
}

// SetCreditsPerUSD publishes a validated durable anchor. Tests may also use it
// to install a fixture; normal option writes cannot change the durable value.
func SetCreditsPerUSD(value decimal.Decimal) error {
	legacy := QuotaPerUnit
	if math.IsNaN(legacy) || math.IsInf(legacy, 0) || legacy <= 0 {
		return errors.New("legacy pricing scale is invalid")
	}
	return SetCreditCurrencyBasis(value, decimal.NewFromFloat(legacy))
}

func SetCreditCurrencyBasis(value, legacy decimal.Decimal) error {
	if err := ValidateCreditsPerUSD(value); err != nil {
		return err
	}
	if !legacy.IsPositive() || legacy.GreaterThan(decimal.NewFromInt(MaxWalletQuota)) {
		return errors.New("legacy pricing scale is invalid")
	}
	creditsPerUSD.Store(&creditCurrencyBasis{usd: value.Copy(), legacy: legacy.Copy()})
	return nil
}

func ClearCreditsPerUSD() {
	creditsPerUSD.Store(nil)
	ClearPublicCreditsPerUSD()
}

func CreditsPerUSD() (decimal.Decimal, error) {
	value := creditsPerUSD.Load()
	if value == nil {
		return decimal.Zero, ErrCreditUnitsUnavailable
	}
	return value.usd.Copy(), nil
}

func LegacyPricingQuotaPerUnit() (decimal.Decimal, error) {
	value := creditsPerUSD.Load()
	if value == nil {
		return decimal.Zero, ErrCreditUnitsUnavailable
	}
	return value.legacy.Copy(), nil
}

func validatedLegacyCreditBasis() (*creditCurrencyBasis, error) {
	value := creditsPerUSD.Load()
	if value == nil {
		return nil, ErrCreditUnitsUnavailable
	}
	legacy := QuotaPerUnit
	if math.IsNaN(legacy) || math.IsInf(legacy, 0) || legacy <= 0 || !decimal.NewFromFloat(legacy).Equal(value.legacy) {
		return nil, errors.New("legacy pricing calibration differs from immutable credit currency basis")
	}
	return value, nil
}

// LegacyPricingUnitsPerUSD bridges the retained legacy price tables. It must
// never be used as the wallet's fiat conversion or a recharge bonus.
func LegacyPricingUnitsPerUSD() (decimal.Decimal, error) {
	basis, err := validatedLegacyCreditBasis()
	if err != nil {
		return decimal.Zero, err
	}
	return basis.usd.Div(basis.legacy), nil
}

func LegacyAmountToUSD(amount decimal.Decimal) (decimal.Decimal, error) {
	basis, err := validatedLegacyCreditBasis()
	if err != nil {
		return decimal.Zero, err
	}
	return amount.Mul(basis.legacy).Div(basis.usd), nil
}

func USDToLegacyAmount(amount decimal.Decimal) (decimal.Decimal, error) {
	basis, err := validatedLegacyCreditBasis()
	if err != nil {
		return decimal.Zero, err
	}
	return amount.Mul(basis.usd).Div(basis.legacy), nil
}

func CreditsToUSD(credits int64) (decimal.Decimal, error) {
	anchor, err := CreditsPerUSD()
	if err != nil {
		return decimal.Zero, err
	}
	return decimal.NewFromInt(credits).Div(anchor), nil
}

// FiatToCreditsDecimal deliberately does not round: granting, charging and
// refunding callers choose their own documented integer-ledger rounding rule.
func FiatToCreditsDecimal(amount decimal.Decimal, currency string, cnyPerUSD decimal.Decimal) (decimal.Decimal, error) {
	anchor, err := CreditsPerUSD()
	if err != nil {
		return decimal.Zero, err
	}
	if amount.IsNegative() {
		return decimal.Zero, errors.New("fiat amount must not be negative")
	}
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "USD":
		return amount.Mul(anchor), nil
	case "CNY":
		if !cnyPerUSD.IsPositive() {
			return decimal.Zero, errors.New("CNY per USD must be positive")
		}
		return amount.Mul(anchor).Div(cnyPerUSD), nil
	default:
		return decimal.Zero, errors.New("unsupported fiat currency")
	}
}

func CreditsToFiat(credits int64, currency string, cnyPerUSD decimal.Decimal) (decimal.Decimal, error) {
	// Even CREDIT display requires initialization, so an unavailable currency
	// system cannot accidentally authorize a new financial operation.
	usd, err := CreditsToUSD(credits)
	if err != nil {
		return decimal.Zero, err
	}
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "CREDIT":
		return LedgerQuotaToPublicCredits(credits)
	case "USD":
		return usd, nil
	case "CNY":
		if !cnyPerUSD.IsPositive() {
			return decimal.Zero, errors.New("CNY per USD must be positive")
		}
		return usd.Mul(cnyPerUSD), nil
	default:
		return decimal.Zero, errors.New("unsupported display currency")
	}
}
