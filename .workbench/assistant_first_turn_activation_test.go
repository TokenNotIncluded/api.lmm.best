package controller

import (
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/LIghtJUNction/api.lmm.best/common"
    "github.com/LIghtJUNction/api.lmm.best/model"
    "github.com/gin-gonic/gin"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestAssistantDirectL1GrantFromFirstHTTPMessageWithoutConversationID(t *testing.T) {
    db := setupTokenControllerTestDB(t)
    require.NoError(t, db.AutoMigrate(model.RegistrationGuardMigrationModels()...))
    require.NoError(t, db.AutoMigrate(
        &model.UserOAuthBinding{}, &model.AssistantUserProfile{},
        &model.AssistantLead{}, &model.AssistantProfileBucket{}, &model.AssistantFirstQuestionStat{},
        &model.AssistantNewUserGift{}, &model.AssistantGiftRiskKey{}, &model.AssistantGiftRiskMemory{},
        &model.TopUp{}, &model.DeveloperAccessRequest{}, &model.DeveloperAccessRecommendationArchive{},
    ))
    user := model.User{Username: "first-turn-user", AffCode: "first-turn-aff", Email: "first-turn@example.test", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}
    require.NoError(t, db.Create(&user).Error)
    withAssistantSettings(t, true, "first-turn-admission-test")
    engine := gin.New()
    reached := false
    engine.POST("/api/assistant/chat", func(c *gin.Context) {
        c.Set("id", user.Id)
        c.Set("session_id", "synthetic-current-browser")
        PrepareAssistantRequest(c)
    }, func(c *gin.Context) {
        reached = true
        require.Zero(t, assistantHistoryConversationID(c))
        require.Equal(t, user.Id, assistantActorUserID(c), "Relay billing identity must not become the account being activated")
        risk := executeAssistantTool(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: "get_registration_risk", Arguments: `{}`}})
        require.Equal(t, true, risk["ok"], "%v", risk)
        grant := executeAssistantTool(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: "grant_l1_access", Arguments: `{"user_statement":"制作开源软件"}`}})
        require.Equal(t, true, grant["ok"], "%v", grant)
        require.Equal(t, "activated", grant["status"])
        require.Positive(t, assistantHistoryConversationID(c))
        writeAssistantHistoryResponse(c, http.StatusOK, []byte(`{"choices":[{"message":{"role":"assistant","content":"L1 is active."}}]}`))
    })
    request := httptest.NewRequest(http.MethodPost, "/api/assistant/chat", strings.NewReader(`{"message":"制作开源软件"}`))
    request.Header.Set("Content-Type", "application/json")
    response := httptest.NewRecorder()
    engine.ServeHTTP(response, request)
    require.True(t, reached, "HTTP prepare stopped before the tool flow: %s", response.Body.String())
    assert.Equal(t, http.StatusOK, response.Code)
    assert.Contains(t, response.Body.String(), "L1 is active.")
    var stored model.User
    require.NoError(t, db.First(&stored, user.Id).Error)
    assert.Positive(t, stored.ConsoleActivatedAt)
    var conversations, messages, grants int64
    require.NoError(t, db.Model(&model.AssistantConversation{}).Where("user_id = ?", user.Id).Count(&conversations).Error)
    require.NoError(t, db.Model(&model.AssistantHistoryMessage{}).Count(&messages).Error)
    require.NoError(t, db.Model(&model.DeveloperAccessRequest{}).Where("user_id = ?", user.Id).Count(&grants).Error)
    assert.EqualValues(t, 1, conversations)
    assert.EqualValues(t, 2, messages)
    assert.EqualValues(t, 1, grants)
}
