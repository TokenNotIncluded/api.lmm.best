/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAffiliateInvitationEmailContentUsesConfiguredOriginAndEscapesCopy(t *testing.T) {
	content, inviteURL, err := buildAffiliateInvitationEmailContent(
		"https://console.example.com/base/",
		"Forge <Preview>",
		"a&b",
	)
	require.NoError(t, err)
	require.Equal(t, "https://console.example.com/base/sign-up?aff=a%26b", inviteURL.String())
	assert.Contains(t, content, "Forge &lt;Preview&gt;")
	assert.Contains(t, content, "aff=a%26b")
	assert.NotContains(t, content, "Forge <Preview>")
}

func TestBuildAffiliateInvitationEmailContentRejectsUnsafeOrigin(t *testing.T) {
	for _, origin := range []string{
		"",
		"console.example.com",
		"ftp://console.example.com",
		"https://user:pass@console.example.com",
		"https://console.example.com?redirect=evil",
	} {
		t.Run(origin, func(t *testing.T) {
			_, _, err := buildAffiliateInvitationEmailContent(origin, "Forge", "code")
			require.Error(t, err)
		})
	}
}

func TestEnsureAffiliateCodeKeepsOneCodeForStaleReaders(t *testing.T) {
	db := setupManageUserTestDB(t)
	persisted := model.User{
		Username: "affiliate-code-owner", Password: "password", Email: "owner@example.com",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
	}
	require.NoError(t, db.Create(&persisted).Error)

	firstReader := model.User{Id: persisted.Id}
	secondReader := model.User{Id: persisted.Id}
	require.NoError(t, ensureAffiliateCode(&firstReader))
	require.NoError(t, ensureAffiliateCode(&secondReader))

	require.NotEmpty(t, firstReader.AffCode)
	require.Equal(t, firstReader.AffCode, secondReader.AffCode)
}

func TestNormalizeAffiliateInvitationSystemNameRemovesHeaderNewlines(t *testing.T) {
	name := normalizeAffiliateInvitationSystemName("  Forge\r\nBcc: attacker@example.com  ")

	require.Equal(t, "Forge Bcc: attacker@example.com", name)
	require.NotContains(t, name, "\r")
	require.NotContains(t, name, "\n")
}

func TestSendAffiliateInvitationUsesAuthenticatedUsersCode(t *testing.T) {
	db := setupManageUserTestDB(t)
	user := model.User{
		Username: "invite-sender", Password: "password", Email: "sender@example.com",
		AffCode: "mine", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
	}
	require.NoError(t, db.Create(&user).Error)

	previousSender := sendAffiliateInvitationEmail
	previousSystemName := common.SystemName
	previousServerAddress := system_setting.ServerAddress
	t.Cleanup(func() {
		sendAffiliateInvitationEmail = previousSender
		common.SystemName = previousSystemName
		system_setting.ServerAddress = previousServerAddress
	})
	common.SystemName = "LMM Test"
	system_setting.ServerAddress = "https://console.example.com"

	var capturedSubject, capturedRecipient, capturedContent string
	sendAffiliateInvitationEmail = func(subject, recipient, content string) error {
		capturedSubject = subject
		capturedRecipient = recipient
		capturedContent = content
		return nil
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/user/aff/invite",
		strings.NewReader(`{"email":"friend@example.com"}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set("id", user.Id)

	SendAffiliateInvitation(context)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, recorder.Body.String())
	assert.Equal(t, "好友邀请你加入 LMM Test", capturedSubject)
	assert.Equal(t, "friend@example.com", capturedRecipient)
	assert.Contains(t, capturedContent, "https://console.example.com/sign-up?aff=mine")
}

func TestSendAffiliateInvitationRejectsSenderAddress(t *testing.T) {
	db := setupManageUserTestDB(t)
	user := model.User{
		Username: "self-invite-sender", Password: "password", Email: "sender@example.com",
		AffCode: "mine", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
	}
	require.NoError(t, db.Create(&user).Error)

	previousSender := sendAffiliateInvitationEmail
	t.Cleanup(func() { sendAffiliateInvitationEmail = previousSender })
	sendCalled := false
	sendAffiliateInvitationEmail = func(_, _, _ string) error {
		sendCalled = true
		return nil
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/user/aff/invite",
		strings.NewReader(`{"email":"SENDER@example.com"}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set("id", user.Id)

	SendAffiliateInvitation(context)

	assert.False(t, sendCalled)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.Contains(t, recorder.Body.String(), "不能向自己的邮箱发送邀请")
}
