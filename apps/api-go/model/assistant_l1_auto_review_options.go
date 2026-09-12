package model

import (
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func validateAssistantL1AutoReviewValues(values map[string]string) error {
	candidate, err := setting.ParseAssistantL1AutoReviewSettings(setting.GetAssistantL1AutoReviewSettings(), values)
	if err != nil {
		return err
	}
	return validateAssistantL1AutoReviewRoute(DB, candidate)
}

func validateAssistantL1AutoReviewRoute(db *gorm.DB, candidate setting.AssistantL1AutoReviewSettings) error {
	// Always allow disabling, even after an operator removes the old model or
	// group. Disabled configuration can be staged without starting a reviewer.
	if !candidate.Enabled {
		return nil
	}
	if !candidate.Ready() {
		return errors.New("configure the L1 review group, model, prompt and confidence before enabling automatic review")
	}
	if !ratio_setting.ContainsGroupRatio(candidate.Group) {
		return errors.New("L1 automatic review routing group must be an existing group")
	}
	if !isModelEnabledForGroup(db, candidate.Group, candidate.Model) {
		return errors.New("L1 automatic review model must be enabled in the selected group")
	}
	return nil
}

// All L1 configuration writers and decision transactions lock the same row,
// including a first-time enable. This also fences writes by another Go node.
func lockAssistantL1AutoReviewOptions(tx *gorm.DB, values map[string]string) error {
	changed := false
	for key := range values {
		if setting.IsAssistantL1AutoReviewOption(key) {
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	policy := Option{Key: setting.AssistantL1AutoReviewEnabledOptionKey, Value: "false"}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&policy).Error; err != nil {
		return err
	}
	if err := lockForUpdate(tx).Where("key = ?", policy.Key).First(&policy).Error; err != nil {
		return err
	}
	stored, err := assistantL1AutoReviewOptionValues(tx)
	if err != nil {
		return err
	}
	for key, value := range values {
		if setting.IsAssistantL1AutoReviewOption(key) {
			stored[key] = value
		}
	}
	candidate, err := setting.ParseAssistantL1AutoReviewSettings(setting.DefaultAssistantL1AutoReviewSettings(), stored)
	if err != nil {
		return err
	}
	if err := validateAssistantL1AutoReviewRoute(tx, candidate); err != nil {
		return err
	}
	// Publish the authoritative complete snapshot after commit, not a mixture
	// of this node's stale cache and another node's newer configuration.
	for key, value := range candidate.OptionValues() {
		values[key] = value
	}
	return nil
}

func assistantL1AutoReviewOptionValues(tx *gorm.DB) (map[string]string, error) {
	values := setting.DefaultAssistantL1AutoReviewSettings().OptionValues()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	var rows []Option
	if err := tx.Where("key IN ?", keys).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	return values, nil
}

func checkAssistantL1AutoReviewOptions(tx *gorm.DB, expected setting.AssistantL1AutoReviewSettings) error {
	var enabled Option
	if err := lockForUpdate(tx).Where("key = ?", setting.AssistantL1AutoReviewEnabledOptionKey).First(&enabled).Error; err != nil {
		return err
	}
	values, err := assistantL1AutoReviewOptionValues(tx)
	if err != nil {
		return err
	}
	stored, err := setting.ParseAssistantL1AutoReviewSettings(setting.DefaultAssistantL1AutoReviewSettings(), values)
	if err != nil {
		return err
	}
	if stored != expected || !stored.Ready() {
		return errors.New("L1 automatic review configuration changed in storage")
	}
	return nil
}

func applyAssistantL1AutoReviewOptionMap(values map[string]string) error {
	updates := make(map[string]string)
	for key, value := range values {
		if setting.IsAssistantL1AutoReviewOption(key) {
			updates[key] = value
		}
	}
	if len(updates) == 0 {
		return nil
	}
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	if err := setting.UpdateAssistantL1AutoReviewOptions(updates); err != nil {
		return err
	}
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	for key, value := range updates {
		common.OptionMap[key] = value
	}
	return nil
}
