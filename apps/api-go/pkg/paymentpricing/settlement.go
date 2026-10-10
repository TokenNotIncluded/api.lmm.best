package paymentpricing

import (
	"fmt"
	"math"
	"regexp"

	"github.com/shopspring/decimal"
)

var settlementRatePattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
var settlementCurrencyPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,16}$`)

// SettlementPricing describes the gateway's base settlement contract. It does
// not include recharge group ratios, channel bonuses, discounts or coupons.
// Platform units are the legacy batch denomination, not public CREDIT units.
type SettlementPricing struct {
	SettlementCurrency                 string
	PlatformUnitsPerUSD                decimal.Decimal
	SettlementUnitsPerUSD              decimal.Decimal
	SettlementUnitsPerPlatformUnit     decimal.Decimal
	UsesSettlementUnitsPerPlatformUnit bool
	UsesFixedCreditDenomination        bool
}

// ParseSettlementPricing parses a PayMethods row without reading configuration
// or changing the row. Custom gateways require an explicit settlement currency;
// there is no implicit fiat fallback. Alipay and wxpay retain their CNY contract.
// defaultPlatformUnitsPerUSD is the caller's immutable K/QPU compatibility basis
// and is used only when a paired settlement rate omits its platform rate, or for
// standard fiat pricing. cnyPerUSD is used only for standard CNY pricing; explicit
// LDC rates never require CNY FX. Non-fiat currencies require explicit pricing.
func ParseSettlementPricing(paymentMethod string, method map[string]string, defaultPlatformUnitsPerUSD, cnyPerUSD decimal.Decimal) (SettlementPricing, error) {
	currency := normalizeCurrency(method["settlement_currency"])
	if currency == "" {
		currency = normalizeCurrency(method["settlement_unit"])
	}
	builtinCNY := paymentMethod == "alipay" || paymentMethod == "wxpay"
	if builtinCNY {
		currency = CurrencyCNY
	}
	if !settlementCurrencyPattern.MatchString(currency) {
		return SettlementPricing{}, fmt.Errorf("payment method %q has invalid settlement_unit", paymentMethod)
	}

	platformRaw, hasPlatformRate := method["platform_units_per_usd"]
	settlementRaw, hasSettlementRate := method["settlement_units_per_usd"]
	directRaw, hasDirectRate := method["settlement_units_per_platform_unit"]
	legacyRaw, hasLegacyRate := method["unit_price"]
	if builtinCNY || (!hasPlatformRate && !hasSettlementRate && !hasDirectRate && !hasLegacyRate) {
		if !defaultPlatformUnitsPerUSD.IsPositive() {
			return SettlementPricing{}, fmt.Errorf("platform units per USD must be positive")
		}
		var rate decimal.Decimal
		switch currency {
		case CurrencyUSD:
			rate = decimal.NewFromInt(1)
		case CurrencyCNY:
			if !cnyPerUSD.IsPositive() {
				return SettlementPricing{}, fmt.Errorf("CNY per USD rate must be positive")
			}
			rate = cnyPerUSD
		default:
			return SettlementPricing{}, fmt.Errorf("unsupported standard settlement currency %q", currency)
		}
		return SettlementPricing{
			SettlementCurrency:          currency,
			PlatformUnitsPerUSD:         defaultPlatformUnitsPerUSD,
			SettlementUnitsPerUSD:       rate,
			UsesFixedCreditDenomination: true,
		}, nil
	}
	if hasPlatformRate && !hasSettlementRate {
		return SettlementPricing{}, fmt.Errorf("payment method %q configures platform_units_per_usd without settlement_units_per_usd", paymentMethod)
	}
	if hasSettlementRate && (hasDirectRate || hasLegacyRate) {
		return SettlementPricing{}, fmt.Errorf("payment method %q mixes FX and per-platform-unit pricing", paymentMethod)
	}
	if hasSettlementRate {
		platformRate := defaultPlatformUnitsPerUSD
		if hasPlatformRate {
			var err error
			platformRate, err = parseSettlementRate(paymentMethod, "platform_units_per_usd", platformRaw)
			if err != nil {
				return SettlementPricing{}, err
			}
		} else if !platformRate.IsPositive() {
			return SettlementPricing{}, fmt.Errorf("payment method %q requires a configured platform USD rate", paymentMethod)
		}
		settlementRate, err := parseSettlementRate(paymentMethod, "settlement_units_per_usd", settlementRaw)
		if err != nil {
			return SettlementPricing{}, err
		}
		return SettlementPricing{
			SettlementCurrency:    currency,
			PlatformUnitsPerUSD:   platformRate,
			SettlementUnitsPerUSD: settlementRate,
		}, nil
	}
	if !hasDirectRate {
		directRaw = legacyRaw
	}
	directRate, err := parseSettlementRate(paymentMethod, "settlement_units_per_platform_unit", directRaw)
	if err != nil {
		return SettlementPricing{}, err
	}
	if hasDirectRate && hasLegacyRate {
		legacyRate, err := parseSettlementRate(paymentMethod, "unit_price", legacyRaw)
		if err != nil {
			return SettlementPricing{}, err
		}
		if !directRate.Equal(legacyRate) {
			return SettlementPricing{}, fmt.Errorf("payment method %q has conflicting per-platform-unit rates", paymentMethod)
		}
	}
	return SettlementPricing{
		SettlementCurrency:                 currency,
		SettlementUnitsPerPlatformUnit:     directRate,
		UsesSettlementUnitsPerPlatformUnit: true,
	}, nil
}

func parseSettlementRate(paymentMethod, field, raw string) (decimal.Decimal, error) {
	if !settlementRatePattern.MatchString(raw) {
		return decimal.Zero, fmt.Errorf("payment method %q has invalid %s", paymentMethod, field)
	}
	rate, err := decimal.NewFromString(raw)
	if err != nil || !rate.IsPositive() {
		return decimal.Zero, fmt.Errorf("payment method %q has invalid %s", paymentMethod, field)
	}
	return rate, nil
}

// QuoteSettlementForCredits quotes raw ledger quota, before any recharge bonus
// or money rounding. creditsPerUSD is K; quotaPerUnit is QPU. Only the basis for
// the selected mode is required. Paired pricing divides q*u by QPU*p as a whole,
// never by a pre-rounded u/p rate. Division uses 96 fractional digits. Callers
// that require exact upward rounding to money minor units must use
// CeilSettlementMinorForCredits rather than rounding this finite decimal quote.
func QuoteSettlementForCredits(credits int64, pricing SettlementPricing, creditsPerUSD, quotaPerUnit decimal.Decimal) (decimal.Decimal, error) {
	numerator, denominator, err := settlementQuoteFraction(credits, pricing, creditsPerUSD, quotaPerUnit)
	if err != nil {
		return decimal.Zero, err
	}
	return numerator.DivRound(denominator, 96), nil
}

// CeilSettlementMinorForCredits returns the exact upward-rounded base amount
// in money minor units (100 minor units per settlement unit for two decimals).
// It uses an integer quotient and remainder, without a rounded exchange rate
// or decimal division. It fails if the result cannot fit the invoice int64.
func CeilSettlementMinorForCredits(credits int64, pricing SettlementPricing, creditsPerUSD, quotaPerUnit decimal.Decimal, minorUnitsPerSettlement int64) (int64, error) {
	if minorUnitsPerSettlement <= 0 {
		return 0, fmt.Errorf("minor units per settlement unit must be positive")
	}
	numerator, denominator, err := settlementQuoteFraction(credits, pricing, creditsPerUSD, quotaPerUnit)
	if err != nil {
		return 0, err
	}
	minor, remainder := numerator.Mul(decimal.NewFromInt(minorUnitsPerSettlement)).QuoRem(denominator, 0)
	if remainder.IsPositive() {
		minor = minor.Add(decimal.NewFromInt(1))
	}
	if minor.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, fmt.Errorf("settlement minor amount exceeds int64")
	}
	return minor.IntPart(), nil
}

func settlementQuoteFraction(credits int64, pricing SettlementPricing, creditsPerUSD, quotaPerUnit decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	if credits <= 0 {
		return decimal.Zero, decimal.Zero, fmt.Errorf("credit amount must be positive")
	}
	if pricing.UsesFixedCreditDenomination && pricing.UsesSettlementUnitsPerPlatformUnit {
		return decimal.Zero, decimal.Zero, fmt.Errorf("conflicting settlement pricing modes")
	}
	amount := decimal.NewFromInt(credits)
	if pricing.UsesFixedCreditDenomination {
		if !creditsPerUSD.IsPositive() || !pricing.SettlementUnitsPerUSD.IsPositive() {
			return decimal.Zero, decimal.Zero, fmt.Errorf("invalid credit settlement pricing")
		}
		return amount.Mul(pricing.SettlementUnitsPerUSD), creditsPerUSD, nil
	}
	if !quotaPerUnit.IsPositive() {
		return decimal.Zero, decimal.Zero, fmt.Errorf("quota per platform unit must be positive")
	}
	if pricing.UsesSettlementUnitsPerPlatformUnit {
		if !pricing.SettlementUnitsPerPlatformUnit.IsPositive() {
			return decimal.Zero, decimal.Zero, fmt.Errorf("settlement units per platform unit must be positive")
		}
		return amount.Mul(pricing.SettlementUnitsPerPlatformUnit), quotaPerUnit, nil
	}
	if !pricing.PlatformUnitsPerUSD.IsPositive() || !pricing.SettlementUnitsPerUSD.IsPositive() {
		return decimal.Zero, decimal.Zero, fmt.Errorf("USD settlement rates must be positive")
	}
	return amount.Mul(pricing.SettlementUnitsPerUSD), quotaPerUnit.Mul(pricing.PlatformUnitsPerUSD), nil
}
