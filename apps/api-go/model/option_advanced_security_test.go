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

func TestUpdateAdvancedSecurityOptionsPersistsAndAppliesAsUnit(t *testing.T) {
	originalDB := DB
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "options.db")), &gorm.Config{})
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

	rules := `{"version":1,"rules":[{"id":"privacy","enabled":true,"groups":["default"],"patterns":["doxx this person"]}]}`
	require.NoError(t, UpdateAdvancedSecurityOptions(true, true, setting.AdvancedSecurityActionAudit, rules))

	var options []Option
	require.NoError(t, database.Find(&options).Error)
	require.Len(t, options, 4)
	persisted := make(map[string]string, len(options))
	for _, option := range options {
		persisted[option.Key] = option.Value
	}
	assert.Equal(t, "true", persisted[setting.AdvancedSecurityEnabledOptionKey])
	assert.Equal(t, "true", persisted[setting.AdvancedSecurityOnPromptOptionKey])
	assert.Equal(t, setting.AdvancedSecurityActionAudit, persisted[setting.AdvancedSecurityActionOptionKey])
	assert.JSONEq(t, rules, persisted[setting.AdvancedSecurityRulesOptionKey])

	runtimeSettings := setting.GetAdvancedSecuritySettings()
	assert.True(t, runtimeSettings.Enabled)
	assert.True(t, runtimeSettings.OnPrompt)
	assert.Equal(t, setting.AdvancedSecurityActionAudit, runtimeSettings.Action)
	require.Len(t, runtimeSettings.RuleSet.Rules, 1)
	assert.Equal(t, "privacy", runtimeSettings.RuleSet.Rules[0].ID)

	err = UpdateAdvancedSecurityOptions(false, false, "invalid", `{"version":1,"rules":[]}`)
	require.Error(t, err)
	var optionsAfterInvalid []Option
	require.NoError(t, database.Find(&optionsAfterInvalid).Error)
	require.Len(t, optionsAfterInvalid, 4)
	afterInvalid := setting.GetAdvancedSecuritySettings()
	assert.True(t, afterInvalid.Enabled)
	assert.True(t, afterInvalid.OnPrompt)
	assert.Equal(t, setting.AdvancedSecurityActionAudit, afterInvalid.Action)
}

func TestUpdateOptionReturnsDatabaseErrors(t *testing.T) {
	originalDB := DB
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "closed.db")), &gorm.Config{})
	require.NoError(t, err)
	DB = database
	t.Cleanup(func() { DB = originalDB })

	sqlDB, err := database.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	err = UpdateOption("unpersistable-option", "value")
	require.Error(t, err)
}
