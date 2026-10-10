package paymentpricing

import (
	"fmt"
	"math/big"
)

// CreditSettlementRate is an immutable, exact settlement-units/ledger-credit
// ratio. It has no display currency, recharge-batch unit, discount, or live
// configuration. The gateway adapter resolves those before constructing it.
// Invoice currency remains attached to the gateway's settlement contract.
// The zero value is invalid. A constructed rate may be shared by goroutines.
type CreditSettlementRate struct {
	ratio *big.Rat
}

// NewCreditSettlementRate binds a positive settlement amount to a positive
// ledger-credit amount. Decimal strings keep the input exact, including paired
// custom prices whose quotient has no finite decimal representation. Do not
// divide the two amounts before calling this constructor.
func NewCreditSettlementRate(settlementUnits, ledgerCredits string) (CreditSettlementRate, error) {
	numerator, ok := positiveCreditRateDecimal(settlementUnits)
	if !ok {
		return CreditSettlementRate{}, fmt.Errorf("settlement units must be a positive decimal")
	}
	denominator, ok := positiveCreditRateDecimal(ledgerCredits)
	if !ok {
		return CreditSettlementRate{}, fmt.Errorf("ledger credits must be a positive decimal")
	}
	return CreditSettlementRate{ratio: new(big.Rat).Quo(numerator, denominator)}, nil
}

// positiveCreditRateDecimal accepts the ordinary decimal spelling used by the
// gateway parser. Reject exponent/fraction syntax before arbitrary precision
// parsing. This function does not read or change the decimal library's globals.
func positiveCreditRateDecimal(value string) (*big.Rat, bool) {
	if len(value) == 0 {
		return nil, false
	}
	dot := false
	for i := 0; i < len(value); i++ {
		if value[i] == '.' && !dot && i > 0 && i < len(value)-1 {
			dot = true
			continue
		}
		if value[i] < '0' || value[i] > '9' {
			return nil, false
		}
	}
	result, ok := new(big.Rat).SetString(value)
	return result, ok && result.Sign() > 0
}

func (rate CreditSettlementRate) fraction(credits int64) (*big.Int, *big.Int, error) {
	if credits <= 0 {
		return nil, nil, fmt.Errorf("credit amount must be positive")
	}
	if rate.ratio == nil || rate.ratio.Sign() <= 0 {
		return nil, nil, fmt.Errorf("credit settlement rate is unavailable")
	}
	// Never use shared ratio integers as destinations: big.Int operations mutate
	// their receiver, and the rate must remain unchanged across concurrent quotes.
	numerator := new(big.Int).Mul(big.NewInt(credits), rate.ratio.Num())
	denominator := new(big.Int).Set(rate.ratio.Denom())
	return numerator, denominator, nil
}

// QuoteDecimal preserves the existing shared quote's 96-place, half-away-from-
// zero division. It is not an instruction to round an invoice to cents. Callers
// still apply their existing group/channel/preset/coupon and final-rounding policy.
func (rate CreditSettlementRate) QuoteDecimal(credits int64) (string, error) {
	numerator, denominator, err := rate.fraction(credits)
	if err != nil {
		return "", err
	}
	return new(big.Rat).SetFrac(numerator, denominator).FloatString(96), nil
}

// CeilMinor applies the existing upward-rounding policy using an exact integer
// quotient and remainder. It does not first round an exchange rate or quote.
func (rate CreditSettlementRate) CeilMinor(credits, minorUnitsPerSettlement int64) (int64, error) {
	if minorUnitsPerSettlement <= 0 {
		return 0, fmt.Errorf("minor units per settlement unit must be positive")
	}
	numerator, denominator, err := rate.fraction(credits)
	if err != nil {
		return 0, err
	}
	numerator.Mul(numerator, big.NewInt(minorUnitsPerSettlement))
	minor, remainder := new(big.Int), new(big.Int)
	minor.QuoRem(numerator, denominator, remainder)
	if remainder.Sign() > 0 {
		minor.Add(minor, big.NewInt(1))
	}
	if !minor.IsInt64() {
		return 0, fmt.Errorf("settlement minor amount exceeds int64")
	}
	return minor.Int64(), nil
}

// ReachesMinor reports whether the unrounded price reaches one settlement minor
// unit. A minimum-price check must not compare an already upward-rounded quote.
func (rate CreditSettlementRate) ReachesMinor(credits, minorUnitsPerSettlement int64) (bool, error) {
	if minorUnitsPerSettlement <= 0 {
		return false, fmt.Errorf("minor units per settlement unit must be positive")
	}
	numerator, denominator, err := rate.fraction(credits)
	if err != nil {
		return false, err
	}
	numerator.Mul(numerator, big.NewInt(minorUnitsPerSettlement))
	return numerator.Cmp(denominator) >= 0, nil
}
