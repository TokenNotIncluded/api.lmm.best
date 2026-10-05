package model

import (
	"context"
	"errors"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrPublicCreditRevisionConflict = errors.New("credit denomination changed; reload before saving")

type PublicCreditUnitUpdate struct {
	CreditUnitSchemaVersion          int    `json:"credit_unit_schema_version"`
	PublicCreditsPerUSDExact         string `json:"public_credits_per_usd_exact"`
	ExpectedPublicCreditsPerUSDExact string `json:"expected_public_credits_per_usd_exact"`
	ExpectedLedgerQuotaPerUSDExact   string `json:"expected_ledger_quota_per_usd_exact"`
}

// CreditDenominationSnapshot reads one durable generation on every public
// amount boundary. An old mutable display basis cannot authorize a new amount.
func CreditDenominationSnapshot() (common.CreditDenomination, error) {
	if DB == nil {
		return common.CreditDenomination{}, common.ErrCreditUnitsUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return CreditDenominationSnapshotForDB(DB.WithContext(ctx))
}

func CreditDenominationSnapshotForDB(db *gorm.DB) (common.CreditDenomination, error) {
	if db == nil {
		return common.CreditDenomination{}, common.ErrCreditUnitsUnavailable
	}
	var rows []Option
	if err := db.Where("key IN ?", []string{CreditsPerUSDOptionKey, LegacyPricingQuotaPerUnitOptionKey, "QuotaPerUnit", PublicCreditsPerUSDOptionKey}).Find(&rows).Error; err != nil {
		return common.CreditDenomination{}, err
	}
	values := make(map[string]string, len(rows))
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	ledger, err := parseCreditAnchor(values[CreditsPerUSDOptionKey])
	if err != nil {
		return common.CreditDenomination{}, common.ErrCreditUnitsUnavailable
	}
	legacy, err := parsePositiveCreditRate(values[LegacyPricingQuotaPerUnitOptionKey])
	if err != nil {
		return common.CreditDenomination{}, common.ErrCreditUnitsUnavailable
	}
	current, err := parsePositiveCreditRate(values["QuotaPerUnit"])
	if err != nil || !current.Equal(legacy) {
		return common.CreditDenomination{}, ErrPricingUnitsStale
	}
	runtimeLedger, err := common.LedgerQuotaPerUSD()
	if err != nil {
		return common.CreditDenomination{}, err
	}
	runtimeLegacy, err := common.LegacyPricingQuotaPerUnit()
	if err != nil {
		return common.CreditDenomination{}, err
	}
	if !ledger.Equal(runtimeLedger) || !legacy.Equal(runtimeLegacy) {
		return common.CreditDenomination{}, ErrPricingUnitsStale
	}
	public := ledger // Read-only compatibility with an unconfigured old schema.
	if raw, exists := values[PublicCreditsPerUSDOptionKey]; exists {
		public, err = parsePublicCreditRate(raw)
		if err != nil {
			return common.CreditDenomination{}, err
		}
	}
	units, err := common.CreditDenominationFromBasis(ledger, public)
	if err != nil {
		return common.CreditDenomination{}, err
	}
	if _, exists := values[PublicCreditsPerUSDOptionKey]; exists {
		err = common.SetPublicCreditsPerUSD(public)
	} else {
		common.ClearPublicCreditsPerUSD()
	}
	return units, err
}

func GetPublicCreditUnitConfig() (common.CreditDenomination, error) {
	return CreditDenominationSnapshot()
}

func UpdatePublicCreditUnitConfig(request PublicCreditUnitUpdate) (common.CreditDenomination, error) {
	if request.CreditUnitSchemaVersion != common.PublicCreditUnitSchemaVersion || request.ExpectedPublicCreditsPerUSDExact == "" || request.ExpectedLedgerQuotaPerUSDExact == "" {
		return common.CreditDenomination{}, errors.New("version 2 and expected credit bases are required")
	}
	public, err := parsePublicCreditRate(request.PublicCreditsPerUSDExact)
	if err != nil {
		return common.CreditDenomination{}, err
	}
	optionUpdateMutex.Lock()
	defer optionUpdateMutex.Unlock()
	if DB == nil {
		return common.CreditDenomination{}, common.ErrCreditUnitsUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var result common.CreditDenomination
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := validateAuthoritativePricingUnits(tx); err != nil {
			return err
		}
		var row Option
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("key = ?", PublicCreditsPerUSDOptionKey).First(&row).Error; err != nil {
			return common.ErrCreditUnitsUnavailable
		}
		stored, err := parsePublicCreditRate(row.Value)
		if err != nil {
			return err
		}
		ledger, err := common.LedgerQuotaPerUSD()
		if err != nil {
			return err
		}
		if stored.String() != request.ExpectedPublicCreditsPerUSDExact || ledger.String() != request.ExpectedLedgerQuotaPerUSDExact {
			return ErrPublicCreditRevisionConflict
		}
		result, err = common.CreditDenominationFromBasis(ledger, public)
		if err != nil {
			return err
		}
		return tx.Model(&row).Update("value", public.String()).Error
	})
	if err != nil {
		return common.CreditDenomination{}, err
	}
	if err := common.SetPublicCreditsPerUSD(public); err != nil {
		return common.CreditDenomination{}, err
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMap[PublicCreditsPerUSDOptionKey] = public.String()
	common.OptionMapRWMutex.Unlock()
	return result, nil
}
