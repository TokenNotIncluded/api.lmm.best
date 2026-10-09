package middleware

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestExtoreCallbackLoggerRemovesSecrets(t *testing.T) {
	raw := "/store/manage?code=fixture-code&state=fixture-state&iss=https%3A%2F%2Fextore.example"
	r := httptest.NewRequest("GET", raw, nil)
	got := loggedRequestPath(gin.LogFormatterParams{Request: r, Path: raw})
	if got != "/store/manage" {
		t.Fatal("callback query was logged")
	}
	if r.URL.RequestURI() != raw {
		t.Fatal("logging modified the real callback")
	}
}
