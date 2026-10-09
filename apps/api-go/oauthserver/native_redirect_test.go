package oauthserver

import "testing"

func TestNativeRedirectTemplatesKeepAddressFamiliesSeparate(t *testing.T) {
	for raw, expected := range map[string]string{
		"http://127.0.0.1:12345/callback": "http://127.0.0.1/callback",
		"http://[::1]:54321/callback":     "http://[::1]/callback",
		"http://[::1]/callback":           "http://[::1]/callback",
	} {
		actual, ok := NativeRedirectTemplate(raw)
		if !ok || actual != expected {
			t.Fatalf("%q = %q, %v", raw, actual, ok)
		}
		client := NativeClient{ID: "native", Name: "Native", RedirectURIs: []string{expected}, Resources: []string{"https://issuer.example/mcp/market"}, Scopes: []string{"market:discover"}}
		if validateClient(client) != nil || !validRedirect(raw, client) {
			t.Fatal("registered native callback rejected", raw)
		}
	}
	client := NativeClient{RedirectURIs: []string{"http://127.0.0.1/callback"}}
	for _, bad := range []string{"http://[::1]:54321/callback", "http://localhost:54321/callback", "http://[::ffff:127.0.0.1]/callback", "http://[::1%25lo]:1234/callback", "http://[::1]:0123/callback", "http://[::1]:65536/callback", "http://[0:0:0:0:0:0:0:1]/callback", "http://[::1]:/callback", "http://[::2]/callback", "http://[::1]/%2e%2e/callback"} {
		if validRedirect(bad, client) {
			t.Errorf("unexpected callback alias %q", bad)
		}
		if bad != "http://[::1]:54321/callback" {
			if _, ok := NativeRedirectTemplate(bad); ok {
				t.Errorf("accepted noncanonical template %q", bad)
			}
		}
	}
}
