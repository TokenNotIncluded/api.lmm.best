package middleware

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHumanGrantLogRedaction(t *testing.T) {
	for _, path := range []string{"/human/private-link", "/invite/private-token/block", "/api/user/auth/humangrant/human/private-link/verify", "/v1/requests/private-id/status.md", "/v1/accounts/private-id/profile"} {
		r := httptest.NewRequest("GET", path+"?token=private-query", nil)
		out := loggedRequestPath(gin.LogFormatterParams{Request: r, Path: r.URL.RequestURI()})
		if strings.Contains(out, "private-") || strings.Contains(out, "?") {
			t.Fatalf("leaked path %q", out)
		}
	}
}
