package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecurityPolicySeparatesPublicAndAdminRuleDetails(t *testing.T) {
	setupSecurityModerationDB(t)
	publicRecorder := httptest.NewRecorder()
	publicContext, _ := gin.CreateTestContext(publicRecorder)
	publicContext.Request = httptest.NewRequest(http.MethodGet, "/api/security/policy", nil)
	GetPublicSecurityPolicy(publicContext)
	require.Equal(t, http.StatusOK, publicRecorder.Code)
	assert.NotContains(t, publicRecorder.Body.String(), "do not publish this matcher")
	assert.NotContains(t, publicRecorder.Body.String(), "violation_fee.usage_policy")
	assert.NotContains(t, publicRecorder.Body.String(), "Grok / xAI upstream")
	assert.Contains(t, publicRecorder.Body.String(), "https://developers.openai.com/api/docs/guides/moderation")

	adminRecorder := httptest.NewRecorder()
	adminContext, _ := gin.CreateTestContext(adminRecorder)
	adminContext.Request = httptest.NewRequest(http.MethodGet, "/api/security/admin/policy", nil)
	GetAdminSecurityPolicy(adminContext)
	require.Equal(t, http.StatusOK, adminRecorder.Code)
	assert.NotContains(t, adminRecorder.Body.String(), "do not publish this matcher")

	var payload struct {
		Data struct {
			Public struct {
				Enforcement struct {
					Enabled  bool   `json:"enabled"`
					OnPrompt bool   `json:"on_prompt"`
					Action   string `json:"action"`
				} `json:"enforcement"`
				Rules []struct {
					Groups   []string `json:"groups"`
					Patterns []string `json:"patterns"`
				} `json:"rules"`
			} `json:"public"`
			Rules []struct {
				Groups   []string `json:"groups"`
				Patterns []string `json:"patterns"`
			} `json:"rules"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(adminRecorder.Body.Bytes(), &payload))
	assert.False(t, payload.Data.Public.Enforcement.Enabled)
	assert.False(t, payload.Data.Public.Enforcement.OnPrompt)
	assert.Equal(t, "retired", payload.Data.Public.Enforcement.Action)
	assert.Empty(t, payload.Data.Public.Rules)
	assert.Empty(t, payload.Data.Rules)
}

func TestCanRevealSecurityEventRespectsAdministratorHierarchy(t *testing.T) {
	roles := map[int]int{
		101: common.RoleAdminUser,
		102: common.RoleRootUser,
	}

	if !canRevealSecurityEvent(7, common.RoleAdminUser, 7, roles) {
		t.Fatal("an administrator should see their own security event")
	}
	if !canRevealSecurityEvent(101, common.RoleRootUser, 7, roles) {
		t.Fatal("root should see a lower-level administrator event")
	}
	if canRevealSecurityEvent(102, common.RoleAdminUser, 7, roles) {
		t.Fatal("an administrator must not see a root event")
	}
}
