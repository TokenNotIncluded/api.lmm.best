package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

// Predicate-only checks. They do not exercise the Gin guard or database writes.
func TestRT15SessionOriginPredicate(t *testing.T) {
	previous := common.SessionCookieTrustedURLs
	t.Cleanup(func() { common.SessionCookieTrustedURLs = previous })
	common.SessionCookieTrustedURLs = []string{"https://trusted.test"}
	cases := []struct {
		name    string
		headers http.Header
		want    bool
	}{
		{"same-origin", http.Header{"Origin": {"https://panel.test"}}, true},
		{"canonical-default-port", http.Header{"Origin": {"https://PANEL.test:443"}}, true},
		{"foreign-origin", http.Header{"Origin": {"https://foreign.test"}}, false},
		{"same-site-subdomain", http.Header{"Origin": {"https://child.panel.test"}}, false},
		{"suffix-confusion", http.Header{"Origin": {"https://panel.test.foreign.test"}}, false},
		{"wrong-port", http.Header{"Origin": {"https://panel.test:8443"}}, false},
		{"wrong-scheme", http.Header{"Origin": {"http://panel.test"}}, false},
		{"opaque-origin", http.Header{"Origin": {"null"}}, false},
		{"empty-origin", http.Header{"Origin": {""}}, false},
		{"missing-origin-and-referer", http.Header{}, false},
		{"multiple-origin-fields", http.Header{"Origin": {"https://panel.test", "https://foreign.test"}}, false},
		{"comma-joined-origins", http.Header{"Origin": {"https://panel.test, https://foreign.test"}}, false},
		{"foreign-origin-good-referer", http.Header{"Origin": {"https://foreign.test"}, "Referer": {"https://panel.test/settings"}}, false},
		{"valid-referer-fallback", http.Header{"Referer": {"https://panel.test/settings?x=1"}}, true},
		{"foreign-referer", http.Header{"Referer": {"https://foreign.test/settings"}}, false},
		{"multiple-referers", http.Header{"Referer": {"https://panel.test/a", "https://panel.test/b"}}, false},
		{"userinfo-referer", http.Header{"Referer": {"https://name@panel.test/settings"}}, false},
		{"trusted-origin", http.Header{"Origin": {"https://trusted.test"}}, true},
		{"untrusted-child-of-trusted", http.Header{"Origin": {"https://child.trusted.test"}}, false},
		{"same-site-fetch-metadata-does-not-authorize", http.Header{"Origin": {"https://child.panel.test"}, "Sec-Fetch-Site": {"same-site"}}, false},
		{"forwarded-proto-does-not-authorize", http.Header{"Origin": {"http://panel.test"}, "X-Forwarded-Proto": {"http"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://panel.test/api/user/auth/refresh", nil)
			r.TLS = &tls.ConnectionState{}
			r.Header = tc.headers
			origin, ok := requestBrowserOrigin(r)
			got := ok && isAllowedSessionOrigin(r, origin)
			if got != tc.want {
				t.Fatalf("allowed=%v want %v", got, tc.want)
			}
		})
	}
}
