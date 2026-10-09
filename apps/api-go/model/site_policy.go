package model

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrSitePolicyChanged = errors.New("site policy changed after the preview; read it again and prepare a new confirmation")

// Keep a fixed lock order for primary documents and translations. Generic
// dashboard updates and assistant confirmations take the same database locks.
var sitePolicyOptionKeys = []string{
	"legal.privacy_policy", "legal.privacy_policy_en",
	"legal.refund_policy", "legal.refund_policy_en",
	"legal.user_agreement", "legal.user_agreement_en",
}

func IsSitePolicyOption(key string) bool {
	for _, candidate := range sitePolicyOptionKeys {
		if candidate == key {
			return true
		}
	}
	return false
}

func ReadSitePolicies(ctx context.Context) (map[string]string, error) {
	if DB == nil {
		return nil, errors.New("site policy database is unavailable")
	}
	values := make(map[string]string, len(sitePolicyOptionKeys))
	for _, key := range sitePolicyOptionKeys {
		values[key] = ""
	}
	var rows []Option
	if err := DB.WithContext(ctx).Where("key IN ?", sitePolicyOptionKeys).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	return values, nil
}

func UpdateSitePolicyWithExpectation(values, expected map[string]string) (OptionUpdateResult, error) {
	if len(values) != 1 || len(expected) == 0 {
		return OptionUpdateResult{}, errors.New("one policy and its read baseline are required")
	}
	for key := range values {
		if !IsSitePolicyOption(key) {
			return OptionUpdateResult{}, errors.New("unknown site policy")
		}
		if _, ok := expected[key]; !ok {
			return OptionUpdateResult{}, errors.New("site policy baseline is missing")
		}
	}
	for key := range expected {
		if !IsSitePolicyOption(key) {
			return OptionUpdateResult{}, errors.New("unknown site policy baseline")
		}
	}
	return updateOptionsWithPriceLocksAndExpectations(values, "", false, nil, nil, expected)
}

func lockSitePolicyUpdate(tx *gorm.DB, values, expected map[string]string) error {
	writing := false
	for key := range values {
		writing = writing || IsSitePolicyOption(key)
	}
	if !writing {
		return nil
	}
	for _, key := range sitePolicyOptionKeys {
		row := Option{Key: key, Value: ""}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("key = ?", key).First(&row).Error; err != nil {
			return err
		}
		if baseline, check := expected[key]; check && baseline != row.Value {
			return ErrSitePolicyChanged
		}
	}
	return nil
}
