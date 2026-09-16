package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegistrationGuardRejectsForgedTargetsBeforeDatabaseAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, name := range []string{"get_registration_risk", "ban_l0_user", "end_registration_conversation", "notify_registration_risk"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("session_id", "signed-browser")
		result := executeAssistantRegistrationTool(c, name, map[string]any{"user_id": 999, "confidence": 1})
		require.Equal(t, false, result["ok"])
		require.Equal(t, "registration_action_denied", result["status"])
		c.Set("use_access_token", true)
		result = executeAssistantRegistrationTool(c, name, nil)
		require.Equal(t, false, result["ok"])
	}
}
func TestRegistrationGuardToolCatalogUsesSignedInActorScope(t *testing.T) {
	for _, context := range []assistantUserContext{
		{AccessLevel: "L0"}, {AccessLevel: "L1", DeveloperAccessGranted: true}, {AccessLevel: "ROOT", AdministratorMode: true},
	} {
		tools := assistantToolDefinitionsForContext(context)
		names := map[string]bool{}
		for _, tool := range tools {
			names[tool.Function.Name] = true
		}
		require.Equal(t, context.AccessLevel == "L0", names["ban_l0_user"])
		require.False(t, names["prepare_l1_recommendation"])
		require.False(t, names[assistantInterlocutorAssessmentTool])
	}
}
func TestRegistrationGuardRetiresPublicRecommendationSubmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	RetiredDeveloperAccessRequest(c)
	require.Equal(t, http.StatusGone, w.Code)
	require.Contains(t, w.Body.String(), "DEVELOPER_ACCESS_LETTER_RETIRED")
}
func TestRegistrationGuardTerminationReturnsRestrictedReceipt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	require.False(t, finishAssistantRegistrationTermination(c))
	c.Set(assistantRegistrationTerminatedKey, true)
	c.Set("assistant_conversation_restricted", true)
	require.True(t, finishAssistantRegistrationTermination(c))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "registration verification")
}
