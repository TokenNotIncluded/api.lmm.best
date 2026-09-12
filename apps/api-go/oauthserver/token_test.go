package oauthserver

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCodeExchangeBindingFailuresDoNotConsumeCode(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		flow := approveFlow(t, s)
		for _, tc := range []struct{ key, value, code string }{
			{"client_id", "other-native", "invalid_grant"},
			{"redirect_uri", "http://127.0.0.1:49214/oauth/callback", "invalid_grant"},
			{"redirect_uri", "http://127.0.0.1:49213/OAuth/callback", "invalid_grant"},
			{"resource", "https://api.lmm.test/v2", "invalid_grant"},
			{"code_verifier", strings.Repeat("x", 43), "invalid_grant"},
			{"code_verifier", "short", "invalid_grant"},
			{"scope", "admin", "invalid_request"},
			{"refresh_token", "mixing-grants", "invalid_request"},
			{"client_secret", "not-supported", "invalid_request"},
			{"username", "not-supported", "invalid_request"},
			{"grant_type", "password", "unsupported_grant_type"},
			{"grant_type", "client_credentials", "unsupported_grant_type"},
			{"grant_type", "implicit", "unsupported_grant_type"},
			{"code", "wrong", "invalid_grant"},
		} {
			t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
				values := codeValues(flow.code)
				values.Set(tc.key, tc.value)
				response, err := s.Exchange(context.Background(), values.Encode(), SenderBinding{})
				require.Nil(t, response)
				expectProtocol(t, err, tc.code)
			})
		}
		for key, value := range codeValues(flow.code) {
			_, err := s.Exchange(context.Background(), codeValues(flow.code).Encode()+"&"+key+"="+url.QueryEscape(value[0]), SenderBinding{})
			expectProtocol(t, err, "invalid_request")
		}
		for _, key := range []string{"client_id", "code", "redirect_uri", "resource", "code_verifier"} {
			values := codeValues(flow.code)
			values.Del(key)
			_, err := s.Exchange(context.Background(), values.Encode(), SenderBinding{})
			require.Error(t, err)
		}
		_, err := s.Exchange(context.Background(), codeValues(flow.code).Encode(), SenderBinding{Method: "dpop", Thumbprint: "unverified"})
		expectProtocol(t, err, "invalid_request")
		tokens, err := s.Exchange(context.Background(), codeValues(flow.code).Encode(), SenderBinding{})
		require.NoError(t, err)
		verify(t, s, tokens.AccessToken)
		// Wrong PKCE even on a used code must not permit a revocation DoS.
		wrong := codeValues(flow.code)
		wrong.Set("code_verifier", strings.Repeat("x", 43))
		_, err = s.Exchange(context.Background(), wrong.Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		verify(t, s, tokens.AccessToken)
		_, err = s.Exchange(context.Background(), codeValues(flow.code).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		expectRevoked(t, s, tokens)
	})
}

func TestRefreshRotationReplayAndMonotonicScope(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		original, _ := issueTokens(t, s)
		values := refreshValues(original.RefreshToken)
		values.Set("scope", "models:read")
		narrow, err := s.Exchange(context.Background(), values.Encode(), SenderBinding{})
		require.NoError(t, err)
		require.NotEqual(t, original.AccessToken, narrow.AccessToken)
		require.NotEqual(t, original.RefreshToken, narrow.RefreshToken)
		require.Equal(t, "models:read", narrow.Scope)
		require.Equal(t, int64(42), verify(t, s, narrow.AccessToken).UserID)
		_, err = s.ValidateAccess(context.Background(), AccessRequest{Token: narrow.AccessToken, Resource: testResource, RequiredScopes: []string{"relay:invoke"}})
		expectProtocol(t, err, "invalid_token")
		escalation := refreshValues(narrow.RefreshToken)
		escalation.Set("scope", "models:read relay:invoke")
		_, err = s.Exchange(context.Background(), escalation.Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_scope")
		next, err := s.Exchange(context.Background(), refreshValues(narrow.RefreshToken).Encode(), SenderBinding{})
		require.NoError(t, err)
		require.Equal(t, "models:read", next.Scope)
		// The public client ID is not an authentication secret. A foreign client
		// cannot use a known refresh value to revoke this family via replay.
		foreign := refreshValues(original.RefreshToken)
		foreign.Set("client_id", "other-native")
		_, err = s.Exchange(context.Background(), foreign.Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		verify(t, s, next.AccessToken)
		_, err = s.Exchange(context.Background(), refreshValues(original.RefreshToken).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		for _, tokens := range []*TokenResponse{original, narrow, next} {
			expectRevoked(t, s, tokens)
		}
		var family model.OAuthServerGrant
		require.NoError(t, db.Take(&family).Error)
		require.Equal(t, "refresh_reuse", family.RevocationReason)
	})
}

func TestRefreshBindingFailures(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		tokens, _ := issueTokens(t, s)
		for _, tc := range []struct{ key, value, code string }{
			{"client_id", "other-native", "invalid_grant"}, {"resource", "https://evil.test/v1", "invalid_grant"},
			{"scope", "admin", "invalid_scope"}, {"scope", "models:read models:read", "invalid_scope"},
			{"code", "incompatible", "invalid_request"}, {"code_verifier", testVerifier, "invalid_request"},
			{"redirect_uri", testRedirect, "invalid_request"}, {"user_id", "43", "invalid_request"},
			{"refresh_token", tokens.AccessToken, "invalid_grant"}, {"client_assertion", "jwt", "invalid_request"},
		} {
			values := refreshValues(tokens.RefreshToken)
			values.Set(tc.key, tc.value)
			_, err := s.Exchange(context.Background(), values.Encode(), SenderBinding{})
			expectProtocol(t, err, tc.code)
		}
		for key, value := range refreshValues(tokens.RefreshToken) {
			_, err := s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode()+"&"+key+"="+url.QueryEscape(value[0]), SenderBinding{})
			expectProtocol(t, err, "invalid_request")
		}
		values := refreshValues(tokens.RefreshToken)
		values.Del("resource")
		_, err := s.Exchange(context.Background(), values.Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		rotated, err := s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
		require.NoError(t, err)
		verify(t, s, rotated.AccessToken)
	})
}

func TestCodeAccessIdleAndAbsoluteExpiry(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, clock, _ := testServer(t, db)
		flow := approveFlow(t, s)
		clock.advance(CodeTTL)
		_, err := s.Exchange(context.Background(), codeValues(flow.code).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		tokens, _ := issueTokens(t, s)
		clock.advance(AccessTTL - time.Millisecond)
		verify(t, s, tokens.AccessToken)
		clock.advance(time.Millisecond)
		_, err = s.ValidateAccess(context.Background(), AccessRequest{Token: tokens.AccessToken, Resource: testResource})
		expectProtocol(t, err, "invalid_token")
		clock.advance(DefaultRefreshIdleTTL - AccessTTL)
		_, err = s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")

		tokens, _ = issueTokens(t, s)
		started := clock.now()
		for range 4 {
			clock.advance(6 * 24 * time.Hour)
			tokens, err = s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
			require.NoError(t, err)
		}
		clock.advance(6*24*time.Hour - time.Minute)
		tokens, err = s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
		require.NoError(t, err)
		require.Equal(t, int64(60), tokens.ExpiresIn)
		grant := verify(t, s, tokens.AccessToken)
		var family model.OAuthServerGrant
		require.NoError(t, db.Where("id = ?", grant.FamilyID).Take(&family).Error)
		require.Equal(t, started.Add(DefaultRefreshAbsoluteTTL).UnixMilli(), family.AbsoluteExpiresAtMs)
		clock.advance(time.Minute)
		expectRevoked(t, s, tokens)
	})
}

func TestExpiredRefreshTombstoneStillRevokesLiveDescendants(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, clock, _ := testServer(t, db)
		original, _ := issueTokens(t, s)
		clock.advance(6 * 24 * time.Hour)
		child, err := s.Exchange(context.Background(), refreshValues(original.RefreshToken).Encode(), SenderBinding{})
		require.NoError(t, err)
		clock.advance(2 * 24 * time.Hour)
		grandchild, err := s.Exchange(context.Background(), refreshValues(child.RefreshToken).Encode(), SenderBinding{})
		require.NoError(t, err)
		verify(t, s, grandchild.AccessToken)
		_, err = s.Exchange(context.Background(), refreshValues(original.RefreshToken).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		expectRevoked(t, s, grandchild)
	})
}

func TestPolicyIsRecheckedWithoutSubjectOrScopeUpgrade(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, policy := testServer(t, db)
		tokens, _ := issueTokens(t, s)
		policy.hook = func(grant Grant) error {
			require.Equal(t, int64(42), grant.UserID)
			grant.UserID = 99
			grant.Scopes[0] = "admin"
			return nil
		}
		grant := verify(t, s, tokens.AccessToken)
		require.Equal(t, int64(42), grant.UserID)
		require.Equal(t, []string{"models:read", "relay:invoke"}, grant.Scopes)
		policy.hook = nil
		flow := approveFlow(t, s)
		policy.deny.Store(true)
		_, err := s.ValidateAccess(context.Background(), AccessRequest{Token: tokens.AccessToken, Resource: testResource})
		expectProtocol(t, err, "invalid_token")
		_, err = s.Exchange(context.Background(), codeValues(flow.code).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		_, err = s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
		expectProtocol(t, err, "invalid_grant")
		pending, err := s.BeginAuthorization(context.Background(), authorizationValues().Encode(), testBrowser)
		require.NoError(t, err)
		_, err = s.TrustedPrepareConsent(context.Background(), pending.Transaction, testBrowser, 42)
		expectProtocol(t, err, "access_denied")
	})
}

func TestRevokeBothKindsAndUnknownForeignTokens(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		for _, kind := range []string{"access", "refresh"} {
			tokens, _ := issueTokens(t, s)
			token := tokens.AccessToken
			if kind == "refresh" {
				token = tokens.RefreshToken
			}
			foreign := url.Values{"client_id": {"other-native"}, "token": {token}}
			require.NoError(t, s.Revoke(context.Background(), foreign.Encode()))
			verify(t, s, tokens.AccessToken)
			for _, unknown := range []string{"not-a-token", accessPrefix + strings.Repeat("A", 43)} {
				require.NoError(t, s.Revoke(context.Background(), url.Values{"client_id": {"pi-native"}, "token": {unknown}}.Encode()))
			}
			// Deliberately wrong and unrecognized hints do not bypass lookup.
			raw := url.Values{"client_id": {"pi-native"}, "token": {token}, "token_type_hint": {"an-unknown-extension"}}.Encode()
			require.NoError(t, s.Revoke(context.Background(), raw))
			require.NoError(t, s.Revoke(context.Background(), raw))
			expectRevoked(t, s, tokens)
		}
		for _, raw := range []string{"client_id=pi-native", "client_id=pi-native&token=a&token=b", "client_id=pi-native&token=a&client_secret=secret"} {
			expectProtocol(t, s.Revoke(context.Background(), raw), "invalid_request")
		}
		expectProtocol(t, s.Revoke(context.Background(), "client_id=unknown&token=a"), "invalid_client")
	})
}

func TestSecretsStoredOnlyAsSHA256Digests(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		tokens, flow := issueTokens(t, s)
		rotated, err := s.Exchange(context.Background(), refreshValues(tokens.RefreshToken).Encode(), SenderBinding{})
		require.NoError(t, err)
		var pending []model.OAuthServerAuthorization
		var codes []model.OAuthServerCode
		var families []model.OAuthServerGrant
		var rows []model.OAuthServerToken
		require.NoError(t, db.Find(&pending).Error)
		require.NoError(t, db.Find(&codes).Error)
		require.NoError(t, db.Find(&families).Error)
		require.NoError(t, db.Find(&rows).Error)
		require.Len(t, rows, 4)
		dump, err := json.Marshal([]any{pending, codes, families, rows})
		require.NoError(t, err)
		for _, secret := range []string{tokens.AccessToken, tokens.RefreshToken, rotated.AccessToken, rotated.RefreshToken, flow.code, flow.pending.Transaction, flow.consent.Secret, testBrowser, testVerifier} {
			require.NotContains(t, string(dump), secret)
		}
		require.Equal(t, digest(flow.pending.Transaction), pending[0].Digest)
		require.Equal(t, digest(flow.consent.Secret), pending[0].ConsentDigest)
		require.Equal(t, browserDigest(testBrowser), pending[0].BrowserDigest)
		require.Equal(t, digest(flow.code), codes[0].Digest)
		seen := make(map[string]bool)
		for _, token := range rows {
			require.Regexp(t, "^[0-9a-f]{64}$", token.Digest)
			seen[token.Digest] = true
		}
		for _, secret := range []string{tokens.AccessToken, tokens.RefreshToken, rotated.AccessToken, rotated.RefreshToken} {
			require.True(t, seen[digest(secret)])
		}
	})
}
