package router

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtoreCallbackFrontendHeaders(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html>callback"), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := newFrontendHandler(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/store/manage?code=fixture&state=fixture", "/store/manage?error=access_denied", "/store/manage"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("unprotected callback", w.Code, w.Header())
		}
		if strings.Contains(path, "?") && !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
			t.Fatal("missing callback CSP")
		}
	}
}
