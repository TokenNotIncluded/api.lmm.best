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

func TestSystemOneRoutesRequireRelayAuthenticationAndAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var handlers []string
	engine.Use(func(c *gin.Context) {
		handlers = c.HandlerNames()
		c.AbortWithStatus(http.StatusNoContent)
	})
	SetRelayRouter(engine)
	for _, endpoint := range []string{"/v1/systemone", "/typesafe/v1/systemone"} {
		t.Run(endpoint, func(t *testing.T) {
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, endpoint, nil))
			require.Equal(t, http.StatusNoContent, response.Code)
			chain := strings.Join(handlers, "\n")
			auth := strings.Index(chain, "TokenAuth")
			admission := strings.Index(chain, "RelayRequestAdmission")
			distribute := strings.Index(chain, "Distribute")
			assert.GreaterOrEqual(t, auth, 0, chain)
			assert.Greater(t, admission, auth, "authenticate before reading the relay body: %s", chain)
			assert.Greater(t, distribute, admission, "apply shared admission before channel selection: %s", chain)
			assert.Contains(t, chain, "ModelRequestRateLimit")
		})
	}

	protected := gin.New()
	SetRelayRouter(protected)
	for _, endpoint := range []string{"/v1/systemone", "/typesafe/v1/systemone"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{"model":"jev-latest","state":null,"questions":{"ok":{"type":"noul"}}}`))
		request.Header.Set("Content-Type", "application/json")
		protected.ServeHTTP(response, request)
		assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		assert.JSONEq(t, `{"message":"Not Found"}`, response.Body.String(), "missing credentials use the existing concealed relay authentication response")

		oauthResponse := httptest.NewRecorder()
		oauthRequest := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{"model":"jev-latest","state":null,"questions":{"ok":{"type":"noul"}}}`))
		oauthRequest.Header.Set("Content-Type", "application/json")
		oauthRequest.Header.Set("Authorization", "Bearer lmm_at_systemone_boundary_fixture")
		protected.ServeHTTP(oauthResponse, oauthRequest)
		assert.Equal(t, http.StatusUnauthorized, oauthResponse.Code, oauthResponse.Body.String())
		assert.Contains(t, oauthResponse.Body.String(), "OAuth request is not authorized", "an OAuth-shaped credential cannot fall through to legacy token authentication")
	}
}

func TestSystemOneNamespaceMissesRemainBackendRequests(t *testing.T) {
	for _, endpoint := range []string{
		"/typesafe", "/typesafe/", "/typesafe/v1/systemone/unknown", "/typesafe/v1/chat/completions?model=jev-latest",
		"/v1/systemone/unknown",
	} {
		assert.True(t, isBackendPath(endpoint), "%s must never receive a frontend app shell", endpoint)
	}
	assert.False(t, isBackendPath("/typesafe-introduction"))
}
