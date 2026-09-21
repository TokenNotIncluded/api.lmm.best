package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assistantOperationTestContext(t *testing.T, role int) (*gin.Context, model.User, *AssistantAdminOperationRegistry, *gin.Engine) {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, middleware.WaitAdminAudits(ctx))
	})
	c, user, session := assistantAutomationTestContext(t, db, role)
	token, _, err := service.IssueAccessToken(service.AuthIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: session.UserAuthVersion, SessionVersion: session.Version})
	require.NoError(t, err)
	c.Request.Header.Set("Authorization", "Bearer "+token)
	engine := gin.New()
	registry := NewAssistantAdminOperationRegistry(engine)
	c.Set(assistantAdminOperationsContextKey, &assistantAdminOperationRequest{registry: registry, headers: c.Request.Header.Clone(), host: c.Request.Host, remoteAddr: c.Request.RemoteAddr})
	return c, user, registry, engine
}

func TestAssistantAdminOperationDispatchPreservesMiddlewareAndActor(t *testing.T) {
	c, user, registry, engine := assistantOperationTestContext(t, common.RoleAdminUser)
	global, grouped, handler := 0, 0, 0
	engine.Use(func(c *gin.Context) { global++; c.Next() })
	api := engine.Group("/api", registry.Middleware(), middleware.AdminAuth(), func(c *gin.Context) { grouped++; c.Next() })
	api.GET("/objects/:id", func(c *gin.Context) {
		handler++
		assert.Equal(t, user.Id, c.GetInt("id"))
		assert.Equal(t, common.RoleAdminUser, c.GetInt("role"))
		assert.Equal(t, "7", c.Param("id"))
		assert.Equal(t, "a&b=1", c.Query("q"))
		var body map[string]any
		require.NoError(t, c.ShouldBindJSON(&body))
		assert.Equal(t, "changed", body["name"])
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"name": "changed", "api_key": "must-not-leak", "session_id": "must-not-leak"}})
	})
	registry.Register(http.MethodGet, "/api/objects/:id", "GetChannel", common.RoleAdminUser)
	// The relay bills a separate root, while operations remain actor-bound.
	c.Set(assistantActorUserIDKey, user.Id)
	c.Set("id", user.Id+99)
	c.Set("role", common.RoleRootUser)
	result := executeAssistantAdminOperationTool(c, user.Id, map[string]any{"operation_id": "GET /api/objects/:id", "path_params": map[string]any{"id": "7"}, "query": map[string]any{"q": "a&b=1"}, "body": map[string]any{"name": "changed"}})
	require.Equal(t, true, result["ok"], "%v", result)
	assert.Equal(t, false, result["mutation_attempted"])
	assert.Equal(t, 1, global)
	assert.Equal(t, 1, grouped)
	assert.Equal(t, 1, handler)
	serialized, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "must-not-leak")
	assert.NotContains(t, string(serialized), c.Request.Header.Get("Authorization"))
}

func TestAssistantAdminOperationCannotSelectAnotherStaticRoute(t *testing.T) {
	c, user, registry, engine := assistantOperationTestContext(t, common.RoleRootUser)
	called := 0
	api := engine.Group("/api", registry.Middleware(), middleware.AdminAuth())
	api.POST("/objects/:id", func(c *gin.Context) { called++; c.JSON(200, gin.H{"success": true}) })
	api.POST("/objects/delete_all", func(c *gin.Context) { called++; c.JSON(200, gin.H{"success": true}) })
	registry.Register("POST", "/api/objects/:id", "UpdateObject", common.RoleAdminUser)
	result := executeAssistantAdminOperationTool(c, user.Id, map[string]any{"operation_id": "POST /api/objects/:id", "path_params": map[string]any{"id": "delete_all"}})
	assert.Equal(t, false, result["ok"])
	assert.Equal(t, "confirmation_required", result["status"])
	assert.Zero(t, called)
}

func TestAssistantAdminOperationRechecksCurrentAuthority(t *testing.T) {
	for _, scenario := range []string{"common_user", "root_required", "disabled", "demoted", "session_revoked", "automation_disabled", "invalid_headers", "actor_override"} {
		t.Run(scenario, func(t *testing.T) {
			role := common.RoleAdminUser
			if scenario == "common_user" {
				role = common.RoleCommonUser
			}
			c, user, registry, engine := assistantOperationTestContext(t, role)
			called := 0
			api := engine.Group("/api", registry.Middleware(), middleware.AdminAuth())
			api.POST("/objects", func(c *gin.Context) { called++; c.JSON(200, gin.H{"success": true}) })
			requiredRole := common.RoleAdminUser
			if scenario == "root_required" {
				requiredRole = common.RoleRootUser
			}
			registry.Register("POST", "/api/objects", "UpdateObjects", requiredRole)
			input := map[string]any{"operation_id": "POST /api/objects"}
			switch scenario {
			case "disabled":
				require.NoError(t, model.DB.Model(&user).Update("status", common.UserStatusDisabled).Error)
			case "demoted":
				require.NoError(t, model.DB.Model(&user).Update("role", common.RoleCommonUser).Error)
			case "session_revoked":
				require.NoError(t, model.DB.Model(&model.UserSession{}).Where("user_id = ?", user.Id).Update("status", model.UserSessionStatusRevoked).Error)
			case "automation_disabled":
				c.Set(assistantAdminAutomationContextKey, false)
			case "invalid_headers":
				state, _, err := assistantAdminOperationContext(c, user.Id)
				require.NoError(t, err)
				state.headers.Set("Authorization", "invalid")
			case "actor_override":
				input["headers"] = map[string]any{"Authorization": "invented"}
			}
			result := executeAssistantAdminOperationTool(c, user.Id, input)
			assert.Equal(t, false, result["ok"], "%v", result)
			assert.Zero(t, called)
		})
	}
}

func TestAssistantAdminOperationRetainsPermissionAndSecurityProofGates(t *testing.T) {
	c, user, registry, engine := assistantOperationTestContext(t, common.RoleAdminUser)
	setupAssistantAdminPermissionTest(t, model.DB)
	require.NoError(t, authz.SetUserPermissions(user.Id, authz.PermissionsMap{"channel": {"write": false}}))
	called := 0
	api := engine.Group("/api", registry.Middleware(), middleware.AdminAuth())
	api.PUT("/channel/", middleware.RequirePermission(authz.ChannelWrite), func(c *gin.Context) { called++; c.JSON(200, gin.H{"success": true}) })
	registry.Register("PUT", "/api/channel/", "UpdateChannel", common.RoleAdminUser)
	result := executeAssistantAdminOperationTool(c, user.Id, map[string]any{"operation_id": "PUT /api/channel/"})
	assert.Equal(t, false, result["ok"])
	assert.Equal(t, "confirmation_required", result["status"])
	assert.Zero(t, called)
}

func TestAssistantAdminOperationCatalogFiltersRolesAndRequiresFreshSession(t *testing.T) {
	c, user, registry, _ := assistantOperationTestContext(t, common.RoleAdminUser)
	registry.Register("GET", "/api/channel/", "GetAllChannels", common.RoleAdminUser)
	registry.Register("PUT", "/api/option/", "UpdateOption", common.RoleRootUser)
	registry.Register("POST", "/api/user/login", "Login", 0)
	result := executeAssistantAdminOperationsTool(c, user.Id, map[string]any{"query": "channel", "operation_id": "GET /api/channel/"})
	require.Equal(t, true, result["ok"])
	assert.Equal(t, 1, result["total"])
	items := result["operations"].([]map[string]any)
	require.Len(t, items, 1)
	assert.Contains(t, items[0], "contract")
	result = executeAssistantAdminOperationsTool(c, user.Id, map[string]any{})
	assert.Equal(t, 1, result["total"])
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("user_id = ?", user.Id).Update("status", model.UserSessionStatusRevoked).Error)
	result = executeAssistantAdminOperationsTool(c, user.Id, map[string]any{})
	assert.Equal(t, false, result["ok"])
}

func TestAssistantAdminOperationUncertainResponsesFenceMutationRetries(t *testing.T) {
	for name, response := range map[string]string{"oversized": strings.Repeat("x", assistantAdminOperationResponseLimit+1), "non_json": "not JSON"} {
		t.Run(name, func(t *testing.T) {
			c, user, registry, engine := assistantOperationTestContext(t, common.RoleAdminUser)
			api := engine.Group("/api", registry.Middleware(), middleware.AdminAuth())
			api.GET("/objects", func(c *gin.Context) { c.String(200, response) })
			registry.Register("GET", "/api/objects", "GetAllChannels", common.RoleAdminUser)
			result := executeAssistantAdminOperationTool(c, user.Id, map[string]any{"operation_id": "GET /api/objects"})
			assert.Equal(t, false, result["mutation_attempted"])
			expectedStatus := map[string]string{"oversized": "response_limit_exceeded", "non_json": "non_json_response"}[name]
			assert.Equal(t, expectedStatus, result["status"])
			assert.NotContains(t, result, "response")
		})
	}
}

func TestAssistantAdminOperationPathRejectsEscapesTraversalAndExtraParameters(t *testing.T) {
	for _, value := range []string{"", ".", "..", "../option", "%2foption", "%252foption", "x\\y", "x?role=100", "x#y", "x\n"} {
		_, err := assistantOperationPath("/api/user/:id", map[string]any{"id": value})
		assert.Error(t, err, "%q", value)
	}
	_, err := assistantOperationPath("/api/user/:id", map[string]any{"id": "1", "role": "100"})
	assert.Error(t, err)
	path, err := assistantOperationPath("/api/user/:id", map[string]any{"id": "1"})
	require.NoError(t, err)
	assert.Equal(t, "/api/user/1", path)
}

func TestAssistantAdminOperationRedactsNestedAndStringEncodedCredentials(t *testing.T) {
	response := []any{
		map[string]any{"key": "ModelRatio", "value": `{"model":1}`},
		map[string]any{"key": "PayKey", "value": "sensitive-value"},
		map[string]any{"key": "site_setting", "value": `{"client_secret":"sensitive-value","enabled":true}`},
		map[string]any{"headers": map[string]any{"Authorization": "sensitive-value", "Set-Cookie": "sensitive-value"}, "nested": []any{map[string]any{"refresh_token": "sensitive-value"}}},
	}
	encoded, err := json.Marshal(assistantRedactOperationResponse(response, "GetOptions"))
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "sensitive-value")
	assert.Contains(t, string(encoded), "ModelRatio")
	assert.Contains(t, string(encoded), "model")
	assert.Contains(t, string(encoded), "enabled")
}

func TestAssistantAdminOperationReadOnlyUsesReviewedHandlers(t *testing.T) {
	for _, handler := range []string{"GetAllChannels", "GetAllModelsMeta", "GetModelMeta", "SearchModelsMeta", "GetVendorMeta", "AdminListSubscriptionPlans", "GetDiscountCode", "AdminGetGifts", "GetAdminSecurityPolicy", "AdminGetAssistantUserProfile"} {
		assert.True(t, assistantAdminHandlerReadOnly("GET", handler), handler)
	}
	for _, handler := range []string{"UpdateAllChannelsBalance", "TestAllChannels", "GetUnknownFutureEndpoint"} {
		assert.False(t, assistantAdminHandlerReadOnly("GET", handler))
	}
	assert.False(t, assistantAdminHandlerReadOnly("POST", "GetAllChannels"))
}

func TestAssistantAdminOperationDemotionCannotRetainCachedRootContext(t *testing.T) {
	c, user, registry, engine := assistantOperationTestContext(t, common.RoleRootUser)
	_, err := model.GetUserCache(user.Id)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&user).Update("role", common.RoleAdminUser).Error)
	api := engine.Group("/api", registry.Middleware(), middleware.AdminAuth())
	api.GET("/objects", func(c *gin.Context) {
		assert.Equal(t, common.RoleAdminUser, c.GetInt("role"))
		c.JSON(200, gin.H{"success": true})
	})
	registry.Register("GET", "/api/objects", "GetAllChannels", common.RoleAdminUser)
	result := executeAssistantAdminOperationTool(c, user.Id, map[string]any{"operation_id": "GET /api/objects"})
	assert.Equal(t, true, result["ok"], "%v", result)
}

func TestAssistantAdminOperationMutationCannotReachSecurityProofMiddleware(t *testing.T) {
	c, user, registry, engine := assistantOperationTestContext(t, common.RoleRootUser)
	require.NoError(t, model.DB.AutoMigrate(&model.TwoFA{}))
	called := 0
	api := engine.Group("/api", registry.Middleware(), middleware.RootAuth())
	api.POST("/channel/:id/key", middleware.SecureVerificationRequired(), func(c *gin.Context) { called++; c.JSON(200, gin.H{"success": true, "data": "credential"}) })
	registry.Register("POST", "/api/channel/:id/key", "GetChannelKey", common.RoleRootUser)
	result := executeAssistantAdminOperationTool(c, user.Id, map[string]any{"operation_id": "POST /api/channel/:id/key", "path_params": map[string]any{"id": "1"}})
	assert.Equal(t, false, result["ok"])
	assert.Equal(t, "confirmation_required", result["status"])
	assert.Zero(t, called)
}
