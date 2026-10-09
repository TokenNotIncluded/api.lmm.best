package router

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

func TestOAuthMCPRegistrationAndBrowserFlow(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "[::1]"} {
		t.Run(host, func(t *testing.T) { testOAuthMCPBrowserFlow(t, host) })
	}
}

func testOAuthMCPBrowserFlow(t *testing.T, host string) {
	h := setupOAuthHTTP(t)
	// Dynamic lookup must not acquire a second connection inside core's tx.
	sqlDB, err := h.db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, h.db.Model(&model.User{}).Where("id = ?", h.user.Id).Update("role", common.RoleCommonUser).Error)
	callback := "http://" + host + ":35679/callback/codex-test"
	response := h.request("POST", "/api/oauth2/register", `{"client_name":"Codex","redirect_uris":["`+callback+`"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`, map[string]string{"Content-Type": "application/json"})
	require.Equal(t, 201, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "client_secret")
	var client service.MCPRegisteredClient
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &client))
	require.True(t, strings.HasPrefix(client.ClientID, "lmm-mcp-"))
	metadata := h.request("GET", "/.well-known/oauth-authorization-server", "", nil)
	require.Contains(t, metadata.Body.String(), oauthTestIssuer+"/api/oauth2/register")
	metadata = h.request("GET", "/.well-known/oauth-protected-resource/mcp/market", "", nil)
	require.Equal(t, 200, metadata.Code)
	require.Contains(t, metadata.Body.String(), h.integration.MarketResource())
	require.NotContains(t, metadata.Body.String(), service.OAuthInvokeScope)

	query, err := url.ParseQuery(h.query)
	require.NoError(t, err)
	query.Set("client_id", client.ClientID)
	query.Set("redirect_uri", callback)
	query.Del("resource")
	query.Del("scope")
	h.query = query.Encode()
	cookie, csrf := h.begin(t)
	response = h.request("POST", "/api/user/auth/oauth2/continue", url.Values{"csrf": {csrf}}.Encode(), map[string]string{"Origin": oauthTestIssuer, "Content-Type": "application/x-www-form-urlencoded"}, cookie, &http.Cookie{Name: service.RefreshCookieName, Value: h.login.RefreshToken})
	bound, secret := oauthFormState(t, response)
	require.Contains(t, response.Body.String(), "External MCP: Codex")
	require.Contains(t, response.Body.String(), h.integration.MarketResource())
	require.NotContains(t, response.Body.String(), "<li>vip</li>")
	require.NotEqual(t, cookie.Value, bound.Value)
	response = h.request("POST", "/api/user/auth/oauth2/consent", url.Values{"csrf": {secret}, "decision": {"allow"}}.Encode(), map[string]string{"Origin": oauthTestIssuer, "Content-Type": "application/x-www-form-urlencoded"}, bound, &http.Cookie{Name: service.RefreshCookieName, Value: h.login.RefreshToken})
	require.Equal(t, 200, response.Code, response.Body.String())
	// The older editor fixture matches only IPv4. Inspect both supported
	// address families, then require the exact registered callback below.
	match := regexp.MustCompile(`<a[^>]*href="(http://(?:127\.0\.0\.1|\[::1\]):[^"]+)"`).FindStringSubmatch(response.Body.String())
	require.Len(t, match, 2)
	redirect, err := url.Parse(html.UnescapeString(match[1]))
	require.NoError(t, err)
	target := *redirect
	target.RawQuery = ""
	require.Equal(t, callback, target.String())
	require.Equal(t, oauthTestIssuer, redirect.Query().Get("iss"))
	require.Equal(t, query.Get("state"), redirect.Query().Get("state"))
	require.NotEmpty(t, redirect.Query().Get("code"))
	form := url.Values{"client_id": {client.ClientID}, "grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}, "redirect_uri": {callback}, "resource": {h.integration.MarketResource()}, "code_verifier": {h.verifier}}
	response = h.request("POST", "/api/oauth2/token", form.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	require.Equal(t, 200, response.Code, response.Body.String())
	var token oauthserver.TokenResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &token))
	grant, user, err := h.integration.ValidateMarketResource(context.Background(), token.AccessToken, service.OAuthMarketDiscoverScope)
	require.NoError(t, err)
	require.Equal(t, h.user.Id, user.Id)
	require.Equal(t, h.integration.MarketResource(), grant.Resource)
	require.ElementsMatch(t, service.OAuthMarketScopes(), grant.Scopes)
	_, _, err = h.integration.ValidateResource(context.Background(), token.AccessToken, service.OAuthCatalogScope)
	require.Error(t, err)
	// Logging in grants no wallet hold or paid-tool permission.
	var after model.User
	require.NoError(t, h.db.First(&after, h.user.Id).Error)
	require.Equal(t, h.user.Quota, after.Quota)
	clients, err := model.ListToolMarketOAuthClients(h.db, h.user.Id, h.integration.Issuer, h.integration.Resource, []string{service.OAuthPiClientID, service.OAuthDshClientID}, service.OAuthMarketScopes(), 0, 100, h.integration.MarketResource())
	require.NoError(t, err)
	require.Equal(t, []model.ToolMarketOAuthClient{{ClientID: "oauth:" + client.ClientID}}, clients)
	subjects, err := model.ToolMarketMetaOAuthSubjects(h.user.Id, "oauth:"+client.ClientID, h.integration.Issuer, h.integration.Resource, h.integration.MarketResource())
	require.NoError(t, err)
	require.Len(t, subjects, 1)
	require.Equal(t, h.integration.MarketResource(), subjects[0].OAuthResource)
	// A different resource cannot refresh this token into model permissions.
	wrong := url.Values{"client_id": {client.ClientID}, "grant_type": {"refresh_token"}, "refresh_token": {token.RefreshToken}, "resource": {h.integration.Resource}}
	response = h.request("POST", "/api/oauth2/token", wrong.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	require.NotEqual(t, 200, response.Code)
	// Disabling the user immediately blocks the marketplace credential.
	require.NoError(t, h.db.Model(&model.User{}).Where("id = ?", h.user.Id).Update("status", common.UserStatusDisabled).Error)
	_, _, err = h.integration.ValidateMarketResource(context.Background(), token.AccessToken, service.OAuthMarketDiscoverScope)
	require.Error(t, err)
}

func TestOAuthMCPRegistrationBoundaries(t *testing.T) {
	h := setupOAuthHTTP(t)
	ctx := context.Background()
	valid := service.MCPClientRegistration{ClientName: "Codex", RedirectURIs: []string{"http://127.0.0.1:32123/callback/instance"}}
	first, err := h.integration.RegisterMCPClient(ctx, valid)
	require.NoError(t, err)
	changedPort := valid
	changedPort.RedirectURIs = []string{"http://127.0.0.1:54321/callback/instance"}
	repeated, err := h.integration.RegisterMCPClient(ctx, changedPort)
	require.NoError(t, err)
	require.Equal(t, first.ClientID, repeated.ClientID)
	for _, callback := range []string{"https://attacker.example/callback", "http://localhost:1234/callback", "http://127.0.0.1.evil.example/callback", "http://user@127.0.0.1:1234/callback", "http://127.0.0.1:1234/callback?next=x", "http://127.0.0.1:1234/%2e%2e/x", "http://127.0.0.1:1234/../x", "http://127.0.0.1:01234/callback", "http://127.0.0.1:65536/callback", "http://127.0.0.1:1234/a//b", "http://127.0.0.1:1234/a!b"} {
		input := valid
		input.RedirectURIs = []string{callback}
		_, err := h.integration.RegisterMCPClient(ctx, input)
		require.ErrorIs(t, err, service.ErrMCPRegistration, callback)
	}
	for _, scope := range []string{"models:invoke", "market:discover group:ZGVmYXVsdA", "market:invoke", "market:discover market:discover"} {
		input := valid
		input.Scope = scope
		_, err := h.integration.RegisterMCPClient(ctx, input)
		require.ErrorIs(t, err, service.ErrMCPRegistration, scope)
	}
	for _, method := range []string{"client_secret_basic", "client_secret_post", "private_key_jwt"} {
		input := valid
		input.TokenEndpointAuthMethod = method
		_, err := h.integration.RegisterMCPClient(ctx, input)
		require.ErrorIs(t, err, service.ErrMCPRegistration)
	}
	input := valid
	input.GrantTypes = []string{"client_credentials"}
	_, err = h.integration.RegisterMCPClient(ctx, input)
	require.ErrorIs(t, err, service.ErrMCPRegistration)
	input = valid
	input.ResponseTypes = []string{"token"}
	_, err = h.integration.RegisterMCPClient(ctx, input)
	require.ErrorIs(t, err, service.ErrMCPRegistration)
	require.NoError(t, h.db.Model(&model.OAuthServerMCPRegistry{}).Where("issuer = ?", h.integration.Issuer).Update("count", 4096).Error)
	_, err = h.integration.RegisterMCPClient(ctx, valid)
	require.NoError(t, err, "a known registration still works at capacity")
	input = valid
	input.ClientName = "another client"
	_, err = h.integration.RegisterMCPClient(ctx, input)
	require.ErrorIs(t, err, service.ErrMCPRegistryFull)
	var count int64
	require.NoError(t, h.db.Model(&model.OAuthServerMCPClient{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	response := h.request("POST", "/api/oauth2/register", strings.Repeat("x", 17000), map[string]string{"Content-Type": "application/json"})
	require.Equal(t, 400, response.Code)
	response = h.request("POST", "/api/oauth2/register", `{} {}`, map[string]string{"Content-Type": "application/json"})
	require.Equal(t, 400, response.Code)
}
