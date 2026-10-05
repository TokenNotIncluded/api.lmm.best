package common

import (
	"errors"
	"math"
	"sync/atomic"

	"github.com/shopspring/decimal"
)

const PublicCreditsPerUSDOptionKey = "PublicCreditsPerUSD"
const PublicCreditUnitSchemaVersion = 2
const LedgerQuotaUnit = "LEDGER_QUOTA"
const PublicCreditUnit = "CREDIT"

var publicCreditsPerUSD atomic.Pointer[decimal.Decimal]

// LedgerQuotaPerUSD is the immutable valuation of existing internal quota
// integers. The historical CreditsPerUSD function remains its compatibility
// alias; changing the public denomination never changes this ledger basis.
func LedgerQuotaPerUSD() (decimal.Decimal, error) { return CreditsPerUSD() }

func ValidatePublicCreditsPerUSD(value decimal.Decimal) error {
	if !value.IsPositive() || !value.IsInteger() || value.GreaterThan(decimal.NewFromInt(MaxWalletQuota)) {
		return errors.New("public credits per USD must be a positive safe integer")
	}
	return nil
}

func SetPublicCreditsPerUSD(value decimal.Decimal) error {
	if err := ValidatePublicCreditsPerUSD(value); err != nil {
		return err
	}
	copy := value.Copy()
	publicCreditsPerUSD.Store(&copy)
	return nil
}

// ClearPublicCreditsPerUSD restores v1 compatibility for a source which has
// not configured a separate public denomination. It does not change a wallet.
func ClearPublicCreditsPerUSD() { publicCreditsPerUSD.Store(nil) }

func PublicCreditsPerUSD() (decimal.Decimal, error) {
	if _, err := LedgerQuotaPerUSD(); err != nil {
		return decimal.Zero, err
	}
	value := publicCreditsPerUSD.Load()
	if value == nil {
		return LedgerQuotaPerUSD()
	}
	return value.Copy(), nil
}

// LedgerQuotaToPublicCredits projects a legacy integer without modifying it.
// The decimal response has 64 fractional places at most; exact input/output
// bases accompany public APIs. UI rounding does not grant or debit balance.
func LedgerQuotaToPublicCredits(quota int64) (decimal.Decimal, error) {
	if quota < MinWalletQuota || quota > MaxWalletQuota {
		return decimal.Zero, errors.New("ledger quota is outside the safe wallet domain")
	}
	ledger, err := LedgerQuotaPerUSD()
	if err != nil {
		return decimal.Zero, err
	}
	public, err := PublicCreditsPerUSD()
	if err != nil {
		return decimal.Zero, err
	}
	return decimal.NewFromInt(quota).Mul(public).DivRound(ledger, 64), nil
}

// PublicCreditsToLedgerQuota belongs only to explicitly versioned CREDIT
// input boundaries. Existing quota inputs and paid-order integers never pass
// through it. Positive fractional ledger dust is floored, never overgranted.
func PublicCreditsToLedgerQuota(amount decimal.Decimal) (int64, error) {
	if amount.IsNegative() || amount.Exponent() < -18 || amount.Exponent() > 18 || len(amount.Coefficient().String()) > 80 {
		return 0, errors.New("public credit amount is invalid")
	}
	ledger, err := LedgerQuotaPerUSD()
	if err != nil {
		return 0, err
	}
	public, err := PublicCreditsPerUSD()
	if err != nil {
		return 0, err
	}
	quotient, _ := amount.Mul(ledger).QuoRem(public, 0)
	if quotient.GreaterThan(decimal.NewFromInt(MaxWalletQuota)) || (amount.IsPositive() && !quotient.IsPositive()) {
		return 0, errors.New("public credit amount is outside the representable ledger domain")
	}
	return quotient.IntPart(), nil
}

// CreditDenomination identifies compatibility quota fields independently of
// the public CREDIT unit. credits_per_usd in schema-2 pricing remains a legacy
// ledger calibration and is never replaced by PublicCreditsPerUSD.
type CreditDenomination struct {
	CreditUnitSchemaVersion  int     `json:"credit_unit_schema_version"`
	QuotaUnit                string  `json:"quota_unit"`
	PublicCreditUnit         string  `json:"public_credit_unit"`
	LegacyCreditUnit         string  `json:"legacy_credit_unit"`
	LedgerQuotaPerUSD        float64 `json:"ledger_quota_per_usd"`
	LedgerQuotaPerUSDExact   string  `json:"ledger_quota_per_usd_exact"`
	PublicCreditsPerUSD      float64 `json:"public_credits_per_usd"`
	PublicCreditsPerUSDExact string  `json:"public_credits_per_usd_exact"`
}

func CreditDenominationMetadata() (CreditDenomination, error) {
	ledger, err := LedgerQuotaPerUSD()
	if err != nil {
		return CreditDenomination{}, err
	}
	public, err := PublicCreditsPerUSD()
	if err != nil {
		return CreditDenomination{}, err
	}
	l, p := ledger.InexactFloat64(), public.InexactFloat64()
	if l <= 0 || p <= 0 || math.IsNaN(l) || math.IsNaN(p) || math.IsInf(l, 0) || math.IsInf(p, 0) {
		return CreditDenomination{}, ErrCreditUnitsUnavailable
	}
	return CreditDenomination{CreditUnitSchemaVersion: PublicCreditUnitSchemaVersion, QuotaUnit: LedgerQuotaUnit, PublicCreditUnit: PublicCreditUnit, LegacyCreditUnit: LedgerQuotaUnit, LedgerQuotaPerUSD: l, LedgerQuotaPerUSDExact: ledger.String(), PublicCreditsPerUSD: p, PublicCreditsPerUSDExact: public.String()}, nil
}
