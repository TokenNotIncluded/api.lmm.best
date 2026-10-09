package router

import (
	"context"
	"encoding/json"
	"fmt"
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
	for _, callback := range []string{"http://127.0.0.1:35679/callback/codex-test", "http://[::1]:35679/callback/codex-test", "http://localhost:35679/callback/codex-test", "https://client.example/callback/mcp"} {
		for _, codeOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/code_only=%v", callback, codeOnly), func(t *testing.T) { testOAuthMCPBrowserFlow(t, callback, codeOnly, false) })
		}
	}
}

func TestOAuthMCPDiscoveryOnlyLoginCanRequestScopedStepUp(t *testing.T) {
	testOAuthMCPBrowserFlow(t, "https://client.example/callback/mcp", false, true)
}

func testOAuthMCPBrowserFlow(t *testing.T, callback string, codeOnly, discoveryOnly bool) {
	h := setupOAuthHTTP(t)
	// Dynamic lookup must not acquire a second connection inside core's tx.
	sqlDB, err := h.db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, h.db.Model(&model.User{}).Where("id = ?", h.user.Id).Update("role", common.RoleCommonUser).Error)
	grants := `["authorization_code","refresh_token"]`
	if codeOnly {
		grants = `["authorization_code"]`
	}
	response := h.request("POST", "/api/oauth2/register", `{"client_name":"Codex","redirect_uris":["`+callback+`"],"token_endpoint_auth_method":"none","grant_types":`+grants+`,"response_types":["code"]}`, map[string]string{"Content-Type": "application/json"})
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
	if discoveryOnly {
		query.Set("scope", service.OAuthMarketDiscoverScope)
	}
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
	// Consent must return only to the exact authorized callback, never an
	// origin or path alias, for both browser and native clients.
	match := regexp.MustCompile(`<a[^>]*href="(` + regexp.QuoteMeta(callback) + `\?[^"]+)"`).FindStringSubmatch(response.Body.String())
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
	if codeOnly {
		require.Empty(t, token.RefreshToken)
		require.NotContains(t, response.Body.String(), "refresh_token")
		require.Equal(t, []string{"authorization_code"}, client.GrantTypes)
		var refreshRows int64
		require.NoError(t, h.db.Model(&model.OAuthServerToken{}).Where("kind = ?", "refresh").Count(&refreshRows).Error)
		require.Zero(t, refreshRows)
		attempt := url.Values{"client_id": {client.ClientID}, "grant_type": {"refresh_token"}, "refresh_token": {"not-issued"}, "resource": {h.integration.MarketResource()}}
		rejected := h.request("POST", "/api/oauth2/token", attempt.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
		require.Equal(t, 400, rejected.Code)
		require.Contains(t, rejected.Body.String(), "unauthorized_client")
	} else {
		require.NotEmpty(t, token.RefreshToken)
	}
	// The actual gateway must expose metamcp immediately after OAuth login,
	// without first buying or installing any published tool.
	SetToolMarketMCPRouter(h.engine)
	response = h.request("POST", "/mcp/market?mode=compact", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`, map[string]string{"Authorization": "Bearer " + token.AccessToken, "Content-Type": "application/json", "Accept": "application/json, text/event-stream", "MCP-Protocol-Version": "2026-07-28"})
	require.Equal(t, 200, response.Code, response.Body.String())
	var list struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	require.Len(t, list.Result.Tools, 1)
	require.Equal(t, "metamcp", list.Result.Tools[0].Name)
	if discoveryOnly {
		for action, argument := range map[string]string{
			"load":   `{"action":"load","tool_id":"fixture-tool","version_id":"fixture-version"}`,
			"invoke": `{"action":"invoke","tool_id":"fixture-tool","version_id":"fixture-version","request_id":"fixture-request","arguments":{}}`,
		} {
			response = h.request("POST", "/mcp/market?mode=compact", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}},"name":"metamcp","arguments":`+argument+`}}`, map[string]string{"Authorization": "Bearer " + token.AccessToken, "Content-Type": "application/json", "Accept": "application/json, text/event-stream", "MCP-Protocol-Version": "2026-07-28"})
			require.Equal(t, 403, response.Code, response.Body.String())
			require.Contains(t, response.Header().Get("WWW-Authenticate"), `, error="insufficient_scope"`)
			require.Contains(t, response.Header().Get("WWW-Authenticate"), `, scope="`)
			needed := service.OAuthMarketManageScope
			if action == "invoke" {
				needed = service.OAuthMarketInvokeScope
			}
			require.Contains(t, response.Header().Get("WWW-Authenticate"), needed)
		}
		grant, _, err := h.integration.ValidateMarketResource(context.Background(), token.AccessToken)
		require.NoError(t, err)
		require.Equal(t, []string{service.OAuthMarketDiscoverScope}, grant.Scopes, "a challenge must not silently expand access")
		return
	}
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
	for _, callback := range []string{"http://attacker.example/callback", "https://attacker.example/callback?next=x", "http://127.0.0.1.evil.example/callback", "http://user@127.0.0.1:1234/callback", "http://127.0.0.1:1234/callback?next=x", "http://127.0.0.1:1234/%2e%2e/x", "http://127.0.0.1:1234/../x", "http://127.0.0.1:01234/callback", "http://127.0.0.1:65536/callback", "http://127.0.0.1:1234/a//b", "http://127.0.0.1:1234/a!b"} {
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

func TestOAuthMCPRegistrationStrictJSONAndApplicationTypes(t *testing.T) {
	for index, body := range []string{
		`{"client_name":"First","client_name":"Second","redirect_uris":["http://127.0.0.1/callback"]}`,
		`{"client_name":"First","CLIENT_NAME":"Second","redirect_uris":["http://127.0.0.1/callback"]}`,
		`{"redirect_uris":["http://127.0.0.1/callback"],"grant_types":null}`,
		`{"redirect_uris":["http://127.0.0.1/callback"],"application_type":"web"}`,
		`{"redirect_uris":["https://client.example/callback"],"application_type":"native"}`,
		`{"redirect_uris":["http://127.0.0.1/callback","https://client.example/callback"]}`,
		`{"redirect_uris":["http://127.0.0.1/callback"],"grant_types":["authorization_code","authorization_code"]}`,
		`[]`, `null`, `{} {}`,
	} {
		t.Run(fmt.Sprintf("invalid-%d", index), func(t *testing.T) {
			h := setupOAuthHTTP(t)
			response := h.request("POST", "/api/oauth2/register", body, map[string]string{"Content-Type": "application/json"})
			require.Equal(t, 400, response.Code, body)
		})
	}
	h := setupOAuthHTTP(t)
	// Extra standard fields are data only and never trigger outbound requests.
	response := h.request("POST", "/api/oauth2/register", `{"redirect_uris":["https://client.example/callback"],"application_type":"web","logo_uri":"http://127.0.0.1/private","client_uri":"https://untrusted.example"}`, map[string]string{"Content-Type": "application/json"})
	require.Equal(t, 201, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"application_type":"web"`)
	require.NotContains(t, response.Body.String(), "logo_uri")
}

func TestOAuthMCPPublicCORSDoesNotExposeBrowserConsent(t *testing.T) {
	h := setupOAuthHTTP(t)
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource/mcp/market", "/api/oauth2/register", "/api/oauth2/token", "/api/oauth2/revoke"} {
		response := h.request("OPTIONS", path, "", map[string]string{"Origin": "https://client.example", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type"})
		require.Equal(t, 204, response.Code, path)
		require.Equal(t, "*", response.Header().Get("Access-Control-Allow-Origin"))
		require.Empty(t, response.Header().Get("Access-Control-Allow-Credentials"))
	}
	response := h.request("GET", "/.well-known/oauth-protected-resource/mcp/market", "", map[string]string{"Origin": "https://client.example"})
	var metadata struct {
		Scopes []string `json:"scopes_supported"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &metadata))
	require.Equal(t, service.OAuthMarketScopes(), metadata.Scopes)
	response = h.request("GET", "/api/oauth2/authorize?"+h.query, "", map[string]string{"Origin": "https://client.example"})
	require.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
	SetToolMarketMCPRouter(h.engine)
	response = h.request("GET", "/mcp/market", "", nil)
	require.Equal(t, 401, response.Code)
	require.Contains(t, response.Header().Get("WWW-Authenticate"), "resource_metadata=")
	require.Contains(t, response.Header().Get("Access-Control-Expose-Headers"), "WWW-Authenticate")
	// A valid model-only grant is not an invalid token: request more scope,
	// but never silently turn it into marketplace access.
	token, _ := h.approve(t)
	response = h.request("POST", "/mcp/market?mode=compact", `{}`, map[string]string{"Authorization": "Bearer " + token.AccessToken})
	require.Equal(t, 403, response.Code, response.Body.String())
	require.Contains(t, response.Header().Get("WWW-Authenticate"), `error="insufficient_scope"`)
}
