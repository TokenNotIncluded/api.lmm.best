package paymentpricing

import (
	"fmt"
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

// QuoteSettlementForCredits adapts a stored payment configuration to the
// integer-credit calculator. Promotions stay outside the base quote. Preserve
// the existing 96-place decimal API; callers requiring exact upward rounding
// must use CeilSettlementMinorForCredits.
func QuoteSettlementForCredits(credits int64, pricing SettlementPricing, creditsPerUSD, quotaPerUnit decimal.Decimal) (decimal.Decimal, error) {
	rate, err := settlementCreditRate(pricing, creditsPerUSD, quotaPerUnit)
	if err != nil {
		return decimal.Zero, err
	}
	amount, err := rate.quote(credits)
	if err != nil {
		return decimal.Zero, err
	}
	return decimal.NewFromBigInt(amount.Num(), 0).DivRound(decimal.NewFromBigInt(amount.Denom(), 0), 96), nil
}

// CeilSettlementMinorForCredits shares the same normalized price as the quote.
// It rounds the exact fraction only at the provider's minor-unit boundary.
func CeilSettlementMinorForCredits(credits int64, pricing SettlementPricing, creditsPerUSD, quotaPerUnit decimal.Decimal, minorUnitsPerSettlement int64) (int64, error) {
	rate, err := settlementCreditRate(pricing, creditsPerUSD, quotaPerUnit)
	if err != nil {
		return 0, err
	}
	return rate.ceilMinor(credits, minorUnitsPerSettlement)
}

// settlementCreditRate is the configuration compatibility boundary. Fixed
// fiat, direct custom prices and paired custom prices all become an exact
// settlement-currency-per-credit rate. Never replace a configured denominator
// with 1 or pre-round paired rates; both would change existing valid prices.
func settlementCreditRate(pricing SettlementPricing, creditsPerUSD, quotaPerUnit decimal.Decimal) (creditRate, error) {
	if pricing.UsesFixedCreditDenomination && pricing.UsesSettlementUnitsPerPlatformUnit {
		return creditRate{}, fmt.Errorf("conflicting settlement pricing modes")
	}
	if pricing.UsesFixedCreditDenomination {
		if !creditsPerUSD.IsPositive() || !pricing.SettlementUnitsPerUSD.IsPositive() {
			return creditRate{}, fmt.Errorf("invalid credit settlement pricing")
		}
		return newCreditRate(pricing.SettlementUnitsPerUSD.Rat(), creditsPerUSD.Rat())
	}
	if !quotaPerUnit.IsPositive() {
		return creditRate{}, fmt.Errorf("quota per platform unit must be positive")
	}
	if pricing.UsesSettlementUnitsPerPlatformUnit {
		if !pricing.SettlementUnitsPerPlatformUnit.IsPositive() {
			return creditRate{}, fmt.Errorf("settlement units per platform unit must be positive")
		}
		return newCreditRate(pricing.SettlementUnitsPerPlatformUnit.Rat(), quotaPerUnit.Rat())
	}
	if !pricing.PlatformUnitsPerUSD.IsPositive() || !pricing.SettlementUnitsPerUSD.IsPositive() {
		return creditRate{}, fmt.Errorf("USD settlement rates must be positive")
	}
	return newCreditRate(pricing.SettlementUnitsPerUSD.Rat(), quotaPerUnit.Mul(pricing.PlatformUnitsPerUSD).Rat())
}
