package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assistantPolicyForTest(t *testing.T, groups, tools map[string]bool) {
	t.Helper()
	before := setting.GetAssistantSettings().ToolPolicy
	beforeReader := assistantToolPolicyReader
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAssistantToolPolicy(before))
		assistantToolPolicyReader = beforeReader
	})
	assistantToolPolicyReader = func(context.Context) (string, setting.AssistantToolPolicy, error) {
		return setting.NormalizeAssistantToolPolicy(setting.GetAssistantSettings().ToolPolicy)
	}
	raw, err := json.Marshal(setting.AssistantToolPolicy{Version: 1, Groups: groups, Tools: tools})
	require.NoError(t, err)
	// nil maps serialize as null, whereas the stored policy requires objects.
	value := strings.ReplaceAll(string(raw), ":null", ":{}")
	require.NoError(t, setting.UpdateAssistantToolPolicy(value))
}

func TestAssistantToolPolicyCatalogueMatchesActualDefinitions(t *testing.T) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	AdminGetAssistantToolCatalogue(c)
	require.Equal(t, http.StatusOK, response.Code)
	var catalog struct {
		Success bool `json:"success"`
		Data    struct {
			Capabilities map[string]bool `json:"capabilities"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &catalog))
	require.True(t, catalog.Success)
	assert.True(t, catalog.Data.Capabilities["policy_rules"])
	definitions := buildAssistantTools()
	registered := map[string]bool{}
	for _, definition := range definitions {
		name := definition.Function.Name
		require.False(t, registered[name], "duplicate tool %s", name)
		registered[name] = true
		if strings.HasPrefix(name, "prepare_admin_") {
			assert.NotContains(t, definition.Function.Description, "immediately")
			assert.Contains(t, definition.Function.Description, "explicit browser confirmation")
		}
	}
	metadata := map[string]bool{}
	effects := map[string]int{}
	for _, group := range setting.AssistantToolCatalogue() {
		for _, tool := range group.Tools {
			require.False(t, metadata[tool.Name], "duplicate metadata %s", tool.Name)
			metadata[tool.Name] = true
			require.NotEmpty(t, tool.Label)
			require.NotEmpty(t, tool.Description)
			effects[tool.Effect]++
		}
	}
	assert.Equal(t, registered, metadata, "new tools must be classified before they can be enabled")
	assert.Len(t, registered, 72)
	assert.Equal(t, map[string]int{"read_only": 42, "confirmation": 16, "server_guarded": 13, "navigation": 1}, effects)
	assert.NotContains(t, assistantAdminAvailableConfigLabels(), setting.AssistantToolPolicyOptionKey, "the model cannot re-enable its tools")
}

func TestAssistantToolPolicyRechecksCachedMembershipAndPermissions(t *testing.T) {
	context := assistantUserContext{AccessLevel: "L1", DeveloperAccessGranted: true}
	assistantPolicyForTest(t, nil, nil)
	initial := assistantToolDefinitionsForContext(context)
	require.True(t, assistantPolicyTestContainsTool(initial, "calculate_math"))
	assistantPolicyForTest(t, map[string]bool{"service_help": false}, map[string]bool{"calculate_math": true})
	assert.False(t, assistantPolicyTestContainsTool(assistantToolDefinitionsForContext(context), "calculate_math"))
	assert.False(t, assistantToolAllowedForContext("calculate_math", context))
	assert.False(t, assistantToolExecutionAllowedForContext("get_service_facts", context))
	assistantPolicyForTest(t, nil, nil)
	assert.True(t, assistantPolicyTestContainsTool(assistantToolDefinitionsForContext(context), "calculate_math"), "old account cache must recover after re-enabling")
	l0 := assistantUserContext{AccessLevel: "L0"}
	assert.False(t, assistantToolAllowedForContext("prepare_admin_config_change", l0))
	assert.False(t, assistantToolAllowedForContext("request_create_key", l0))
	assert.False(t, assistantToolAllowedForContext("prepare_admin_config_change", assistantUserContext{AdministratorMode: true, AccessLevel: "ADMIN"}))
	assert.False(t, assistantToolExecutionAllowedForContext("not_a_tool", context))
}

func assistantPolicyTestContainsTool(tools []assistantOpenAIToolDefinition, name string) bool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

func TestAssistantToolPolicyBlocksEveryMockCallBeforeAnyWork(t *testing.T) {
	groups := map[string]bool{}
	for _, group := range setting.AssistantToolCatalogue() {
		groups[group.ID] = false
	}
	assistantPolicyForTest(t, groups, nil)
	for _, definition := range buildAssistantTools() {
		result := executeAssistantTool(nil, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: definition.Function.Name, Arguments: `{"ignore_policy":true}`}})
		assert.Equal(t, false, result["ok"], definition.Function.Name)
		assert.Equal(t, "tool_disabled", result["status"], definition.Function.Name)
	}
	assert.False(t, assistantToolExecutionAllowedForContext("get_plan_offers", assistantUserContext{AccessLevel: "L0"}), "view-only L0 exception cannot bypass disabled tools")
}

func TestAssistantToolPolicyForcedChoicesAndTitleRespectPolicy(t *testing.T) {
	assistantPolicyForTest(t, nil, map[string]bool{"set_conversation_title": false, "get_registration_risk": false, "request_create_key": false})
	mathContext := assistantUserContext{AccessLevel: "L0", ConversationTitleNeeded: true, Intent: model.AssistantIntentMath}
	assert.Equal(t, "calculate_math", assistantNamedToolChoiceName(assistantToolChoiceForContext(mathContext)))
	assert.Equal(t, "calculate_math", assistantNamedToolChoiceName(assistantToolChoiceForAgentStep(mathContext, nil, nil)))
	keyContext := assistantUserContext{AccessLevel: "L1", DeveloperAccessGranted: true, CreateKeyAction: assistantCreateKeyActionRequest}
	choice := assistantToolChoiceForAgentStep(keyContext, map[string]bool{"get_service_facts": true}, map[string]bool{"get_service_facts": true})
	assert.Equal(t, "none", choice, "a disabled forced tool must not be sent to the provider")
	l0 := assistantUserContext{AccessLevel: "L0", Intent: model.AssistantIntentRecommendation, CompletedAssistantTurns: model.AssistantDirectGrantMinCompletedTurns, RecommendationAction: assistantRecommendationActionRevise}
	choice = assistantToolChoiceForAgentStep(l0, map[string]bool{"get_account_access": true}, map[string]bool{"get_account_access": true})
	assert.Equal(t, "none", choice)
}

func TestAssistantToolPolicySeparatesReplyCache(t *testing.T) {
	settings := setting.GetAssistantSettings()
	settings.CacheEnabled, settings.CacheTTLMinutes = true, 30
	conversation := []assistantOpenAIMessage{{Role: "user", Content: "hello"}}
	before := assistantCacheKey(settings, conversation)
	settings.ToolPolicy = `{"version":1,"groups":{},"tools":{"search_web":false}}`
	assert.NotEqual(t, before, assistantCacheKey(settings, conversation))
}

func TestAssistantToolPolicyAllDisabledStillAnswersWithoutProviderToolChoice(t *testing.T) {
	groups := map[string]bool{}
	for _, group := range setting.AssistantToolCatalogue() {
		groups[group.ID] = false
	}
	assistantPolicyForTest(t, groups, nil)
	c, recorder := assistantLoopTestContext(t)
	c.Set(assistantUserContextKey, assistantUserContext{AccessLevel: "L0", Intent: model.AssistantIntentMath})
	original := relayAssistantAgentTurn
	t.Cleanup(func() { relayAssistantAgentTurn = original })
	turns := 0
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		assert.Empty(t, request.Tools)
		assert.Nil(t, request.ToolChoice, "providers may reject tool_choice without any tools")
		return http.StatusOK, assistantLoopCallBody(t, nil, "The calculator is currently unavailable."), nil
	}
	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 3}, []assistantOpenAIMessage{{Role: "user", Content: "calculate 1+1"}})
	assert.Equal(t, 1, turns)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "unavailable")
}

func TestAssistantToolPolicyBlocksPendingConfirmationsWithoutDatabaseAccess(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{"request_create_key", PrepareAssistantDefaultKey}, {"request_create_key", CreateAssistantDefaultKey},
		{"prepare_api_key_action", ConfirmAssistantAPIKeyAction}, {"prepare_user_action", ConfirmAssistantDisplayName},
		{"prepare_image_generation", PrepareAssistantDrawing}, {"prepare_new_user_gift", ClaimAssistantNewUserGift},
		{"prepare_weekly_discount", ClaimAssistantWeeklyDiscount}, {"request_human_support", SubmitAssistantHandoff},
	} {
		t.Run(test.name, func(t *testing.T) {
			assistantPolicyForTest(t, nil, map[string]bool{test.name: false})
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/mock-confirmation", strings.NewReader(`{"confirmed":true,"confirmation_token":"mock"}`))
			test.handler(c)
			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.Contains(t, recorder.Body.String(), "ASSISTANT_TOOL_DISABLED")
			assert.True(t, c.IsAborted())
		})
	}
	assistantPolicyForTest(t, nil, map[string]bool{"request_human_support": false})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	submitAssistantAccountDisableRequest(c, accountActionRequestInput{})
	assert.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestAssistantToolPolicyBlocksPendingAdminCardBeforeConsumption(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	c, user, session := assistantAutomationTestContext(t, db, common.RoleRootUser)
	for _, kind := range []string{assistantAdminConfigChangeKind, assistantAdminPricingChangeKind, assistantAdminChannelChangeKind, assistantAdminUserSkillChangeKind, assistantAdminModelSyncChangeKind} {
		t.Run(kind, func(t *testing.T) {
			tool := assistantAdminChangeTool(kind)
			payload, err := json.Marshal(assistantAdminChangePayload{Kind: kind})
			require.NoError(t, err)
			token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: model.AuthFlowPurposeAssistantAdmin, UserId: user.Id, SessionId: session.SID, Payload: string(payload), ExpiresAt: time.Now().Add(time.Minute)})
			require.NoError(t, err)
			assistantPolicyForTest(t, nil, map[string]bool{tool: false})
			recorder := httptest.NewRecorder()
			applyContext, _ := gin.CreateTestContext(recorder)
			applyContext.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/admin/apply", strings.NewReader(`{"confirmed":true,"confirmation_token":"`+token+`"}`))
			applyContext.Request.Header.Set("Content-Type", "application/json")
			for key, value := range c.Keys {
				applyContext.Set(key, value)
			}
			ApplyAssistantAdminChange(applyContext)
			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.Contains(t, recorder.Body.String(), "ASSISTANT_TOOL_DISABLED")
			_, err = model.GetAuthFlow(token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeAssistantAdmin, UserId: user.Id, SessionId: session.SID})
			require.NoError(t, err, "disabling a tool must not consume a pending preview")
			_, err = applyAssistantAdminChange(c, assistantAdminChangePayload{Kind: kind})
			require.ErrorContains(t, err, "disabled")
		})
	}
}

func TestAssistantToolPolicyGenericReadCannotBypassDisabledSpecializedTool(t *testing.T) {
	c, user, registry, engine := assistantOperationTestContext(t, common.RoleRootUser)
	called := 0
	engine.GET("/api/policy-test-channel", func(c *gin.Context) { called++; c.JSON(http.StatusOK, gin.H{"success": true}) })
	registry.Register(http.MethodGet, "/api/policy-test-channel", "GetChannel", common.RoleRootUser)
	assistantPolicyForTest(t, nil, map[string]bool{"get_admin_channels": false})
	result := executeAssistantAdminOperationTool(c, user.Id, map[string]any{"operation_id": "GET /api/policy-test-channel"})
	assert.Equal(t, "tool_disabled", result["status"])
	assert.Zero(t, called)
	listed := executeAssistantAdminOperationsTool(c, user.Id, map[string]any{})
	assert.Equal(t, 0, listed["total"])
	assistantPolicyForTest(t, nil, nil)
	result = executeAssistantAdminOperationTool(c, user.Id, map[string]any{"operation_id": "GET /api/policy-test-channel"})
	assert.Equal(t, true, result["ok"])
	assert.Equal(t, 1, called)
}

func TestAssistantToolPolicyAnotherNodeRevokesCallsAndPendingCardsImmediately(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	c, user, session := assistantAutomationTestContext(t, db, common.RoleRootUser)
	before := setting.GetAssistantSettings().ToolPolicy
	t.Cleanup(func() { require.NoError(t, setting.UpdateAssistantToolPolicy(before)) })
	require.NoError(t, setting.UpdateAssistantToolPolicy(setting.DefaultAssistantToolPolicy))
	payload, err := json.Marshal(assistantAdminChangePayload{Kind: assistantAdminConfigChangeKind})
	require.NoError(t, err)
	token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: model.AuthFlowPurposeAssistantAdmin, UserId: user.Id, SessionId: session.SID, Payload: string(payload), ExpiresAt: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	// Simulate another server committing a change without local option sync.
	require.NoError(t, db.Create(&model.Option{Key: setting.AssistantToolPolicyOptionKey, Value: `{"version":1,"tools":{"calculate_math":false,"prepare_admin_config_change":false}}`}).Error)
	require.True(t, setting.AssistantToolEnabled("calculate_math"), "this process has not synchronized yet")
	result := executeAssistantTool(c, assistantLoopMathCall("remote-revocation", 1))
	assert.Equal(t, "tool_disabled", result["status"])
	assert.False(t, setting.AssistantToolEnabled("calculate_math"), "the authoritative snapshot updates directory filtering")
	recorder := httptest.NewRecorder()
	applyContext, _ := gin.CreateTestContext(recorder)
	applyContext.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/admin/apply", strings.NewReader(`{"confirmed":true,"confirmation_token":"`+token+`"}`))
	applyContext.Request.Header.Set("Content-Type", "application/json")
	for key, value := range c.Keys {
		applyContext.Set(key, value)
	}
	// Keep the local process deliberately stale again before confirming.
	require.NoError(t, setting.UpdateAssistantToolPolicy(setting.DefaultAssistantToolPolicy))
	ApplyAssistantAdminChange(applyContext)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "ASSISTANT_TOOL_DISABLED")
	_, err = model.GetAuthFlow(token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeAssistantAdmin, UserId: user.Id, SessionId: session.SID})
	require.NoError(t, err, "a remotely disabled preview remains unconsumed")
	settings := setting.GetAssistantSettings()
	settings.CacheEnabled, settings.CacheTTLMinutes = true, 30
	settings.ToolPolicy = setting.DefaultAssistantToolPolicy
	conversation := []assistantOpenAIMessage{{Role: "user", Content: "hello"}}
	beforeKey := assistantCacheKey(settings, conversation)
	settings.ToolPolicy, _, err = refreshAssistantToolPolicy(c)
	require.NoError(t, err)
	assert.NotEqual(t, beforeKey, assistantCacheKey(settings, conversation), "cache preparation uses the committed remote policy")
}

func TestAssistantToolPolicyInvalidOrUnavailableAuthorityNeverExecutes(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	before := setting.GetAssistantSettings().ToolPolicy
	t.Cleanup(func() { require.NoError(t, setting.UpdateAssistantToolPolicy(before)) })
	require.NoError(t, setting.UpdateAssistantToolPolicy(setting.DefaultAssistantToolPolicy))
	require.NoError(t, db.Create(&model.Option{Key: setting.AssistantToolPolicyOptionKey, Value: `{"version":999}`}).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("id", 1)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
	result := executeAssistantTool(c, assistantLoopMathCall("invalid-policy", 1))
	assert.Equal(t, "tool_policy_unavailable", result["status"])
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", setting.AssistantToolPolicyOptionKey).Update("value", setting.DefaultAssistantToolPolicy).Error)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	result = executeAssistantTool(c, assistantLoopMathCall("cancelled-policy", 1))
	assert.Equal(t, "tool_policy_unavailable", result["status"])
}

func TestAssistantToolPolicyRevokedDuringModelCallDoesNotWriteMemory(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantMemory{}))
	c, _, _ := assistantAutomationTestContext(t, db, common.RoleRootUser)
	c.Set(assistantUserContextKey, assistantUserContext{AccessLevel: "L1", DeveloperAccessGranted: true})
	before := setting.GetAssistantSettings().ToolPolicy
	require.NoError(t, setting.UpdateAssistantToolPolicy(setting.DefaultAssistantToolPolicy))
	original := relayAssistantAgentTurn
	t.Cleanup(func() {
		relayAssistantAgentTurn = original
		require.NoError(t, setting.UpdateAssistantToolPolicy(before))
	})
	turns := 0
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		if turns == 1 {
			assert.False(t, assistantPolicyTestContainsTool(request.Tools, "remember_memory"))
			return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{assistantControlCall("discover_tools", `{"names":["remember_memory"]}`)}, ""), nil
		}
		if turns == 2 {
			require.True(t, assistantPolicyTestContainsTool(request.Tools, "remember_memory"))
			require.NoError(t, db.Create(&model.Option{Key: setting.AssistantToolPolicyOptionKey, Value: `{"version":1,"tools":{"remember_memory":false}}`}).Error)
			call := assistantOpenAIToolCall{ID: "revoked-mid-turn", Type: "function", Function: assistantOpenAIToolCallFunction{Name: "remember_memory", Arguments: `{"title":"Project preference","content":"Use short answers for this project"}`}}
			return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{call}, ""), nil
		}
		assert.False(t, assistantPolicyTestContainsTool(request.Tools, "remember_memory"))
		assert.Contains(t, request.Messages[len(request.Messages)-1].Content, "tool_disabled")
		return http.StatusOK, assistantLoopCallBody(t, nil, "Memory storage was disabled before execution."), nil
	}
	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 4}, []assistantOpenAIMessage{{Role: "user", Content: "Remember my project preference"}})
	assert.Equal(t, 3, turns)
	var count int64
	require.NoError(t, db.Model(&model.AssistantMemory{}).Count(&count).Error)
	assert.Zero(t, count, "a model response generated before revocation cannot commit a later memory")
}
