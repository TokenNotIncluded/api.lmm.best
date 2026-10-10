/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAIDirectoryPaymentAndModerationRejectAnonymousRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/ai-directory/ads/quote?bid_cents=100"},
		{http.MethodGet, "/api/ai-directory/ads/mine"},
		{http.MethodPost, "/api/ai-directory/ads"},
		{http.MethodPost, "/api/ai-directory/ads/1/hide"},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(response, request)
		require.Equal(t, http.StatusUnauthorized, response.Code, route.path)
	}
}
