package common

import (
	"errors"
	"math"
	"strings"
	"sync/atomic"

	"github.com/shopspring/decimal"
)

// Credits are the integer wallet ledger. This anchor is initialized from the
// durable option once at startup, independently of subsequent exchange rates.
var creditsPerUSD atomic.Pointer[decimal.Decimal]

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
	if err := ValidateCreditsPerUSD(value); err != nil {
		return err
	}
	copy := value.Copy()
	creditsPerUSD.Store(&copy)
	return nil
}

func ClearCreditsPerUSD() { creditsPerUSD.Store(nil) }

func CreditsPerUSD() (decimal.Decimal, error) {
	value := creditsPerUSD.Load()
	if value == nil {
		return decimal.Zero, ErrCreditUnitsUnavailable
	}
	return value.Copy(), nil
}

// LegacyPricingUnitsPerUSD bridges the retained legacy price tables. It must
// never be used as the wallet's fiat conversion or a recharge bonus.
func LegacyPricingUnitsPerUSD() (decimal.Decimal, error) {
	anchor, err := CreditsPerUSD()
	if err != nil {
		return decimal.Zero, err
	}
	if math.IsNaN(QuotaPerUnit) || math.IsInf(QuotaPerUnit, 0) || QuotaPerUnit <= 0 {
		return decimal.Zero, errors.New("legacy pricing scale is invalid")
	}
	return anchor.Div(decimal.NewFromFloat(QuotaPerUnit)), nil
}

func LegacyAmountToUSD(amount decimal.Decimal) (decimal.Decimal, error) {
	_, err := LegacyPricingUnitsPerUSD()
	if err != nil {
		return decimal.Zero, err
	}
	anchor, _ := CreditsPerUSD()
	return amount.Mul(decimal.NewFromFloat(QuotaPerUnit)).Div(anchor), nil
}

func USDToLegacyAmount(amount decimal.Decimal) (decimal.Decimal, error) {
	scale, err := LegacyPricingUnitsPerUSD()
	if err != nil {
		return decimal.Zero, err
	}
	return amount.Mul(scale), nil
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
		return decimal.NewFromInt(credits), nil
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
