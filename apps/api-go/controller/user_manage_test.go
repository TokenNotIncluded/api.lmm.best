package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service/authz"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupManageUserTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.UserSession{}, &model.Log{}, &model.CasbinRule{}, &model.AuthzRole{},
		&model.TopUp{}, &model.DeveloperAccessRequest{}, &model.DeveloperAccessRecommendationArchive{}, &model.UnifiedTodoRead{},
		&model.AccountActionRequest{}, &model.L1OnboardingTodo{}, &model.AdvancedSecurityEvent{},
		&model.AssistantLead{}, &model.AssistantUserProfile{}, &model.AssistantUserProfileAudit{}, &model.AssistantMemory{},
		&model.PromptConversionRef{}, &model.PromptConversationRef{}, &model.AssistantConversation{}, &model.AssistantHistoryMessage{},
		&model.AssistantSecureCard{}, &model.AssistantSecurityIncident{},
		&model.AssistantNewUserGift{},
	))

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func performManageUserRequest(t *testing.T, body string) *httptest.ResponseRecorder {
	return performManageUserRequestAsRole(t, body, common.RoleRootUser)
}

func performManageUserRequestAsRole(t *testing.T, body string, role int) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/manage", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 9999)
	c.Set("role", role)
	c.Set("username", "root-operator")
	ManageUser(c)
	return recorder
}

func performSelfUserRequest(t *testing.T, userID int) map[string]interface{} {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
	c.Set("id", userID)
	GetSelf(c)
	var payload struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload.Data
}

func performAssistantStatusRequest(t *testing.T, userID int) map[string]interface{} {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/assistant/status", nil)
	c.Set("id", userID)
	GetAssistantStatus(c)
	var payload struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload.Data
}

func performDeveloperAccessRequest(t *testing.T, userID int) map[string]interface{} {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/developer-access/request", nil)
	c.Set("id", userID)
	GetDeveloperAccessRequest(c)
	var payload struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload.Data
}

func TestManageUserTrustLevelPersistsOverrideAndCanRestoreAutomatic(t *testing.T) {
	db := setupManageUserTestDB(t)
	user := model.User{
		Username: "managed-trust-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default",
	}
	require.NoError(t, db.Create(&user).Error)

	for _, value := range []int{0, 1, 2, 3, 4} {
		recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"set_trust_level","value":%d}`, user.Id, value))
		assert.Contains(t, recorder.Body.String(), `"success":true`, "value %d", value)

		var updated model.User
		require.NoError(t, db.First(&updated, user.Id).Error)
		require.NotNil(t, updated.TrustLevelOverride)
		assert.Equal(t, value, *updated.TrustLevelOverride)
	}

	recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"set_trust_level","value":-1}`, user.Id))
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var updated model.User
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.Nil(t, updated.TrustLevelOverride)

	for _, value := range []int{-2, 5} {
		recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"set_trust_level","value":%d}`, user.Id, value))
		assert.Contains(t, recorder.Body.String(), `"success":false`, "value %d", value)
	}
}

func TestManageUserTrustLevelRejectsAdministratorsAndPeerTargets(t *testing.T) {
	db := setupManageUserTestDB(t)
	admin := model.User{
		Username: "managed-trust-admin", Password: "password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default",
	}
	require.NoError(t, db.Create(&admin).Error)

	rootRecorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"set_trust_level","value":4}`, admin.Id))
	assert.Contains(t, rootRecorder.Body.String(), `"success":false`)

	peerRecorder := performManageUserRequestAsRole(t, fmt.Sprintf(`{"id":%d,"action":"set_trust_level","value":4}`, admin.Id), common.RoleAdminUser)
	assert.Contains(t, peerRecorder.Body.String(), `"success":false`)
}

func TestManageUserTrustLevelL0ClearsApprovedDeveloperAccessAndAllowsReapproval(t *testing.T) {
	db := setupManageUserTestDB(t)
	now := time.Now().Unix()
	user := model.User{
		Username: "managed-l0-downgrade-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 3,
	}
	require.NoError(t, db.Create(&user).Error)

	request, err := model.SubmitAssistantDeveloperAccessRecommendation(
		user.Id,
		"I need access for the managed downgrade test",
		"Recommend L1 because the user supplied a concrete integration purpose for the test.",
	)
	require.NoError(t, err)
	approved, err := model.ReviewDeveloperAccessRequest(9999, request.Id, true, "approved for the downgrade test")
	require.NoError(t, err)
	assert.Equal(t, model.DeveloperAccessRequestApproved, approved.Status)

	var activated model.User
	require.NoError(t, db.First(&activated, user.Id).Error)
	require.Positive(t, activated.ConsoleActivatedAt)
	require.NoError(t, db.Create(&model.UserSession{
		SID: "managed-l0-downgrade-session", UserID: user.Id, Version: 1, UserAuthVersion: activated.AuthVersion,
		Status: model.UserSessionStatusActive, RefreshHash: "managed-l0-refresh-hash", LoginMethod: "password",
		LastActiveAt: now, ExpiresAt: now + 3600,
	}).Error)

	recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"set_trust_level","value":0}`, user.Id))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)

	var downgraded model.User
	require.NoError(t, db.First(&downgraded, user.Id).Error)
	assert.EqualValues(t, 0, downgraded.ConsoleActivatedAt)
	require.NotNil(t, downgraded.TrustLevelOverride)
	assert.Equal(t, model.TrustLevelMinUser, *downgraded.TrustLevelOverride)
	assert.EqualValues(t, activated.AuthVersion+1, downgraded.AuthVersion)
	var session model.UserSession
	require.NoError(t, db.First(&session, "sid = ?", "managed-l0-downgrade-session").Error)
	assert.Equal(t, model.UserSessionStatusRevoked, session.Status)

	self := performSelfUserRequest(t, user.Id)
	assert.Equal(t, false, self["developer_access_granted"])
	trust, ok := self["trust_level_info"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(model.TrustLevelMinUser), trust["level"])
	assistantStatus := performAssistantStatusRequest(t, user.Id)
	assert.Equal(t, false, assistantStatus["developer_access_granted"])
	assert.Equal(t, "L0", assistantStatus["access_level"])

	latestRequest := performDeveloperAccessRequest(t, user.Id)
	assert.Equal(t, model.DeveloperAccessRequestPending, latestRequest["status"])
	assert.NotEqual(t, model.DeveloperAccessRequestApproved, latestRequest["status"])
	assert.Equal(t, approved.Id, int(latestRequest["id"].(float64)))
	var requestCount int64
	require.NoError(t, db.Model(&model.DeveloperAccessRequest{}).Where("user_id = ?", user.Id).Count(&requestCount).Error)
	assert.EqualValues(t, 1, requestCount)

	reopened, err := model.SubmitAssistantDeveloperAccessRecommendation(
		user.Id,
		"I need access again after the administrator reset the account",
		"Recommend L1 because the user supplied a concrete integration purpose for the test.",
	)
	require.NoError(t, err)
	assert.Equal(t, int(latestRequest["id"].(float64)), reopened.Id)
	approvedAgain, err := model.ReviewDeveloperAccessRequest(9999, reopened.Id, true, "approved again after the explicit L0 reset")
	require.NoError(t, err)
	assert.Equal(t, model.DeveloperAccessRequestApproved, approvedAgain.Status)

	self = performSelfUserRequest(t, user.Id)
	assert.Equal(t, true, self["developer_access_granted"])
	trust, ok = self["trust_level_info"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(model.TrustLevelMinUser+1), trust["level"])
	assistantStatus = performAssistantStatusRequest(t, user.Id)
	assert.Equal(t, true, assistantStatus["developer_access_granted"])
	assert.Equal(t, "L1", assistantStatus["access_level"])
}

func TestManageUserResetOnboardingToL0(t *testing.T) {
	db := setupManageUserTestDB(t)
	now := time.Now().Unix()
	user := model.User{
		Username: "managed-reset-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", ConsoleActivatedAt: now - 3600,
		AuthVersion: 4,
	}
	require.NoError(t, db.Create(&user).Error)
	// A completed external payment is a durable L1 fact. The explicit L0
	// override must take precedence over it until a later administrator action
	// deliberately clears the reset.
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: "managed-reset-paid", Amount: 1,
		CreditedQuota: int64(common.QuotaPerUnit), Money: 1,
		Status: common.TopUpStatusSuccess, PaymentProvider: model.PaymentProviderStripe,
		CompleteTime: now,
	}).Error)
	approvedRequest, err := model.SubmitAssistantDeveloperAccessRecommendation(
		user.Id,
		"I need access for the managed reset test",
		"Recommend L1 because the user supplied a concrete integration purpose for the test.",
	)
	require.NoError(t, err)
	approvedRequest, err = model.ReviewDeveloperAccessRequest(9999, approvedRequest.Id, true, "approved before the managed reset")
	require.NoError(t, err)
	assert.Equal(t, model.DeveloperAccessRequestApproved, approvedRequest.Status)
	require.NoError(t, db.Create(&model.UserSession{
		SID: "managed-reset-session", UserID: user.Id, Version: 4, UserAuthVersion: 4,
		Status: model.UserSessionStatusActive, RefreshHash: "reset-refresh-hash", LoginMethod: "password",
		LastActiveAt: now, ExpiresAt: now + 3600,
	}).Error)

	recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"reset_onboarding"}`, user.Id))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)

	var updated model.User
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.EqualValues(t, 0, updated.ConsoleActivatedAt)
	require.NotNil(t, updated.TrustLevelOverride)
	assert.Equal(t, 0, *updated.TrustLevelOverride)
	assert.EqualValues(t, 5, updated.AuthVersion)
	access, err := model.GetDeveloperAccessStateForUser(&updated)
	require.NoError(t, err)
	assert.False(t, access.Granted)
	latestRequest, err := model.GetDeveloperAccessRequest(user.Id)
	require.NoError(t, err)
	require.NotNil(t, latestRequest)
	assert.Equal(t, model.DeveloperAccessRequestPending, latestRequest.Status)
	assert.Equal(t, approvedRequest.Id, latestRequest.Id)
	var requestCount int64
	require.NoError(t, db.Model(&model.DeveloperAccessRequest{}).Where("user_id = ?", user.Id).Count(&requestCount).Error)
	assert.EqualValues(t, 1, requestCount)
	trust, err := model.GetTrustLevelInfoForUser(&updated)
	require.NoError(t, err)
	assert.Equal(t, model.TrustLevelMinUser, trust.Level)

	var session model.UserSession
	require.NoError(t, db.First(&session, "sid = ?", "managed-reset-session").Error)
	assert.Equal(t, model.UserSessionStatusRevoked, session.Status)

	// The reset is a temporary, administrator-controlled floor rather than a
	// permanent lock. A fresh AI recommendation approved by an administrator
	// must clear the override and restore L1 access.
	request, err := model.SubmitAssistantDeveloperAccessRecommendation(
		user.Id,
		"I need access for the managed integration test",
		"Recommend L1 because the user supplied a concrete integration purpose for the test.",
	)
	require.NoError(t, err)
	_, err = model.ReviewDeveloperAccessRequest(9999, request.Id, true, "approved for the managed test")
	require.NoError(t, err)
	var upgraded model.User
	require.NoError(t, db.First(&upgraded, user.Id).Error)
	assert.Nil(t, upgraded.TrustLevelOverride)
	assert.Positive(t, upgraded.ConsoleActivatedAt)
	access, err = model.GetDeveloperAccessStateForUser(&upgraded)
	require.NoError(t, err)
	assert.True(t, access.Granted)
}

func TestManageUserDisableAdvancesAuthVersionOnceAndRevokesSession(t *testing.T) {
	db := setupManageUserTestDB(t)
	now := time.Now().Unix()
	user := model.User{
		Username: "managed-disable-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.UserSession{
		SID: "managed-disable-session", UserID: user.Id, Version: 1, UserAuthVersion: 1,
		Status: model.UserSessionStatusActive, RefreshHash: "refresh-hash", LoginMethod: "password",
		LastActiveAt: now, ExpiresAt: now + 3600,
	}).Error)

	recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"disable"}`, user.Id))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)

	var updated model.User
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.Equal(t, common.UserStatusDisabled, updated.Status)
	assert.EqualValues(t, 2, updated.AuthVersion)
	var session model.UserSession
	require.NoError(t, db.First(&session, "sid = ?", "managed-disable-session").Error)
	assert.Equal(t, model.UserSessionStatusRevoked, session.Status)
}

func TestManageUserDemoteAdvancesAuthVersionAndRevokesSessionsOnce(t *testing.T) {
	db := setupManageUserTestDB(t)
	previousMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	require.NoError(t, authz.Init(db))

	now := time.Now().Unix()
	user := model.User{
		Username: "managed-demote-user", Password: "password", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1,
	}
	require.NoError(t, db.Create(&user).Error)
	for _, sid := range []string{"managed-demote-session-one", "managed-demote-session-two"} {
		require.NoError(t, db.Create(&model.UserSession{
			SID: sid, UserID: user.Id, Version: 1, UserAuthVersion: 1,
			Status: model.UserSessionStatusActive, RefreshHash: "refresh-" + sid, LoginMethod: "password",
			LastActiveAt: now, ExpiresAt: now + 3600,
		}).Error)
	}

	sessionUpdateCount := 0
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:count_demote_session_updates", func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "user_sessions" {
			sessionUpdateCount++
		}
	}))

	recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"demote"}`, user.Id))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)

	var updated model.User
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.Equal(t, common.RoleCommonUser, updated.Role)
	assert.EqualValues(t, 2, updated.AuthVersion)
	var sessions []model.UserSession
	require.NoError(t, db.Where("user_id = ?", user.Id).Order("sid asc").Find(&sessions).Error)
	require.Len(t, sessions, 2)
	for _, session := range sessions {
		assert.Equal(t, model.UserSessionStatusRevoked, session.Status)
		assert.Equal(t, "admin_demote", session.RevokedReason)
	}
	assert.Equal(t, 1, sessionUpdateCount)
}

func TestManageUserQuotaActionsEnforceSymmetricJavaScriptSafeBounds(t *testing.T) {
	db := setupManageUserTestDB(t)
	user := model.User{
		Username: "managed-wallet-boundary", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: common.MaxWalletQuota - 1,
	}
	require.NoError(t, db.Create(&user).Error)

	recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"add","value":1}`, user.Id))
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, common.MaxWalletQuota, user.Quota)

	recorder = performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"add","value":1}`, user.Id))
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, common.MaxWalletQuota, user.Quota)

	recorder = performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"override","value":%d}`, user.Id, common.MinWalletQuota))
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, common.MinWalletQuota, user.Quota)

	recorder = performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"subtract","value":1}`, user.Id))
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, common.MinWalletQuota, user.Quota)

	for _, value := range []int{common.MaxWalletQuota + 1, common.MinWalletQuota - 1} {
		recorder = performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"override","value":%d}`, user.Id, value))
		assert.Contains(t, recorder.Body.String(), `"success":false`, value)
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, common.MinWalletQuota, user.Quota)
	}
}

func TestManageUserDeleteReturnsImmediatelyAndUnknownActionFails(t *testing.T) {
	db := setupManageUserTestDB(t)
	deleted := model.User{
		Username: "managed-delete-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "delete-aff",
	}
	require.NoError(t, db.Create(&deleted).Error)

	recorder := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"delete"}`, deleted.Id))
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var deletedCount int64
	require.NoError(t, db.Unscoped().Model(&model.User{}).Where("id = ? AND deleted_at IS NOT NULL", deleted.Id).Count(&deletedCount).Error)
	assert.EqualValues(t, 1, deletedCount)

	unchanged := model.User{
		Username: "managed-unknown-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "unknown-aff",
	}
	require.NoError(t, db.Create(&unchanged).Error)
	recorder = performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"unknown"}`, unchanged.Id))
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	require.NoError(t, db.First(&unchanged, unchanged.Id).Error)
	assert.EqualValues(t, 1, unchanged.AuthVersion)
	assert.Equal(t, common.UserStatusEnabled, unchanged.Status)
}
