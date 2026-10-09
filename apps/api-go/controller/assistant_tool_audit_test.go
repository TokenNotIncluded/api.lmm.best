package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantConfigReadsPageAndReadExactUnicodeValue(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	value := strings.Repeat("退款\x00<", 4000)
	common.OptionMap = map[string]string{"group_ratio_setting.group_warnings": value, "PayKey": "never-disclose"}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() { common.OptionMapRWMutex.Lock(); common.OptionMap = previous; common.OptionMapRWMutex.Unlock() })
	page := assistantAdminConfigPage(common.RoleRootUser, nil)
	require.Equal(t, true, page["ok"])
	require.Len(t, page["configurable_settings"], 10)
	require.Equal(t, true, page["has_more"])
	encoded, err := common.MarshalLimit(page, assistantToolResultMaxBytes)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "never-disclose")
	var rebuilt strings.Builder
	for offset := 0; ; {
		page = assistantAdminConfigPage(common.RoleRootUser, map[string]any{"key": "group_ratio_setting.group_warnings", "value_offset": float64(offset), "value_limit": float64(6000)})
		require.Equal(t, true, page["ok"])
		_, err = common.MarshalLimit(page, assistantToolResultMaxBytes)
		require.NoError(t, err)
		rows := page["configurable_settings"].([]map[string]any)
		require.Len(t, rows, 1)
		rebuilt.WriteString(rows[0]["current_value"].(string))
		if rows[0]["value_truncated"] == false {
			break
		}
		next := rows[0]["next_value_offset"].(int)
		require.Greater(t, next, offset)
		offset = next
	}
	assert.Equal(t, value, rebuilt.String())
	query := assistantAdminConfigPage(common.RoleRootUser, map[string]any{"query": "group_warnings"})
	assert.Equal(t, 1, query["total"])
	for _, input := range []map[string]any{
		{"key": "PayKey"}, {"key": "AssistantToolPolicy"}, {"limit": float64(21)}, {"offset": -1.0}, {"limit": 1.5}, {"offset": "2"}, {"value_offset": 0.0}, {"query": true}, {"key": "SystemName", "query": "name"},
	} {
		require.Equal(t, false, assistantAdminConfigPage(common.RoleRootUser, input)["ok"], "%v", input)
	}
}

func TestAssistantAdminChatRegistryUsesOriginalCredentialsWithoutManualInjection(t *testing.T) {
	original, user, registry, engine := assistantOperationTestContext(t, common.RoleRootUser)
	engine.Use(registry.AssistantChatMiddleware())
	api := engine.Group("/api", registry.Middleware(), middleware.RootAuth())
	api.GET("/option/", func(c *gin.Context) { c.JSON(200, gin.H{"success": true, "data": []any{}}) })
	registry.Register("GET", "/api/option/", "GetOptions", common.RoleRootUser)
	engine.POST("/api/assistant/chat", middleware.UserAuth(), func(c *gin.Context) {
		// Match relay preparation: billing identity is not the authenticated actor.
		c.Set(assistantActorUserIDKey, user.Id)
		c.Set("id", user.Id+100)
		c.Request.Header.Set("Authorization", "changed-by-relay")
		state, _, err := assistantAdminOperationContext(c, user.Id)
		require.NoError(t, err)
		assert.Equal(t, original.Request.Header.Get("Authorization"), state.headers.Get("Authorization"))
		result := executeAssistantAdminOperationsTool(c, user.Id, map[string]any{"query": "option"})
		require.Equal(t, true, result["ok"], "%v", result)
		assert.Equal(t, 1, result["total"])
		result = executeAssistantAdminOperationTool(c, user.Id, map[string]any{"operation_id": "GET /api/option/"})
		require.Equal(t, true, result["ok"], "%v", result)
		c.JSON(200, gin.H{"success": true})
	})
	request := httptest.NewRequest("POST", "/api/assistant/chat", nil)
	request.Header = original.Request.Header.Clone()
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, 200, response.Code, response.Body.String())
}

func TestAssistantAdminChatRegistryDoesNotCrossEnginesOrCaptureRelay(t *testing.T) {
	for _, label := range []string{"first", "second"} {
		engine := gin.New()
		registry := NewAssistantAdminOperationRegistry(engine)
		engine.Use(registry.AssistantChatMiddleware())
		engine.POST("/api/assistant/chat", func(c *gin.Context) {
			state, ok := c.Get(assistantAdminOperationsContextKey)
			require.True(t, ok, label)
			require.Same(t, registry, state.(*assistantAdminOperationRequest).registry)
			c.Status(http.StatusNoContent)
		})
		engine.POST("/v1/chat/completions", func(c *gin.Context) {
			_, exists := c.Get(assistantAdminOperationsContextKey)
			require.False(t, exists)
			c.Status(204)
		})
		for _, path := range []string{"/api/assistant/chat", "/v1/chat/completions"} {
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest("POST", path, nil))
			require.Equal(t, 204, recorder.Code)
		}
	}
}

func TestAssistantTraceErrorsAreSafeAndActionable(t *testing.T) {
	for _, status := range []string{"tool_disabled", "tool_level_denied", "tool_policy_unavailable", "operation_dispatch_unavailable", "invalid_arguments", "response_limit_exceeded", "policy_changed", "policy_unavailable", "not_found", "forbidden", "admin_access_denied", "context_unavailable", "provider-secret-with-token"} {
		t.Run(status, func(t *testing.T) {
			trace := buildAssistantToolTrace(assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: "get_admin_server_config", Arguments: `{"key":"SystemName","content":"must-not-appear"}`}}, map[string]any{"ok": false, "status": status, "error": "Bearer must-not-appear; sql password=must-not-appear"})
			assert.Equal(t, "output-error", trace.Status)
			require.NotEmpty(t, trace.ErrorCode)
			data, err := json.Marshal(trace)
			require.NoError(t, err)
			assert.NotContains(t, string(data), "must-not-appear")
			if status == "provider-secret-with-token" {
				assert.Equal(t, "tool_failed", trace.ErrorCode)
			}
		})
	}
}

func TestAssistantRootContextAliasesHaveIdenticalToolPermissions(t *testing.T) {
	assistantPolicyForTest(t, nil, nil)
	for _, level := range []string{"ROOT", "L6"} {
		state := assistantUserContext{AdministratorMode: true, AccessLevel: level}
		for _, name := range []string{"get_admin_server_config", "prepare_admin_site_policy_change", "prepare_admin_pricing_change"} {
			assert.True(t, assistantToolAllowedForContext(name, state), "%s %s", level, name)
			assert.True(t, assistantPolicyTestContainsTool(assistantToolDefinitionsForContext(state), name), "%s %s", level, name)
		}
	}
	assert.False(t, assistantToolAllowedForContext("prepare_admin_site_policy_change", assistantUserContext{AdministratorMode: true, AccessLevel: "L5"}))
}

func TestAssistantMissingAdminDispatchReportsInfrastructureNotForbidden(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	c, user, _ := assistantAutomationTestContext(t, db, common.RoleRootUser)
	result := executeAssistantAdminOperationsTool(c, user.Id, nil)
	assert.Equal(t, "operation_dispatch_unavailable", result["status"])
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("role", common.RoleCommonUser).Error)
	result = executeAssistantAdminOperationsTool(c, user.Id, nil)
	assert.Equal(t, "forbidden", result["status"])
}
