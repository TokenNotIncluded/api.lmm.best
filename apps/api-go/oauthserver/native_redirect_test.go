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

func TestMCPRedirectPolicyIsExplicitAndExactForWebClients(t *testing.T) {
	for _, callback := range []string{"https://client.example/callback", "http://localhost:3210/callback"} {
		client := NativeClient{ID: "public-mcp", Name: "External MCP", RedirectURIs: []string{callback}, Resources: []string{"https://issuer.example/mcp/market"}, Scopes: []string{"market:discover"}, MCPRedirects: true}
		if validateClient(client) != nil || !validRedirect(callback, client) {
			t.Fatal("registered MCP callback rejected", callback)
		}
		for _, other := range []string{callback + "/", callback + "?next=1", callback + "#fragment", "https://other.example/callback", "https://client.example:443/callback", "http://localhost:3211/callback", "http://127.0.0.1:3210/callback"} {
			if validRedirect(other, client) {
				t.Errorf("accepted callback alias %q for %q", other, callback)
			}
		}
		client.MCPRedirects = false
		if validateClient(client) == nil || validRedirect(callback, client) {
			t.Fatal("MCP policy leaked into legacy native clients")
		}
	}
	for _, bad := range []string{"https://client.example", "http://client.example/callback", "https://user@client.example/callback", "https://client.example/%2e%2e/callback", "https://client.example/../callback", "https://127.0.0.1/callback", "http://localhost.evil.example/callback", "http://localhost:0/callback", "http://localhost:0123/callback", "http://localhost:65536/callback", "http://localhost:/callback", "http://LOCALHOST:123/callback"} {
		if _, ok := MCPRedirectTemplate(bad); ok {
			t.Errorf("accepted unsafe callback %q", bad)
		}
	}
}
