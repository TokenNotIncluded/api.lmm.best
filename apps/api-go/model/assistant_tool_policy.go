package model

import (
	"context"
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrAssistantToolPolicyConflict = errors.New("assistant tool policy changed since the browser baseline")

func lockAssistantToolPolicyUpdate(tx *gorm.DB, values map[string]string, expected *string) error {
	raw, writing := values[setting.AssistantToolPolicyOptionKey]
	if !writing {
		if expected != nil {
			return errors.New("assistant tool policy expectation requires a policy update")
		}
		return nil
	}
	canonical, _, err := setting.NormalizeAssistantToolPolicy(raw)
	if err != nil {
		return err
	}
	policy := Option{Key: setting.AssistantToolPolicyOptionKey, Value: setting.DefaultAssistantToolPolicy}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&policy).Error; err != nil {
		return err
	}
	if err := lockForUpdate(tx).Where("key = ?", policy.Key).First(&policy).Error; err != nil {
		return err
	}
	if expected != nil {
		baseline, _, err := setting.NormalizeAssistantToolPolicy(*expected)
		if err != nil {
			return err
		}
		current, _, err := setting.NormalizeAssistantToolPolicy(policy.Value)
		if err != nil {
			return err
		}
		if baseline != current {
			return ErrAssistantToolPolicyConflict
		}
	}
	values[policy.Key] = canonical
	return nil
}

// Read the committed policy at a capability boundary. Background option sync
// is deliberately insufficient for revocation across multiple Go instances.
func ReadAssistantToolPolicy(ctx context.Context) (string, setting.AssistantToolPolicy, error) {
	if DB == nil {
		return "", setting.AssistantToolPolicy{}, errors.New("assistant tool policy database is unavailable")
	}
	return ReadAssistantToolPolicyDB(DB.WithContext(ctx))
}

// A reward transaction retains the same policy row until it commits.
func ReadAssistantToolPolicyDB(db *gorm.DB) (string, setting.AssistantToolPolicy, error) {
	if db == nil {
		return "", setting.AssistantToolPolicy{}, errors.New("assistant tool policy database is unavailable")
	}
	var option Option
	if err := lockForShare(db).Select("value").Where("key = ?", setting.AssistantToolPolicyOptionKey).Limit(1).Find(&option).Error; err != nil {
		return "", setting.AssistantToolPolicy{}, errors.New("assistant tool policy could not be loaded")
	}
	canonical, policy, err := setting.NormalizeAssistantToolPolicy(option.Value)
	if err != nil {
		return "", setting.AssistantToolPolicy{}, errors.New("assistant tool policy is invalid")
	}
	return canonical, policy, nil
}

func AssistantToolLevelDB(db *gorm.DB, userID int) (int, error) {
	if db == nil || userID <= 0 {
		return 0, gorm.ErrInvalidData
	}
	var user User
	if err := db.Select("id", "status", "role", "trust_level_override", "created_at", "last_api_activity_at", "console_activated_at").First(&user, userID).Error; err != nil {
		return 0, err
	}
	if user.Status != common.UserStatusEnabled {
		return 0, errors.New("account is not active")
	}
	snapshot, err := getFreshUserAccessSnapshotDB(db, &user)
	if err != nil {
		return 0, err
	}
	return snapshot.TrustLevel.Level, nil
}

func assistantWeeklyDiscountLimitDB(db *gorm.DB, userID int) (int, error) {
	_, policy, err := ReadAssistantToolPolicyDB(db)
	if err != nil {
		return 0, err
	}
	level, err := AssistantToolLevelDB(db, userID)
	if err != nil {
		return 0, err
	}
	if !policy.AllowedAtLevel("prepare_weekly_discount", level) || level >= 5 {
		return 0, nil
	}
	return policy.WeeklyDiscountLimit(level), nil
}
