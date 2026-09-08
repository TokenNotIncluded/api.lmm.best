package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assistantTargetTestSession(t *testing.T, user *model.User) (*gin.Context, model.UserSession) {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(&model.UserSession{}))
	session := model.UserSession{SID: fmt.Sprintf("assistant-target-%d", user.Id), UserID: user.Id, Version: 1, UserAuthVersion: user.AuthVersion,
		Status: model.UserSessionStatusActive, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, model.DB.Create(&session).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
	c.Set("id", user.Id)
	c.Set(assistantActorUserIDKey, user.Id)
	c.Set("auth_identity", service.AuthIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: user.AuthVersion, SessionVersion: 1})
	return c, session
}

func TestResolveAssistantUserTargetFiltersHigherRoleCandidates(t *testing.T) {
	db := setupManageUserTestDB(t)
	operator := &model.User{
		Username: "assistant-search-operator", Password: "password", AffCode: "assistant-search-operator-aff",
		Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default",
	}
	manageable := &model.User{
		Username: "shared-assistant-target-low", Password: "password", AffCode: "shared-assistant-target-low-aff",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
	}
	higher := &model.User{
		Username: "shared-assistant-target-high", Password: "password", AffCode: "shared-assistant-target-high-aff",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default",
	}
	root := &model.User{
		Username: "assistant-search-root", Password: "password", AffCode: "assistant-search-root-aff",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default",
	}
	require.NoError(t, db.Create([]*model.User{operator, manageable, higher, root}).Error)
	operatorContext, _ := assistantTargetTestSession(t, operator)
	rootContext, _ := assistantTargetTestSession(t, root)

	// The substring matches both a permitted user and a root user.  The
	// lower-level administrator must receive only the permitted identity and
	// should not be forced to disambiguate an invisible match.
	target, result := resolveAssistantUserTarget(operatorContext, operator.Id, map[string]any{
		"identifier": "shared-assistant-target",
	}, false)
	require.NotNil(t, target)
	require.Nil(t, result)
	assert.Equal(t, manageable.Id, target.User.Id)
	assert.Equal(t, manageable.Username, target.User.Username)

	// Root retains the legitimate ability to disambiguate across all roles.
	rootTarget, rootResult := resolveAssistantUserTarget(rootContext, root.Id, map[string]any{
		"identifier": "shared-assistant-target",
	}, false)
	require.Nil(t, rootTarget)
	assert.Equal(t, "target_ambiguous", rootResult["status"])
	rootCandidates, ok := rootResult["candidates"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, rootCandidates, 2)
	assert.Equal(t, manageable.Id, rootCandidates[0]["id"])
	assert.Equal(t, higher.Id, rootCandidates[1]["id"])
}

func TestAssistantUserTargetRechecksSessionAndAccount(t *testing.T) {
	db := setupManageUserTestDB(t)
	admin := &model.User{Username: "target-session-admin", AffCode: "target-session-admin-aff", Password: "password", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	targetUser := &model.User{Username: "target-session-user", AffCode: "target-session-user-aff", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create([]*model.User{admin, targetUser}).Error)
	c, session := assistantTargetTestSession(t, admin)
	input := map[string]any{"user_id": float64(targetUser.Id)}
	target, result := resolveAssistantUserTarget(c, admin.Id, input, false)
	require.Nil(t, result)
	require.NotNil(t, target)

	// The root relay billing identity must not replace the signed-in actor.
	c.Set("id", 999999)
	target, result = resolveAssistantUserTarget(c, admin.Id, input, false)
	require.Nil(t, result)
	require.NotNil(t, target)
	assert.Equal(t, admin.Id, target.Actor.Id)

	require.NoError(t, db.Model(&session).Update("status", model.UserSessionStatusRevoked).Error)
	target, result = resolveAssistantUserTarget(c, admin.Id, input, false)
	assert.Nil(t, target)
	assert.Equal(t, "admin_access_denied", result["status"])
	target, result = resolveAssistantUserTarget(c, admin.Id, map[string]any{"identifier": "target-session"}, false)
	assert.Nil(t, target)
	assert.Equal(t, "admin_access_denied", result["status"])
	assert.NotContains(t, result, "candidates")
	c.Set(assistantUserContextKey, assistantUserContext{UserID: admin.Id, AdministratorMode: true})
	toolResult := executeAssistantTool(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: "get_service_facts", Arguments: "{}"}})
	assert.Equal(t, false, toolResult["ok"])
	assert.Equal(t, "admin_access_denied", toolResult["status"], "revocation applies to every tool in an administrator conversation")

	require.NoError(t, db.Model(targetUser).Update("status", common.UserStatusDisabled).Error)
	target, result = resolveAssistantUserTarget(nil, targetUser.Id, nil, false)
	assert.Nil(t, target)
	assert.Equal(t, "context_unavailable", result["status"])
}

func TestAssistantSafeToolInputPreservesConversationTitle(t *testing.T) {
	input := assistantSafeToolInput(`{"title":"配置 API 密钥","secret":"must-not-leak"}`)

	assert.Equal(t, map[string]any{"title": "配置 API 密钥"}, input)
}

func TestAssistantMathToolTraceShowsSafeExpressionResultAndActionableErrors(t *testing.T) {
	success := buildAssistantToolTrace(assistantOpenAIToolCall{
		Function: assistantOpenAIToolCallFunction{
			Name:      "calculate_math",
			Arguments: `{"expression":"6 * 7","variables":{"secret":42}}`,
		},
	}, map[string]any{"ok": true, "result": float64(42)})
	require.NotNil(t, success.Result)
	assert.Equal(t, float64(42), *success.Result)
	assert.Equal(t, map[string]any{"expression": "6 * 7"}, success.Input)
	assert.Empty(t, success.ErrorCode)

	missing := buildAssistantToolTrace(assistantOpenAIToolCall{
		Function: assistantOpenAIToolCallFunction{Name: "calculate_math", Arguments: `{}`},
	}, map[string]any{"ok": false, "error": "a math expression is required"})
	assert.Equal(t, "output-error", missing.Status)
	assert.Equal(t, "missing_math_expression", missing.ErrorCode)
	assert.Nil(t, missing.Result)
}
