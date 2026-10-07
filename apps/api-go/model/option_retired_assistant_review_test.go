package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetiredAssistantReviewOptionsCannotReloadOrChangeStoredState(t *testing.T) {
	oldDB, oldOptions := DB, common.OptionMap
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))
	DB = db
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		DB, common.OptionMap = oldDB, oldOptions
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	keys := []string{
		"AssistantReviewEnabled", "AssistantReviewWindowDays", "AssistantReviewIntervalHours",
		"AssistantReviewProbability", "AssistantReviewGroup", "AssistantReviewModel",
		"AssistantReviewReasoningEffort", "AssistantReviewGroupPolicies",
	}
	require.NoError(t, db.Create(&Option{Key: "Theme", Value: "dark"}).Error)
	for _, key := range keys {
		require.NoError(t, db.Create(&Option{Key: key, Value: "historical"}).Error)
		require.Error(t, ValidateOptionValue(key, "replacement"))
		require.Error(t, UpdateOption(key, "replacement"))
		require.Error(t, UpdateOptionsBulk(map[string]string{key: "replacement", "Theme": "light"}))
		common.OptionMap[key] = "historical"
		require.NoError(t, updateOptionMap(key, "historical"))
		require.NotContains(t, common.OptionMap, key)
		var stored Option
		require.NoError(t, db.First(&stored, "key = ?", key).Error)
		require.Equal(t, "historical", stored.Value)
	}
	active, err := AllOption()
	require.NoError(t, err)
	require.Equal(t, []*Option{{Key: "Theme", Value: "dark"}}, active)
}
