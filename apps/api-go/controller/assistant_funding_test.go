package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminGetAssistantFundingSummaryUsesAssistantSpendAndRootBalance(t *testing.T) {
	setupAssistantCurrencyTest(t)
	db := setupManageUserTestDB(t)
	root := &model.User{
		Username: "assistant-funding-root",
		Password: "password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Quota:    3500000,
		Group:    "default",
	}
	require.NoError(t, db.Create(root).Error)

	now := time.Now().Unix()
	require.NoError(t, db.Create(&[]model.Log{
		{UserId: root.Id, CreatedAt: now, Type: model.LogTypeConsume, PromptTokens: 10, CompletionTokens: 5, Quota: 3500000, Other: `{"billing_source":"assistant"}`},
		{UserId: root.Id, CreatedAt: now, Type: model.LogTypeConsume, PromptTokens: 20, CompletionTokens: 10, Quota: 200, Other: `{"billing_source":"wallet"}`},
	}).Error)

	originalLoader := loadAssistantBillingUser
	loadAssistantBillingUser = func() (*model.User, error) {
		return model.GetUserById(root.Id, false)
	}
	t.Cleanup(func() { loadAssistantBillingUser = originalLoader })

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/assistant/admin/funding?days=30", nil)
	AdminGetAssistantFundingSummary(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Requests       int64   `json:"requests"`
			Quota          int64   `json:"quota"`
			CostUSD        float64 `json:"cost_usd"`
			RemainingQuota int     `json:"remaining_quota"`
			RemainingUSD   float64 `json:"remaining_usd"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Equal(t, int64(1), response.Data.Requests)
	assert.Equal(t, int64(3500000), response.Data.Quota)
	assert.Equal(t, float64(7), response.Data.CostUSD)
	assert.Equal(t, 3500000, response.Data.RemainingQuota)
	assert.InDelta(t, 7, response.Data.RemainingUSD, 0.0000001)
}
