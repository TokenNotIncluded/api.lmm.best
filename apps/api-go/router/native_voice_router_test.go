package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeVoiceRoutesShareRelayAuthenticationAndAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []struct{ method, path, handler string }{
		{http.MethodGet, "/v1/live/sessions", "NativeVoiceWebSocket"},
		{http.MethodPost, "/v1/live/sessions", "NativeVoiceWebRTC"},
		{http.MethodGet, "/v1/realtime/translations", "NativeVoiceWebSocket"},
		{http.MethodPost, "/v1/realtime/translations/calls", "UnsupportedNativeVoiceWebRTC"},
		{http.MethodPost, "/v1/realtime/client_secrets", "UnsupportedNativeVoiceWebRTC"},
		{http.MethodPost, "/v1/realtime/transcription_sessions", "UnsupportedNativeVoiceWebRTC"},
	} {
		t.Run(endpoint.method+endpoint.path, func(t *testing.T) {
			engine := gin.New()
			var chain string
			engine.Use(func(c *gin.Context) {
				chain = strings.Join(c.HandlerNames(), "\n")
				c.AbortWithStatus(http.StatusNoContent)
			})
			SetRelayRouter(engine)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(endpoint.method, endpoint.path, nil))
			require.Equal(t, http.StatusNoContent, response.Code)
			auth, admission := strings.Index(chain, "TokenAuth"), strings.Index(chain, "RelayRequestAdmission")
			assert.GreaterOrEqual(t, auth, 0, chain)
			assert.Greater(t, admission, auth, chain)
			assert.Greater(t, strings.Index(chain, endpoint.handler), admission, chain)
			assert.Contains(t, chain, "ModelRequestRateLimit")

			protected := gin.New()
			SetRelayRouter(protected)
			response = httptest.NewRecorder()
			protected.ServeHTTP(response, httptest.NewRequest(endpoint.method, endpoint.path, nil))
			assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
			assert.JSONEq(t, `{"message":"Not Found"}`, response.Body.String())
		})
	}
}
