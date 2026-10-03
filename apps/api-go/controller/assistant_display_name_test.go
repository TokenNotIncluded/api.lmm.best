package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nicknameTestUser(t *testing.T, name string, role int) (*model.User, *gin.Context, model.UserSession) {
	t.Helper()
	user := &model.User{Username: name, AffCode: name + "-aff", DisplayName: "旧昵称", Password: "original-password", Role: role, Status: common.UserStatusEnabled, Group: "default", Quota: 42, Setting: `{"language":"zh"}`}
	require.NoError(t, model.DB.Create(user).Error)
	c, session := assistantTargetTestSession(t, user)
	return user, c, session
}

func nicknameTestAction(t *testing.T, c *gin.Context, userID int, input map[string]any) map[string]any {
	t.Helper()
	result := executeAssistantPrepareUserActionTool(c, userID, input)
	require.Equal(t, true, result["ok"], result)
	require.Equal(t, "confirmation_required", result["status"])
	action, exists := c.Get(assistantClientActionKey)
	require.True(t, exists)
	return action.(map[string]any)
}

func confirmNicknameRequest(t *testing.T, identity service.AuthIdentity, body map[string]any, pat bool) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/assistant/profile/display-name", strings.NewReader(string(data)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", identity.UserID)
	c.Set("auth_identity", identity)
	c.Set("use_access_token", pat)
	ConfirmAssistantDisplayName(c)
	return recorder
}

func TestAssistantDisplayNamePreparesForL0WithoutChangingAccount(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	user, c, _ := nicknameTestUser(t, "nickname-l0", common.RoleCommonUser)
	assert.True(t, assistantToolAllowedForContext("prepare_user_action", assistantUserContext{UserID: user.Id, AccessLevel: "L0"}))
	// The relay's billing ID must never become the profile owner.
	c.Set("id", 9999)
	action := nicknameTestAction(t, c, user.Id, map[string]any{"action": "change_display_name", "display_name": " 小明 "})
	assert.Equal(t, "user_display_name_change", action["type"])
	assert.Equal(t, true, action["target_is_self"])
	assert.Equal(t, user.Id, action["target_user_id"])
	assert.Equal(t, "小明", action["proposed_display_name"])
	assert.NotEmpty(t, action["confirmation_token"])
	loaded, err := model.GetUserById(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "旧昵称", loaded.DisplayName)
	assert.Zero(t, loaded.ConsoleActivatedAt)

	// A new nickname is optional until the user edits the confirmation form.
	action = nicknameTestAction(t, c, user.Id, map[string]any{"action": "change_display_name"})
	assert.Equal(t, "旧昵称", action["proposed_display_name"])
	require.NoError(t, db.Model(user).Update("display_name", "legacy\nname").Error)
	action = nicknameTestAction(t, c, user.Id, map[string]any{"action": "change_display_name"})
	assert.Equal(t, "", action["proposed_display_name"], "legacy invalid names must not prevent opening the editable form")
}

func TestAssistantDisplayNameToolRejectsOtherOwnersAndInvalidInput(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	admin, c, session := nicknameTestUser(t, "nickname-admin", common.RoleRootUser)
	target, _, _ := nicknameTestUser(t, "nickname-other", common.RoleCommonUser)
	for _, input := range []map[string]any{
		{"action": "change_display_name", "user_id": float64(target.Id)},
		{"action": "change_display_name", "identifier": target.Username},
	} {
		result := executeAssistantPrepareUserActionTool(c, admin.Id, input)
		assert.Equal(t, false, result["ok"])
		assert.Equal(t, "target_forbidden", result["status"])
	}
	for _, name := range []any{"", "   ", strings.Repeat("字", 21), "a\nb", 42} {
		result := executeAssistantPrepareUserActionTool(c, admin.Id, map[string]any{"action": "change_display_name", "display_name": name})
		assert.Equal(t, "display_name_invalid", result["status"], name)
	}
	c.Set("use_access_token", true)
	assert.Equal(t, "session_required", executeAssistantPrepareUserActionTool(c, admin.Id, map[string]any{"action": "change_display_name"})["status"])
	c.Set("use_access_token", false)
	require.NoError(t, db.Model(&session).Update("status", model.UserSessionStatusRevoked).Error)
	assert.Equal(t, "session_required", executeAssistantPrepareUserActionTool(c, admin.Id, map[string]any{"action": "change_display_name"})["status"])
}

func TestAssistantDisplayNameConfirmationRequiresOwnerSessionAndOneUse(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	user, c, session := nicknameTestUser(t, "nickname-owner", common.RoleCommonUser)
	other, otherContext, _ := nicknameTestUser(t, "nickname-foreign", common.RoleRootUser)
	identity := c.MustGet("auth_identity").(service.AuthIdentity)
	otherIdentity := otherContext.MustGet("auth_identity").(service.AuthIdentity)
	action := nicknameTestAction(t, c, user.Id, map[string]any{"action": "change_display_name", "display_name": "草稿昵称"})
	body := map[string]any{"display_name": "确认昵称", "confirmation_token": action["confirmation_token"], "confirmed": true}

	assert.Equal(t, http.StatusForbidden, confirmNicknameRequest(t, identity, body, true).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, confirmNicknameRequest(t, otherIdentity, body, false).Code)
	wrongSession := identity
	wrongSession.SessionID = "another-session"
	assert.Equal(t, http.StatusUnprocessableEntity, confirmNicknameRequest(t, wrongSession, body, false).Code)
	body["confirmed"] = false
	assert.Equal(t, http.StatusUnprocessableEntity, confirmNicknameRequest(t, identity, body, false).Code)
	body["confirmed"] = true
	body["display_name"] = strings.Repeat("字", 21)
	assert.Equal(t, http.StatusUnprocessableEntity, confirmNicknameRequest(t, identity, body, false).Code)
	body["display_name"] = "确认昵称"
	body["target_user_id"] = other.Id
	assert.Equal(t, http.StatusBadRequest, confirmNicknameRequest(t, identity, body, false).Code)
	delete(body, "target_user_id")
	before, err := model.GetUserById(user.Id, false)
	require.NoError(t, err)
	response := confirmNicknameRequest(t, identity, body, false)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"display_name":"确认昵称"`)
	after, err := model.GetUserById(user.Id, false)
	require.NoError(t, err)
	before.DisplayName = "确认昵称"
	assert.Equal(t, before, after, "only display_name is allowed to change")
	assert.Equal(t, http.StatusUnprocessableEntity, confirmNicknameRequest(t, identity, body, false).Code)
	unchanged, err := model.GetUserById(other.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "旧昵称", unchanged.DisplayName)
	refreshedSession, err := model.GetUserSessionBySID(session.SID)
	require.NoError(t, err)
	assert.Equal(t, session.Version, refreshedSession.Version)
	assert.Equal(t, model.UserSessionStatusActive, refreshedSession.Status)
}

func TestAssistantDisplayNameConfirmationRechecksLiveAuthority(t *testing.T) {
	for _, mutation := range []string{"revoked", "expired", "password_changed", "disabled"} {
		t.Run(mutation, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
			user, c, session := nicknameTestUser(t, "nickname-live", common.RoleCommonUser)
			action := nicknameTestAction(t, c, user.Id, map[string]any{"action": "change_display_name"})
			identity := c.MustGet("auth_identity").(service.AuthIdentity)
			switch mutation {
			case "revoked":
				require.NoError(t, db.Model(&session).Update("status", model.UserSessionStatusRevoked).Error)
			case "expired":
				require.NoError(t, db.Model(&session).Update("expires_at", time.Now().Add(-time.Second).Unix()).Error)
			case "password_changed":
				require.NoError(t, db.Model(user).Update("auth_version", user.AuthVersion+1).Error)
			case "disabled":
				require.NoError(t, db.Model(user).Update("status", common.UserStatusDisabled).Error)
			}
			response := confirmNicknameRequest(t, identity, map[string]any{"display_name": "新昵称", "confirmation_token": action["confirmation_token"], "confirmed": true}, false)
			assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
			loaded, err := model.GetUserById(user.Id, false)
			require.NoError(t, err)
			assert.Equal(t, "旧昵称", loaded.DisplayName)
			flow, err := model.GetAuthFlow(action["confirmation_token"].(string), model.AuthFlowMatch{Purpose: model.AuthFlowPurposeAssistantDisplayName})
			require.NoError(t, err)
			assert.Nil(t, flow.ConsumedAt, "denied changes must not consume the draft")
		})
	}
}
