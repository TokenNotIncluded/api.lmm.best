package router

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

func TestOAuthCLIConsentOnlyDisplaysRequestedPermissions(t *testing.T) {
	h := setupOAuthHTTP(t)
	h.query = cliOAuthQuery(t, h).Encode()
	cookie, csrf := h.begin(t)
	response := h.request("POST", "/api/user/auth/oauth2/continue", url.Values{"csrf": {csrf}}.Encode(), map[string]string{"Origin": oauthTestIssuer, "Content-Type": "application/x-www-form-urlencoded"}, cookie, &http.Cookie{Name: service.RefreshCookieName, Value: h.login.RefreshToken})
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), "Authorize LMM CLI")
	require.Contains(t, response.Body.String(), "Read available models")
	require.NotContains(t, response.Body.String(), "Call models in these groups")
}

func cliOAuthQuery(t *testing.T, h *oauthHTTPTest) url.Values {
	t.Helper()
	query, err := url.ParseQuery(h.query)
	require.NoError(t, err)
	query.Set("client_id", service.OAuthCLIClientID)
	query.Set("scope", service.OAuthCatalogScope+" "+service.OAuthBalanceScope)
	return query
}

func TestOAuthCLIReadOnlyLoginRefreshAndRevocation(t *testing.T) {
	h := setupOAuthHTTP(t)
	// A different application's grant must survive CLI logout.
	pi, _ := h.approve(t)
	h.query = cliOAuthQuery(t, h).Encode()
	cli, _ := h.approve(t)
	granted := strings.Fields(cli.Scope)
	require.Contains(t, granted, service.OAuthCatalogScope)
	require.Contains(t, granted, service.OAuthBalanceScope)
	require.NotContains(t, granted, service.OAuthInvokeScope)
	require.NotContains(t, granted, service.OAuthUsageScope)
	for _, scope := range service.OAuthBuiltinMCPScopes() {
		require.NotContains(t, granted, scope)
	}
	auth := map[string]string{"Authorization": "Bearer " + cli.AccessToken}
	require.Equal(t, 200, h.request("GET", "/api/oauth2/catalog", "", auth).Code)
	require.Equal(t, 200, h.request("GET", "/api/oauth2/balance", "", auth).Code)
	require.NotEqual(t, 200, h.request("GET", "/api/oauth2/usage/activity", "", auth).Code)
	relay := map[string]string{"Authorization": "Bearer " + cli.AccessToken, service.OAuthGroupHeader: service.OAuthGroupID("default"), "Content-Type": "application/json"}
	require.NotEqual(t, 200, h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, relay).Code)
	var wallet model.User
	require.NoError(t, h.db.First(&wallet, h.user.Id).Error)
	require.Equal(t, h.user.Quota, wallet.Quota, "read-only CLI grant cannot spend wallet quota")
	var bindings int64
	require.NoError(t, h.db.Model(&model.OAuthBillingBinding{}).Count(&bindings).Error)
	require.Zero(t, bindings, "denied invocation must not create managed billing credentials")
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {service.OAuthCLIClientID}, "refresh_token": {cli.RefreshToken}, "resource": {h.integration.Resource}}
	response := h.request("POST", "/api/oauth2/token", form.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	require.Equal(t, 200, response.Code, response.Body.String())
	var rotated oauthserver.TokenResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &rotated))
	require.NotEqual(t, cli.RefreshToken, rotated.RefreshToken)
	require.Equal(t, cli.Scope, rotated.Scope)
	revoke := url.Values{"client_id": {service.OAuthCLIClientID}, "token": {rotated.RefreshToken}}
	require.Equal(t, 200, h.request("POST", "/api/oauth2/revoke", revoke.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}).Code)
	require.NotEqual(t, 200, h.request("GET", "/api/oauth2/catalog", "", map[string]string{"Authorization": "Bearer " + rotated.AccessToken}).Code)
	require.Equal(t, 200, h.request("GET", "/api/oauth2/catalog", "", map[string]string{"Authorization": "Bearer " + pi.AccessToken}).Code)
}

func TestOAuthCLIRejectsApplicationAndAdministrativeScopes(t *testing.T) {
	h := setupOAuthHTTP(t)
	for _, extra := range []string{service.OAuthInvokeScope, service.OAuthUsageScope, service.OAuthMCPBountiesScope, "account:write", "applications:write"} {
		query := cliOAuthQuery(t, h)
		query.Set("scope", query.Get("scope")+" "+extra)
		_, _, err := h.integration.ConsentQuery(query.Encode(), &h.user)
		require.ErrorIs(t, err, service.ErrOAuthDenied)
		require.NotEqual(t, 200, h.request("GET", "/api/oauth2/authorize?"+query.Encode(), "", nil).Code)
	}
}

func TestOAuthCLICannotRefreshAnotherClientsGrant(t *testing.T) {
	h := setupOAuthHTTP(t)
	pi, _ := h.approve(t)
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {service.OAuthCLIClientID}, "refresh_token": {pi.RefreshToken}, "resource": {h.integration.Resource}}
	require.NotEqual(t, 200, h.request("POST", "/api/oauth2/token", form.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}).Code)
	require.Equal(t, 200, h.request("GET", "/api/oauth2/catalog", "", map[string]string{"Authorization": "Bearer " + pi.AccessToken}).Code)
}
