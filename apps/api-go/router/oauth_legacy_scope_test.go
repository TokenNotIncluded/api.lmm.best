package router

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

func assertOAuthRequestedScopesPreserved(t *testing.T, token oauthserver.TokenResponse, requested []string) {
	t.Helper()
	granted := strings.Fields(token.Scope)
	for _, scope := range requested {
		require.Contains(t, granted, scope)
	}
	optional := []string{service.OAuthUsageScope, service.OAuthMCPBountiesScope, service.OAuthMCPDrawingScope}
	for _, scope := range optional {
		if !slices.Contains(requested, scope) {
			require.NotContains(t, granted, scope, "authorization must not widen a historical client scope set")
		}
	}
}

func TestOAuthHTTPCanonicalAndHistoricalScopeProfilesPreserveRequestedScopes(t *testing.T) {
	legacy := []string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthInvokeScope}
	current := []string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthUsageScope, service.OAuthInvokeScope}
	profiles := []struct {
		name   string
		scopes []string
	}{
		{name: "legacy without mcp", scopes: slices.Clone(legacy)},
		{name: "legacy with mcp", scopes: append(slices.Clone(legacy), service.OAuthBuiltinMCPScopes()...)},
		{name: "current without mcp", scopes: slices.Clone(current)},
		{name: "current with mcp", scopes: append(slices.Clone(current), service.OAuthBuiltinMCPScopes()...)},
	}

	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			h := setupOAuthHTTP(t)
			query, err := url.ParseQuery(h.query)
			require.NoError(t, err)
			query.Set("scope", strings.Join(profile.scopes, " "))
			h.query = query.Encode()

			token, _ := h.approve(t)
			assertOAuthRequestedScopesPreserved(t, token, profile.scopes)

			refreshForm := url.Values{
				"grant_type":    {"refresh_token"},
				"client_id":     {service.OAuthPiClientID},
				"refresh_token": {token.RefreshToken},
				"resource":      {h.integration.Resource},
			}
			response := h.request("POST", "/api/oauth2/token", refreshForm.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
			require.Equal(t, 200, response.Code, response.Body.String())
			var refreshed oauthserver.TokenResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &refreshed))
			assertOAuthRequestedScopesPreserved(t, refreshed, profile.scopes)
		})
	}
}

func TestOAuthConsentRejectsPartialOrUnknownScopeProfiles(t *testing.T) {
	h := setupOAuthHTTP(t)
	legacy := []string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthInvokeScope}
	cases := [][]string{
		append(slices.Clone(legacy), service.OAuthMCPBountiesScope),
		{service.OAuthCatalogScope, service.OAuthBalanceScope},
		{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthInvokeScope, "unknown:scope"},
	}
	for _, scopes := range cases {
		_, _, err := h.integration.ConsentQuery(url.Values{"scope": {strings.Join(scopes, " ")}}.Encode(), &h.user)
		require.ErrorIs(t, err, service.ErrOAuthDenied)
	}
}
