package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLoggerStripsOAuthQueriesButRetainsOrdinaryQueries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := gin.DefaultWriter
	defer func() { gin.DefaultWriter = previous }()
	var logs bytes.Buffer
	gin.DefaultWriter = &logs
	engine := gin.New()
	SetUpLogger(engine)
	engine.Any("/*path", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, path := range []string{
		"/api/oauth2/authorize?state=secret-state&code_challenge=secret-challenge",
		"/api/user/auth/oauth2/consent?csrf=secret-csrf",
		"/oauth/lmm/callback?code=secret-code&state=secret-state",
		"/api/models?filter=visible",
	} {
		logs.Reset()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		engine.ServeHTTP(httptest.NewRecorder(), request)
		line := logs.String()
		if strings.HasPrefix(path, "/api/models") {
			if !strings.Contains(line, "/api/models?filter=visible") {
				t.Fatalf("ordinary query was stripped: %q", line)
			}
		} else if strings.Contains(line, "?") || strings.Contains(line, "secret-") {
			t.Fatalf("OAuth query leaked: %q", line)
		}
	}
}
