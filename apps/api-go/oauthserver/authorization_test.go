package oauthserver

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAuthorizationHappyPathAndMetadata(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, clock, policy := testServer(t, db)
		metadata := s.Metadata()
		require.Equal(t, testIssuer, metadata.Issuer)
		require.Equal(t, testIssuer+"/oauth/token", metadata.TokenEndpoint)
		require.Equal(t, []string{"code"}, metadata.ResponseTypesSupported)
		require.Equal(t, []string{"query"}, metadata.ResponseModesSupported)
		require.Equal(t, []string{"S256"}, metadata.CodeChallengeMethodsSupported)
		require.Equal(t, []string{"none"}, metadata.TokenEndpointAuthMethodsSupported)
		require.True(t, metadata.AuthorizationResponseISSParameterSupported)
		metadata.GrantTypesSupported[0] = "password"
		require.Equal(t, []string{"authorization_code", "refresh_token"}, s.Metadata().GrantTypesSupported)

		tokens, flow := issueTokens(t, s)
		require.Equal(t, "Bearer", tokens.TokenType)
		require.Equal(t, int64(600), tokens.ExpiresIn)
		require.Equal(t, "models:read relay:invoke", tokens.Scope)
		require.Equal(t, clock.now().Add(AuthorizationTTL), flow.pending.ExpiresAt)
		grant := verify(t, s, tokens.AccessToken)
		require.Equal(t, int64(42), grant.UserID)
		require.Equal(t, "pi-native", grant.ClientID)
		require.Equal(t, testResource, grant.Resource)
		require.Empty(t, grant.Binding)
		require.Equal(t, int64(4), policy.calls.Load())
		grant.Scopes[0] = "admin"
		require.Equal(t, []string{"models:read", "relay:invoke"}, verify(t, s, tokens.AccessToken).Scopes)

		var code model.OAuthServerCode
		require.NoError(t, db.Take(&code).Error)
		require.Equal(t, CodeTTL.Milliseconds(), code.ExpiresAtMs-code.CreatedAtMs)
	})
}

func TestAuthorizationRejectsAmbiguityAndUnsupportedFlows(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		cases := []struct{ key, value, code string }{
			{"response_type", "token", "unsupported_response_type"},
			{"response_type", "code token", "unsupported_response_type"},
			{"client_id", "unknown", "invalid_client"},
			{"code_challenge_method", "plain", "invalid_request"},
			{"code_challenge", strings.Repeat("a", 43), "invalid_request"},
			{"code_challenge", "short", "invalid_request"},
			{"state", "short", "invalid_request"},
			{"state", strings.Repeat("a", 513), "invalid_request"},
			{"scope", "admin", "invalid_scope"},
			{"scope", "models:read models:read", "invalid_scope"},
			{"scope", "models:read  relay:invoke", "invalid_scope"},
			{"resource", "https://api.lmm.test/v2", "invalid_target"},
			{"resource", testResource + "#fragment", "invalid_target"},
			{"nonce", "oidc-not-supported", "invalid_request"},
			{"client_secret", "not-supported", "invalid_request"},
			{"access_token", "url-bearer-not-supported", "invalid_request"},
			{"response_mode", "fragment", "invalid_request"},
			{"dpop_jkt", "not-implemented", "invalid_request"},
			{"redirect_uri", testRedirect + "\r\nLocation:https://evil.test", "invalid_request"},
		}
		for _, tc := range cases {
			t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
				values := authorizationValues()
				values.Set(tc.key, tc.value)
				pending, err := s.BeginAuthorization(context.Background(), values.Encode(), testBrowser)
				require.Nil(t, pending)
				expectProtocol(t, err, tc.code)
			})
		}
		for key, value := range authorizationValues() {
			t.Run("duplicate/"+key, func(t *testing.T) {
				_, err := s.BeginAuthorization(context.Background(), authorizationValues().Encode()+"&"+url.QueryEscape(key)+"="+url.QueryEscape(value[0]), testBrowser)
				expectProtocol(t, err, "invalid_request")
			})
			t.Run("missing/"+key, func(t *testing.T) {
				values := authorizationValues()
				values.Del(key)
				_, err := s.BeginAuthorization(context.Background(), values.Encode(), testBrowser)
				require.Error(t, err)
			})
		}
		for _, raw := range []string{"x=%ZZ", "x=1;x=2", "#fragment", strings.Repeat("x", maxFormBytes+1), authorizationValues().Encode() + "&%63lient_id=pi-native"} {
			_, err := s.BeginAuthorization(context.Background(), raw, testBrowser)
			expectProtocol(t, err, "invalid_request")
		}
		var count int64
		require.NoError(t, db.Model(&model.OAuthServerAuthorization{}).Count(&count).Error)
		require.Zero(t, count)
	})
}

func TestLoopbackRedirectExactPathAndOnlyPortException(t *testing.T) {
	client := testConfig().Clients[0]
	for _, raw := range []string{testRedirect, "http://127.0.0.1/oauth/callback", "http://127.0.0.1:1/oauth/callback", "http://127.0.0.1:65535/oauth/callback"} {
		require.True(t, validRedirect(raw, client), raw)
	}
	for _, raw := range []string{
		"https://127.0.0.1:49213/oauth/callback", "http://localhost:49213/oauth/callback", "http://[::1]:49213/oauth/callback",
		"http://127.1:49213/oauth/callback", "http://2130706433:49213/oauth/callback", "http://127.0.0.2:49213/oauth/callback",
		"http://127.0.0.1.evil.test:49213/oauth/callback", "http://evil.test:49213/oauth/callback",
		"http://user@127.0.0.1:49213/oauth/callback", "http://127.0.0.1@evil.test:49213/oauth/callback",
		testRedirect + "?", testRedirect + "?foo=bar", testRedirect + "?access_token=x", testRedirect + "#", testRedirect + "#fragment",
		"http://127.0.0.1:49213/OAuth/callback", "http://127.0.0.1:49213/oauth/callback/", "http://127.0.0.1:49213/oauth/%63allback",
		"http://127.0.0.1:49213/oauth//callback", "http://127.0.0.1:49213/foo/../oauth/callback", "http://127.0.0.1:49213/oauth/callback.evil",
		"http://127.0.0.1:0/oauth/callback", "http://127.0.0.1:65536/oauth/callback", "http://127.0.0.1:049213/oauth/callback",
		"http://127.0.0.1:/oauth/callback", "http://127.0.0.1:+49213/oauth/callback", "HTTP://127.0.0.1:49213/oauth/callback",
		"http://127.0.0.1:49213\\@evil.test/oauth/callback", "//127.0.0.1:49213/oauth/callback", "javascript:alert(1)",
	} {
		require.False(t, validRedirect(raw, client), raw)
	}
}

func TestConsentCannotApproveWithUserIDOrCrossBrowser(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, clock, policy := testServer(t, db)
		pending, err := s.BeginAuthorization(context.Background(), authorizationValues().Encode(), testBrowser)
		require.NoError(t, err)
		_, err = s.TrustedPrepareConsent(context.Background(), pending.Transaction, "another-server-browser-binding-with-32-bytes", 42)
		expectProtocol(t, err, "invalid_request")
		_, err = s.TrustedPrepareConsent(context.Background(), pending.Transaction, testBrowser, 0)
		expectProtocol(t, err, "invalid_request")
		_, err = s.TrustedApprove(context.Background(), pending.Transaction, testBrowser, "42")
		expectProtocol(t, err, "invalid_request")
		consent, err := s.TrustedPrepareConsent(context.Background(), pending.Transaction, testBrowser, 42)
		require.NoError(t, err)
		for _, id := range []int64{42, 43} {
			_, err = s.TrustedPrepareConsent(context.Background(), pending.Transaction, testBrowser, id)
			expectProtocol(t, err, "invalid_request")
		}
		other := prepareFlow(t, s)
		_, err = s.TrustedApprove(context.Background(), pending.Transaction, testBrowser, other.consent.Secret)
		expectProtocol(t, err, "invalid_request")
		_, err = s.TrustedApprove(context.Background(), pending.Transaction, "another-server-browser-binding-with-32-bytes", consent.Secret)
		expectProtocol(t, err, "invalid_request")
		policy.deny.Store(true)
		_, err = s.TrustedApprove(context.Background(), pending.Transaction, testBrowser, consent.Secret)
		expectProtocol(t, err, "access_denied")
		policy.deny.Store(false)
		clock.advance(AuthorizationTTL)
		_, err = s.TrustedApprove(context.Background(), pending.Transaction, testBrowser, consent.Secret)
		expectProtocol(t, err, "invalid_request")
		var count int64
		require.NoError(t, db.Model(&model.OAuthServerCode{}).Count(&count).Error)
		require.Zero(t, count)
	})
}

func TestConsentDenialIsOneUseAndContainsIssuer(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		flow := prepareFlow(t, s)
		denied, err := s.TrustedDeny(context.Background(), flow.pending.Transaction, testBrowser, flow.consent.Secret)
		require.NoError(t, err)
		u, err := url.Parse(denied.RedirectURI)
		require.NoError(t, err)
		require.Equal(t, url.Values{"error": {"access_denied"}, "state": {testState}, "iss": {testIssuer}}, u.Query())
		_, err = s.TrustedApprove(context.Background(), flow.pending.Transaction, testBrowser, flow.consent.Secret)
		expectProtocol(t, err, "invalid_request")
		_, err = s.TrustedDeny(context.Background(), flow.pending.Transaction, testBrowser, flow.consent.Secret)
		expectProtocol(t, err, "invalid_request")
		var count int64
		require.NoError(t, db.Model(&model.OAuthServerGrant{}).Count(&count).Error)
		require.Zero(t, count)
	})
}

func TestPKCEVerifierGrammarAndRFCTestVector(t *testing.T) {
	require.True(t, matchesChallenge(testVerifier, testChallenge))
	for _, invalid := range []string{"", strings.Repeat("a", 42), strings.Repeat("a", 129), strings.Repeat("a", 42) + "+", strings.Repeat("a", 42) + "/", strings.Repeat("a", 42) + "=", strings.Repeat("a", 42) + "\n"} {
		require.False(t, validVerifier(invalid))
	}
	require.True(t, validVerifier(strings.Repeat("a", 128)))
	require.False(t, matchesChallenge(strings.Repeat("a", 43), testChallenge))
}

func TestTrustedConsentExpiryAtExactBoundary(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, clock, _ := testServer(t, db)
		pending, err := s.BeginAuthorization(context.Background(), authorizationValues().Encode(), testBrowser)
		require.NoError(t, err)
		clock.advance(AuthorizationTTL)
		_, err = s.TrustedPrepareConsent(context.Background(), pending.Transaction, testBrowser, 42)
		expectProtocol(t, err, "invalid_request")
		clock.advance(time.Millisecond)
		_, err = s.TrustedPrepareConsent(context.Background(), pending.Transaction, testBrowser, 42)
		expectProtocol(t, err, "invalid_request")
	})
}

// authorizeWithScope runs a full begin/consent/approve cycle for user 42 with
// an explicit scope, independent of the fixed scope baked into authorizationValues.
func authorizeWithScope(t *testing.T, s *Server, scope string) testFlow {
	t.Helper()
	values := authorizationValues()
	values.Set("scope", scope)
	pending, err := s.BeginAuthorization(context.Background(), values.Encode(), testBrowser)
	require.NoError(t, err)
	consent, err := s.TrustedPrepareConsent(context.Background(), pending.Transaction, testBrowser, 42)
	require.NoError(t, err)
	response, err := s.TrustedApprove(context.Background(), pending.Transaction, testBrowser, consent.Secret)
	require.NoError(t, err)
	u, err := url.Parse(response.RedirectURI)
	require.NoError(t, err)
	return testFlow{pending: pending, consent: consent, code: u.Query().Get("code")}
}

// TestGrantReuseOnReapprovalRefreshesInPlace guards reuseOrCreateGrant: a
// second login for the same issuer/client/user/resource must reuse the
// existing token family (same ID, same CreatedAtMs) rather than mint a
// duplicate, and its absolute expiry must be extended by the reapproval.
func TestGrantReuseOnReapprovalRefreshesInPlace(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, clock, _ := testServer(t, db)
		approveFlow(t, s)
		var first model.OAuthServerGrant
		require.NoError(t, db.Take(&first).Error)

		clock.advance(time.Hour)
		approveFlow(t, s)
		var second model.OAuthServerGrant
		require.NoError(t, db.Take(&second).Error)

		require.Equal(t, first.ID, second.ID)
		require.Equal(t, first.CreatedAtMs, second.CreatedAtMs)
		require.Equal(t, clock.now().Add(s.absoluteTTL).UnixMilli(), second.AbsoluteExpiresAtMs)
		require.Greater(t, second.AbsoluteExpiresAtMs, first.AbsoluteExpiresAtMs)

		var count int64
		require.NoError(t, db.Model(&model.OAuthServerGrant{}).Count(&count).Error)
		require.Equal(t, int64(1), count)
	})
}

// TestGrantReuseUnionsScopeAcrossLogins guards unionScopes: reapproving with a
// different scope grows the reused family's scope, and reapproving with an
// already-covered scope never shrinks it back down.
func TestGrantReuseUnionsScopeAcrossLogins(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		authorizeWithScope(t, s, "models:read")
		var grant model.OAuthServerGrant
		require.NoError(t, db.Take(&grant).Error)
		require.Equal(t, "models:read", grant.Scope)

		authorizeWithScope(t, s, "relay:invoke")
		require.NoError(t, db.Take(&grant).Error)
		require.Equal(t, "models:read relay:invoke", grant.Scope)

		authorizeWithScope(t, s, "models:read")
		require.NoError(t, db.Take(&grant).Error)
		require.Equal(t, "models:read relay:invoke", grant.Scope)

		var count int64
		require.NoError(t, db.Model(&model.OAuthServerGrant{}).Count(&count).Error)
		require.Equal(t, int64(1), count)
	})
}

// TestRevokedGrantIsNotReusedButLeftIntact guards the reuseOrCreateGrant WHERE
// clause boundary "revoked_at_ms = 0": a revoked family must never be resurrected
// by a later login, and a brand new family must be minted alongside it instead.
func TestRevokedGrantIsNotReusedButLeftIntact(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		approveFlow(t, s)
		var original model.OAuthServerGrant
		require.NoError(t, db.Take(&original).Error)
		require.NoError(t, db.Model(&model.OAuthServerGrant{}).Where("id = ?", original.ID).
			Updates(map[string]any{"revoked_at_ms": 1, "revocation_reason": "test"}).Error)

		approveFlow(t, s)

		var count int64
		require.NoError(t, db.Model(&model.OAuthServerGrant{}).Count(&count).Error)
		require.Equal(t, int64(2), count)
		var stillRevoked model.OAuthServerGrant
		require.NoError(t, db.Where("id = ?", original.ID).Take(&stillRevoked).Error)
		require.Equal(t, int64(1), stillRevoked.RevokedAtMs)
		require.Equal(t, "test", stillRevoked.RevocationReason)
		var fresh model.OAuthServerGrant
		require.NoError(t, db.Where("id <> ?", original.ID).Take(&fresh).Error)
		require.Zero(t, fresh.RevokedAtMs)
	})
}

// TestExpiredGrantIsNotReusedButLeftIntact guards the reuseOrCreateGrant WHERE
// clause boundary "absolute_expires_at_ms > now": a family expired at or
// before the current instant must not be reused, mirroring the strict
// inequality already covered for authorizationLive in
// TestTrustedConsentExpiryAtExactBoundary.
func TestExpiredGrantIsNotReusedButLeftIntact(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, clock, _ := testServer(t, db)
		approveFlow(t, s)
		var original model.OAuthServerGrant
		require.NoError(t, db.Take(&original).Error)

		clock.millis.Store(original.AbsoluteExpiresAtMs)
		approveFlow(t, s)

		var count int64
		require.NoError(t, db.Model(&model.OAuthServerGrant{}).Count(&count).Error)
		require.Equal(t, int64(2), count)
		var stillExpired model.OAuthServerGrant
		require.NoError(t, db.Where("id = ?", original.ID).Take(&stillExpired).Error)
		require.Equal(t, original.AbsoluteExpiresAtMs, stillExpired.AbsoluteExpiresAtMs)
		var fresh model.OAuthServerGrant
		require.NoError(t, db.Where("id <> ?", original.ID).Take(&fresh).Error)
		require.Greater(t, fresh.AbsoluteExpiresAtMs, original.AbsoluteExpiresAtMs)
	})
}
