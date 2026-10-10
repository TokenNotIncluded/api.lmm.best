package model

import (
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"gorm.io/gorm"
)

// Use the durable option in a granting transaction, so a different node cannot
// grant above a recently lowered cap using its stale process-local cache.
func AssistantGiftMaxCreditsDB(db *gorm.DB) (int, error) {
	if db == nil {
		return 0, gorm.ErrInvalidDB
	}
	var option Option
	result := lockForShare(db).Where("key = ?", setting.AssistantNewUserGiftMaxCreditsOptionKey).Limit(1).Find(&option)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		return setting.AssistantNewUserGiftMaxCredits("")
	}
	if _, err := setting.ParseAssistantNewUserGiftMaxCredits(option.Value); err != nil {
		return 0, err
	}
	return setting.AssistantNewUserGiftMaxCredits(option.Value)
}

func checkAssistantGiftLimitTx(tx *gorm.DB, credits int) error {
	cap, err := AssistantGiftMaxCreditsDB(tx)
	if err != nil {
		return err
	}
	return CheckAssistantGiftCreditLimit(credits, cap)
}

// The administrator's upper bound is exclusive. Check again at claim time.
func CheckAssistantGiftCreditLimit(credits, cap int) error {
	if credits < 0 {
		return assistantGiftError("invalid_decision", ErrAssistantGiftInvalid)
	}
	if cap <= 0 {
		return assistantGiftError("gift_disabled", ErrAssistantGiftDisabled)
	}
	if credits >= cap {
		return assistantGiftError("gift_limit_exceeded", ErrAssistantGiftLimit)
	}
	return nil
}
