package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAcquisitionPrivateRoutesRejectAnonymousRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/acquisition/links/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/preview"},
		{http.MethodDelete, "/api/admin/acquisition/links/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{http.MethodGet, "/api/admin/acquisition/cost"},
		{http.MethodPut, "/api/admin/acquisition/cost"},
		{http.MethodPost, "/api/acquisition/consent"},
		{http.MethodGet, "/api/acquisition/self-report"},
		{http.MethodPut, "/api/acquisition/self-report"},
		{http.MethodDelete, "/api/acquisition/self-report"},
		{http.MethodPost, "/api/admin/acquisition/activity/rebuild"},
		{http.MethodGet, "/api/admin/acquisition/users/export"},
		{http.MethodGet, "/api/admin/acquisition/users/1/corrections"},
		{http.MethodPost, "/api/admin/acquisition/users/1/corrections"},
		{http.MethodGet, "/api/admin/acquisition/users"},
		{http.MethodGet, "/api/admin/acquisition/visitors"},
		{http.MethodGet, "/api/admin/acquisition/funnel"},
		{http.MethodGet, "/api/admin/acquisition/users/1"},
		{http.MethodPut, "/api/admin/acquisition/lookback"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(request.method, request.path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code, request.path)
	}
}
