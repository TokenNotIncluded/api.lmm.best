package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAssistantReviewCleanupHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupUserOnboardingTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SystemTask{}, &model.Log{}))

	previousLogDB := model.LOG_DB
	previousSecret := common.SessionSecret
	previousRedis := common.RedisEnabled
	model.LOG_DB = db
	common.SessionSecret = "assistant-review-cleanup-proof-secret"
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.LOG_DB = previousLogDB
		common.SessionSecret = previousSecret
		common.RedisEnabled = previousRedis
	})

	admin := &model.User{
		Username:    "assistant-review-cleanup-admin",
		Password:    "password-placeholder",
		Email:       "admin@example.com",
		Role:        common.RoleAdminUser,
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}
	require.NoError(t, db.Create(admin).Error)
	for index := 0; index < 35; index++ {
		taskID, err := model.GenerateSystemTaskID()
		require.NoError(t, err)
		require.NoError(t, db.Create(&model.SystemTask{
			TaskID: taskID,
			Type:   model.SystemTaskTypeAssistantReview,
			Status: model.SystemTaskStatusSucceeded,
		}).Error)
	}
	activeID, err := model.GenerateSystemTaskID()
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.SystemTask{
		TaskID: activeID,
		Type:   model.SystemTaskTypeAssistantReview,
		Status: model.SystemTaskStatusRunning,
	}).Error)
	otherID, err := model.GenerateSystemTaskID()
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.SystemTask{
		TaskID: otherID,
		Type:   model.SystemTaskTypeLogCleanup,
		Status: model.SystemTaskStatusSucceeded,
	}).Error)

	identity := service.AuthIdentity{
		UserID: admin.Id, SessionID: "assistant-review-cleanup-session",
		UserAuthVersion: 1, SessionVersion: 1,
	}

	t.Run("preview", func(t *testing.T) {
		context, response := assistantReviewCleanupTestContext(identity, http.MethodGet, "/api/security/admin/review-runs/cleanup-preview?keep=30", "")
		PreviewAdminAssistantReviewTaskCleanup(context)

		assert.Equal(t, http.StatusOK, response.Code)
		var body struct {
			Success bool                           `json:"success"`
			Data    assistantReviewCleanupResponse `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
		assert.True(t, body.Success)
		assert.Equal(t, model.SystemTaskTypeAssistantReview, body.Data.TaskType)
		assert.Equal(t, 30, body.Data.Keep)
		assert.EqualValues(t, 5, body.Data.EligibleCount)
		assert.Zero(t, body.Data.DeletedCount)
	})

	t.Run("invalid keep", func(t *testing.T) {
		context, response := assistantReviewCleanupTestContext(identity, http.MethodGet, "/api/security/admin/review-runs/cleanup-preview?keep=0", "")
		PreviewAdminAssistantReviewTaskCleanup(context)
		assert.Equal(t, http.StatusBadRequest, response.Code)
	})

	t.Run("missing proof", func(t *testing.T) {
		context, response := assistantReviewCleanupTestContext(identity, http.MethodDelete, "/api/security/admin/review-runs?keep=30", "")
		DeleteAdminAssistantReviewTasks(context)
		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Contains(t, response.Body.String(), "SECURITY_PROOF_REQUIRED")
	})

	wrongProof, _, err := service.IssueSecurityProof(identity, secureVerificationMethodEmail, []string{securityProofScopeChannelKeyRead})
	require.NoError(t, err)
	t.Run("wrong scope proof", func(t *testing.T) {
		context, response := assistantReviewCleanupTestContext(identity, http.MethodDelete, "/api/security/admin/review-runs?keep=30", wrongProof)
		DeleteAdminAssistantReviewTasks(context)
		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Contains(t, response.Body.String(), "SECURITY_PROOF_SCOPE_MISMATCH")
	})

	proof, _, err := service.IssueSecurityProof(identity, secureVerificationMethodEmail, []string{securityProofScopeReviewRunsDelete})
	require.NoError(t, err)
	t.Run("missing expected count", func(t *testing.T) {
		context, response := assistantReviewCleanupTestContext(identity, http.MethodDelete, "/api/security/admin/review-runs?keep=30", proof)
		DeleteAdminAssistantReviewTasks(context)
		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Contains(t, response.Body.String(), "expected_count")
	})

	t.Run("stale preview does not delete", func(t *testing.T) {
		context, response := assistantReviewCleanupTestContext(identity, http.MethodDelete, "/api/security/admin/review-runs?keep=30&expected_count=4", proof)
		DeleteAdminAssistantReviewTasks(context)
		assert.Equal(t, http.StatusConflict, response.Code)
		assert.Contains(t, response.Body.String(), "STALE_PREVIEW")
		assert.EqualValues(t, 35, assistantReviewTerminalTaskCount(t, db))
	})

	t.Run("audit failure rolls back deletion", func(t *testing.T) {
		require.NoError(t, db.Migrator().DropTable(&model.Log{}))
		context, response := assistantReviewCleanupTestContext(identity, http.MethodDelete, "/api/security/admin/review-runs?keep=30&expected_count=5", proof)
		DeleteAdminAssistantReviewTasks(context)
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"success":false`)
		assert.EqualValues(t, 35, assistantReviewTerminalTaskCount(t, db))
		require.NoError(t, db.AutoMigrate(&model.Log{}))
	})

	t.Run("successful cleanup", func(t *testing.T) {
		context, response := assistantReviewCleanupTestContext(identity, http.MethodDelete, "/api/security/admin/review-runs?keep=30&expected_count=5", proof)
		DeleteAdminAssistantReviewTasks(context)

		assert.Equal(t, http.StatusOK, response.Code)
		var body struct {
			Success bool                           `json:"success"`
			Data    assistantReviewCleanupResponse `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
		assert.True(t, body.Success)
		assert.EqualValues(t, 5, body.Data.EligibleCount)
		assert.EqualValues(t, 5, body.Data.DeletedCount)
		assert.EqualValues(t, 30, assistantReviewTerminalTaskCount(t, db))

		var audit model.Log
		require.NoError(t, db.Where("user_id = ? AND type = ?", admin.Id, model.LogTypeSystem).First(&audit).Error)
		assert.Equal(t, admin.Username, audit.Username)
		assert.Contains(t, audit.Content, "deleted 5 assistant review run history records")
		for _, taskID := range []string{activeID, otherID} {
			task, err := model.GetSystemTaskByTaskID(taskID)
			require.NoError(t, err)
			require.NotNil(t, task)
		}
	})

	t.Run("zero expected count is supported", func(t *testing.T) {
		context, response := assistantReviewCleanupTestContext(identity, http.MethodDelete, "/api/security/admin/review-runs?keep=30&expected_count=0", proof)
		DeleteAdminAssistantReviewTasks(context)
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"deleted_count":0`)
		assert.EqualValues(t, 30, assistantReviewTerminalTaskCount(t, db))
	})
}

func assistantReviewTerminalTaskCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&model.SystemTask{}).
		Where("type = ? AND status IN ?", model.SystemTaskTypeAssistantReview, []model.SystemTaskStatus{model.SystemTaskStatusSucceeded, model.SystemTaskStatusFailed}).
		Count(&count).Error)
	return count
}

func assistantReviewCleanupTestContext(identity service.AuthIdentity, method, target, proof string) (*gin.Context, *httptest.ResponseRecorder) {
	request := httptest.NewRequest(method, target, strings.NewReader(""))
	if proof != "" {
		request.Header.Set("X-Security-Proof", proof)
	}
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = request
	context.Set("id", identity.UserID)
	context.Set("session_id", identity.SessionID)
	context.Set("auth_version", identity.UserAuthVersion)
	context.Set("session_version", identity.SessionVersion)
	return context, response
}

func TestAssistantReviewCleanupScopeIsAllowed(t *testing.T) {
	assert.True(t, isAllowedSecurityProofScope(securityProofScopeReviewRunsDelete))
}
