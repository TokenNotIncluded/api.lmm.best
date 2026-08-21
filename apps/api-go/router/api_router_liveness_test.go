package router

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestApiRouterRegistersPublicLivenessRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	for _, route := range engine.Routes() {
		if route.Method == "GET" && route.Path == "/api/livez" {
			require.Equal(t, "github.com/LIghtJUNction/api.lmm.best/controller.GetLiveness", route.Handler)
			return
		}
	}
	t.Fatal("GET /api/livez was not registered")
}

func TestApiRouterRegistersReleaseNoteRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	routes := make(map[string]bool)
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, expected := range []string{
		"GET /api/release-notes/latest",
		"POST /api/release-notes/:id/read",
		"GET /api/release-notes/admin",
		"POST /api/release-notes/admin",
	} {
		require.True(t, routes[expected], expected)
	}
}

func TestApiRouterRegistersSelfOnboardingTodoRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	routes := make(map[string]bool)
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, expected := range []string{
		"GET /api/user/self/onboarding/todo",
		"PATCH /api/user/self/onboarding/todo",
	} {
		require.True(t, routes[expected], expected)
	}
}
