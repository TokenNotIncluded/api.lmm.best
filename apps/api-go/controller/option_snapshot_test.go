package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetOptionsSnapshotKeepsSensitiveFieldsPrivate(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	previousPrice := ratio_setting.ModelPrice2JSONString()
	previousL1 := setting.GetAssistantL1AutoReviewSettings().OptionValues()
	previousModeration := setting.GetModerationSettings().OptionValues()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrice))
		require.NoError(t, setting.UpdateAssistantL1AutoReviewOptions(previousL1))
		require.NoError(t, setting.UpdateModerationSettings(previousModeration))
		model.InvalidatePricingCache()
	})
	stored := []model.Option{
		{Key: "Notice", Value: "visible committed value"}, {Key: "ModelPrice", Value: `{"snapshot-model":2}`},
		{Key: "ModelPriceLock", Value: `{"snapshot-model":true}`},
		{Key: "AccessToken", Value: "private-token"}, {Key: "ClientSecret", Value: "private-secret"},
		{Key: "ClientKey", Value: "private-key"}, {Key: "client_secret", Value: "private-lower-secret"},
		{Key: "provider_api_key", Value: "private-api-key"}, {Key: "theme.frontend", Value: "hidden-theme"},
	}
	require.NoError(t, db.Create(&stored).Error)
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{
		"Notice": "stale local value", "ModelPrice": `{"snapshot-model":999}`,
		"ModelPriceLock": `{"snapshot-model":true}`, "CompletionRatioMeta": "stale metadata",
		"AccessToken": "private-token", "ClientSecret": "private-secret", "ClientKey": "private-key",
		"client_secret": "private-lower-secret", "provider_api_key": "private-api-key",
		"theme.frontend": "hidden-theme",
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/option/", nil)
	GetOptions(c)
	require.Equal(t, http.StatusOK, r.Code)
	require.Equal(t, "no-store", r.Header().Get("Cache-Control"))
	var body struct {
		Success bool `json:"success"`
		Data    []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"data"`
		Capabilities map[string]bool `json:"capabilities"`
	}
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.True(t, body.Capabilities["model_price_locks"])
	values := make(map[string]string)
	for _, option := range body.Data {
		require.NotContains(t, values, option.Key, "one entry per option")
		values[option.Key] = option.Value
	}
	require.GreaterOrEqual(t, len(values), 4)
	require.Equal(t, "visible committed value", values["Notice"])
	require.JSONEq(t, `{"snapshot-model":2}`, values["ModelPrice"])
	var meta map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(values["CompletionRatioMeta"]), &meta))
	require.Contains(t, meta, "snapshot-model")
	require.NotContains(t, r.Body.String(), "private-")
	require.NotContains(t, r.Body.String(), "hidden-theme")
	require.NotContains(t, r.Body.String(), "stale metadata")
	require.NotContains(t, r.Body.String(), "stale local value")
}

func TestGetOptionsUnavailableDatabaseDoesNotReturnStaleSuccess(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{"Notice": "stale cached option"}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
	for _, unavailable := range []string{"nil database", "failed query"} {
		t.Run(unavailable, func(t *testing.T) {
			if unavailable == "nil database" {
				model.DB = nil
				defer func() { model.DB = db }()
			} else {
				require.NoError(t, db.Migrator().DropTable(&model.Option{}))
			}
			r := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(r)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/option/", nil)
			GetOptions(c)
			require.Equal(t, http.StatusServiceUnavailable, r.Code)
			require.NotContains(t, r.Body.String(), "stale cached option")
			require.JSONEq(t, `{"success":false,"message":"Failed to load current settings; please retry."}`, r.Body.String())
			require.Equal(t, "stale cached option", model.GetOptionsSnapshot()["Notice"])
		})
	}
}

func TestPriceLockInvalidBooleanCannotReturnSnapshot(t *testing.T) {
	for _, value := range []string{`"true"`, `null`, `1`, `{}`} {
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(
			`{"key":"ModelPriceLock","model":"source","value":`+value+`}`))
		UpdateOption(c)
		require.Equal(t, http.StatusBadRequest, r.Code)
		require.NotContains(t, r.Body.String(), "pricing")
	}
}
