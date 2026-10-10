package router

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

func TestOAuthOpenCodeIndependentLoginRotationAndRevocation(t *testing.T) {
	h := setupOAuthHTTP(t)
	pi, _ := h.approve(t)
	query, err := url.ParseQuery(h.query)
	require.NoError(t, err)
	query.Set("client_id", service.OAuthOpenCodeClientID)
	query.Set("scope", strings.Join([]string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthUsageScope, service.OAuthInvokeScope}, " "))
	h.query = query.Encode()
	page := h.request("GET", "/api/oauth2/authorize?"+h.query, "", nil)
	require.Equal(t, 200, page.Code)
	require.Contains(t, page.Body.String(), service.OAuthOpenCodeClientName)
	grant, _ := h.approve(t)
	require.Contains(t, grant.Scope, service.OAuthInvokeScope)
	require.NotContains(t, grant.Scope, service.OAuthMCPBountiesScope)
	require.NotContains(t, grant.Scope, service.OAuthMarketManageScope)
	relayHeaders := map[string]string{"Authorization": "Bearer " + grant.AccessToken, service.OAuthGroupHeader: service.OAuthGroupID("default"), "Content-Type": "application/json"}
	invocation := h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o","messages":[]}`, relayHeaders)
	require.Equal(t, 200, invocation.Code, invocation.Body.String())
	var wallet model.User
	require.NoError(t, h.db.First(&wallet, h.user.Id).Error)
	require.Equal(t, 9920, wallet.Quota, "OpenCode model invocation must settle against the user wallet")
	relayHeaders[service.OAuthGroupHeader] = service.OAuthGroupID("future")
	require.NotEqual(t, 200, h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, relayHeaders).Code)
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {service.OAuthPiClientID}, "refresh_token": {grant.RefreshToken}, "resource": {h.integration.Resource}}
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	require.NotEqual(t, 200, h.request("POST", "/api/oauth2/token", form.Encode(), headers).Code)
	form.Set("client_id", service.OAuthOpenCodeClientID)
	response := h.request("POST", "/api/oauth2/token", form.Encode(), headers)
	require.Equal(t, 200, response.Code, response.Body.String())
	var rotated oauthserver.TokenResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &rotated))
	require.NotEqual(t, grant.RefreshToken, rotated.RefreshToken)
	require.Equal(t, grant.Scope, rotated.Scope)
	require.Equal(t, 200, h.request("GET", "/api/oauth2/catalog", "", map[string]string{"Authorization": "Bearer " + rotated.AccessToken}).Code)
	revoke := url.Values{"client_id": {service.OAuthOpenCodeClientID}, "token": {rotated.RefreshToken}}
	require.Equal(t, 200, h.request("POST", "/api/oauth2/revoke", revoke.Encode(), headers).Code)
	require.NotEqual(t, 200, h.request("GET", "/api/oauth2/catalog", "", map[string]string{"Authorization": "Bearer " + rotated.AccessToken}).Code)
	require.Equal(t, 200, h.request("GET", "/api/oauth2/catalog", "", map[string]string{"Authorization": "Bearer " + pi.AccessToken}).Code)
}

func TestOAuthOpenCodeRejectsUnimplementedPermissions(t *testing.T) {
	h := setupOAuthHTTP(t)
	query, err := url.ParseQuery(h.query)
	require.NoError(t, err)
	query.Set("client_id", service.OAuthOpenCodeClientID)
	base := strings.Join([]string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthUsageScope, service.OAuthInvokeScope}, " ")
	for _, extra := range []string{service.OAuthMCPBountiesScope, service.OAuthMCPBountiesScope + " " + service.OAuthMCPDrawingScope, service.OAuthMarketDiscoverScope, "account:write"} {
		query.Set("scope", base+" "+extra)
		_, _, err := h.integration.ConsentQuery(query.Encode(), &h.user)
		require.ErrorIs(t, err, service.ErrOAuthDenied)
		require.NotEqual(t, 200, h.request("GET", "/api/oauth2/authorize?"+query.Encode(), "", nil).Code)
	}
}
