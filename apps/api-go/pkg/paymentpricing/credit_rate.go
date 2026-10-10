package paymentpricing

import (
	"errors"
	"math/big"
)

// creditRate is an exact amount of settlement currency per integer wallet
// credit. Currency stays on the invoice contract. Configuration adapters must
// resolve their units before constructing this value; quoting knows nothing
// about display currencies, old request units, or promotion settings.
//
// The rational is owned by the rate and never mutated after construction.
// Keeping a fraction avoids rounding a repeating configured rate before the
// actual number of credits is known.
type creditRate struct {
	perCredit *big.Rat
}

func newCreditRate(settlementUnits, credits *big.Rat) (creditRate, error) {
	if settlementUnits == nil || credits == nil || settlementUnits.Sign() <= 0 || credits.Sign() <= 0 {
		return creditRate{}, errors.New("credit settlement rate must be positive")
	}
	return creditRate{perCredit: new(big.Rat).Quo(settlementUnits, credits)}, nil
}

func (rate creditRate) quote(credits int64) (*big.Rat, error) {
	if credits <= 0 {
		return nil, errors.New("credit amount must be positive")
	}
	if rate.perCredit == nil || rate.perCredit.Sign() <= 0 {
		return nil, errors.New("credit settlement rate is unavailable")
	}
	return new(big.Rat).Mul(new(big.Rat).SetInt64(credits), rate.perCredit), nil
}

func (rate creditRate) ceilMinor(credits, minorUnitsPerSettlement int64) (int64, error) {
	if minorUnitsPerSettlement <= 0 {
		return 0, errors.New("minor units per settlement unit must be positive")
	}
	amount, err := rate.quote(credits)
	if err != nil {
		return 0, err
	}
	amount.Mul(amount, new(big.Rat).SetInt64(minorUnitsPerSettlement))
	minor, remainder := new(big.Int), new(big.Int)
	minor.QuoRem(amount.Num(), amount.Denom(), remainder)
	if remainder.Sign() != 0 {
		minor.Add(minor, big.NewInt(1))
	}
	if !minor.IsInt64() {
		return 0, errors.New("settlement minor amount exceeds int64")
	}
	return minor.Int64(), nil
}
