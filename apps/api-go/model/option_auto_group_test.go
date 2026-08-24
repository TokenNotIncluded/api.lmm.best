package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOptionValueRejectsUnsafePaymentReturnURLs(t *testing.T) {
	assert.NoError(t, validateOptionValue("WaffoPancakeReturnURL", "https://pay.example.com/return"))
	assert.NoError(t, validateOptionValue("WaffoPancakeReturnURL", ""))
	assert.Error(t, validateOptionValue("WaffoPancakeReturnURL", "javascript:alert(1)"))
	assert.Error(t, validateOptionValue("WaffoPancakeReturnURL", "/wallet"))
	assert.Error(t, validateOptionValue("WaffoPancakeReturnURL", "https://user:pass@pay.example.com/return"))
}

func TestValidateOptionValueRejectsInvalidMaxTokenAutoGroups(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5", "invalid"} {
		t.Run(value, func(t *testing.T) {
			assert.Error(t, validateOptionValue("MaxTokenAutoGroups", value))
		})
	}
	require.NoError(t, validateOptionValue("MaxTokenAutoGroups", "999999"))
}

func TestValidateOptionValueRejectsUnavailableAssistantReviewModel(t *testing.T) {
	originalSettings := setting.GetAssistantSettings()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"review-premium":1}`))
	require.NoError(t, setting.UpdateAssistantReviewGroup("review-premium"))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAssistantReviewGroup(originalSettings.ReviewGroup))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})

	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&Ability{}, &Channel{}))
	require.NoError(t, db.Create(&[]Channel{
		{Id: 1, Name: "review-live", Key: "sk-live", Status: common.ChannelStatusEnabled},
		{Id: 2, Name: "review-disabled", Key: "sk-disabled", Status: common.ChannelStatusManuallyDisabled},
	}).Error)
	require.NoError(t, db.Create(&[]Ability{
		{Group: "review-premium", Model: "review-live", Enabled: true, ChannelId: 1},
		{Group: "review-premium", Model: "review-disabled", Enabled: true, ChannelId: 2},
	}).Error)

	require.NoError(t, validateOptionValue(setting.AssistantReviewGroupOptionKey, "review-premium"))
	require.NoError(t, validateOptionValue(setting.AssistantReviewModelOptionKey, "review-live"))
	require.Error(t, validateOptionValue(setting.AssistantReviewModelOptionKey, "review-disabled"))
	require.Error(t, validateOptionValue(setting.AssistantReviewModelOptionKey, "review-missing"))
}

func TestValidateOptionValueAcceptsOnlyExistingAssistantGroup(t *testing.T) {
	assert.NoError(t, validateOptionValue(setting.AssistantGroupOptionKey, "default"))
	assert.Error(t, validateOptionValue(setting.AssistantGroupOptionKey, "missing-group"))
}

func TestValidateOptionValuesChecksAssistantModelAgainstCandidateGroup(t *testing.T) {
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"premium":1}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios)) })
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&Ability{}, &Channel{}))
	require.NoError(t, db.Create(&Channel{
		Id: 1, Name: "premium-assistant", Key: "sk-test", Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&Ability{
		Group: "premium", Model: "premium-assistant-model", Enabled: true, ChannelId: 1,
	}).Error)

	require.NoError(t, ValidateOptionValues(map[string]string{
		setting.AssistantGroupOptionKey: "premium",
		setting.AssistantModelOptionKey: "premium-assistant-model",
	}))
	assert.Error(t, ValidateOptionValues(map[string]string{
		setting.AssistantGroupOptionKey: "premium",
		setting.AssistantModelOptionKey: "missing-model",
	}))
}

func TestValidateOptionValuesChecksAssistantReviewModelAgainstCandidateGroup(t *testing.T) {
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"review-premium":1}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios)) })
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&Ability{}, &Channel{}))
	require.NoError(t, db.Create(&Channel{
		Id: 1, Name: "review-premium", Key: "sk-test", Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&Ability{
		Group: "review-premium", Model: "review-premium-model", Enabled: true, ChannelId: 1,
	}).Error)

	require.NoError(t, ValidateOptionValues(map[string]string{
		setting.AssistantReviewGroupOptionKey: "review-premium",
		setting.AssistantReviewModelOptionKey: "review-premium-model",
	}))
	assert.Error(t, ValidateOptionValues(map[string]string{
		setting.AssistantReviewGroupOptionKey: "review-premium",
		setting.AssistantReviewModelOptionKey: "missing-model",
	}))
}

func TestValidateIPAccessRoutingOptions(t *testing.T) {
	require.NoError(t, validateOptionValue(setting.IPAccessRoutingRulesOptionKey, setting.DefaultIPAccessRoutingRules))
	require.NoError(t, validateOptionValue(setting.IPAccessRoutingRulesOptionKey, "domain(example.com) -> direct"))
	assert.Error(t, validateOptionValue(setting.IPAccessRoutingRulesOptionKey, "dip(geoip:china) -> reject"))
	for key := range retiredIPAccessOptionKeys {
		assert.ErrorContains(t, validateOptionValue(key, "false"), "retired")
	}
}

func TestUpdateOptionMapRejectsMalformedIPAccessRoutingWithoutMutation(t *testing.T) {
	previousOptions := common.OptionMap
	previousRules := setting.GetIPAccessRoutingRules()
	common.OptionMap = map[string]string{}
	require.NoError(t, setting.UpdateIPAccessRoutingRules(setting.DefaultIPAccessRoutingRules))
	t.Cleanup(func() {
		common.OptionMap = previousOptions
		require.NoError(t, setting.UpdateIPAccessRoutingRules(previousRules))
	})

	assert.Error(t, updateOptionMap(setting.IPAccessRoutingRulesOptionKey, "not a route"))
	assert.Equal(t, setting.DefaultIPAccessRoutingRules, setting.GetIPAccessRoutingRules())
	assert.NotContains(t, common.OptionMap, setting.IPAccessRoutingRulesOptionKey)
}

func TestUpdateOptionMapIgnoresRetiredIPAccessOptions(t *testing.T) {
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{
		"GlobalIPWhitelistEnabled":  "true",
		"GlobalIPWhitelistCIDRs":    `["203.0.113.8"]`,
		"RegionAccessPolicyEnabled": "true",
		"RegionBlockedCountryCodes": "CN",
	}
	t.Cleanup(func() { common.OptionMap = previousOptions })

	for key := range retiredIPAccessOptionKeys {
		require.NoError(t, updateOptionMap(key, "legacy"))
		assert.NotContains(t, common.OptionMap, key)
	}
}
