package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
