package router

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAssistantChatInRealRouterReceivesAPIOperationRegistry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	// This runs after the engine-local registry, but before chat authentication
	// and model distribution. No paid model or real account is needed here.
	found := false
	engine.Use(func(c *gin.Context) { _, found = c.Get("assistant_admin_operations"); c.AbortWithStatus(204) })
	SetRelayRouter(engine)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/assistant/chat", nil))
	require.Equal(t, 204, recorder.Code)
	require.True(t, found)
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest("POST", "/v1/chat/completions", nil))
	require.Equal(t, 204, recorder.Code)
	require.False(t, found)
}
