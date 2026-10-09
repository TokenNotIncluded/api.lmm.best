package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantToolPolicyMixedUserReadsExcludeDisabledProfiles(t *testing.T) {
	db := setupManageUserTestDB(t)
	persistCreditDenominationFixture(t, db)
	c, actor, session := assistantAutomationTestContext(t, db, common.RoleRootUser)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, middleware.WaitAdminAudits(ctx))
	})
	before := setting.GetAssistantSettings().ToolPolicy
	t.Cleanup(func() { require.NoError(t, setting.UpdateAssistantToolPolicy(before)) })
	require.NoError(t, db.Create(&model.Option{Key: setting.AssistantToolPolicyOptionKey, Value: setting.DefaultAssistantToolPolicy}).Error)
	target := model.User{
		Username: "mixed-policy-profile-target", Password: "local-fixture-only",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Group: "default", AffCode: "mixed-policy-profile-target",
	}
	require.NoError(t, db.Create(&target).Error)
	_, err := model.SaveProfile(target.Id, actor.Id, model.ProfileInput{
		Key: model.AssistantProfileGuided, Tags: []string{"synthetic-profile-tag"},
		Strategy: "A synthetic response preference.", Source: model.AssistantProfileSourceAdmin, Enabled: true,
	})
	require.NoError(t, err)
	token, _, err := service.IssueAccessToken(service.AuthIdentity{
		UserID: actor.Id, SessionID: session.SID,
		UserAuthVersion: session.UserAuthVersion, SessionVersion: session.Version,
	})
	require.NoError(t, err)
	c.Request.Header.Set("Authorization", "Bearer "+token)
	engine := gin.New()
	registry := NewAssistantAdminOperationRegistry(engine)
	c.Set(assistantAdminOperationsContextKey, &assistantAdminOperationRequest{
		registry: registry, headers: c.Request.Header.Clone(), host: c.Request.Host, remoteAddr: c.Request.RemoteAddr,
	})
	route := engine.Group("/api", registry.Middleware(), middleware.AdminAuth())
	route.GET("/user/:id", GetUser)
	route.GET("/user/", GetAllUsers)
	route.GET("/user/search", SearchUsers)
	registry.Register(http.MethodGet, "/api/user/:id", "GetUser", common.RoleAdminUser)
	registry.Register(http.MethodGet, "/api/user/", "GetAllUsers", common.RoleAdminUser)
	registry.Register(http.MethodGet, "/api/user/search", "SearchUsers", common.RoleAdminUser)
	// Billing belongs to a different account; dispatch and policy use the actor.
	c.Set(assistantActorUserIDKey, actor.Id)
	c.Set("id", actor.Id+99)
	reads := []struct {
		operation string
		params    map[string]any
		query     map[string]any
	}{
		{"GET /api/user/:id", map[string]any{"id": strconv.Itoa(target.Id)}, nil},
		{"GET /api/user/", nil, map[string]any{"page_size": "10"}},
		{"GET /api/user/search", nil, map[string]any{"keyword": target.Username, "page_size": "10"}},
	}
	for _, phase := range []struct {
		name    string
		policy  string
		visible bool
	}{
		{"enabled", setting.DefaultAssistantToolPolicy, true},
		{"tool_disabled", `{"version":1,"tools":{"get_admin_user_skills":false}}`, false},
		{"group_disabled", `{"version":1,"groups":{"admin_read":false}}`, false},
		{"enabled_again", setting.DefaultAssistantToolPolicy, true},
	} {
		t.Run(phase.name, func(t *testing.T) {
			require.NoError(t, db.Model(&model.Option{}).Where("key = ?", setting.AssistantToolPolicyOptionKey).Update("value", phase.policy).Error)
			// Leave local state stale to model a policy committed on another node.
			require.NoError(t, setting.UpdateAssistantToolPolicy(setting.DefaultAssistantToolPolicy))
			for _, read := range reads {
				result := executeAssistantAdminOperationTool(c, actor.Id, map[string]any{
					"operation_id": read.operation, "path_params": read.params, "query": read.query,
				})
				require.Equal(t, true, result["ok"], "%s: %v", read.operation, result)
				response, ok := result["response"].(map[string]any)
				require.True(t, ok)
				data, ok := response["data"].(map[string]any)
				require.True(t, ok)
				row := data
				if read.operation != "GET /api/user/:id" {
					items, ok := data["items"].([]any)
					require.True(t, ok)
					row = nil
					for _, item := range items {
						candidate, ok := item.(map[string]any)
						require.True(t, ok)
						if candidate["username"] == target.Username {
							row = candidate
							break
						}
					}
					require.NotNil(t, row, "target missing in %s", read.operation)
				}
				assert.Equal(t, target.Username, row["username"])
				assert.Equal(t, float64(target.Id), row["id"])
				if phase.visible {
					profile, ok := row["assistant_profile"].(map[string]any)
					require.True(t, ok, "%s: profile must be visible when enabled", read.operation)
					assert.Equal(t, []any{"synthetic-profile-tag"}, profile["tags"])
				} else {
					assert.NotContains(t, row, "assistant_profile", "%s: mixed reads cannot expose disabled personalization", read.operation)
				}
			}
			// Ordinary administrator queries keep their existing profile visibility.
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/user/"+strconv.Itoa(target.Id), nil)
			request.Header = c.Request.Header.Clone()
			engine.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusOK, recorder.Code)
			var normalResponse map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &normalResponse))
			require.Equal(t, true, normalResponse["success"])
			assert.Contains(t, normalResponse["data"].(map[string]any), "assistant_profile")
		})
	}
}
