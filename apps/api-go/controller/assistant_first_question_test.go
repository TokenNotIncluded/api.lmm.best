package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminGetAssistantFirstQuestionSummary(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantFirstQuestionStat{}))
	require.NoError(t, model.RecordAssistantFirstQuestion("  How   do I use the API? email: alice@example.com api_key=sk_live_secret123  ")) // gitleaks:allow
	require.NoError(t, model.RecordAssistantFirstQuestion("how do I use the api? email: bob@example.com api_key=sk_live_other456"))          // gitleaks:allow

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/assistant/admin/first-questions?days=30", nil)
	AdminGetAssistantFirstQuestionSummary(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool                                  `json:"success"`
		Data    []model.AssistantFirstQuestionSummary `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	require.Len(t, response.Data, 1)
	assert.EqualValues(t, 2, response.Data[0].Count)
	assert.Positive(t, response.Data[0].LastAskedAt)
	assert.NotContains(t, response.Data[0].Question, "alice@example.com")
	assert.NotContains(t, response.Data[0].Question, "sk_live_secret123")
}
