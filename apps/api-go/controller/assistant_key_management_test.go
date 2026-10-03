package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assistantManagementListForTest(t *testing.T, c *gin.Context, userID int) {
	t.Helper()
	listed := executeAssistantListMyAPIKeysTool(c, userID, map[string]any{})
	require.Equal(t, true, listed["ok"], listed)
}

func TestAssistantKeyManagementL0CanRevokeOwnedKeyWithoutCreatingOne(t *testing.T) {
	db, user := createAssistantKeyFixture(t, "key-management-l0")
	l0 := 0
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Updates(map[string]any{"console_activated_at": 0, "trust_level_override": l0}).Error)
	key := model.Token{UserId: user.Id, Key: "never-expose-this-key", Name: "desktop", Group: "default", Status: common.TokenStatusEnabled, RemainQuota: 321, UsedQuota: 123}
	require.NoError(t, db.Create(&key).Error)
	c, _ := assistantKeyContext(t, http.MethodPost, "/api/assistant/chat", "", user.Id, "l0-revoke-session")
	c.Set(assistantUserContextKey, assistantUserContext{UserID: user.Id, AccessLevel: "L0", LatestUserRequest: "删除我的 API key desktop"})
	for _, name := range []string{"list_my_api_keys", "prepare_api_key_action"} {
		assert.True(t, assistantToolAllowedForContext(name, assistantUserContext{AccessLevel: "L0"}))
	}
	assert.False(t, assistantToolAllowedForContext("request_create_key", assistantUserContext{AccessLevel: "L0"}))
	creation := executeAssistantCreateKeyRequestTool(c, user.Id, map[string]any{"group": "default"})
	assert.Equal(t, false, creation["ok"])
	assistantManagementListForTest(t, c, user.Id)
	preview := executeAssistantPrepareAPIKeyActionTool(c, user.Id, map[string]any{"action": "delete", "token_id": float64(key.Id)})
	require.Equal(t, true, preview["ok"], preview)
	raw, err := json.Marshal(preview)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "confirmation_token")
	assert.NotContains(t, string(raw), key.Key)
	var before model.Token
	require.NoError(t, db.First(&before, key.Id).Error, "preparation must not delete")
	action, exists := c.Get(assistantClientActionKey)
	require.True(t, exists)
	token := action.(map[string]any)["confirmation_token"].(string)
	confirm, response := assistantKeyContext(t, http.MethodPost, "/api/assistant/tools/key-action", fmt.Sprintf(`{"confirmation_token":%q}`, token), user.Id, "l0-revoke-session")
	ConfirmAssistantAPIKeyAction(confirm)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"action":"delete"`)
	assert.NotContains(t, response.Body.String(), key.Key)
	var deleted model.Token
	require.NoError(t, db.Unscoped().First(&deleted, key.Id).Error)
	assert.True(t, deleted.DeletedAt.Valid)
	assert.Equal(t, 321, deleted.RemainQuota)
	assert.Equal(t, 123, deleted.UsedQuota)
	replay, replayResponse := assistantKeyContext(t, http.MethodPost, "/api/assistant/tools/key-action", fmt.Sprintf(`{"confirmation_token":%q}`, token), user.Id, "l0-revoke-session")
	ConfirmAssistantAPIKeyAction(replay)
	assert.Equal(t, http.StatusUnprocessableEntity, replayResponse.Code)
	assert.Contains(t, replayResponse.Body.String(), "ASSISTANT_KEY_CONFIRMATION_INVALID")
}

func TestAssistantKeyManagementRejectsGuessingAmbiguousNamesAndActorOverride(t *testing.T) {
	db, user := createAssistantKeyFixture(t, "key-management-targets")
	keys := []model.Token{
		{UserId: user.Id, Key: "private-one", Name: "same", Group: "default"},
		{UserId: user.Id, Key: "private-two", Name: "same", Group: "default"},
		{UserId: user.Id + 1, Key: "foreign-private", Name: "foreign", Group: "default"},
	}
	for i := range keys {
		require.NoError(t, db.Create(&keys[i]).Error)
	}
	c, _ := assistantKeyContext(t, http.MethodPost, "/api/assistant/chat", "", user.Id, "target-session")
	c.Set(assistantUserContextKey, assistantUserContext{LatestUserRequest: "帮我删除 same 这个密钥"})
	input := map[string]any{"action": "delete", "token_id": float64(keys[0].Id)}
	assert.Equal(t, "list_required", executeAssistantPrepareAPIKeyActionTool(c, user.Id, input)["status"])
	assert.Equal(t, "input_invalid", executeAssistantListMyAPIKeysTool(c, user.Id, map[string]any{"user_id": float64(user.Id + 1)})["status"])
	assistantManagementListForTest(t, c, user.Id)
	assert.Equal(t, "list_required", executeAssistantPrepareAPIKeyActionTool(c, user.Id, map[string]any{"action": "delete", "token_id": float64(keys[2].Id)})["status"])
	assert.Equal(t, "target_choice_required", executeAssistantPrepareAPIKeyActionTool(c, user.Id, input)["status"])
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", keys[1].Id).Update("name", "other").Error)
	c.Set(assistantUserContextKey, assistantUserContext{LatestUserRequest: "帮我删除一个 API key"})
	assistantManagementListForTest(t, c, user.Id)
	assert.Equal(t, "target_choice_required", executeAssistantPrepareAPIKeyActionTool(c, user.Id, input)["status"], "a unique name still cannot be arbitrarily selected among several keys")
	c.Set(assistantUserContextKey, assistantUserContext{LatestUserRequest: "帮我删除 same 这个密钥"})
	assert.Equal(t, true, executeAssistantPrepareAPIKeyActionTool(c, user.Id, input)["ok"])
	c.Set(assistantClientActionKey, nil)
	stored, _ := c.Get(assistantClientActionKey)
	assert.Nil(t, stored)
	c.Set("assistant_history_latest_message", fmt.Sprintf("删除 ID %d", keys[0].Id))
	assert.Equal(t, true, executeAssistantPrepareAPIKeyActionTool(c, user.Id, input)["ok"])
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", keys[0].Id).Update("name", "changed").Error)
	assert.Equal(t, "target_changed", executeAssistantPrepareAPIKeyActionTool(c, user.Id, input)["status"])
	for _, token := range keys {
		var current model.Token
		require.NoError(t, db.First(&current, token.Id).Error)
	}
}

func TestAssistantKeyManagementConfirmRejectsRetargetingWrongSessionAndWrongOwner(t *testing.T) {
	db, user := createAssistantKeyFixture(t, "key-management-confirm")
	key := model.Token{UserId: user.Id, Key: "confirmation-private", Name: "desktop", Group: "default"}
	require.NoError(t, db.Create(&key).Error)
	c, _ := assistantKeyContext(t, http.MethodPost, "/api/assistant/chat", "", user.Id, "confirm-session")
	c.Set(assistantUserContextKey, assistantUserContext{LatestUserRequest: fmt.Sprintf("停用 ID %d", key.Id)})
	assistantManagementListForTest(t, c, user.Id)
	require.Equal(t, true, executeAssistantPrepareAPIKeyActionTool(c, user.Id, map[string]any{"action": "disable", "token_id": float64(key.Id)})["ok"])
	stored, _ := c.Get(assistantClientActionKey)
	token := stored.(map[string]any)["confirmation_token"].(string)
	for _, test := range []struct {
		name, session, extra string
		userID, status       int
	}{
		{"retarget", "confirm-session", `,"token_id":999`, user.Id, http.StatusBadRequest},
		{"wrong session", "other-session", "", user.Id, http.StatusUnprocessableEntity},
		{"wrong owner", "foreign-session", "", user.Id + 1, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, response := assistantKeyContext(t, http.MethodPost, "/api/assistant/tools/key-action", fmt.Sprintf(`{"confirmation_token":%q%s}`, token, test.extra), test.userID, test.session)
			ConfirmAssistantAPIKeyAction(ctx)
			assert.Equal(t, test.status, response.Code, response.Body.String())
			var current model.Token
			require.NoError(t, db.First(&current, key.Id).Error)
			assert.Equal(t, common.TokenStatusEnabled, current.Status)
		})
	}
}

func TestAssistantKeyManagementNamesCannotSelectTheirPrefixes(t *testing.T) {
	db, user := createAssistantKeyFixture(t, "key-management-prefixes")
	keys := []model.Token{
		{UserId: user.Id, Key: "prefix-private-1", Name: "测试", Group: "default"},
		{UserId: user.Id, Key: "prefix-private-2", Name: "测试2", Group: "default"},
		{UserId: user.Id, Key: "prefix-private-3", Name: "测试专用", Group: "default"},
	}
	for i := range keys {
		require.NoError(t, db.Create(&keys[i]).Error)
	}
	c, _ := assistantKeyContext(t, http.MethodPost, "/api/assistant/chat", "", user.Id, "prefix-session")
	assistantManagementListForTest(t, c, user.Id)
	for _, text := range []string{"删除测试2这个密钥", "删除测试专用这个密钥"} {
		c.Set(assistantUserContextKey, assistantUserContext{LatestUserRequest: text})
		result := executeAssistantPrepareAPIKeyActionTool(c, user.Id, map[string]any{"action": "delete", "token_id": float64(keys[0].Id)})
		assert.Equal(t, "target_choice_required", result["status"])
	}
	c.Set(assistantUserContextKey, assistantUserContext{LatestUserRequest: "删除「测试2」这个密钥"})
	assert.Equal(t, true, executeAssistantPrepareAPIKeyActionTool(c, user.Id, map[string]any{"action": "delete", "token_id": float64(keys[1].Id)})["ok"])
	assert.False(t, assistantKeyIDSelectedByUser(c, keys[0].Id))
	c.Set("assistant_history_latest_message", "ID 100")
	assert.False(t, assistantKeyIDSelectedByUser(c, 10))
}

func TestAssistantKeyManagementExactNameSelectionSurvivesFilteredAndPagedLists(t *testing.T) {
	for _, test := range []struct {
		name, targetName, longerName, message string
		filtered, allowed                     bool
	}{
		{"filtered CJK prefix", "测试", "测试甲", "删除测试甲的密钥", true, false},
		{"unread page CJK prefix", "测试", "测试甲", "删除测试甲的密钥", false, false},
		{"quoted longer CJK cannot select prefix", "测试", "测试甲", "删除「测试甲」的密钥", true, false},
		{"quoted short CJK is exact", "测试", "测试甲", "删除「测试」的密钥", true, true},
		{"quoted short CJK with unread longer name", "测试", "测试甲", "删除“测试”的密钥", false, true},
		{"whole CJK selection reply", "测试", "测试甲", "测试", true, true},
		{"different CJK suffix", "测试甲", "测试乙", "删除「测试乙」的密钥", true, false},
		{"whole different CJK suffix", "测试甲", "测试乙", "测试乙", true, false},
		{"mixed CJK English prefix", "测试Alpha", "测试Alpha生产", "删除测试Alpha生产的密钥", true, false},
		{"quoted mixed CJK English name", "测试Alpha", "测试Alpha生产", "删除『测试Alpha』的密钥", true, true},
		{"ASCII name with CJK suffix", "abc", "abc甲", "删除 abc甲 的密钥", true, false},
		{"ASCII name with hyphen suffix", "abc", "abc-prod", "删除 abc-prod 的密钥", true, false},
		{"ASCII name with dot suffix", "abc", "abc.prod", "删除 abc.prod 的密钥", true, false},
		{"ASCII full whitespace token", "abc", "abc甲", "删除 abc 这个密钥", true, true},
		{"case is part of exact name", "Test", "test", "删除 \"test\" 这个密钥", true, false},
		{"padded name is quoted exactly", " spaced ", "spaced", "删除 \" spaced \" 这个密钥", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, user := createAssistantKeyFixture(t, "key-exact-selection")
			// The longer name is older than the first metadata page. No name
			// overlap from an unfiltered previous read can rescue the check.
			longer := model.Token{UserId: user.Id, Key: "exact-selection-long-secret", Name: test.longerName, Group: "default"}
			require.NoError(t, db.Create(&longer).Error)
			for i := range 24 {
				filler := model.Token{UserId: user.Id, Key: fmt.Sprintf("exact-selection-filler-secret-%d", i), Name: fmt.Sprintf("filler-%d", i), Group: "default"}
				require.NoError(t, db.Create(&filler).Error)
			}
			target := model.Token{UserId: user.Id, Key: "exact-selection-target-secret", Name: test.targetName, Group: "default"}
			require.NoError(t, db.Create(&target).Error)
			c, _ := assistantKeyContext(t, http.MethodPost, "/api/assistant/chat", "", user.Id, "exact-selection-session")
			c.Set(assistantUserContextKey, assistantUserContext{LatestUserRequest: test.message})
			input := map[string]any{}
			if test.filtered {
				input["exact_name"] = test.targetName
			}
			listed := executeAssistantListMyAPIKeysTool(c, user.Id, input)
			require.Equal(t, true, listed["ok"])
			stored, _ := c.Get(assistantListedKeyMetadataContextKey)
			metadata := stored.(map[int]model.AssistantKeyMetadata)
			require.Contains(t, metadata, target.Id)
			require.NotContains(t, metadata, longer.Id, "longer name must be absent from the provider's read")
			result := executeAssistantPrepareAPIKeyActionTool(c, user.Id, map[string]any{"action": "delete", "token_id": float64(target.Id)})
			if test.allowed {
				require.Equal(t, true, result["ok"], result)
				assert.Equal(t, "confirmation_required", result["status"])
			} else {
				assert.Equal(t, "target_choice_required", result["status"], result)
				_, exists := c.Get(assistantClientActionKey)
				assert.False(t, exists, "a mismatched target must not reach a confirmation card")
			}
			var unchanged model.Token
			require.NoError(t, db.First(&unchanged, target.Id).Error)
			assert.Equal(t, common.TokenStatusEnabled, unchanged.Status)
		})
	}
}

func TestAssistantKeyManagementReadAndPrepareRunWhenAgentLoopDisabled(t *testing.T) {
	db, user := createAssistantKeyFixture(t, "key-management-loop")
	key := model.Token{UserId: user.Id, Key: "loop-private-never-provider", Name: "desktop", Group: "default"}
	require.NoError(t, db.Create(&key).Error)
	c, response := assistantKeyContext(t, http.MethodPost, "/api/assistant/chat", "", user.Id, "loop-session")
	message := "我也看不到密钥的删除键，你帮我删除 desktop 吧"
	context := assistantUserContext{UserID: user.Id, AccessLevel: "L1", DeveloperAccessGranted: true, LatestUserRequest: message}
	c.Set(assistantUserContextKey, context)
	turns := 0
	original := relayAssistantAgentTurn
	t.Cleanup(func() { relayAssistantAgentTurn = original })
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		encoded := string(mustAssistantJSON(t, request.Messages))
		assert.NotContains(t, encoded, key.Key)
		assert.NotContains(t, encoded, "confirmation_token")
		if turns == 1 {
			requireAssistantPairedReadReceipt(t, request, "list_my_api_keys", true)
			assert.Equal(t, "auto", request.ToolChoice)
			return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{{ID: "prepare-delete", Type: "function", Function: assistantOpenAIToolCallFunction{Name: "prepare_api_key_action", Arguments: fmt.Sprintf(`{"action":"delete","token_id":%d}`, key.Id)}}}, ""), nil
		}
		assert.Equal(t, "none", request.ToolChoice)
		assert.Contains(t, encoded, `\"status\":\"confirmation_required\"`)
		return http.StatusOK, []byte(`{"choices":[{"message":{"role":"assistant","content":"请在卡片中确认删除 desktop。"}}]}`), nil
	}
	runAssistantAgent(c, setting.AssistantSettings{Model: "management-test", AgentLoopEnabled: false, MaxSteps: 1, TimeoutSeconds: 30}, []assistantOpenAIMessage{{Role: "user", Content: message}})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, 2, turns)
	assert.Contains(t, response.Body.String(), `"type":"api_key_action"`)
	assert.Contains(t, response.Body.String(), `"confirmation_token"`)
	var unchanged model.Token
	require.NoError(t, db.First(&unchanged, key.Id).Error)
}

func TestAssistantKeyManagementSelectionCarriesIntoNextTurn(t *testing.T) {
	conversation := []assistantOpenAIMessage{
		{Role: "user", Content: "帮我删除 API key"},
		{Role: "assistant", Content: "你有多个密钥，请选择要删除的 ID。"},
		{Role: "user", Content: "ID 123"},
	}
	context := assistantUserContextForRequest(0, "ID 123", conversation)
	require.True(t, context.KeyManagementRequested)
	assert.True(t, assistantKeyManagementWorkflowRequired(context))
	assert.Equal(t, "list_my_api_keys", assistantNamedToolChoiceName(assistantToolChoiceForAgentStep(context, nil, nil)))
	assert.Equal(t, 4, assistantKeyManagementWorkflowMinSteps(context))
	conversation[2].Content = "取消"
	assert.False(t, assistantUserContextForRequest(0, "取消", conversation).KeyManagementRequested)
}

func TestAssistantKeyManagementCanConfirmSelectedIDOutsideFirstPage(t *testing.T) {
	db, user := createAssistantKeyFixture(t, "key-management-old-id")
	keys := make([]model.Token, 25)
	for i := range keys {
		keys[i] = model.Token{UserId: user.Id, Key: fmt.Sprintf("old-target-private-%d", i), Name: fmt.Sprintf("key-%d", i), Group: "default"}
		require.NoError(t, db.Create(&keys[i]).Error)
	}
	c, response := assistantKeyContext(t, http.MethodPost, "/api/assistant/chat", "", user.Id, "old-id-session")
	message := fmt.Sprintf("ID %d", keys[0].Id)
	c.Set(assistantUserContextKey, assistantUserContext{UserID: user.Id, AccessLevel: "L0", KeyManagementRequested: true, LatestUserRequest: message})
	turns := 0
	original := relayAssistantAgentTurn
	t.Cleanup(func() { relayAssistantAgentTurn = original })
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		switch turns {
		case 1:
			requireAssistantPairedReadReceipt(t, request, "list_my_api_keys", true)
			return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{{ID: "exact-old-key", Type: "function", Function: assistantOpenAIToolCallFunction{Name: "list_my_api_keys", Arguments: fmt.Sprintf(`{"token_id":%d}`, keys[0].Id)}}}, ""), nil
		case 2:
			assert.Equal(t, "auto", request.ToolChoice)
			return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{{ID: "prepare-old-key", Type: "function", Function: assistantOpenAIToolCallFunction{Name: "prepare_api_key_action", Arguments: fmt.Sprintf(`{"action":"delete","token_id":%d}`, keys[0].Id)}}}, ""), nil
		default:
			assert.Empty(t, request.Tools)
			return http.StatusOK, []byte(`{"choices":[{"message":{"role":"assistant","content":"请在卡片中确认删除选中的密钥。"}}]}`), nil
		}
	}
	runAssistantAgent(c, setting.AssistantSettings{Model: "management-selected-id", AgentLoopEnabled: false, MaxSteps: 1, TimeoutSeconds: 30}, []assistantOpenAIMessage{{Role: "user", Content: message}})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, 3, turns)
	assert.Contains(t, response.Body.String(), `"type":"api_key_action"`)
	stored, _ := c.Get(assistantClientActionKey)
	require.NotNil(t, stored)
	assert.Equal(t, keys[0].Id, stored.(map[string]any)["token"].(*model.AssistantKeyMetadata).ID)
	var untouched model.Token
	require.NoError(t, db.First(&untouched, keys[0].Id).Error)
}

func TestAssistantKeyManagementLatestMetadataBypassesCachedReply(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantLead{}, &model.AssistantProfileBucket{}, &model.AssistantFirstQuestionStat{}))
	withAssistantSettings(t, true, "key-management-cache-model")
	original := setting.GetAssistantSettings()
	setting.SetAssistantCacheEnabled(true)
	require.NoError(t, setting.UpdateAssistantCacheTTLMinutes("10"))
	t.Cleanup(func() {
		setting.SetAssistantCacheEnabled(original.CacheEnabled)
		_ = setting.UpdateAssistantCacheTTLMinutes(fmt.Sprint(original.CacheTTLMinutes))
	})
	message := "列出我的现有 API keys " + t.Name()
	settings := setting.GetAssistantSettings()
	context := assistantUserContextForRequest(42, message)
	require.True(t, assistantKeyManagementWorkflowRequired(context))
	cacheKey := assistantCacheKey(settings, []assistantOpenAIMessage{{Role: "user", Content: message}}, context)
	require.NotEmpty(t, cacheKey)
	storeAssistantCachedResponse(settings, cacheKey, http.StatusOK, []byte(`{"choices":[{"message":{"role":"assistant","content":"stale keys"}}]}`))
	dowstreamCalls := 0
	engine := gin.New()
	engine.POST("/api/assistant/chat", func(c *gin.Context) { c.Set("id", 42); PrepareAssistantRequest(c) }, func(c *gin.Context) { dowstreamCalls++; c.Status(http.StatusNoContent) })
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/assistant/chat", strings.NewReader(fmt.Sprintf(`{"message":%q}`, message)))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	assert.Equal(t, 1, dowstreamCalls)
	assert.Empty(t, response.Header().Get("X-LMM-Assistant-Cache"))
	assert.NotContains(t, response.Body.String(), "stale keys")
}
