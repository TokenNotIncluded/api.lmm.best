package model

import (
	"context"
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupModerationOptionTest(t *testing.T) {
	t.Helper()
	setupPriceLockTest(t)
	previous := setting.GetModerationSettings()
	previousRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateModerationSettings(setting.DefaultModerationSettings().OptionValues()))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"review-official":1,"premium":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateModerationSettings(previous.OptionValues()))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
	})
}

func seedOfficialModerationRoute(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Create(&Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Name: "official", Status: common.ChannelStatusEnabled, Key: "test-fixture-not-a-key"}).Error)
	require.NoError(t, DB.Create(&[]Ability{
		{Group: "review-official", Model: setting.DefaultModerationModel, Enabled: true, ChannelId: 1},
		{Group: "review-official", Model: "omni-moderation-2024-09-26", Enabled: true, ChannelId: 1},
	}).Error)
}

func TestModerationOfficialMetadataCatalogExcludesCredentialsAndOverrides(t *testing.T) {
	setupModerationOptionTest(t)
	seedOfficialModerationRoute(t)
	proxy, mapped, override := `{"proxy":"http://proxy.example"}`, `{"omni-moderation-latest":"gpt-5"}`, `{"model":"gpt-5"}`
	for index, channel := range []Channel{
		{Type: constant.ChannelTypeOpenAI, BaseURL: common.GetPointer("https://relay.example")},
		{Type: constant.ChannelTypeAzure},
		{Type: constant.ChannelTypeOpenAI, Setting: &proxy},
		{Type: constant.ChannelTypeOpenAI, ModelMapping: &mapped},
		{Type: constant.ChannelTypeOpenAI, ParamOverride: &override},
		{Type: constant.ChannelTypeOpenAI, HeaderOverride: &override},
	} {
		channel.Id, channel.Status, channel.Key = index+2, common.ChannelStatusEnabled, "test-fixture-not-a-key"
		require.NoError(t, DB.Create(&channel).Error)
		require.NoError(t, DB.Create(&Ability{Group: "review-official", Model: setting.DefaultModerationModel, Enabled: true, ChannelId: channel.Id}).Error)
	}
	channels, err := ListOfficialModerationChannels(context.Background(), "review-official", setting.DefaultModerationModel)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	require.Equal(t, 1, channels[0].Id)
	require.Empty(t, channels[0].Key)
	require.NoError(t, ValidateModerationRoute("review-official", setting.DefaultModerationModel))
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 1).Update("status", common.ChannelStatusManuallyDisabled).Error)
	require.Error(t, ValidateModerationRoute("review-official", setting.DefaultModerationModel))
}

func TestModerationOfficialChannelRequiresExactOfficialOrigin(t *testing.T) {
	for _, base := range []string{"", "https://api.openai.com", "https://api.openai.com/", "https://api.openai.com/v1"} {
		channel := Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, BaseURL: &base}
		require.True(t, IsOfficialModerationChannel(&channel))
	}
	for _, base := range []string{
		"http://api.openai.com", "https://api.openai.com.evil.example", "https://user@api.openai.com",
		"https://api.openai.com:443", "https://api.openai.com/v1?proxy=1", "https://api.openai.com/v1#other",
		"https://api.openai.com/v1/../relay", "https://api.openai.com/v1/moderations",
	} {
		channel := Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, BaseURL: &base}
		require.False(t, IsOfficialModerationChannel(&channel))
	}
}

func TestModerationOptionsValidateAndPublishCompleteCandidate(t *testing.T) {
	setupModerationOptionTest(t)
	seedOfficialModerationRoute(t)
	values := map[string]string{
		setting.ModerationEnabledOptionKey: "true", setting.ModerationGroupOptionKey: "review-official",
		setting.ModerationModelOptionKey:            "omni-moderation-2024-09-26",
		setting.AssistantModerationEnabledOptionKey: "true", setting.AssistantModerationGroupOptionKey: "review-official",
		setting.AssistantModerationModelOptionKey: setting.DefaultModerationModel,
		setting.ModerationGroupPoliciesOptionKey:  `{"premium":{"mode":"strict","category_fines_usd":{"hate":0.5}},"default":{"mode":"tolerant"}}`,
	}
	require.NoError(t, ValidateOptionValues(values))
	require.False(t, setting.GetModerationSettings().Enabled)
	require.NoError(t, UpdateOptionsBulk(values))
	runtime := setting.GetModerationSettings()
	require.True(t, runtime.Enabled)
	require.True(t, runtime.AssistantEnabled)
	require.Equal(t, "review-official", runtime.Group)
	require.Equal(t, "omni-moderation-2024-09-26", runtime.Model)
	stored, err := ReadModerationSettingsContext(context.Background())
	require.NoError(t, err)
	require.Equal(t, runtime, stored)
	for key, value := range runtime.OptionValues() {
		require.Equal(t, value, persistedPriceOption(t, key))
		require.Equal(t, value, GetOptionsSnapshot()[key])
	}
	require.Error(t, UpdateOptionsBulk(map[string]string{setting.ModerationGroupPoliciesOptionKey: `{"missing":{"mode":"strict"}}`, "Notice": "must not save"}))
	require.Equal(t, runtime, setting.GetModerationSettings())
	var count int64
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "Notice").Count(&count).Error)
	require.Zero(t, count)
}

func TestModerationOptionsRollbackLeavesRuntimeAndStorageDisabled(t *testing.T) {
	setupModerationOptionTest(t)
	seedOfficialModerationRoute(t)
	callback := "test:moderation-option-failure"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if option, ok := tx.Statement.Dest.(*Option); ok && option.Key == setting.ModerationGroupOptionKey {
			tx.AddError(errors.New("injected moderation option write failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Create().Remove(callback)) })
	require.Error(t, UpdateOptionsBulk(map[string]string{
		setting.ModerationEnabledOptionKey: "true", setting.ModerationGroupOptionKey: "review-official",
	}))
	require.False(t, setting.GetModerationSettings().Enabled)
	stored, err := ReadModerationSettingsContext(context.Background())
	require.NoError(t, err)
	require.False(t, stored.Enabled)
	var count int64
	require.NoError(t, DB.Model(&Option{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestModerationWriterUsesAuthoritativeSnapshotAndCanDisableRemovedRoute(t *testing.T) {
	setupModerationOptionTest(t)
	seedOfficialModerationRoute(t)
	require.NoError(t, UpdateOptionsBulk(map[string]string{
		setting.ModerationEnabledOptionKey: "true", setting.ModerationGroupOptionKey: "review-official",
		setting.ModerationGroupPoliciesOptionKey: `{"default":{"mode":"strict","category_fines_usd":{"hate":0.75}}}`,
	}))
	// Simulate a different node publishing a policy not yet in this cache.
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", setting.ModerationGroupPoliciesOptionKey).
		Update("value", `{"default":{"mode":"tolerant"}}`).Error)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 1).Update("status", common.ChannelStatusManuallyDisabled).Error)
	require.NoError(t, UpdateOption(setting.ModerationEnabledOptionKey, "false"))
	current := setting.GetModerationSettings()
	require.False(t, current.Enabled)
	require.Equal(t, setting.ModerationModeTolerant, current.GroupPolicies["default"].Mode)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		locked, err := LockModerationSettings(tx)
		require.NoError(t, err)
		require.Equal(t, current, locked)
		return nil
	}))
}

func TestModerationReloadMissingOptionsDefaultsOffAndMalformedFailsClosed(t *testing.T) {
	setupModerationOptionTest(t)
	require.NoError(t, setting.UpdateModerationSettings(map[string]string{
		setting.ModerationEnabledOptionKey: "true", setting.AssistantModerationEnabledOptionKey: "true",
	}))
	loadOptionsFromDatabase()
	require.False(t, setting.GetModerationSettings().Enabled)
	require.False(t, setting.GetModerationSettings().AssistantEnabled)
	require.NoError(t, DB.Create(&Option{Key: setting.ModerationEnabledOptionKey, Value: "true"}).Error)
	require.NoError(t, DB.Create(&Option{Key: setting.ModerationGroupPoliciesOptionKey, Value: `{"*":{"mode":"strict"}}`}).Error)
	loadOptionsFromDatabase()
	require.False(t, setting.GetModerationSettings().Enabled)
	require.Equal(t, "false", GetOptionsSnapshot()[setting.ModerationEnabledOptionKey])
	_, err := ReadModerationSettingsContext(context.Background())
	require.ErrorIs(t, err, errInvalidStoredModerationSettings)
}
