package oauthserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/glebarez/sqlite"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	testIssuer    = "https://auth.lmm.test"
	testResource  = "https://api.lmm.test/v1"
	testRedirect  = "http://127.0.0.1:49213/oauth/callback"
	testVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	testChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	testBrowser   = "server-only-browser-binding-32-bytes-minimum"
	testState     = "client-random-state-at-least-128-bits"
)

type testPolicy struct {
	deny  atomic.Bool
	calls atomic.Int64
	hook  func(Grant) error
}

func (p *testPolicy) Authorize(_ context.Context, _ *gorm.DB, grant Grant) error {
	p.calls.Add(1)
	if p.deny.Load() {
		return errors.New("user disabled or authorization removed")
	}
	if p.hook != nil {
		return p.hook(grant)
	}
	return nil
}

type testClock struct{ millis atomic.Int64 }

func (c *testClock) now() time.Time                 { return time.UnixMilli(c.millis.Load()) }
func (c *testClock) advance(duration time.Duration) { c.millis.Add(duration.Milliseconds()) }

func testConfig() Config {
	client := NativeClient{ID: "pi-native", Name: "LMM Pi", RedirectURIs: []string{"http://127.0.0.1/oauth/callback"}, Resources: []string{testResource}, Scopes: []string{"models:read", "relay:invoke"}}
	other := client
	other.ID, other.Name = "other-native", "Other app"
	return Config{Issuer: testIssuer, Clients: []NativeClient{client, other}}
}

func testServer(t *testing.T, db *gorm.DB) (*Server, *testClock, *testPolicy) {
	t.Helper()
	clock, policy := &testClock{}, &testPolicy{}
	clock.millis.Store(time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC).UnixMilli())
	server, err := New(db, testConfig(), policy)
	require.NoError(t, err)
	server.now = clock.now
	return server, clock, policy
}

// Each case uses a fresh SQLite file and, when explicitly enabled, a fresh
// PostgreSQL schema. Two independent pools test cross-worker DB serialization.
func forDatabases(t *testing.T, run func(*testing.T, *gorm.DB, *gorm.DB)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		dsn := "file:" + filepath.Join(t.TempDir(), "oauth.sqlite") + "?_pragma=busy_timeout(15000)&_pragma=journal_mode(WAL)"
		open := func() *gorm.DB {
			db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			pool, err := db.DB()
			require.NoError(t, err)
			pool.SetMaxOpenConns(12)
			t.Cleanup(func() { require.NoError(t, pool.Close()) })
			return db
		}
		db, other := open(), open()
		require.NoError(t, model.MigrateOAuthServer(db))
		run(t, db, other)
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("OAUTH_SERVER_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("set OAUTH_SERVER_TEST_POSTGRES_DSN to an isolated LOCAL test PostgreSQL instance")
		}
		config, err := pgx.ParseConfig(dsn)
		require.NoError(t, err)
		// Prevent accidental use of a production/remote host by this harness.
		require.True(t, config.Host == "127.0.0.1" || config.Host == "localhost" || strings.HasPrefix(config.Host, os.TempDir()+"/"), "test PostgreSQL must be local")
		var random [12]byte
		_, err = rand.Read(random[:])
		require.NoError(t, err)
		schema := "oauth_stage1_" + hex.EncodeToString(random[:])
		admin := stdlib.OpenDB(*config)
		t.Cleanup(func() { require.NoError(t, admin.Close()) })
		_, err = admin.Exec("CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize())
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
			require.NoError(t, err)
		})
		config.RuntimeParams["search_path"] = schema
		config.RuntimeParams["lock_timeout"] = "15000"
		config.RuntimeParams["statement_timeout"] = "20000"
		open := func() *gorm.DB {
			pool := stdlib.OpenDB(*config)
			pool.SetMaxOpenConns(12)
			t.Cleanup(func() { require.NoError(t, pool.Close()) })
			db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			return db
		}
		db, other := open(), open()
		require.NoError(t, model.MigrateOAuthServer(db))
		run(t, db, other)
	})
}

func authorizationValues() url.Values {
	return url.Values{"response_type": {"code"}, "client_id": {"pi-native"}, "redirect_uri": {testRedirect},
		"resource": {testResource}, "scope": {"relay:invoke models:read"}, "state": {testState},
		"code_challenge": {testChallenge}, "code_challenge_method": {"S256"}}
}

type testFlow struct {
	pending *PendingAuthorization
	consent *Consent
	code    string
}

func prepareFlow(t *testing.T, s *Server) testFlow {
	t.Helper()
	pending, err := s.BeginAuthorization(context.Background(), authorizationValues().Encode(), testBrowser)
	require.NoError(t, err)
	consent, err := s.TrustedPrepareConsent(context.Background(), pending.Transaction, testBrowser, 42)
	require.NoError(t, err)
	return testFlow{pending: pending, consent: consent}
}

func approveFlow(t *testing.T, s *Server) testFlow {
	t.Helper()
	flow := prepareFlow(t, s)
	response, err := s.TrustedApprove(context.Background(), flow.pending.Transaction, testBrowser, flow.consent.Secret)
	require.NoError(t, err)
	u, err := url.Parse(response.RedirectURI)
	require.NoError(t, err)
	require.Equal(t, testIssuer, u.Query().Get("iss"))
	require.Equal(t, testState, u.Query().Get("state"))
	require.Len(t, u.Query(), 3)
	flow.code = u.Query().Get("code")
	require.True(t, validSecret(flow.code, codePrefix))
	return flow
}

func codeValues(code string) url.Values {
	return url.Values{"grant_type": {"authorization_code"}, "client_id": {"pi-native"}, "redirect_uri": {testRedirect},
		"resource": {testResource}, "code": {code}, "code_verifier": {testVerifier}}
}

func refreshValues(token string) url.Values {
	return url.Values{"grant_type": {"refresh_token"}, "client_id": {"pi-native"}, "resource": {testResource}, "refresh_token": {token}}
}

func issueTokens(t *testing.T, s *Server) (*TokenResponse, testFlow) {
	t.Helper()
	flow := approveFlow(t, s)
	response, err := s.Exchange(context.Background(), codeValues(flow.code).Encode(), SenderBinding{})
	require.NoError(t, err)
	return response, flow
}

func expectProtocol(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	var protocol *ProtocolError
	require.ErrorAs(t, err, &protocol)
	require.Equal(t, code, protocol.Code)
}

func verify(t *testing.T, s *Server, token string) *Grant {
	t.Helper()
	grant, err := s.ValidateAccess(context.Background(), AccessRequest{Token: token, Resource: testResource, RequiredScopes: []string{"models:read"}})
	require.NoError(t, err)
	return grant
}

func expectRevoked(t *testing.T, s *Server, tokens *TokenResponse) {
	t.Helper()
	_, err := s.ValidateAccess(context.Background(), AccessRequest{Token: tokens.AccessToken, Resource: testResource})
	expectProtocol(t, err, "invalid_token")
	_, err = s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
	expectProtocol(t, err, "invalid_grant")
}
