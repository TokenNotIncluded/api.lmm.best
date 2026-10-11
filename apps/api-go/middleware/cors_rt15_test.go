package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// This tests the real middleware's response headers, not browser enforcement,
// authentication, or database mutations. Those need a separate local browser run.
func TestRT15CORSExplicitBearerPreflight(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			app := gin.New()
			app.Use(CORS())
			calls := 0
			app.Handle(method, "/rt15-probe", func(c *gin.Context) {
				calls++
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodOptions, "/rt15-probe", nil)
			req.Header.Set("Origin", "https://client.test")
			req.Header.Set("Access-Control-Request-Method", method)
			req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
			response := httptest.NewRecorder()
			app.ServeHTTP(response, req)
			if response.Code != http.StatusNoContent || calls != 0 {
				t.Fatalf("preflight status=%d downstream calls=%d", response.Code, calls)
			}
			// Fetch defines Authorization as a non-wildcard request header.
			// '*' alone never grants an explicit Authorization request header.
			if !rt15HeaderListContains(response.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
				t.Fatalf("Authorization is not explicitly allowed: %q", response.Header().Get("Access-Control-Allow-Headers"))
			}
			if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Fatalf("token API compatibility changed: Allow-Origin=%q", got)
			}
		})
	}
}

func TestRT15CORSNoOriginClientContinues(t *testing.T) {
	app := gin.New()
	app.Use(CORS())
	calls := 0
	app.GET("/rt15-probe", func(c *gin.Context) {
		calls++
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/rt15-probe", nil)
	// Synthetic input only. The handler is not an authentication substitute.
	req.Header.Set("Authorization", "Bearer rt15-fixture-not-a-credential")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent || calls != 1 {
		t.Fatalf("no-Origin client was blocked: status=%d calls=%d", response.Code, calls)
	}
}

func TestRT15CORSDoesNotTurnAuthRejectionIntoSuccess(t *testing.T) {
	app := gin.New()
	app.Use(CORS())
	app.GET("/rt15-probe", func(c *gin.Context) {
		c.AbortWithStatus(http.StatusUnauthorized)
	})
	req := httptest.NewRequest(http.MethodGet, "/rt15-probe", nil)
	req.Header.Set("Origin", "https://foreign.test")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("middleware changed downstream rejection: status=%d", response.Code)
	}
	// Do not equate this header assertion with a real browser read test.
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("unexpected origin reflection: %q", got)
	}
}

func rt15HeaderListContains(value, want string) bool {
	for _, header := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(header), want) {
			return true
		}
	}
	return false
}
