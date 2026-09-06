package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoginSessionSettingsRequireBrowserAndBoolean(t *testing.T) {
	for _, test := range []struct {
		body    string
		browser bool
		status  int
	}{
		{`{"session_auto_logout":false}`, false, http.StatusForbidden},
		{`{}`, true, http.StatusBadRequest},
		{`{"session_auto_logout":null}`, true, http.StatusBadRequest},
		{`{"session_auto_logout":"false"}`, true, http.StatusBadRequest},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/user/sessions/settings", strings.NewReader(test.body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", 1)
		if test.browser {
			c.Set("session_id", "current")
			c.Set("auth_version", int64(1))
			c.Set("session_version", int64(1))
		}
		UpdateLoginSessionSettings(c)
		assert.Equal(t, test.status, recorder.Code, test.body)
	}
}

func TestLoginSessionSettingsPersistAndApplyImmediately(t *testing.T) {
	setupAuthFlowControllerTest(t)
	user := &model.User{Username: "session-settings", Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, model.DB.Create(user).Error)
	now := time.Now().Unix()
	session := model.UserSession{
		SID: "old-current", UserID: user.Id, Version: 1, UserAuthVersion: 1,
		Status: model.UserSessionStatusActive, RefreshHash: "hash", LoginMethod: "password",
		CreatedAt: now - 8*24*60*60, LastActiveAt: now, ExpiresAt: now + 86400,
	}
	require.NoError(t, model.DB.Create(&session).Error)
	request := func(method, path, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", user.Id)
		c.Set("session_id", session.SID)
		c.Set("auth_version", int64(1))
		c.Set("session_version", int64(1))
		handler(c)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		assert.Contains(t, recorder.Body.String(), `"success":true`)
		return recorder
	}
	request(http.MethodPut, "/api/user/sessions/settings", `{"session_auto_logout":false}`, UpdateLoginSessionSettings)
	// Saving unrelated notification preferences must preserve an explicit opt-out.
	request(http.MethodPut, "/api/user/setting", `{"notify_type":"email","quota_warning_threshold":42}`, UpdateUserSetting)
	response := request(http.MethodGet, "/api/user/sessions", "", GetLoginSessions)
	assert.Contains(t, response.Body.String(), `"session_auto_logout":false`)
	assert.Contains(t, response.Body.String(), session.SID)
	request(http.MethodPut, "/api/user/sessions/settings", `{"session_auto_logout":true}`, UpdateLoginSessionSettings)
	stored, err := model.GetUserSessionBySID(session.SID)
	require.NoError(t, err)
	assert.Equal(t, model.UserSessionStatusRevoked, stored.Status, "enabling applies to the old current session immediately")
}
