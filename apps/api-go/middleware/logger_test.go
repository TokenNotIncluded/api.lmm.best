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

func TestSensitiveRequestQueriesStayOutOfAccessLogs(t *testing.T) {
	previous := gin.DefaultWriter
	t.Cleanup(func() { gin.DefaultWriter = previous })
	for _, path := range []string{
		"/api/tool-market/inspect",
		"/api/tool-market/services/test-service/credentials",
		"/api/tool-market",
		"/api/oauth2/authorize",
	} {
		t.Run(path, func(t *testing.T) {
			var output bytes.Buffer
			gin.DefaultWriter = &output
			router := gin.New()
			SetUpLogger(router)
			router.GET(path, func(c *gin.Context) {
				if c.Query("token") != "private-test-credential" {
					t.Error("log redaction must preserve the actual request")
				}
				c.Status(http.StatusUnprocessableEntity)
			})
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path+"?token=private-test-credential", nil))
			if log := output.String(); strings.Contains(log, "private-test-credential") || strings.Contains(log, "token=") || !strings.Contains(log, path) {
				t.Fatalf("access log leaked query or lost route: %q", log)
			}
		})
	}
}

func TestStoreLoggerRedactsPickupCredentialsAndCallbackQueries(t *testing.T) {
	previous := gin.DefaultWriter
	t.Cleanup(func() { gin.DefaultWriter = previous })
	for _, requestPath := range []string{
		"/api/store/claim/private-pickup-token?pickup_code=secret-code",
		"/store/claim/private-pickup-token?ordinary=secret-value",
		"/api/store/claim/malformed-secret-token/extra",
		"/api/user/auth/store-claim/private-pickup-token?pickup_code=secret-code",
		"/api/user/auth/store-claim/malformed-secret-token/extra",
		"/api/store/payments/epay/MSabcdefgh/notify?sign=secret-signature&money=1",
	} {
		t.Run(requestPath, func(t *testing.T) {
			var output bytes.Buffer
			gin.DefaultWriter = &output
			engine := gin.New()
			SetUpLogger(engine)
			engine.Any("/*path", func(c *gin.Context) {
				if c.Request.URL.RequestURI() != requestPath {
					t.Error("redaction changed the real request")
				}
				c.Status(http.StatusUnprocessableEntity)
			})
			engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, requestPath, nil))
			line := output.String()
			if strings.Contains(line, "secret") || strings.Contains(line, "private-pickup-token") || strings.Contains(line, "?") {
				t.Fatalf("shop access log exposed a credential: %q", line)
			}
			if (strings.Contains(requestPath, "/claim/") || strings.Contains(requestPath, "/store-claim/")) && !strings.Contains(line, "[REDACTED]") {
				t.Fatalf("missing redacted pickup route: %q", line)
			}
		})
	}
}
