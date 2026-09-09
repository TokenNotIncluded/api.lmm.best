package controller

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAssistantAdminPermissionTest(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
	previous := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previous })
	require.NoError(t, authz.Init(db))
}

func assistantAutomationTestContext(t *testing.T, db *gorm.DB, role int) (*gin.Context, model.User, model.UserSession) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Option{}, &model.Log{}))
	user := model.User{Username: "automation-actor", Password: "password", Role: role, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	session := model.UserSession{SID: "automation-session", UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, db.Create(&session).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/assistant/chat", nil)
	c.Set("id", user.Id)
	c.Set("role", role)
	c.Set("session_id", session.SID)
	c.Set("auth_version", int64(1))
	c.Set("session_version", int64(1))
	c.Set("auth_identity", service.AuthIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1})
	c.Set(assistantAdminAutomationContextKey, true)
	return c, user, session
}

func TestAssistantAdminAutomationRechecksActorSessionAndRole(t *testing.T) {
	for _, scenario := range []string{"demoted", "disabled", "revoked", "version_changed", "security_reset", "expired", "old_session", "foreign_session", "billing_identity", "personal_access_token"} {
		t.Run(scenario, func(t *testing.T) {
			db := setupTokenControllerTestDB(t)
			c, user, session := assistantAutomationTestContext(t, db, common.RoleRootUser)
			require.NotNil(t, user.ToBaseUser())
			_, err := model.GetUserCache(user.Id)
			require.NoError(t, err)
			switch scenario {
			case "demoted":
				require.NoError(t, db.Model(&user).Update("role", common.RoleCommonUser).Error)
			case "disabled":
				require.NoError(t, db.Model(&user).Update("status", common.UserStatusDisabled).Error)
			case "revoked":
				require.NoError(t, db.Model(&session).Update("status", model.UserSessionStatusRevoked).Error)
			case "version_changed":
				require.NoError(t, db.Model(&session).Update("version", 2).Error)
			case "security_reset":
				require.NoError(t, db.Model(&user).Update("auth_version", 2).Error)
			case "expired":
				require.NoError(t, db.Model(&session).Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error)
			case "old_session":
				require.NoError(t, db.Model(&session).Update("created_at", time.Now().Add(-8*24*time.Hour).Unix()).Error)
			case "foreign_session":
				require.NoError(t, db.Model(&session).Update("user_id", user.Id+1).Error)
			case "billing_identity":
				c.Set(assistantActorUserIDKey, user.Id+1)
			case "personal_access_token":
				c.Set("use_access_token", true)
			}
			result, handled := maybeApplyAssistantAdminAutomatically(c, user.Id, assistantAdminChangePayload{Kind: assistantAdminConfigChangeKind, ConfigChanges: map[string]string{"SystemName": "must-not-apply"}, ConfigExpected: map[string]string{"SystemName": common.SystemName}})
			require.True(t, handled)
			assert.Equal(t, false, result["ok"])
			var count int64
			require.NoError(t, db.Model(&model.Option{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestAssistantAdminAutomationUsesValidatedPayloadWithoutConfirmationToken(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	c, user, _ := assistantAutomationTestContext(t, db, common.RoleRootUser)
	previousName := common.SystemName
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{"SystemName": previousName}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.SystemName = previousName
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	result := executeAssistantAdminConfigChangeTool(c, user.Id, map[string]any{"changes": map[string]any{"SystemName": "automatic-test"}})
	require.Equal(t, true, result["ok"], "%v", result)
	assert.Equal(t, "applied", result["status"])
	assert.Equal(t, "automatic-test", common.SystemName)
	_, hasCard := c.Get(assistantClientActionKey)
	assert.False(t, hasCard)
	var logs []model.Log
	require.NoError(t, db.Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, user.Id, logs[0].UserId)
	assert.Contains(t, logs[0].Other, "assistant.admin_config_apply.automatic")
	assert.NotContains(t, logs[0].Other, "automatic-test")
}

func TestAssistantAdminAutomationNeverUsesRelayBillingAccountAuthority(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	c, actor, _ := assistantAutomationTestContext(t, db, common.RoleAdminUser)
	c.Set(assistantActorUserIDKey, actor.Id)
	c.Set("id", actor.Id+100)
	c.Set("role", common.RoleRootUser)
	result, handled := maybeApplyAssistantAdminAutomatically(c, actor.Id, assistantAdminChangePayload{Kind: assistantAdminConfigChangeKind})
	require.True(t, handled)
	assert.Equal(t, "forbidden", result["status"])
}

func TestAssistantAdminChannelToolsRespectPermissionOverrides(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	c, actor, _ := assistantAutomationTestContext(t, db, common.RoleAdminUser)
	setupAssistantAdminPermissionTest(t, db)
	require.NoError(t, authz.SetUserPermissions(actor.Id, authz.PermissionsMap{"channel": {"read": false, "write": false, "operate": true}}))
	assert.Equal(t, false, executeAssistantAdminChannelsTool(actor.Id)["ok"])
	assert.Error(t, assistantAdminChannelPermission(actor.Id, map[string]string{"models": "example-model"}))
	assert.NoError(t, assistantAdminChannelPermission(actor.Id, map[string]string{"status": "2"}))
	_, err := applyAssistantAdminChange(c, assistantAdminChangePayload{Kind: assistantAdminChannelChangeKind, Channel: &assistantAdminChannelChange{ChannelID: 1, Changes: map[string]string{"models": "example-model"}}})
	assert.Error(t, err)
}
