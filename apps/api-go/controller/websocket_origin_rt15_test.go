package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

// Predicate-only checks. They do not establish a WebSocket connection or authenticate.
func TestRT15WebSocketOriginPredicateOnly(t *testing.T) {
	previous := common.SessionCookieTrustedURLs
	t.Cleanup(func() { common.SessionCookieTrustedURLs = previous })
	common.SessionCookieTrustedURLs = []string{"https://trusted.test"}
	cases := []struct {
		name, url string
		origins   []string
		want      bool
	}{
		{"same-origin", "https://panel.test/v1/realtime", []string{"https://panel.test"}, true},
		{"foreign-origin", "https://panel.test/v1/realtime", []string{"https://foreign.test"}, false},
		{"same-site-subdomain", "https://panel.test/v1/realtime", []string{"https://child.panel.test"}, false},
		{"wrong-port", "https://panel.test/v1/realtime", []string{"https://panel.test:8443"}, false},
		{"wrong-scheme", "https://panel.test/v1/realtime", []string{"http://panel.test"}, false},
		{"opaque-origin", "https://panel.test/v1/realtime", []string{"null"}, false},
		{"empty-origin", "https://panel.test/v1/realtime", []string{""}, false},
		{"duplicate-origin", "https://panel.test/v1/realtime", []string{"https://panel.test", "https://panel.test"}, false},
		{"combined-origin", "https://panel.test/v1/realtime", []string{"https://panel.test, https://foreign.test"}, false},
		{"origin-free-cli", "https://panel.test/v1/realtime", nil, true},
		{"trusted-browser", "https://panel.test/v1/realtime", []string{"https://trusted.test"}, true},
		{"untrusted-child", "https://panel.test/v1/realtime", []string{"https://child.trusted.test"}, false},
		{"loopback-dev-split", "http://127.0.0.1:3000/v1/realtime", []string{"http://localhost:5173"}, true},
		{"foreign-to-loopback", "http://127.0.0.1:3000/v1/realtime", []string{"https://foreign.test"}, false},
		{"loopback-to-public", "https://panel.test/v1/realtime", []string{"http://localhost:5173"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.url, nil)
			for _, o := range tc.origins {
				r.Header.Add("Origin", o)
			}
			if got := checkWebSocketOrigin(r); got != tc.want {
				t.Fatalf("allowed=%v want %v", got, tc.want)
			}
		})
	}
}
