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

func TestAssistantHistoryAndDrawingBootstrapDoNotRequireModelCapacity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var handlers []string
	engine.Use(func(c *gin.Context) {
		handlers = c.HandlerNames()
		c.AbortWithStatus(http.StatusNoContent)
	})
	SetRelayRouter(engine)
	for _, endpoint := range []struct{ path, controller string }{
		{"/api/assistant/status", "GetAssistantStatus"},
		{"/api/assistant/conversations", "ListAssistantConversations"},
		{"/api/assistant/conversations?archived=true", "ListAssistantConversations"},
		{"/api/assistant/conversations?user_id=42", "ListAssistantConversations"},
		{"/api/assistant/conversations/1", "GetAssistantConversationHistory"},
	} {
		t.Run(endpoint.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, endpoint.path, nil))
			require.Equal(t, http.StatusNoContent, response.Code)
			chain := strings.Join(handlers, "\n")
			assert.Contains(t, chain, "UserAuth", "CPU availability must not replace account authentication")
			assert.Contains(t, chain, "DisableCache", "private history and capabilities must remain uncacheable")
			assert.Contains(t, chain, endpoint.controller, "the existing permission-aware controller must still serve the read")
			assert.NotContains(t, chain, "SystemPerformanceCheck", "CPU peaks must not turn lightweight reads into model-overload 503 responses")
			assert.NotContains(t, chain, "PrepareAssistantRequest")
			assert.NotContains(t, chain, "Distribute")
		})
	}
}

func TestAssistantGenerationAndMutationsKeepPerformanceAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var handlers []string
	engine.Use(func(c *gin.Context) {
		handlers = c.HandlerNames()
		c.AbortWithStatus(http.StatusNoContent)
	})
	SetRelayRouter(engine)
	for _, path := range []string{
		"/api/assistant/chat",
		"/api/assistant/drawing/generate",
		"/api/assistant/drawing/key",
		"/api/assistant/conversations/1/archive",
		"/api/assistant/conversations/1/unarchive",
		"/api/assistant/tools/create-key",
	} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
			require.Equal(t, http.StatusNoContent, response.Code)
			chain := strings.Join(handlers, "\n")
			assert.Contains(t, chain, "SystemPerformanceCheck")
			assert.Contains(t, chain, "UserAuth")
		})
	}
}

func TestAssistantLightweightReadsStillRejectAnonymousViewers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)
	for _, path := range []string{
		"/api/assistant/status",
		"/api/assistant/conversations",
		"/api/assistant/conversations?user_id=42",
		"/api/assistant/conversations/1",
	} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			assert.Equal(t, http.StatusUnauthorized, response.Code)
			assert.Contains(t, response.Body.String(), "AUTH_UNAUTHORIZED")
		})
	}
}
