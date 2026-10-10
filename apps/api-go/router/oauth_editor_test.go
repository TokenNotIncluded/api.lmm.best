package router

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

var oauthEditorClients = []struct{ id, name string }{
	{service.OAuthVSCodeClientID, service.OAuthVSCodeClientName},
	{service.OAuthZedClientID, service.OAuthZedClientName},
}

func TestOAuthEditorIndependentLoginRotationAndRevocation(t *testing.T) {
	for _, client := range oauthEditorClients {
		t.Run(client.id, func(t *testing.T) {
			h := setupOAuthHTTP(t)
			pi, _ := h.approve(t)
			query, err := url.ParseQuery(h.query)
			require.NoError(t, err)
			query.Set("client_id", client.id)
			query.Set("scope", strings.Join([]string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthUsageScope, service.OAuthInvokeScope}, " "))
			h.query = query.Encode()
			page := h.request("GET", "/api/oauth2/authorize?"+h.query, "", nil)
			require.Equal(t, 200, page.Code)
			require.Contains(t, page.Body.String(), client.name)
			grant, _ := h.approve(t)
			require.ElementsMatch(t, []string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthUsageScope, service.OAuthInvokeScope, service.OAuthGroupScope("default"), service.OAuthGroupScope("vip")}, strings.Fields(grant.Scope))
			for _, scope := range []string{service.OAuthMCPBountiesScope, service.OAuthMCPDrawingScope, service.OAuthMarketDiscoverScope, service.OAuthMarketInvokeScope, service.OAuthMarketManageScope} {
				require.NotContains(t, strings.Fields(grant.Scope), scope)
				_, _, err := h.integration.ValidateResource(context.Background(), grant.AccessToken, scope)
				require.Error(t, err, "editor grant must not reach MCP or marketplace resources")
			}
			for _, route := range []string{"/api/oauth2/catalog", "/api/oauth2/balance", "/api/oauth2/usage/activity"} {
				require.Equal(t, 200, h.request("GET", route, "", map[string]string{"Authorization": "Bearer " + grant.AccessToken}).Code)
			}
			relayHeaders := map[string]string{"Authorization": "Bearer " + grant.AccessToken, service.OAuthGroupHeader: service.OAuthGroupID("default"), "Content-Type": "application/json"}
			invocation := h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o","messages":[]}`, relayHeaders)
			require.Equal(t, 200, invocation.Code, invocation.Body.String())
			var wallet model.User
			require.NoError(t, h.db.First(&wallet, h.user.Id).Error)
			require.Equal(t, 9920, wallet.Quota, "Editor model invocation must settle against the user wallet")
			relayHeaders[service.OAuthGroupHeader] = service.OAuthGroupID("future")
			require.NotEqual(t, 200, h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, relayHeaders).Code)
			form := url.Values{"grant_type": {"refresh_token"}, "client_id": {service.OAuthPiClientID}, "refresh_token": {grant.RefreshToken}, "resource": {h.integration.Resource}}
			headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
			require.NotEqual(t, 200, h.request("POST", "/api/oauth2/token", form.Encode(), headers).Code)
			for _, other := range oauthEditorClients {
				if other.id != client.id {
					form.Set("client_id", other.id)
					require.NotEqual(t, 200, h.request("POST", "/api/oauth2/token", form.Encode(), headers).Code)
				}
			}
			form.Set("client_id", client.id)
			response := h.request("POST", "/api/oauth2/token", form.Encode(), headers)
			require.Equal(t, 200, response.Code, response.Body.String())
			var rotated oauthserver.TokenResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &rotated))
			require.NotEqual(t, grant.RefreshToken, rotated.RefreshToken)
			require.Equal(t, grant.Scope, rotated.Scope)
			require.Equal(t, 200, h.request("GET", "/api/oauth2/catalog", "", map[string]string{"Authorization": "Bearer " + rotated.AccessToken}).Code)
			revoke := url.Values{"client_id": {client.id}, "token": {rotated.RefreshToken}}
			require.Equal(t, 200, h.request("POST", "/api/oauth2/revoke", revoke.Encode(), headers).Code)
			require.NotEqual(t, 200, h.request("GET", "/api/oauth2/catalog", "", map[string]string{"Authorization": "Bearer " + rotated.AccessToken}).Code)
			require.Equal(t, 200, h.request("GET", "/api/oauth2/catalog", "", map[string]string{"Authorization": "Bearer " + pi.AccessToken}).Code)
		})
	}
}

func TestOAuthEditorRejectsUnimplementedPermissions(t *testing.T) {
	for _, client := range oauthEditorClients {
		t.Run(client.id, func(t *testing.T) {
			h := setupOAuthHTTP(t)
			query, err := url.ParseQuery(h.query)
			require.NoError(t, err)
			query.Set("client_id", client.id)
			base := strings.Join([]string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthUsageScope, service.OAuthInvokeScope}, " ")
			for _, extra := range []string{service.OAuthMCPBountiesScope, service.OAuthMCPBountiesScope + " " + service.OAuthMCPDrawingScope, service.OAuthMarketDiscoverScope, service.OAuthMarketInvokeScope, service.OAuthMarketManageScope, "account:write", "applications:write"} {
				query.Set("scope", base+" "+extra)
				_, _, err := h.integration.ConsentQuery(query.Encode(), &h.user)
				require.ErrorIs(t, err, service.ErrOAuthDenied)
				require.NotEqual(t, 200, h.request("GET", "/api/oauth2/authorize?"+query.Encode(), "", nil).Code)
			}
			query.Set("scope", service.OAuthCatalogScope+" "+service.OAuthBalanceScope+" "+service.OAuthInvokeScope)
			_, _, err = h.integration.ConsentQuery(query.Encode(), &h.user)
			require.ErrorIs(t, err, service.ErrOAuthDenied, "new editor clients have no historical permission profiles")
			// Even bypassing consent expansion cannot authorize forbidden scopes.
			query.Set("scope", base+" "+service.OAuthGroupScope("default")+" "+service.OAuthMarketInvokeScope)
			require.NotEqual(t, 200, h.request("GET", "/api/oauth2/authorize?"+query.Encode(), "", nil).Code)
		})
	}
}
