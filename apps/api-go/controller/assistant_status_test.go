package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type assistantStatusErrorResponse struct {
	Success bool            `json:"success"`
	Code    string          `json:"code"`
	Data    json.RawMessage `json:"data"`
}

func performAssistantStatusTestRequest(t *testing.T, userID int) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/assistant/status", nil)
	c.Set("id", userID)
	GetAssistantStatus(c)
	return recorder
}

func assertAssistantStatusUnavailable(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	var payload assistantStatusErrorResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.False(t, payload.Success)
	assert.Equal(t, "ASSISTANT_PERMISSION_STATE_UNAVAILABLE", payload.Code)
	assert.Empty(t, payload.Data)
	assert.NotContains(t, recorder.Body.String(), `"access_level"`)
}

func TestGetAssistantStatusKeepsSuccessfulStateUncached(t *testing.T) {
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	override := model.TrustLevelMinUser
	user := model.User{
		Id:                 781101,
		Username:           "assistant-status-l0",
		Password:           "password",
		Role:               common.RoleCommonUser,
		Status:             common.UserStatusEnabled,
		Group:              "default",
		TrustLevelOverride: &override,
	}
	require.NoError(t, db.Create(&user).Error)

	recorder := performAssistantStatusTestRequest(t, user.Id)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	assert.Contains(t, recorder.Body.String(), `"access_level":"L0"`)
}

func TestGetAssistantStatusFailsClosedWhenUserLookupFails(t *testing.T) {
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))

	recorder := performAssistantStatusTestRequest(t, 781102)

	assertAssistantStatusUnavailable(t, recorder)
}

func TestGetAssistantStatusFailsClosedWhenTrustLevelReadFails(t *testing.T) {
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	user := model.User{
		Id:       781103,
		Username: "assistant-status-trust-error",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)
	require.False(t, db.Migrator().HasTable(&model.TopUp{}))

	recorder := performAssistantStatusTestRequest(t, user.Id)

	assertAssistantStatusUnavailable(t, recorder)
}

func TestGetAssistantStatusFailsClosedWhenDeveloperAccessReadFails(t *testing.T) {
	installIdentityCurrencyFixture(t)
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}))
	user := model.User{
		Id:       781104,
		Username: "assistant-status-access-error",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)

	first := performAssistantStatusTestRequest(t, user.Id)
	require.Equal(t, http.StatusOK, first.Code)
	require.NoError(t, db.Migrator().DropTable(&model.TopUp{}))

	recorder := performAssistantStatusTestRequest(t, user.Id)

	assertAssistantStatusUnavailable(t, recorder)
}

func TestAssistantStatusCapabilities(t *testing.T) {
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}))
	users := []model.User{
		{Id: 781105, Username: "assistant-status-admin", Password: "password", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "status-admin"},
		{Id: 781106, Username: "assistant-status-root", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "status-root"},
	}
	for index := range users {
		require.NoError(t, db.Create(&users[index]).Error)
	}

	capabilities := func(userID int) map[string]bool {
		recorder := performAssistantStatusTestRequest(t, userID)
		require.Equal(t, http.StatusOK, recorder.Code)
		var payload struct {
			Data struct {
				Capabilities map[string]bool `json:"capabilities"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
		return payload.Data.Capabilities
	}

	admin := capabilities(users[0].Id)
	assert.False(t, admin["admin_config"])
	assert.False(t, admin["admin_pricing"])
	root := capabilities(users[1].Id)
	assert.True(t, root["admin_config"])
	assert.True(t, root["admin_pricing"])
}
