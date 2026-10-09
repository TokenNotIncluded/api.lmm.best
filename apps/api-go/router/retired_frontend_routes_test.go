package router

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRetiredFrontendAPIRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	routes := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	_, hasAsyncCleanup := routes[http.MethodPost+" /api/system-task/log-cleanup"]
	_, hasDirectDelete := routes[http.MethodDelete+" /api/log/"]
	_, hasConsoleMigration := routes[http.MethodPost+" /api/option/migrate_console_setting"]
	assert.True(t, hasAsyncCleanup)
	assert.False(t, hasDirectDelete)
	assert.False(t, hasConsoleMigration)
}

func TestRetiredL1RoutesCannotReachLegacyReadOrWriteHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	routes := make(map[string]string)
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = route.Handler
	}
	for _, endpoint := range []string{
		"GET /api/user/developer-access/request",
		"POST /api/user/developer-access/request",
		"GET /api/user/:id/developer-access/archives",
		"GET /api/developer-access/requests",
		"POST /api/developer-access/requests/:id/approve",
		"POST /api/developer-access/requests/:id/reject",
	} {
		assert.True(t, strings.HasSuffix(routes[endpoint], ".RetiredDeveloperAccessRequest"), endpoint)
	}
}
