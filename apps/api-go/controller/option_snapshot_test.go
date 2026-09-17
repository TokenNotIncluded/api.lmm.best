package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetOptionsSnapshotKeepsSensitiveFieldsPrivate(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{
		"Notice": "visible", "ModelPrice": `{"snapshot-model":2}`,
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
	GetOptions(c)
	require.Equal(t, http.StatusOK, r.Code)
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
	require.Len(t, values, 4)
	require.Equal(t, "visible", values["Notice"])
	var meta map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(values["CompletionRatioMeta"]), &meta))
	require.Contains(t, meta, "snapshot-model")
	require.NotContains(t, r.Body.String(), "private-")
	require.NotContains(t, r.Body.String(), "hidden-theme")
	require.NotContains(t, r.Body.String(), "stale metadata")
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
