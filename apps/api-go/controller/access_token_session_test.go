package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// These cases exercise the controller guard, not the bearer-token parser.
// Rejected requests must return before key generation or database access.
func TestGenerateAccessTokenRejectsNonBrowserSessions(t *testing.T) {
	tests := []struct {
		name           string
		identity       *service.AuthIdentity
		fallbackFields bool
	}{
		{name: "missing session"},
		{
			name: "personal access token",
			identity: &service.AuthIdentity{
				UserID: 7, UserAuthVersion: 1,
			},
		},
		{
			name: "personal token cannot borrow fallback session fields",
			identity: &service.AuthIdentity{
				UserID: 7, UserAuthVersion: 1,
			},
			fallbackFields: true,
		},
		{
			name: "missing user version",
			identity: &service.AuthIdentity{
				UserID: 7, SessionID: "test-browser-session", SessionVersion: 1,
			},
		},
		{
			name: "missing session version",
			identity: &service.AuthIdentity{
				UserID: 7, SessionID: "test-browser-session", UserAuthVersion: 1,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/user/token", nil)
			// A request header is not evidence of a server-validated session.
			c.Request.Header.Set("X-Auth-Session", "test-browser-session")
			c.Set("id", 7)
			if test.identity != nil {
				c.Set("auth_identity", *test.identity)
			}
			if test.fallbackFields {
				c.Set("session_id", "test-browser-session")
				c.Set("auth_version", int64(1))
				c.Set("session_version", int64(1))
			}

			GenerateAccessToken(c)

			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			var body struct {
				Success bool            `json:"success"`
				Code    string          `json:"code"`
				Data    json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Success || body.Code != "AUTH_SESSION_REQUIRED" || len(body.Data) != 0 {
				t.Fatalf("expected a session error without issued credential data")
			}
		})
	}
}
