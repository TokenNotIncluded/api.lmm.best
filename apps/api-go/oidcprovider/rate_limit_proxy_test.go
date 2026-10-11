package oidcprovider

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func rateLimitProxyProvider(t *testing.T) *Provider {
	t.Helper()
	p := &Provider{rates: map[string]bucket{}}
	for _, cidr := range []string{"127.0.0.1/32", "::1/128", "10.0.0.0/8"} {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		p.config.TrustedProxies = append(p.config.TrustedProxies, network)
	}
	return p
}

func TestRateLimitProxyIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, peer, want string
		forwarded        []string
	}{
		{"single client", "127.0.0.1:1234", "198.51.100.7", []string{"198.51.100.7"}},
		{"nginx appended client", "127.0.0.1:1234", "198.51.100.7", []string{"203.0.113.8, 198.51.100.7"}},
		{"untrusted prefix is ignored", "127.0.0.1:1234", "198.51.100.7", []string{"invalid, 198.51.100.7"}},
		{"trusted proxy chain", "127.0.0.1:1234", "198.51.100.7", []string{"203.0.113.8, 198.51.100.7, 10.1.2.3"}},
		{"all trusted chain", "127.0.0.1:1234", "10.1.2.3", []string{"10.1.2.3, 10.4.5.6"}},
		{"ipv6 client", "[::1]:1234", "2001:db8::7", []string{" 2001:0db8:0:0::7 "}},
		{"mapped ipv4 client", "[::1]:1234", "198.51.100.7", []string{"::ffff:198.51.100.7"}},
		{"mapped trusted peer", "[::ffff:127.0.0.1]:1234", "198.51.100.7", []string{"198.51.100.7"}},
		{"untrusted peer", "198.51.100.7:1234", "198.51.100.7", []string{"203.0.113.8"}},
		{"untrusted ipv6 peer", "[2001:db8::7]:1234", "2001:db8::7", []string{"203.0.113.8"}},
		{"missing header", "127.0.0.1:1234", "127.0.0.1", nil},
		{"empty header", "127.0.0.1:1234", "127.0.0.1", []string{""}},
		{"invalid address", "127.0.0.1:1234", "127.0.0.1", []string{"not-an-ip"}},
		{"address with port", "127.0.0.1:1234", "127.0.0.1", []string{"198.51.100.7:1234"}},
		{"bracketed ipv6", "[::1]:1234", "::1", []string{"[2001:db8::7]"}},
		{"empty nearest hop", "127.0.0.1:1234", "127.0.0.1", []string{"198.51.100.7,"}},
		{"invalid trusted chain", "127.0.0.1:1234", "127.0.0.1", []string{"invalid, 10.1.2.3"}},
		{"duplicate headers", "127.0.0.1:1234", "127.0.0.1", []string{"198.51.100.7", "203.0.113.8"}},
		{"malformed peer", "not-a-peer", "", []string{"198.51.100.7"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := rateLimitProxyProvider(t)
			r := httptest.NewRequest("GET", "https://api.lmm.best/api/oidc/jwks", nil)
			r.RemoteAddr = tc.peer
			r.Header["X-Forwarded-For"] = tc.forwarded
			r.Header.Set("X-Real-IP", "192.0.2.99")
			if !p.allowed(r) {
				t.Fatal("first request rejected")
			}
			if len(p.rates) != 1 || p.rates[tc.want+":"+r.URL.Path].count != 1 {
				t.Fatalf("unexpected rate bucket: %#v", p.rates)
			}
		})
	}
}

func TestRateLimitProxyIsolationAcrossHandlers(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		status             int
		handler            func(*Provider) http.Handler
	}{
		{"protocol", "GET", "/oidc/.well-known/openid-configuration", http.StatusOK, (*Provider).Handler},
		{"browser", "GET", "/api/user/auth/oidc/grants", http.StatusOK, (*Provider).BrowserEntryHandler},
		{"attestation", "POST", "/api/oidc/attest", http.StatusUnauthorized, (*Provider).AttestationHandler},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := rateLimitProxyProvider(t)
			p.config.BrowserIdentity = func(context.Context, *http.Request) (Identity, error) {
				return Identity{}, ErrDenied
			}
			handler := tc.handler(p)
			request := func(client string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(tc.method, "http://api.lmm.best"+tc.path, nil)
				r.RemoteAddr = "127.0.0.1:1234"
				r.Header.Set("X-Forwarded-Proto", "https")
				r.Header.Set("X-Forwarded-For", "203.0.113.8, "+client)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			for i := 0; i < 120; i++ {
				if w := request("198.51.100.7"); w.Code != tc.status {
					t.Fatalf("request %d: got %d, want %d", i+1, w.Code, tc.status)
				}
			}
			if w := request("198.51.100.7"); w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "rate_limited") {
				t.Fatalf("limit not enforced: %d %s", w.Code, w.Body.String())
			}
			if w := request("198.51.100.8"); w.Code != tc.status {
				t.Fatalf("another client inherited the limit: %d", w.Code)
			}
		})
	}
}

func TestRateLimitProxyCannotRotateUntrustedHeaders(t *testing.T) {
	p := rateLimitProxyProvider(t)
	r := httptest.NewRequest("GET", "https://api.lmm.best/api/oidc/jwks", nil)
	r.RemoteAddr = "198.51.100.7:1234"
	for i := 0; i < 121; i++ {
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		r.Header.Set("X-Real-IP", fmt.Sprintf("192.0.2.%d", i))
		if got := p.allowed(r); got != (i < 120) {
			t.Fatalf("request %d: allowed=%v", i+1, got)
		}
	}
}

func TestRateLimitProxyPreservesPathWindowAndCapacity(t *testing.T) {
	p := rateLimitProxyProvider(t)
	r := httptest.NewRequest("GET", "https://api.lmm.best/api/oidc/jwks", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	key := "198.51.100.7:" + r.URL.Path
	p.rates[key] = bucket{start: time.Now(), count: 120}
	if p.allowed(r) {
		t.Fatal("active full window accepted")
	}
	r.URL.Path = "/api/oidc/token"
	if !p.allowed(r) {
		t.Fatal("different endpoint shared the limit")
	}
	r.URL.Path = "/api/oidc/jwks"
	p.rates[key] = bucket{start: time.Now().Add(-time.Minute - time.Second), count: 120}
	if !p.allowed(r) || p.rates[key].count != 1 {
		t.Fatal("expired window did not restart")
	}
	p.rates = make(map[string]bucket, 4096)
	for i := 0; i < 4096; i++ {
		p.rates[fmt.Sprint(i)] = bucket{start: time.Now(), count: 1}
	}
	if p.allowed(r) || len(p.rates) != 4096 {
		t.Fatal("live bucket capacity was not enforced")
	}
	p.rates["0"] = bucket{start: time.Now().Add(-time.Minute - time.Second), count: 1}
	if !p.allowed(r) || len(p.rates) != 4096 || p.rates[key].count != 1 {
		t.Fatal("expired bucket was not reclaimed")
	}
}
