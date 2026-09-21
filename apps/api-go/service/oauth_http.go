package service

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// OAuthRequestTransport trusts TLS or an explicitly configured loopback-only
// TLS terminator. The terminator MUST replace, not append, forwarding headers.
// Host remains a comparison against trusted configuration, never an issuer input.
func (s *OAuthIntegration) OAuthRequestTransport(r *http.Request) bool {
	issuer, err := url.Parse(s.Issuer)
	if err != nil || r.Host != issuer.Host {
		return false
	}
	if r.TLS != nil {
		return true
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(peer)
	return s.TrustLoopbackProxy && err == nil && ip != nil && ip.IsLoopback() && len(r.Header.Values("X-Forwarded-Proto")) == 1 && r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *OAuthIntegration) OAuthSameOrigin(r *http.Request) bool {
	if !s.OAuthRequestTransport(r) || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.Issuer {
		return false
	}
	site := r.Header.Get("Sec-Fetch-Site")
	return site == "" || site == "same-origin"
}

func (s *OAuthIntegration) OAuthRateKey(r *http.Request) string {
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(peer)
	if s.TrustLoopbackProxy && ip != nil && ip.IsLoopback() && len(r.Header.Values("X-Forwarded-For")) == 1 {
		// Multiple-hop/untrusted chains do not create attacker-chosen buckets.
		forwarded := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Forwarded-For")))
		if forwarded != nil {
			return forwarded.String()
		}
	}
	return peer
}

// OAuthAlternateCredentials rejects every legacy/API-key credential source.
// The native OAuth profile does not combine browser cookies with bearer tokens.
func OAuthAlternateCredentials(r *http.Request) bool {
	for name := range r.Header {
		switch strings.ToLower(name) {
		case "x-api-key", "x-goog-api-key", "mj-api-secret", "sec-websocket-protocol", "cookie", "new-api-user", "content-encoding":
			return true
		}
	}
	return false
}

func OAuthNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
}
