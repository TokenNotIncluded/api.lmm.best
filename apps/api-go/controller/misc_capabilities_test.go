package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetStatusAdvertisesFrontendBackendCapabilities(t *testing.T) {
	installStatusCurrencyFixture(t)
	previousMap := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { common.OptionMap = previousMap })

	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)

	GetStatus(context)

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			BackendCapabilities map[string]bool `json:"backend_capabilities"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Equal(t, map[string]bool{
		"bounty_notifications":    true,
		"bounty_challenge_cancel": true,
		"bounty_public_read":      true,
		"self_oauth_unbind":       true,
		"responses_websocket":     true,
	}, payload.Data.BackendCapabilities)
}
