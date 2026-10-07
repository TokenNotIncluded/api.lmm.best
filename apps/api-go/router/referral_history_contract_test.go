package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Check the UI against the registered handler, rather than another copied URL.
func TestReferralHistoryClientUsesRegisteredAuthenticatedRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	var path string
	for _, route := range engine.Routes() {
		if route.Method == http.MethodGet && strings.HasSuffix(route.Handler, ".GetReferralRewards") {
			require.Empty(t, path, "referral history must have one canonical route")
			path = route.Path
		}
	}
	require.NotEmpty(t, path, "referral history handler must be registered")
	client, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "features", "wallet", "components", "referral-history-dialog.tsx"))
	require.NoError(t, err)
	require.Contains(t, string(client), "'"+path+"'", "the referral dialog must request the registered backend route")

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusUnauthorized, response.Code, "anonymous requests must reach authentication, not return a route 404")
}
