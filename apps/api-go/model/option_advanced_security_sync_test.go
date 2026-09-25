package model

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLoadOptionsFromDatabasePublishesAdvancedSecurityAsUnit(t *testing.T) {
	originalDB := DB
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "advanced-sync.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&Option{}))
	DB = database

	originalSettings := setting.GetAdvancedSecuritySettings()
	originalRules, err := json.Marshal(originalSettings.RuleSet)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		DB = originalDB
		sqlDB, err := database.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
		_ = setting.ApplyAdvancedSecuritySettings(
			originalSettings.Enabled,
			originalSettings.OnPrompt,
			originalSettings.Action,
			string(originalRules),
		)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	baselineRules := `{"version":1,"rules":[{"id":"baseline","enabled":true,"groups":["default"],"patterns":["baseline pattern"]}]}`
	require.NoError(t, setting.ApplyAdvancedSecuritySettings(false, true, setting.AdvancedSecurityActionBlock, baselineRules))
	baselineValues := map[string]string{
		setting.AdvancedSecurityEnabledOptionKey:  "false",
		setting.AdvancedSecurityOnPromptOptionKey: "true",
		setting.AdvancedSecurityActionOptionKey:   setting.AdvancedSecurityActionBlock,
		setting.AdvancedSecurityRulesOptionKey:    baselineRules,
	}
	common.OptionMapRWMutex.Lock()
	for key, value := range baselineValues {
		common.OptionMap[key] = value
	}
	common.OptionMapRWMutex.Unlock()

	nextRules := `{"version":1,"rules":[{"id":"next","enabled":true,"groups":["fast"],"patterns":["next pattern"]}]}`
	options := []Option{
		{Key: setting.AdvancedSecurityEnabledOptionKey, Value: "true"},
		{Key: setting.AdvancedSecurityOnPromptOptionKey, Value: "false"},
		{Key: setting.AdvancedSecurityActionOptionKey, Value: setting.AdvancedSecurityActionAudit},
		{Key: setting.AdvancedSecurityRulesOptionKey, Value: nextRules},
	}
	require.NoError(t, database.Create(&options).Error)

	loadOptionsFromDatabase()

	applied := setting.GetAdvancedSecuritySettings()
	assert.True(t, applied.Enabled)
	assert.False(t, applied.OnPrompt)
	assert.Equal(t, setting.AdvancedSecurityActionAudit, applied.Action)
	require.Len(t, applied.RuleSet.Rules, 1)
	assert.Equal(t, "next", applied.RuleSet.Rules[0].ID)

	common.OptionMapRWMutex.RLock()
	assert.Equal(t, "true", common.OptionMap[setting.AdvancedSecurityEnabledOptionKey])
	assert.Equal(t, "false", common.OptionMap[setting.AdvancedSecurityOnPromptOptionKey])
	assert.Equal(t, setting.AdvancedSecurityActionAudit, common.OptionMap[setting.AdvancedSecurityActionOptionKey])
	assert.JSONEq(t, nextRules, common.OptionMap[setting.AdvancedSecurityRulesOptionKey])
	common.OptionMapRWMutex.RUnlock()

	require.NoError(t, database.Model(&Option{}).
		Where("key = ?", setting.AdvancedSecurityEnabledOptionKey).
		Update("value", "false").Error)
	require.NoError(t, database.Model(&Option{}).
		Where("key = ?", setting.AdvancedSecurityActionOptionKey).
		Update("value", "invalid").Error)

	loadOptionsFromDatabase()

	afterInvalid := setting.GetAdvancedSecuritySettings()
	assert.True(t, afterInvalid.Enabled)
	assert.False(t, afterInvalid.OnPrompt)
	assert.Equal(t, setting.AdvancedSecurityActionAudit, afterInvalid.Action)
	require.Len(t, afterInvalid.RuleSet.Rules, 1)
	assert.Equal(t, "next", afterInvalid.RuleSet.Rules[0].ID)

	common.OptionMapRWMutex.RLock()
	assert.Equal(t, "true", common.OptionMap[setting.AdvancedSecurityEnabledOptionKey])
	assert.Equal(t, setting.AdvancedSecurityActionAudit, common.OptionMap[setting.AdvancedSecurityActionOptionKey])
	common.OptionMapRWMutex.RUnlock()
}

func TestApplyAdvancedSecurityOptionValuesRejectsInvalidBooleanWithoutMutation(t *testing.T) {
	originalSettings := setting.GetAdvancedSecuritySettings()
	originalRules, err := json.Marshal(originalSettings.RuleSet)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = setting.ApplyAdvancedSecuritySettings(
			originalSettings.Enabled,
			originalSettings.OnPrompt,
			originalSettings.Action,
			string(originalRules),
		)
	})

	rules := `{"version":1,"rules":[{"id":"stable","enabled":true,"groups":["default"],"patterns":["stable pattern"]}]}`
	require.NoError(t, setting.ApplyAdvancedSecuritySettings(true, true, setting.AdvancedSecurityActionBlock, rules))

	err = applyAdvancedSecurityOptionValues(map[string]string{
		setting.AdvancedSecurityEnabledOptionKey:  "not-a-bool",
		setting.AdvancedSecurityOnPromptOptionKey: "false",
		setting.AdvancedSecurityActionOptionKey:   setting.AdvancedSecurityActionAudit,
		setting.AdvancedSecurityRulesOptionKey:    `{"version":1,"rules":[]}`,
	})
	require.Error(t, err)

	current := setting.GetAdvancedSecuritySettings()
	assert.True(t, current.Enabled)
	assert.True(t, current.OnPrompt)
	assert.Equal(t, setting.AdvancedSecurityActionBlock, current.Action)
	require.Len(t, current.RuleSet.Rules, 1)
	assert.Equal(t, "stable", current.RuleSet.Rules[0].ID)
}
