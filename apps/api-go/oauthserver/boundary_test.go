package oauthserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTrustedIssuerAndClientConfiguration(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		for _, issuer := range []string{
			"", "http://auth.lmm.test", "https://auth.lmm.test/", "https://auth.lmm.test/oauth", "https://auth.lmm.test?", "https://auth.lmm.test#",
			"https://user@auth.lmm.test", "https://auth.lmm.test#fragment", "https://auth.lmm.test?issuer=evil", "https://auth.lmm.test/%2e",
			"https://AUTH.lmm.test", "https://auth.lmm.test.", "https://127.0.0.1", "https://localhost", "https://auth.lmm.test:443",
			"//auth.lmm.test", "https://auth.lmm.test\\@evil.test", "https://bad_host.lmm.test", "https://-bad.lmm.test",
		} {
			config := testConfig()
			config.Issuer = issuer
			_, err := New(db, config, &testPolicy{})
			require.Error(t, err, issuer)
		}
		_, err := New(db, testConfig(), nil)
		require.Error(t, err)
		_, err = New(nil, testConfig(), &testPolicy{})
		require.Error(t, err)
		for _, mutate := range []func(*Config){
			func(c *Config) { c.Clients = nil },
			func(c *Config) { c.Clients[1].ID = c.Clients[0].ID },
			func(c *Config) { c.Clients[0].RedirectURIs = []string{testRedirect} },
			func(c *Config) { c.Clients[0].RedirectURIs = []string{"http://localhost/callback"} },
			func(c *Config) { c.Clients[0].Resources = []string{"https://api.lmm.test/v1#fragment"} },
			func(c *Config) { c.Clients[0].Scopes = []string{"bad scope"} },
			func(c *Config) { c.RefreshIdleTTL = -1 },
			func(c *Config) { c.RefreshAbsoluteTTL = AccessTTL - 1 },
			func(c *Config) { c.RefreshIdleTTL = DefaultRefreshAbsoluteTTL + 1 },
		} {
			config := testConfig()
			mutate(&config)
			_, err = New(db, config, &testPolicy{})
			require.Error(t, err)
		}
		config := testConfig()
		s, err := New(db, config, &testPolicy{})
		require.NoError(t, err)
		config.Clients[0].RedirectURIs[0] = "http://127.0.0.1/evil"
		config.Clients[0].Scopes[0] = "admin"
		config.Clients[0].Resources[0] = "https://evil.test/v1"
		require.True(t, validRedirect(testRedirect, s.clients["pi-native"]))
		require.Equal(t, []string{"models:read", "relay:invoke"}, s.clients["pi-native"].Scopes)
	})
}

func TestIssuerIsolationEvenWithSharedDatabase(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, otherDB *gorm.DB) {
		first, clock, _ := testServer(t, db)
		tokens, flow := issueTokens(t, first)
		config := testConfig()
		config.Issuer = "https://different.lmm.test"
		other, err := New(otherDB, config, &testPolicy{})
		require.NoError(t, err)
		other.now = clock.now
		_, err = other.Exchange(context.Background(), codeValues(flow.code).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		_, err = other.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		_, err = other.ValidateAccess(context.Background(), AccessRequest{Token: tokens.AccessToken, Resource: testResource})
		expectProtocol(t, err, "invalid_token")
		require.NoError(t, other.Revoke(context.Background(), url.Values{"client_id": {"pi-native"}, "token": {tokens.RefreshToken}}.Encode()))
		_, err = other.TrustedApprove(context.Background(), flow.pending.Transaction, testBrowser, flow.consent.Secret)
		expectProtocol(t, err, "invalid_request")
		verify(t, first, tokens.AccessToken)
		require.Equal(t, config.Issuer+"/oauth/authorize", other.Metadata().AuthorizationEndpoint)
	})
}

func TestBearerTransportRejectsURLTokensAndConflicts(t *testing.T) {
	token, err := newSecret(accessPrefix)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "https://api.lmm.test/v1/responses", strings.NewReader(`{"model":"test"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	value, err := BearerFromRequest(request)
	require.NoError(t, err)
	require.Equal(t, token, value)
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.URL.RawQuery = "access_token=" + token },
		func(r *http.Request) { r.URL.RawQuery = "%61ccess_token=" + token },
		func(r *http.Request) { r.URL.RawQuery = "Access_Token=" + token },
		func(r *http.Request) { r.URL.RawQuery = "token=" + token },
		func(r *http.Request) { r.URL.RawQuery = "a=%ZZ" },
		func(r *http.Request) { r.URL.User = url.User("bearer") },
		func(r *http.Request) { r.URL.Fragment = "access_token=" + token },
		func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+token) },
		func(r *http.Request) { r.Header["authorization"] = []string{"Bearer " + token} },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token+",Bearer "+token) },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer  "+token) },
		func(r *http.Request) { r.Header.Set("Authorization", "Basic "+token) },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer sk-existing-api-key") },
		func(r *http.Request) { r.Header.Set("DPoP", "not-verified") },
		func(r *http.Request) { r.Header.Del("Authorization") },
	} {
		copy := request.Clone(context.Background())
		mutate(copy)
		_, err := BearerFromRequest(copy)
		expectProtocol(t, err, "invalid_token")
	}
	_, err = BearerFromRequest(nil)
	expectProtocol(t, err, "invalid_token")
}

func TestPublicClientFormTransport(t *testing.T) {
	raw := "grant_type=refresh_token&client_id=pi-native&refresh_token=example&resource=" + url.QueryEscape(testResource)
	newRequest := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, testIssuer+"/oauth/token", strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		return r
	}
	value, err := ReadPublicClientForm(newRequest())
	require.NoError(t, err)
	require.Equal(t, raw, value)
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Method = http.MethodGet },
		func(r *http.Request) { r.URL.RawQuery = "client_id=pi-native" },
		func(r *http.Request) { r.URL.RawQuery = "access_token=secret" },
		func(r *http.Request) { r.URL.ForceQuery = true },
		func(r *http.Request) { r.URL.Fragment = "fragment" },
		func(r *http.Request) { r.URL.User = url.UserPassword("client", "secret") },
		func(r *http.Request) { r.Header.Set("Authorization", "Basic x") },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer x") },
		func(r *http.Request) { r.Header["Authorization"] = []string{} },
		func(r *http.Request) { r.Header.Set("DPoP", "not-supported") },
		func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") },
		func(r *http.Request) { r.Header.Add("Content-Type", "application/x-www-form-urlencoded") },
		func(r *http.Request) { r.Header.Set("Content-Type", "application/json") },
		func(r *http.Request) {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=latin1")
		},
		func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded; boundary=abc") },
		func(r *http.Request) { r.ContentLength = maxFormBytes + 1 },
		func(r *http.Request) {
			r.ContentLength = -1
			r.Body = io.NopCloser(strings.NewReader(strings.Repeat("a", maxFormBytes+1)))
		},
		func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader("")) },
		func(r *http.Request) { r.Body = io.NopCloser(errorReader{}) },
	} {
		r := newRequest()
		mutate(r)
		_, err := ReadPublicClientForm(r)
		expectProtocol(t, err, "invalid_request")
	}
	_, err = ReadPublicClientForm(nil)
	expectProtocol(t, err, "invalid_request")
	require.Equal(t, "no-store", SensitiveResponseHeaders().Get("Cache-Control"))
	require.Equal(t, "no-referrer", SensitiveResponseHeaders().Get("Referrer-Policy"))
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("simulated read failure") }

func TestAccessBindingAndLiveConfiguration(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, clock, _ := testServer(t, db)
		tokens, _ := issueTokens(t, s)
		for _, request := range []AccessRequest{
			{Token: tokens.RefreshToken, Resource: testResource},
			{Token: tokens.AccessToken, Resource: "https://other.lmm.test/v1"},
			{Token: tokens.AccessToken, Resource: testResource, RequiredScopes: []string{"admin"}},
			{Token: tokens.AccessToken, Resource: testResource, Binding: SenderBinding{Method: "dpop", Thumbprint: "unverified"}},
			{Token: tokens.AccessToken, Resource: testResource, RequiredScopes: []string{"models:read models:read"}},
		} {
			_, err := s.ValidateAccess(context.Background(), request)
			expectProtocol(t, err, "invalid_token")
		}
		for _, change := range []func(*Config){
			func(c *Config) { c.Clients = c.Clients[1:] },
			func(c *Config) { c.Clients[0].Scopes = []string{"models:read"} },
			func(c *Config) { c.Clients[0].Resources = []string{"https://api.lmm.test/v2"} },
			func(c *Config) { c.Clients[0].RedirectURIs = []string{"http://127.0.0.1/new-callback"} },
		} {
			config := testConfig()
			change(&config)
			updated, err := New(db, config, &testPolicy{})
			require.NoError(t, err)
			updated.now = clock.now
			_, err = updated.ValidateAccess(context.Background(), AccessRequest{Token: tokens.AccessToken, Resource: testResource})
			expectProtocol(t, err, "invalid_token")
			_, err = updated.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
			require.Error(t, err)
		}
	})
}
