package controller

import (
	"encoding/json"
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

func assistantUserProfileRequest(t *testing.T, method, path, body string, role int) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "41"}}
	c.Set("id", 99)
	c.Set("role", role)
	c.Set("username", "profile-admin")
	if method == http.MethodGet {
		AdminGetAssistantUserProfile(c)
	} else {
		AdminUpdateAssistantUserProfile(c)
	}
	return recorder
}

func TestAdminAssistantUserProfileIsAdminOnlyAndRedactsSecrets(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantUserProfile{}))
	target := &model.User{Id: 41, Username: "profile-target", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "profile-target-41"}
	require.NoError(t, db.Create(target).Error)
	admin := &model.User{Id: 99, Username: "profile-admin", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "profile-admin-99"}
	require.NoError(t, db.Create(admin).Error)

	denied := assistantUserProfileRequest(t, http.MethodGet, "/api/user/41/assistant-profile", "", common.RoleCommonUser)
	assert.Equal(t, http.StatusForbidden, denied.Code)

	updated := assistantUserProfileRequest(t, http.MethodPut, "/api/user/41/assistant-profile", `{"profile_key":"guided_buyer","tags":["new user","needs setup"],"strategy":"Never reveal api_key: sk-hidden-value; guide one step at a time.","enabled":true}`, common.RoleRootUser)
	assert.Equal(t, http.StatusOK, updated.Code)
	assert.NotContains(t, updated.Body.String(), "sk-hidden-value")
	assert.Contains(t, updated.Body.String(), "needs setup")

	loaded := assistantUserProfileRequest(t, http.MethodGet, "/api/user/41/assistant-profile", "", common.RoleRootUser)
	assert.Equal(t, http.StatusOK, loaded.Code)
	assert.Contains(t, loaded.Body.String(), "guided_buyer")
	assert.NotContains(t, loaded.Body.String(), "sk-hidden-value")
}

func assistantUserListRequest(t *testing.T, path string, viewerID, viewerRole int) []map[string]interface{} {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Set("id", viewerID)
	c.Set("role", viewerRole)
	c.Set("username", "profile-list-viewer")
	if strings.Contains(path, "/search") {
		SearchUsers(c)
	} else {
		GetAllUsers(c)
	}
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var payload struct {
		Data struct {
			Items []map[string]interface{} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload.Data.Items
}

func assertAssistantProfileVisibility(t *testing.T, rows []map[string]interface{}, userID int, visible bool) {
	t.Helper()
	for _, row := range rows {
		if int(row["id"].(float64)) != userID {
			continue
		}
		if visible {
			profile, ok := row["assistant_profile"].(map[string]interface{})
			require.True(t, ok, "user %d profile missing: %#v", userID, row)
			assert.Equal(t, []interface{}{"guided"}, profile["tags"])
		} else {
			assert.NotContains(t, row, "assistant_profile", "user %d leaked profile: %#v", userID, row)
		}
		return
	}
	t.Fatalf("user %d missing from response", userID)
}

func TestUserListAssistantProfileJSONRespectsRoleVisibility(t *testing.T) {
	installIdentityCurrencyFixture(t)
	db := setupManageUserTestDB(t)
	persistCreditDenominationFixture(t, db)
	root := &model.User{Username: "profile-json-root", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "profile-json-root"}
	rootPeer := &model.User{Username: "profile-json-root-peer", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "profile-json-root-peer"}
	admin := &model.User{Username: "profile-json-admin", Password: "password", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "profile-json-admin"}
	adminPeer := &model.User{Username: "profile-json-admin-peer", Password: "password", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "profile-json-admin-peer"}
	user := &model.User{Username: "profile-json-user", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "profile-json-user"}
	for _, record := range []*model.User{root, rootPeer, admin, adminPeer, user} {
		require.NoError(t, db.Create(record).Error)
	}
	for _, target := range []*model.User{rootPeer, adminPeer, user} {
		_, err := model.SaveProfile(target.Id, target.Id, model.ProfileInput{
			Key: model.AssistantProfileGuided, Tags: []string{"guided"},
			Strategy: "Use short steps.", Source: model.AssistantProfileSourceAI, Enabled: true,
		})
		require.NoError(t, err)
	}

	for _, path := range []string{
		"/api/user/?p=1&page_size=100",
		"/api/user/search?keyword=profile-json&p=1&page_size=100",
	} {
		rootRows := assistantUserListRequest(t, path, root.Id, root.Role)
		assertAssistantProfileVisibility(t, rootRows, user.Id, true)
		assertAssistantProfileVisibility(t, rootRows, adminPeer.Id, true)
		assertAssistantProfileVisibility(t, rootRows, rootPeer.Id, false)

		adminRows := assistantUserListRequest(t, path, admin.Id, admin.Role)
		assertAssistantProfileVisibility(t, adminRows, user.Id, true)
		assertAssistantProfileVisibility(t, adminRows, adminPeer.Id, false)
		assertAssistantProfileVisibility(t, adminRows, rootPeer.Id, false)

		commonRows := assistantUserListRequest(t, path, user.Id, user.Role)
		assertAssistantProfileVisibility(t, commonRows, user.Id, false)
		assertAssistantProfileVisibility(t, commonRows, adminPeer.Id, false)
	}
}
