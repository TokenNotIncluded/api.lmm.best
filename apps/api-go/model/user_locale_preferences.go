package model

import (
	"encoding/json"
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdateUserLocalePreferences merges only the supplied fields under a user-row
// lock. Concurrent language and currency saves cannot erase each other, and
// unknown future settings survive rather than being lost in DTO reserialization.
func UpdateUserLocalePreferences(userID int, language, currency *string) error {
	if userID <= 0 || (language == nil && currency == nil) {
		return errors.New("invalid locale preference update")
	}
	if language != nil && len(*language) > 64 {
		return errors.New("invalid interface language")
	}
	if currency != nil {
		normalized, err := dto.NormalizeSettlementCurrencyPreference(*currency)
		if err != nil {
			return err
		}
		currency = &normalized
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "setting").First(&user, userID).Error; err != nil {
			return err
		}
		values := make(map[string]json.RawMessage)
		if user.Setting != "" {
			if err := json.Unmarshal([]byte(user.Setting), &values); err != nil {
				return err
			}
		}
		if values == nil {
			values = make(map[string]json.RawMessage)
		}
		if language != nil {
			values["language"], _ = json.Marshal(*language)
		}
		if currency != nil {
			values["settlement_currency"], _ = json.Marshal(*currency)
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", userID).Update("setting", string(encoded)).Error
	}); err != nil {
		return err
	}
	return invalidateUserCache(userID)
}

// UpdateUserSettingPreservingLocale is for unrelated preference forms that
// submit an older settings snapshot. Only the dedicated locale endpoint may
// change these two fields, so a concurrent save cannot erase a chosen currency.
func UpdateUserSettingPreservingLocale(userID int, setting dto.UserSetting) error {
	if userID <= 0 {
		return errors.New("invalid user id")
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "setting").First(&user, userID).Error; err != nil {
			return err
		}
		var current dto.UserSetting
		if user.Setting != "" {
			if err := json.Unmarshal([]byte(user.Setting), &current); err != nil {
				return err
			}
		}
		setting.Language = current.Language
		setting.SettlementCurrency = current.SettlementCurrency
		encoded, err := json.Marshal(setting)
		if err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", userID).Update("setting", string(encoded)).Error
	}); err != nil {
		return err
	}
	return invalidateUserCache(userID)
}
