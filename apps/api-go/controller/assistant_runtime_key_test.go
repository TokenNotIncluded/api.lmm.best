package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureAssistantRuntimeKeyCreatesOnlyForBillingRootWithoutRevealingSecret(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	root := model.User{Username: "runtime-key-root", AffCode: "runtime-key-root", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	otherRoot := model.User{Username: "runtime-key-other-root", AffCode: "runtime-key-other-root", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&otherRoot).Error)

	originalSettings := setting.GetAssistantSettings()
	originalLoader := loadAssistantBillingUser
	originalRouteResolver := assistantConfiguredRouteResolver
	setting.SetAssistantEnabled(true)
	loadAssistantBillingUser = func() (*model.User, error) { return &root, nil }
	assistantConfiguredRouteResolver = func(setting.AssistantSettings) (string, string, error) {
		return "default", "assistant-model", nil
	}
	t.Cleanup(func() {
		setting.SetAssistantEnabled(originalSettings.Enabled)
		loadAssistantBillingUser = originalLoader
		assistantConfiguredRouteResolver = originalRouteResolver
	})

	for attempt := 0; attempt < 2; attempt++ {
		ctx, response := newAuthenticatedContext(t, http.MethodPost, "/api/assistant/runtime-key", map[string]any{}, root.Id)
		ctx.Set("session_id", "browser-session")
		EnsureAssistantRuntimeKey(ctx)
		assert.Equal(t, http.StatusOK, response.Code)
		assert.NotContains(t, response.Body.String(), `"key"`)
		assert.NotContains(t, response.Body.String(), "sk-")
	}
	var tokens []model.Token
	require.NoError(t, db.Where("user_id = ?", root.Id).Find(&tokens).Error)
	require.Len(t, tokens, 1)
	assert.Equal(t, model.TokenCreationSourceAssistantRuntime, tokens[0].CreationSource)
	ctx, response := newAuthenticatedContext(t, http.MethodGet, "/api/token/?creation_mode=automatic", nil, root.Id)
	GetAllTokens(ctx)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"creation_source":"assistant_runtime"`)
	assert.NotContains(t, response.Body.String(), tokens[0].Key)

	ctx, response = newAuthenticatedContext(t, http.MethodGet, "/api/token/1/key", nil, root.Id)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tokens[0].Id)}}
	GetTokenKey(ctx)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Contains(t, response.Body.String(), "TOKEN_INTERNAL_ONLY")
	assert.NotContains(t, response.Body.String(), tokens[0].Key)

	ctx, response = newAuthenticatedContext(t, http.MethodPost, "/api/assistant/runtime-key", map[string]any{}, otherRoot.Id)
	ctx.Set("session_id", "browser-session")
	EnsureAssistantRuntimeKey(ctx)
	assert.Equal(t, http.StatusForbidden, response.Code)

	ctx, response = newAuthenticatedContext(t, http.MethodPost, "/api/assistant/runtime-key", map[string]any{}, root.Id)
	ctx.Set("use_access_token", true)
	EnsureAssistantRuntimeKey(ctx)
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestAssistantChatUsesPersistedSuperAdministratorRuntimeKey(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	root := model.User{Id: 987, Username: "runtime-chat-root", AffCode: "runtime-chat-root", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&root).Error)
	withAssistantSettings(t, true, "assistant-model")
	ensureAssistantRuntimeToken = model.EnsureAssistantRuntimeToken

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
	ctx.Set("id", root.Id)
	ctx.Set(assistantRouteGroupContextKey, "default")
	ctx.Set(assistantRouteModelContextKey, "assistant-model")
	AssistantChat(ctx)

	assert.Equal(t, http.StatusInternalServerError, response.Code, "the deliberately absent conversation should stop before relay")
	var token model.Token
	require.NoError(t, db.Where("user_id = ? AND creation_source = ?", root.Id, model.TokenCreationSourceAssistantRuntime).First(&token).Error)
	assert.Equal(t, token.Id, ctx.GetInt("token_id"))
	assert.Equal(t, token.Key, ctx.GetString("token_key"))
}
