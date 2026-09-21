package oauthserver

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAuthorizationErrorRedirectNeedsValidatedClientAndURI(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		values := authorizationValues()
		values.Set("scope", "admin")
		_, err := s.BeginAuthorization(context.Background(), values.Encode(), testBrowser)
		expectProtocol(t, err, "invalid_scope")
		var protocol *ProtocolError
		require.ErrorAs(t, err, &protocol)
		u, err := url.Parse(protocol.RedirectURI)
		require.NoError(t, err)
		require.Equal(t, "http://127.0.0.1:49213/oauth/callback", u.Scheme+"://"+u.Host+u.Path)
		require.Equal(t, url.Values{"error": {"invalid_scope"}, "iss": {testIssuer}, "state": {testState}}, u.Query())
		encoded, err := json.Marshal(protocol)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "127.0.0.1")
		for _, change := range []func(url.Values){
			func(v url.Values) { v.Set("client_id", "unknown") },
			func(v url.Values) { v.Set("redirect_uri", "https://evil.test/callback") },
			func(v url.Values) { v.Add("redirect_uri", testRedirect) },
			func(v url.Values) { v.Add("client_id", "other-native") },
		} {
			values := authorizationValues()
			change(values)
			_, err := s.BeginAuthorization(context.Background(), values.Encode(), testBrowser)
			require.ErrorAs(t, err, &protocol)
			require.Empty(t, protocol.RedirectURI)
		}
	})
}

func TestReturnedConsentDataCannotMutateAuthorizationAndStateIsEscaped(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		values := authorizationValues()
		state := "random-state&iss=https://evil.test/&code=attacker"
		values.Set("state", state)
		pending, err := s.BeginAuthorization(context.Background(), values.Encode(), testBrowser)
		require.NoError(t, err)
		consent, err := s.TrustedPrepareConsent(context.Background(), pending.Transaction, testBrowser, 42)
		require.NoError(t, err)
		consent.UserID = 99
		consent.ClientID = "other-native"
		consent.Scopes[0] = "admin"
		consent.Resource = "https://evil.test/v1"
		consent.RedirectURI = "https://evil.test/callback"
		response, err := s.TrustedApprove(context.Background(), pending.Transaction, testBrowser, consent.Secret)
		require.NoError(t, err)
		u, err := url.Parse(response.RedirectURI)
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1:49213", u.Host)
		require.Equal(t, state, u.Query().Get("state"))
		require.Equal(t, []string{testIssuer}, u.Query()["iss"])
		require.Len(t, u.Query()["code"], 1)
		tokens, err := s.Exchange(context.Background(), codeValues(u.Query().Get("code")).Encode(), SenderBinding{})
		require.NoError(t, err)
		grant := verify(t, s, tokens.AccessToken)
		require.Equal(t, int64(42), grant.UserID)
		require.Equal(t, "pi-native", grant.ClientID)
		require.Equal(t, testResource, grant.Resource)
		require.Equal(t, []string{"models:read", "relay:invoke"}, grant.Scopes)
	})
}
