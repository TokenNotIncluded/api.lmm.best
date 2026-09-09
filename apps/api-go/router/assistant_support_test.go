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

func TestAssistantSupportRoutesUseAuthenticationWithoutModelAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var handlers []string
	engine.Use(func(c *gin.Context) {
		handlers = c.HandlerNames()
		c.AbortWithStatus(http.StatusNoContent)
	})
	SetRelayRouter(engine)
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/assistant/support/eligibility"},
		{http.MethodGet, "/api/assistant/support/self"},
		{http.MethodPost, "/api/assistant/support"},
		{http.MethodGet, "/api/assistant/support/1"},
		{http.MethodPost, "/api/assistant/support/1/messages"},
		{http.MethodPost, "/api/assistant/support/1/accept"},
		{http.MethodPost, "/api/assistant/support/1/close"},
	} {
		t.Run(endpoint.method+" "+endpoint.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(endpoint.method, endpoint.path, nil))
			require.Equal(t, http.StatusNoContent, response.Code)
			chain := strings.Join(handlers, "\n")
			assert.Contains(t, chain, "UserAuth", "every support operation needs dashboard authentication")
			assert.NotContains(t, chain, "PrepareAssistantRequest")
			assert.NotContains(t, chain, "Distribute")
			assert.NotContains(t, chain, "RelayRequestAdmission", "model capacity must not block human support")
		})
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil))
	chain := strings.Join(handlers, "\n")
	handoff := strings.Index(chain, "RouteAssistantHumanSupport")
	prepare := strings.Index(chain, "PrepareAssistantRequest")
	distribute := strings.Index(chain, "Distribute")
	require.GreaterOrEqual(t, handoff, 0)
	require.Greater(t, prepare, handoff, "human transfer is checked before AI availability and funding")
	require.Greater(t, distribute, prepare)
}
