package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantAdminConfigValidationKeepsWriteSurfaceSafe(t *testing.T) {
	_, err := assistantAdminConfigChanges(map[string]any{
		"changes": map[string]any{"NotAnOption": "value"},
	})
	require.Error(t, err)
	require.Error(t, validateAssistantAdminConfigValue("AssistantSearchURL", "https://search.example.test/?api_key=secret"))
	require.Error(t, validateAssistantAdminConfigValue("QuotaPerUnit", "0"))
	require.Error(t, validateAssistantAdminConfigValue("general_setting.quota_display_type", "BITCOIN"))
	require.NoError(t, validateAssistantAdminConfigValue("GroupRatio", `{"default":1,"vip":0.8}`))
	require.NoError(t, validateAssistantAdminConfigValue("ModelRequestRateLimitGroup", `{"default":[100,20]}`))
	require.NoError(t, validateAssistantAdminConfigValue("payment_setting.amount_options", `[10,50,100]`))
	require.NoError(t, validateAssistantAdminConfigValue("payment_setting.amount_discount", `{"100":0.9}`))
	require.NoError(t, validateAssistantAdminConfigValue("group_ratio_setting.group_special_usable_group", `{"vip":{"default":"Default"}}`))
	require.NoError(t, validateAssistantAdminConfigValue("CreemProducts", `[{"productId":"prod_123","price":10,"currency":"USD","quota":1000}]`))
	require.Error(t, validateAssistantAdminConfigValue("payment_setting.amount_discount", `{"100":1.2}`))
	require.Error(t, validateAssistantAdminConfigValue("CreemProducts", `[{"productId":"prod_123","price":10,"currency":"USD"}]`))
	require.Error(t, validateAssistantAdminConfigValue("PayMethods", `[{"type":"custom","url":"https://secret.example"}]`))
	require.NoError(t, validateAssistantAdminConfigValue("billing_setting.billing_mode", `{"tiered-model":"tiered_expr"}`))
	require.NoError(t, validateAssistantAdminConfigValue("billing_setting.billing_expr", `{"tiered-model":"tier(\"base\", p * 2 + c * 3)"}`))
	require.Error(t, validateAssistantAdminConfigValue("billing_setting.billing_mode", `{"tiered-model":"shell"}`))
}

func TestAssistantAdminConfigExposesSafeDynamicPricingAndGroupWarnings(t *testing.T) {
	labels := assistantAdminAvailableConfigLabels()
	for _, key := range []string{
		"dynamic_pricing_setting.enabled",
		"dynamic_pricing_setting.min_factor",
		"dynamic_pricing_setting.base_price_usd_per_million",
		"dynamic_pricing_setting.cost_floor_factor",
		"dynamic_pricing_setting.max_factor",
		"dynamic_pricing_setting.channel_costs",
		"group_ratio_setting.group_warnings",
	} {
		assert.Contains(t, labels, key)
	}
	assert.NotContains(t, labels, "dynamic_pricing_setting.per_model")
	require.NoError(t, validateAssistantAdminConfigValue(
		"group_ratio_setting.group_warnings",
		`{"free":{"enabled":true,"message":"Accept the relay risks","mode":"modal","confirmations":3}}`,
	))

	changes, err := assistantAdminConfigChanges(map[string]any{
		"changes": map[string]any{
			"dynamic_pricing_setting.enabled":                    true,
			"dynamic_pricing_setting.base_price_usd_per_million": 2.0,
			"dynamic_pricing_setting.channel_costs":              map[string]any{"42": 1.25},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "true", changes["dynamic_pricing_setting.enabled"])
	assert.Equal(t, `{"42":1.25}`, changes["dynamic_pricing_setting.channel_costs"])
}

func TestAssistantAdminConfigExposesNonSecretRuntimeControls(t *testing.T) {
	labels := assistantAdminAvailableConfigLabels()
	assert.Contains(t, labels, "WaffoPancakeMerchantID")
	assert.NotContains(t, labels, "WaffoPancakePrivateKey")

	require.NoError(t, validateAssistantAdminConfigValue("WaffoPancakeMerchantID", "merchant-from-dashboard"))
	require.Error(t, validateAssistantAdminConfigValue("TelegramOAuthEnabled", "enabled"))
}

func TestAssistantAdminModelInventoryRejectsUnreadyPricingCache(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Ability{}))
	admin := model.User{
		Username: "assistant-model-inventory-cache-unready",
		Password: "password",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&admin).Error)

	previousPricing := getPricingCache
	getPricingCache = func() []model.Pricing { return nil }
	t.Cleanup(func() { getPricingCache = previousPricing })

	inventory := executeAssistantAdminModelInventoryTool(admin.Id)
	assert.Equal(t, false, inventory["ok"])
	assert.Equal(t, "pricing_cache_unready", inventory["status"])
	assert.Equal(t, false, inventory["pricing_cache_ready"])
	assert.NotContains(t, inventory, "model_ids")

	models := executeAssistantModelsTool(admin.Id)
	assert.Equal(t, false, models["ok"])
	assert.Equal(t, "pricing_cache_unready", models["status"])
	assert.Equal(t, false, models["pricing_cache_ready"])
	assert.NotContains(t, models, "model_ids")
}

func TestAssistantAdminModelInventoryRejectsEmptyPricingCache(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Ability{}))
	admin := model.User{
		Username: "assistant-model-inventory-cache-empty",
		Password: "password",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&admin).Error)

	previousPricing := getPricingCache
	getPricingCache = func() []model.Pricing {
		return []model.Pricing{{ModelName: "  "}}
	}
	t.Cleanup(func() { getPricingCache = previousPricing })

	inventory := executeAssistantAdminModelInventoryTool(admin.Id)
	assert.Equal(t, false, inventory["ok"])
	assert.Equal(t, "pricing_cache_empty", inventory["status"])
	assert.Equal(t, false, inventory["pricing_cache_ready"])
	assert.NotContains(t, inventory, "model_ids")

	models := executeAssistantModelsTool(admin.Id)
	assert.Equal(t, false, models["ok"])
	assert.Equal(t, "pricing_cache_empty", models["status"])
	assert.Equal(t, false, models["pricing_cache_ready"])
	assert.NotContains(t, models, "model_ids")
}

func TestAssistantReviewToolReturnsAggregateResult(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.SystemTask{}, &model.SystemTaskLock{}))
	admin := model.User{
		Username: "assistant-review-admin", Password: "password",
		Role: common.RoleAdminUser, Status: common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&admin).Error)

	review := model.AssistantReview{
		WindowStart: 1, WindowEnd: 2,
		Actions: []model.AssistantReviewAction{{Code: "review_support_queue", Count: 3}},
	}
	task, err := model.CreateSystemTask(model.SystemTaskTypeAssistantReview, nil, nil)
	require.NoError(t, err)
	claimed, ok, err := model.ClaimSystemTask(task.ID, task.Type, "review-tool-test", time.Now().Add(time.Minute).Unix())
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, model.FinishSystemTask(claimed.TaskID, "review-tool-test", model.SystemTaskStatusSucceeded, review, ""))

	result := executeAssistantReviewTool(admin.Id)
	assert.Equal(t, true, result["ok"])
	assert.Equal(t, "aggregate_only", result["privacy_scope"])
	assert.Equal(t, review, result["review"])
}

func TestAssistantAdminPricingOptionsSwitchModeWithoutMutatingCache(t *testing.T) {
	modelID := "assistant-admin-test-model"
	options, err := assistantAdminPricingOptions(assistantAdminPricingChange{
		ModelID:              modelID,
		Mode:                 "fixed_request",
		Value:                4.25,
		CompletionRatio:      float64Pointer(0.7),
		ImageRatio:           float64Pointer(1.5),
		AudioRatio:           float64Pointer(2.5),
		AudioCompletionRatio: float64Pointer(3.5),
	})
	require.NoError(t, err)

	var prices map[string]float64
	require.NoError(t, json.Unmarshal([]byte(options["ModelPrice"]), &prices))
	require.Equal(t, 4.25, prices[modelID])

	var ratios map[string]float64
	require.NoError(t, json.Unmarshal([]byte(options["ModelRatio"]), &ratios))
	_, stillRatio := ratios[modelID]
	require.False(t, stillRatio)

	var completion map[string]float64
	require.NoError(t, json.Unmarshal([]byte(options["CompletionRatio"]), &completion))
	require.Equal(t, 0.7, completion[modelID])

	var image map[string]float64
	require.NoError(t, json.Unmarshal([]byte(options["ImageRatio"]), &image))
	require.Equal(t, 1.5, image[modelID])
	var audio map[string]float64
	require.NoError(t, json.Unmarshal([]byte(options["AudioRatio"]), &audio))
	require.Equal(t, 2.5, audio[modelID])
	var audioCompletion map[string]float64
	require.NoError(t, json.Unmarshal([]byte(options["AudioCompletionRatio"]), &audioCompletion))
	require.Equal(t, 3.5, audioCompletion[modelID])
}

func TestAssistantAdminPricingPreviewAndApplyUpdatesRuntimeRates(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.AuthFlow{}, &model.Log{}))
	admin := model.User{
		Username: "assistant-admin-pricing-apply",
		Password: "password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&admin).Error)

	modelID := "assistant-admin-runtime-priced-model"
	previousPricing := getPricingCache
	previousRefresh := refreshPricingCache
	getPricingCache = func() []model.Pricing {
		return []model.Pricing{{ModelName: modelID, BillingMode: "ratio"}}
	}
	refreshPricingCache = func() error { return nil }
	previousModelRatio := ratio_setting.ModelRatio2JSONString()
	previousCompletionRatio := ratio_setting.CompletionRatio2JSONString()
	previousCacheRatio := ratio_setting.CacheRatio2JSONString()
	previousCreateCacheRatio := ratio_setting.CreateCacheRatio2JSONString()
	previousImageRatio := ratio_setting.ImageRatio2JSONString()
	previousAudioRatio := ratio_setting.AudioRatio2JSONString()
	previousAudioCompletionRatio := ratio_setting.AudioCompletionRatio2JSONString()
	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	optionMapCopy := make(map[string]string, len(previousOptionMap))
	for key, value := range previousOptionMap {
		optionMapCopy[key] = value
	}
	common.OptionMap = optionMapCopy
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		getPricingCache = previousPricing
		refreshPricingCache = previousRefresh
		_ = ratio_setting.UpdateModelRatioByJSONString(previousModelRatio)
		_ = ratio_setting.UpdateCompletionRatioByJSONString(previousCompletionRatio)
		_ = ratio_setting.UpdateCacheRatioByJSONString(previousCacheRatio)
		_ = ratio_setting.UpdateCreateCacheRatioByJSONString(previousCreateCacheRatio)
		_ = ratio_setting.UpdateImageRatioByJSONString(previousImageRatio)
		_ = ratio_setting.UpdateAudioRatioByJSONString(previousAudioRatio)
		_ = ratio_setting.UpdateAudioCompletionRatioByJSONString(previousAudioCompletionRatio)
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	previewRecorder := httptest.NewRecorder()
	previewContext, _ := gin.CreateTestContext(previewRecorder)
	previewContext.Set("id", admin.Id)
	previewContext.Set("session_id", "assistant-admin-pricing-session")
	preview := executeAssistantAdminPricingChangeTool(previewContext, admin.Id, map[string]any{
		"model_id":         modelID,
		"mode":             "ratio",
		"value":            float64(2),
		"completion_ratio": float64(1.5),
	})
	require.Equal(t, true, preview["ok"])
	action, ok := previewContext.Get(assistantClientActionKey)
	require.True(t, ok)
	actionMap, ok := action.(map[string]any)
	require.True(t, ok)
	token, ok := actionMap["confirmation_token"].(string)
	require.True(t, ok)
	require.NotEmpty(t, token)

	applyRecorder := httptest.NewRecorder()
	applyContext, _ := gin.CreateTestContext(applyRecorder)
	applyContext.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/admin/apply", strings.NewReader(`{"confirmed":true,"confirmation_token":"`+token+`"}`))
	applyContext.Request.Header.Set("Content-Type", "application/json")
	applyContext.Set("id", admin.Id)
	applyContext.Set("session_id", "assistant-admin-pricing-session")
	ApplyAssistantAdminChange(applyContext)
	require.Equal(t, http.StatusOK, applyRecorder.Code)

	state := assistantAdminCurrentPricingState(modelID)
	assert.Equal(t, "ratio", state.Mode)
	assert.Equal(t, 2.0, state.Value)
	assert.Equal(t, 1.5, state.CompletionRatio)
}

func TestAssistantAdminConfigDirectoryOmitsSensitiveRegisteredFields(t *testing.T) {
	labels := assistantAdminAvailableConfigLabels()
	require.Contains(t, labels, "general_setting.docs_link")
	require.Contains(t, labels, "performance_setting.monitor_enabled")
	require.Contains(t, labels, "payment_setting.amount_discount")
	require.Contains(t, labels, "group_ratio_setting.group_special_usable_group")
	require.Contains(t, labels, "token_setting.max_user_tokens")
	require.Contains(t, labels, "claude.default_max_tokens")
	require.Contains(t, labels, "AdvancedSecurityRules")
	require.Contains(t, labels, "WaffoNotifyUrl")
	require.NotContains(t, labels, "claude.model_headers_settings")
	require.NotContains(t, labels, "performance_setting.disk_cache_path")
	require.NotContains(t, labels, "channel_affinity_setting.rules")
	require.NotContains(t, labels, "payment_setting.compliance_confirmed_by")
}

func TestAssistantAdminToolsRejectNonAdministrator(t *testing.T) {
	c, _ := createAssistantKeyTestContext(t, "assistant-admin-denied")
	result := executeAssistantAdminConfigTool(c, c.GetInt("id"))
	require.Equal(t, false, result["ok"])
	result = executeAssistantAdminPricingChangeTool(c, c.GetInt("id"), map[string]any{
		"model_id": "model",
		"mode":     "ratio",
		"value":    float64(1),
	})
	require.Equal(t, false, result["ok"])
}

func TestAssistantAdminChannelPreviewKeepsProviderSecretsOut(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	setupAssistantAdminPermissionTest(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.AuthFlow{}))
	user := model.User{
		Username: "assistant-channel-admin",
		Password: "password",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)
	channel := model.Channel{
		Type:   1,
		Key:    "provider-secret-key",
		Status: common.ChannelStatusEnabled,
		Name:   "primary",
		Models: "gpt-4o",
		Group:  "default",
	}
	require.NoError(t, db.Create(&channel).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("id", user.Id)
	context.Set("session_id", "channel-admin-session")
	result := executeAssistantAdminChannelChangeTool(context, user.Id, map[string]any{
		"channel_id": float64(channel.Id),
		"changes": map[string]any{
			"models": "gpt-4o,gpt-4o-mini",
			"status": float64(common.ChannelStatusManuallyDisabled),
		},
	})
	require.Equal(t, true, result["ok"])
	action, ok := context.Get(assistantClientActionKey)
	require.True(t, ok)
	actionMap, ok := action.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "admin_config_change", actionMap["type"])
	preview, ok := actionMap["changes"].([]assistantAdminConfigPreview)
	require.True(t, ok)
	require.Len(t, preview, 2)
	for _, item := range preview {
		require.NotContains(t, item.OldValue, "provider-secret-key")
		require.NotContains(t, item.NewValue, "provider-secret-key")
	}

	inventory := executeAssistantAdminChannelsTool(user.Id)
	require.Equal(t, true, inventory["ok"])
	channels, ok := inventory["channels"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, channels, 1)
	_, includesKey := channels[0]["key"]
	require.False(t, includesKey)

	_, _, err := assistantAdminChannelChanges(map[string]any{
		"channel_id": float64(channel.Id),
		"changes":    map[string]any{"base_url": "https://provider.example.test"},
	})
	require.Error(t, err)
}

func TestAssistantAdminChannelValidationRejectsUnsafeMappings(t *testing.T) {
	_, _, err := assistantAdminChannelChanges(map[string]any{
		"channel_id": float64(1),
		"changes":    map[string]any{"status": float64(common.ChannelStatusAutoDisabled)},
	})
	require.Error(t, err)
	require.Error(t, validateAssistantAdminChannelValue("model_mapping", `{"gpt-4o":""}`))
	require.Error(t, validateAssistantAdminChannelValue("status_code_mapping", `{"500":700}`))
	require.NoError(t, validateAssistantAdminChannelValue("status_code_mapping", `{"500":503}`))
}

func TestAssistantAdminPreviewIsSessionBoundAndOneTime(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.AuthFlow{}, &model.Log{}))
	user := model.User{
		Username: "assistant-admin-apply",
		Password: "password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	oldOption, hadOption := common.OptionMap["DefaultCollapseSidebar"]
	common.OptionMap["DefaultCollapseSidebar"] = "false"
	common.OptionMapRWMutex.Unlock()
	oldCollapsed := common.DefaultCollapseSidebar
	t.Cleanup(func() {
		common.DefaultCollapseSidebar = oldCollapsed
		common.OptionMapRWMutex.Lock()
		if hadOption {
			common.OptionMap["DefaultCollapseSidebar"] = oldOption
		} else {
			delete(common.OptionMap, "DefaultCollapseSidebar")
		}
		common.OptionMapRWMutex.Unlock()
	})

	previewRecorder := httptest.NewRecorder()
	previewContext, _ := gin.CreateTestContext(previewRecorder)
	previewContext.Set("id", user.Id)
	previewContext.Set("session_id", "admin-browser-session")
	preview := executeAssistantAdminConfigChangeTool(previewContext, user.Id, map[string]any{
		"changes": map[string]any{"DefaultCollapseSidebar": true},
	})
	require.Equal(t, true, preview["ok"])
	action, ok := previewContext.Get(assistantClientActionKey)
	require.True(t, ok)
	actionMap, ok := action.(map[string]any)
	require.True(t, ok)
	token, ok := actionMap["confirmation_token"].(string)
	require.True(t, ok)
	require.NotEmpty(t, token)

	applyRecorder := httptest.NewRecorder()
	applyContext, _ := gin.CreateTestContext(applyRecorder)
	applyContext.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/admin/apply", strings.NewReader(`{"confirmed":true,"confirmation_token":"`+token+`"}`))
	applyContext.Request.Header.Set("Content-Type", "application/json")
	applyContext.Set("id", user.Id)
	applyContext.Set("session_id", "admin-browser-session")
	ApplyAssistantAdminChange(applyContext)
	require.Equal(t, http.StatusOK, applyRecorder.Code)

	var option model.Option
	require.NoError(t, db.Where("key = ?", "DefaultCollapseSidebar").First(&option).Error)
	require.Equal(t, "true", option.Value)

	replayRecorder := httptest.NewRecorder()
	replayContext, _ := gin.CreateTestContext(replayRecorder)
	replayContext.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/admin/apply", strings.NewReader(`{"confirmed":true,"confirmation_token":"`+token+`"}`))
	replayContext.Request.Header.Set("Content-Type", "application/json")
	replayContext.Set("id", user.Id)
	replayContext.Set("session_id", "admin-browser-session")
	ApplyAssistantAdminChange(replayContext)
	require.Equal(t, http.StatusUnprocessableEntity, replayRecorder.Code)
}

func TestAssistantConfigChangesRequireRootAdministrator(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.AuthFlow{}))
	admin := model.User{
		Username: "assistant-non-root-admin",
		Password: "password",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&admin).Error)

	previewContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	previewContext.Set("session_id", "non-root-admin-session")
	preview := executeAssistantAdminConfigChangeTool(previewContext, admin.Id, map[string]any{
		"changes": map[string]any{"RegisterEnabled": false},
	})
	require.Equal(t, false, preview["ok"])
	require.Contains(t, preview["error"], "root administrator access is required")

	payload, err := json.Marshal(assistantAdminChangePayload{
		Kind:           assistantAdminConfigChangeKind,
		ConfigChanges:  map[string]string{"RegisterEnabled": "false"},
		ConfigExpected: map[string]string{"RegisterEnabled": "true"},
	})
	require.NoError(t, err)
	token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   model.AuthFlowPurposeAssistantAdmin,
		UserId:    admin.Id,
		SessionId: "non-root-admin-session",
		Payload:   string(payload),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	applyRecorder := httptest.NewRecorder()
	applyContext, _ := gin.CreateTestContext(applyRecorder)
	applyContext.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/admin/apply", strings.NewReader(`{"confirmed":true,"confirmation_token":"`+token+`"}`))
	applyContext.Request.Header.Set("Content-Type", "application/json")
	applyContext.Set("id", admin.Id)
	applyContext.Set("session_id", "non-root-admin-session")
	ApplyAssistantAdminChange(applyContext)
	require.Equal(t, http.StatusForbidden, applyRecorder.Code)
}

func TestAssistantAdminUserSkillsUseStrictScopeAndOneTimeConfirmation(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.AuthFlow{}, &model.AssistantMemory{},
		&model.AssistantUserProfile{}, &model.Log{},
	))
	admin := model.User{
		Username: "assistant-skill-admin", Password: "password",
		Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "skill-admin-aff",
	}
	peer := model.User{
		Username: "assistant-skill-peer", Password: "password",
		Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "skill-peer-aff",
	}
	target := model.User{
		Username: "assistant-skill-target", Password: "password",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "skill-target-aff",
	}
	require.NoError(t, db.Create(&admin).Error)
	require.NoError(t, db.Create(&peer).Error)
	require.NoError(t, db.Create(&target).Error)
	_, err := model.SaveMemory(target.Id, target.Id, model.MemoryInput{
		Title: "editor preference", Content: "prefers concise diffs", Tags: []string{"style"},
		Source: model.AssistantMemorySourceAssistant, Enabled: true,
	})
	require.NoError(t, err)

	read := executeAssistantAdminUserSkillsTool(admin.Id, map[string]any{"target_user_id": float64(target.Id)})
	assert.Equal(t, true, read["ok"])
	assert.Equal(t, target.Id, read["target_user_id"])
	memories, ok := read["memories"].([]model.AssistantMemoryView)
	require.True(t, ok)
	require.Len(t, memories, 1)
	assert.Equal(t, "editor preference", memories[0].Title)

	peerRead := executeAssistantAdminUserSkillsTool(peer.Id, map[string]any{"target_user_id": float64(admin.Id)})
	assert.Equal(t, false, peerRead["ok"])
	assert.Equal(t, "target_forbidden", peerRead["status"])

	previewContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	previewContext.Set("id", admin.Id)
	previewContext.Set("session_id", "assistant-skill-session")
	preview := executeAssistantAdminUserSkillChangeTool(previewContext, admin.Id, map[string]any{
		"target_user_id": float64(target.Id), "kind": "memory", "operation": "upsert",
		"memory_id": float64(memories[0].Id), "title": "editor preference",
		"content": "prefers small, concise diffs", "tags": []any{"style"}, "enabled": true,
	})
	assert.Equal(t, true, preview["ok"])
	assert.Equal(t, "confirmation_required", preview["status"])
	action, ok := previewContext.Get(assistantClientActionKey)
	require.True(t, ok)
	actionMap, ok := action.(map[string]any)
	require.True(t, ok)
	token, ok := actionMap["confirmation_token"].(string)
	require.True(t, ok)
	require.NotEmpty(t, token)

	applyContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	applyContext.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/admin/apply", strings.NewReader(`{"confirmed":true,"confirmation_token":"`+token+`"}`))
	applyContext.Request.Header.Set("Content-Type", "application/json")
	applyContext.Set("id", admin.Id)
	applyContext.Set("session_id", "assistant-skill-session")
	ApplyAssistantAdminChange(applyContext)
	assert.Equal(t, http.StatusOK, applyContext.Writer.Status())

	updated, err := model.GetMemory(target.Id, memories[0].Id)
	require.NoError(t, err)
	assert.Equal(t, "prefers small, concise diffs", updated.Content)

	replayRecorder := httptest.NewRecorder()
	replayContext, _ := gin.CreateTestContext(replayRecorder)
	replayContext.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/admin/apply", strings.NewReader(`{"confirmed":true,"confirmation_token":"`+token+`"}`))
	replayContext.Request.Header.Set("Content-Type", "application/json")
	replayContext.Set("id", admin.Id)
	replayContext.Set("session_id", "assistant-skill-session")
	ApplyAssistantAdminChange(replayContext)
	assert.Equal(t, http.StatusUnprocessableEntity, replayRecorder.Code)
}

func float64Pointer(value float64) *float64 {
	return &value
}
