package controller

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantDirectL1GrantToolRequiresEligibleL0Context(t *testing.T) {
	toolNames := func(context assistantUserContext) map[string]bool {
		tools := assistantToolDefinitionsForContext(context)
		names := make(map[string]bool, len(tools))
		for _, tool := range tools {
			names[tool.Function.Name] = true
		}
		return names
	}
	belowThreshold := assistantUserContext{AccessLevel: "L0", CompletedAssistantTurns: 2}
	assert.False(t, toolNames(belowThreshold)["grant_l1_access"])
	eligible := assistantUserContext{AccessLevel: "L0", CompletedAssistantTurns: 3}
	assert.True(t, toolNames(eligible)["grant_l1_access"])
	eligible.DeveloperAccessGranted = true
	assert.False(t, toolNames(eligible)["grant_l1_access"])
	eligible.DeveloperAccessGranted = false
	eligible.AdministratorMode = true
	assert.False(t, toolNames(eligible)["grant_l1_access"])
}

func TestExecuteAssistantDirectL1GrantToolActivatesWithoutConfirmation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(model.RegistrationGuardMigrationModels()...))
	require.NoError(t, db.AutoMigrate(&model.AssistantNewUserGift{}, &model.AssistantGiftRiskKey{}, &model.AssistantGiftRiskMemory{}))
	require.NoError(t, db.AutoMigrate(
		&model.TopUp{}, &model.DeveloperAccessRequest{}, &model.DeveloperAccessRecommendationArchive{},
		&model.AssistantConversation{}, &model.AssistantHistoryMessage{}, &model.AssistantSupportRequest{},
	))
	user := model.User{Email: "direct@example.test", Username: "assistant-direct-controller", AffCode: "assistant-direct-controller-aff", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, model.ObserveAssistantRegistration(user.Id, "198.51.100.10", ""))
	conversation, err := model.PrepareAssistantConversation(user.Id, 0, "first")
	require.NoError(t, err)
	for range model.AssistantDirectGrantMinCompletedTurns - 1 {
		require.NoError(t, model.RecordAssistantConversationTurn(user.Id, conversation.Id, "concrete coding use", "assistant response"))
	}

	c, _ := gin.CreateTestContext(nil)
	c.Set(assistantActorUserIDKey, user.Id)
	c.Set("assistant_history_conversation_id", conversation.Id)
	c.Set(assistantUserContextKey, assistantUserContext{
		UserID: user.Id, AccessLevel: "L0", CompletedAssistantTurns: model.AssistantDirectGrantMinCompletedTurns,
	})
	call := assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{
		Name:      "grant_l1_access",
		Arguments: `{"user_statement":"I will use LMM for a concrete coding workflow.","recommendation":"The user described a legitimate coding workflow over three assistant turns."}`,
	}}
	result := executeAssistantTool(c, call)
	assert.Equal(t, false, result["ok"])
	assert.Equal(t, "turns_required", result["status"], "request context must not substitute for durable conversation turns")

	require.NoError(t, model.RecordAssistantConversationTurn(user.Id, conversation.Id, "third concrete coding question", "third assistant response"))
	result = executeAssistantTool(c, call)
	assert.Equal(t, true, result["ok"])
	assert.Equal(t, "activated", result["status"])
	assert.Equal(t, "L1", result["access_level"])
	_, hasConfirmation := result["confirmation_token"]
	assert.False(t, hasConfirmation)

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Positive(t, stored.ConsoleActivatedAt)
}
