package oauthserver_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCodewhaleClientLifecycleAndIsolation(t *testing.T) {
	oauthserver.ForTestDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s := productionPolicyFixture(t, db, false)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		query, err := url.ParseQuery(productionPolicyQuery(s))
		require.NoError(t, err)
		query.Set("client_id", service.OAuthCodewhaleClientID)
		query.Set("scope", strings.Join([]string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthUsageScope, service.OAuthInvokeScope}, " "))
		user := &model.User{Group: "default"}
		expanded, groups, err := s.ConsentQuery(query.Encode(), user)
		require.NoError(t, err)
		require.Equal(t, []string{"default"}, groups)
		pending, err := s.Core.BeginAuthorization(ctx, expanded, policyBrowser)
		require.NoError(t, err)
		consent, err := s.Core.TrustedPrepareConsent(ctx, pending.Transaction, policyBrowser, 42)
		require.NoError(t, err)
		approved, err := s.Core.TrustedApprove(ctx, consent.Transaction, policyBrowser, consent.Secret)
		require.NoError(t, err)
		redirect, err := url.Parse(approved.RedirectURI)
		require.NoError(t, err)
		form := url.Values{"grant_type": {"authorization_code"}, "client_id": {service.OAuthCodewhaleClientID}, "code": {redirect.Query().Get("code")}, "redirect_uri": {policyRedirect}, "code_verifier": {policyVerifier}, "resource": {s.Resource}}
		tokens, err := s.Core.Exchange(ctx, form.Encode(), oauthserver.SenderBinding{})
		require.NoError(t, err)
		grant, err := s.Core.ValidateAccess(ctx, oauthserver.AccessRequest{Token: tokens.AccessToken, Resource: s.Resource, RequiredScopes: []string{service.OAuthInvokeScope}})
		require.NoError(t, err)
		require.Equal(t, service.OAuthCodewhaleClientID, grant.ClientID)
		for _, scope := range []string{service.OAuthMCPBountiesScope, service.OAuthMCPDrawingScope, service.OAuthMarketDiscoverScope, service.OAuthMarketManageScope} {
			_, err = s.Core.ValidateAccess(ctx, oauthserver.AccessRequest{Token: tokens.AccessToken, Resource: s.Resource, RequiredScopes: []string{scope}})
			require.Error(t, err)
		}
		refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {service.OAuthCodewhaleClientID}, "refresh_token": {tokens.RefreshToken}, "resource": {s.Resource}}
		rotated, err := s.Core.Exchange(ctx, refresh.Encode(), oauthserver.SenderBinding{})
		require.NoError(t, err)
		require.NotEqual(t, tokens.RefreshToken, rotated.RefreshToken)
		grant, err = s.Core.ValidateAccess(ctx, oauthserver.AccessRequest{Token: rotated.AccessToken, Resource: s.Resource, RequiredScopes: []string{service.OAuthUsageScope}})
		require.NoError(t, err)
		require.Equal(t, service.OAuthCodewhaleClientID, grant.ClientID)
		refresh.Set("client_id", service.OAuthPiClientID)
		refresh.Set("refresh_token", rotated.RefreshToken)
		_, err = s.Core.Exchange(ctx, refresh.Encode(), oauthserver.SenderBinding{})
		require.Error(t, err, "Pi must not refresh Codewhale credentials")
	})
}

func TestCodewhaleConsentRejectsUnsupportedScopes(t *testing.T) {
	oauthserver.ForTestDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s := productionPolicyFixture(t, db, false)
		base := "catalog:read balance:read usage:read models:invoke"
		for _, scope := range []string{base + " mcp:bounties mcp:drawing", base + " market:discover", "catalog:read balance:read", base + " group:" + service.OAuthGroupID("default")} {
			query := url.Values{"client_id": {service.OAuthCodewhaleClientID}, "scope": {scope}}
			_, _, err := s.ConsentQuery(query.Encode(), &model.User{Group: "default"})
			require.Error(t, err)
		}
	})
}
