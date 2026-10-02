package router

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

// Use real TLS requests, the browser cookie jar and a real loopback callback.
// The public Host stays bound to the configured issuer while the test transport
// connects only to its own TLS listener; no production account is involved.
type oauthEditorNetwork struct {
	h        *oauthHTTPTest
	server   *httptest.Server
	callback *httptest.Server
	client   *http.Client
	clientID string
	query    url.Values
}

type oauthEditorNetworkResponse struct {
	status int
	body   string
	header http.Header
}

func newOAuthEditorNetwork(t *testing.T, clientID string) *oauthEditorNetwork {
	t.Helper()
	h := setupOAuthHTTP(t)
	server := httptest.NewTLSServer(h.engine)
	t.Cleanup(server.Close)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/oauth/lmm/callback" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.URL.Query())
	}))
	t.Cleanup(callback.Close)
	client := server.Client()
	client.Timeout = 10 * time.Second
	// Keep both the URL and Host at the configured issuer. Cookie-jar lookup
	// differed across Go versions for a listener URL with an overridden Host.
	// Route that one logical origin to this fixture and verify its TLS certificate
	// against the listener name; no request can reach an external server.
	listenerURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	issuerURL, err := url.Parse(oauthTestIssuer)
	require.NoError(t, err)
	issuerAddress := net.JoinHostPort(issuerURL.Hostname(), "443")
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = listenerURL.Hostname()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != issuerAddress {
			return nil, errors.New("OAuth TLS fixture cannot contact an external origin")
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, listenerURL.Host)
	}
	client.Transport = transport
	t.Cleanup(client.CloseIdleConnections)
	client.Jar, err = cookiejar.New(nil)
	require.NoError(t, err)
	query, err := url.ParseQuery(h.query)
	require.NoError(t, err)
	query.Set("client_id", clientID)
	query.Set("scope", strings.Join([]string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthUsageScope, service.OAuthInvokeScope}, " "))
	query.Set("redirect_uri", callback.URL+"/oauth/lmm/callback")
	return &oauthEditorNetwork{h: h, server: server, callback: callback, client: client, clientID: clientID, query: query}
}

func (n *oauthEditorNetwork) request(t *testing.T, method, path, body string, headers map[string]string) oauthEditorNetworkResponse {
	t.Helper()
	request, err := http.NewRequest(method, oauthTestIssuer+path, strings.NewReader(body))
	require.NoError(t, err)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	if method == http.MethodPost {
		if request.Header.Get("Content-Type") == "" {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		request.Header.Set("Origin", oauthTestIssuer)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	response, err := n.client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	bytes, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	require.NoError(t, err)
	if !strings.HasPrefix(path, "/v1/") {
		require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	}
	return oauthEditorNetworkResponse{status: response.StatusCode, body: string(bytes), header: response.Header}
}

func (n *oauthEditorNetwork) loginCookie(t *testing.T, refresh string) {
	t.Helper()
	target, err := url.Parse(oauthTestIssuer)
	require.NoError(t, err)
	n.client.Jar.SetCookies(target, []*http.Cookie{{Name: service.RefreshCookieName, Value: refresh, Path: "/api/user/auth", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}})
}

func oauthEditorNetworkCSRF(t *testing.T, response oauthEditorNetworkResponse) string {
	t.Helper()
	require.Equal(t, http.StatusOK, response.status)
	require.Equal(t, "strict-origin", response.header.Get("Referrer-Policy"))
	match := oauthCSRFFixture.FindStringSubmatch(response.body)
	require.Len(t, match, 2)
	return html.UnescapeString(match[1])
}

func (n *oauthEditorNetwork) consent(t *testing.T) string {
	t.Helper()
	csrf := oauthEditorNetworkCSRF(t, n.request(t, http.MethodGet, "/api/oauth2/authorize?"+n.query.Encode(), "", nil))
	n.loginCookie(t, n.h.login.RefreshToken)
	response := n.request(t, http.MethodPost, "/api/user/auth/oauth2/continue", url.Values{"csrf": {csrf}}.Encode(), nil)
	require.True(t, strings.Contains(response.body, "oauth-user"), "the browser's authenticated account must reach consent")
	return oauthEditorNetworkCSRF(t, response)
}

func (n *oauthEditorNetwork) callbackTarget(t *testing.T, decision string) *url.URL {
	t.Helper()
	csrf := n.consent(t)
	response := n.request(t, http.MethodPost, "/api/user/auth/oauth2/consent", url.Values{"csrf": {csrf}, "decision": {decision}}.Encode(), nil)
	require.Equal(t, http.StatusOK, response.status)
	application := map[string]string{service.OAuthPiClientID: "Pi", service.OAuthDshClientID: "DSH", service.OAuthCLIClientID: "LMM CLI", service.OAuthCodewhaleClientID: "Codewhale", service.OAuthOpenCodeClientID: "OpenCode", service.OAuthVSCodeClientID: "VS Code", service.OAuthZedClientID: "Zed"}[n.clientID]
	require.True(t, strings.Contains(response.body, "Return to "+application), "completion page must return to the requesting application")
	match := oauthRedirectFixture.FindStringSubmatch(response.body)
	require.Len(t, match, 2)
	target, err := url.Parse(html.UnescapeString(match[1]))
	require.NoError(t, err)
	require.Equal(t, n.query.Get("redirect_uri"), target.Scheme+"://"+target.Host+target.Path)
	return target
}

func (n *oauthEditorNetwork) callbackCode(t *testing.T, decision string) url.Values {
	t.Helper()
	target := n.callbackTarget(t, decision)
	callbackResponse, err := n.callback.Client().Get(target.String())
	require.NoError(t, err)
	defer callbackResponse.Body.Close()
	require.Equal(t, http.StatusOK, callbackResponse.StatusCode)
	var callback url.Values
	require.NoError(t, json.NewDecoder(callbackResponse.Body).Decode(&callback))
	require.Equal(t, oauthTestIssuer, callback.Get("iss"))
	require.Equal(t, n.query.Get("state"), callback.Get("state"))
	return callback
}

func (n *oauthEditorNetwork) exchangeForm(code string) url.Values {
	return url.Values{"grant_type": {"authorization_code"}, "client_id": {n.clientID}, "code": {code}, "redirect_uri": {n.query.Get("redirect_uri")}, "resource": {n.h.integration.Resource}, "code_verifier": {n.h.verifier}}
}

func (n *oauthEditorNetwork) publicClient(t *testing.T) {
	t.Helper()
	// Native token and resource requests must never carry dashboard cookies.
	n.client.Jar = nil
}

func oauthEditorNetworkTokens(t *testing.T, response oauthEditorNetworkResponse) oauthserver.TokenResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, response.status)
	var tokens oauthserver.TokenResponse
	require.NoError(t, json.Unmarshal([]byte(response.body), &tokens))
	require.NotEmpty(t, tokens.AccessToken)
	require.NotEmpty(t, tokens.RefreshToken)
	return tokens
}

func TestOAuthEditorNetworkLoginRefreshAndRevocation(t *testing.T) {
	for _, client := range oauthEditorClients {
		t.Run(client.id, func(t *testing.T) {
			n := newOAuthEditorNetwork(t, client.id)
			callback := n.callbackCode(t, "allow")
			require.NotEmpty(t, callback.Get("code"))
			n.publicClient(t)
			tokens := oauthEditorNetworkTokens(t, n.request(t, http.MethodPost, "/api/oauth2/token", n.exchangeForm(callback.Get("code")).Encode(), nil))
			for _, scope := range []string{service.OAuthMCPBountiesScope, service.OAuthMCPDrawingScope, service.OAuthMarketDiscoverScope, service.OAuthMarketInvokeScope, service.OAuthMarketManageScope} {
				require.NotContains(t, strings.Fields(tokens.Scope), scope)
			}
			for _, path := range []string{"/api/oauth2/catalog", "/api/oauth2/balance", "/api/oauth2/usage/activity"} {
				require.Equal(t, http.StatusOK, n.request(t, http.MethodGet, path, "", map[string]string{"Authorization": "Bearer " + tokens.AccessToken}).status)
			}
			invoked := n.request(t, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o","messages":[]}`, map[string]string{"Authorization": "Bearer " + tokens.AccessToken, service.OAuthGroupHeader: service.OAuthGroupID("default"), "Content-Type": "application/json"})
			require.Equal(t, http.StatusOK, invoked.status)
			var charged model.User
			require.NoError(t, n.h.db.First(&charged, n.h.user.Id).Error)
			require.Equal(t, 9920, charged.Quota, "the real HTTP model route must settle the fixture wallet")
			refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {client.id}, "refresh_token": {tokens.RefreshToken}, "resource": {n.h.integration.Resource}}
			wrong := maps.Clone(refresh)
			wrong.Set("client_id", service.OAuthPiClientID)
			require.Equal(t, http.StatusBadRequest, n.request(t, http.MethodPost, "/api/oauth2/token", wrong.Encode(), nil).status)
			wrong = maps.Clone(refresh)
			wrong.Set("resource", oauthTestIssuer+"/wrong")
			require.Equal(t, http.StatusBadRequest, n.request(t, http.MethodPost, "/api/oauth2/token", wrong.Encode(), nil).status)
			rotated := oauthEditorNetworkTokens(t, n.request(t, http.MethodPost, "/api/oauth2/token", refresh.Encode(), nil))
			require.NotEqual(t, tokens.RefreshToken, rotated.RefreshToken)
			require.Equal(t, tokens.Scope, rotated.Scope)
			require.Equal(t, http.StatusOK, n.request(t, http.MethodPost, "/api/oauth2/revoke", url.Values{"client_id": {client.id}, "token": {rotated.RefreshToken}}.Encode(), nil).status)
			require.Equal(t, http.StatusUnauthorized, n.request(t, http.MethodGet, "/api/oauth2/catalog", "", map[string]string{"Authorization": "Bearer " + rotated.AccessToken}).status)
		})
	}
}

func TestOAuthEditorNetworkCodeBindingAndExpiry(t *testing.T) {
	for _, client := range oauthEditorClients {
		for _, mutation := range []string{"pkce", "redirect-port", "redirect-path", "other-editor", "pi-client", "resource", "expired-code", "replay"} {
			t.Run(client.id+"/"+mutation, func(t *testing.T) {
				n := newOAuthEditorNetwork(t, client.id)
				callback := n.callbackCode(t, "allow")
				n.publicClient(t)
				form := n.exchangeForm(callback.Get("code"))
				var replayed oauthserver.TokenResponse
				switch mutation {
				case "pkce":
					form.Set("code_verifier", strings.Repeat("x", 64))
				case "redirect-port":
					form.Set("redirect_uri", "http://127.0.0.1:1/oauth/lmm/callback")
				case "redirect-path":
					form.Set("redirect_uri", n.callback.URL+"/wrong")
				case "other-editor":
					other := service.OAuthVSCodeClientID
					if client.id == other {
						other = service.OAuthZedClientID
					}
					form.Set("client_id", other)
				case "pi-client":
					form.Set("client_id", service.OAuthPiClientID)
				case "resource":
					form.Set("resource", oauthTestIssuer+"/wrong")
				case "expired-code":
					require.NoError(t, n.h.db.Model(&model.OAuthServerCode{}).Where("issuer = ?", oauthTestIssuer).Update("expires_at_ms", time.Now().Add(-time.Second).UnixMilli()).Error)
				case "replay":
					replayed = oauthEditorNetworkTokens(t, n.request(t, http.MethodPost, "/api/oauth2/token", form.Encode(), nil))
				}
				response := n.request(t, http.MethodPost, "/api/oauth2/token", form.Encode(), nil)
				require.Equal(t, http.StatusBadRequest, response.status)
				var rejected map[string]any
				require.NoError(t, json.Unmarshal([]byte(response.body), &rejected))
				require.Equal(t, "invalid_grant", rejected["error"])
				require.NotContains(t, rejected, "access_token")
				if mutation == "replay" {
					require.Equal(t, http.StatusUnauthorized, n.request(t, http.MethodGet, "/api/oauth2/catalog", "", map[string]string{"Authorization": "Bearer " + replayed.AccessToken}).status)
				}
				if mutation != "expired-code" && mutation != "replay" {
					// An attacker with the wrong binding cannot consume the code.
					_ = oauthEditorNetworkTokens(t, n.request(t, http.MethodPost, "/api/oauth2/token", n.exchangeForm(callback.Get("code")).Encode(), nil))
				}
			})
		}
	}
}

func TestOAuthEditorNetworkConsentIdentityAndExpiry(t *testing.T) {
	for _, client := range oauthEditorClients {
		for _, mutation := range []string{"account-switch", "session-version", "auth-version", "expired-flow", "deny"} {
			t.Run(client.id+"/"+mutation, func(t *testing.T) {
				n := newOAuthEditorNetwork(t, client.id)
				if mutation == "deny" {
					callback := n.callbackCode(t, "deny")
					require.Equal(t, "access_denied", callback.Get("error"))
					require.Empty(t, callback.Get("code"))
				} else {
					csrf := n.consent(t)
					switch mutation {
					case "account-switch":
						n.loginCookie(t, n.h.otherLogin.RefreshToken)
					case "session-version":
						require.NoError(t, n.h.db.Model(&model.UserSession{}).Where("sid = ?", n.h.login.Session.SID).Update("version", 2).Error)
					case "auth-version":
						require.NoError(t, n.h.db.Model(&model.User{}).Where("id = ?", n.h.user.Id).Update("auth_version", 2).Error)
					case "expired-flow":
						require.NoError(t, n.h.db.Model(&model.OAuthServerAuthorization{}).Where("issuer = ?", oauthTestIssuer).Update("expires_at_ms", time.Now().Add(-time.Second).UnixMilli()).Error)
					}
					response := n.request(t, http.MethodPost, "/api/user/auth/oauth2/consent", url.Values{"csrf": {csrf}, "decision": {"allow"}}.Encode(), nil)
					require.Equal(t, http.StatusBadRequest, response.status)
					require.Empty(t, oauthRedirectFixture.FindStringSubmatch(response.body))
				}
				var grants int64
				require.NoError(t, n.h.db.Model(&model.OAuthServerGrant{}).Count(&grants).Error)
				require.Zero(t, grants)
			})
		}
	}
}

func TestOAuthEditorNetworkReauthorizationPreservesCodeRedirect(t *testing.T) {
	for _, client := range oauthEditorClients {
		t.Run(client.id, func(t *testing.T) {
			n := newOAuthEditorNetwork(t, client.id)
			first := n.callbackCode(t, "allow")
			firstForm := n.exchangeForm(first.Get("code"))
			// A second outstanding sign-in uses a different native callback port.
			// Its approval must not rewrite the first code's exact redirect.
			n.query.Set("redirect_uri", "http://127.0.0.1:1/oauth/lmm/callback")
			second := n.callbackTarget(t, "allow")
			n.publicClient(t)
			wrong := n.exchangeForm(first.Get("code"))
			require.Equal(t, http.StatusBadRequest, n.request(t, http.MethodPost, "/api/oauth2/token", wrong.Encode(), nil).status)
			_ = oauthEditorNetworkTokens(t, n.request(t, http.MethodPost, "/api/oauth2/token", firstForm.Encode(), nil))
			_ = oauthEditorNetworkTokens(t, n.request(t, http.MethodPost, "/api/oauth2/token", n.exchangeForm(second.Query().Get("code")).Encode(), nil))
		})
	}
}

func TestOAuthNetworkReauthorizationDoesNotExpandConsentScopes(t *testing.T) {
	n := newOAuthEditorNetwork(t, service.OAuthPiClientID)
	base := n.query.Get("scope")
	n.query.Set("scope", base+" "+strings.Join(service.OAuthBuiltinMCPScopes(), " "))
	first := n.callbackCode(t, "allow")
	// Keep both codes pending so the same grant family is reused while each
	// retains the exact set of permissions shown in its own consent page.
	n.query.Set("scope", base)
	second := n.callbackCode(t, "allow")
	n.publicClient(t)
	broad := oauthEditorNetworkTokens(t, n.request(t, http.MethodPost, "/api/oauth2/token", n.exchangeForm(first.Get("code")).Encode(), nil))
	narrow := oauthEditorNetworkTokens(t, n.request(t, http.MethodPost, "/api/oauth2/token", n.exchangeForm(second.Get("code")).Encode(), nil))
	for _, scope := range service.OAuthBuiltinMCPScopes() {
		require.Contains(t, strings.Fields(broad.Scope), scope)
		require.NotContains(t, strings.Fields(narrow.Scope), scope)
	}
}

func TestOAuthNetworkAllNativeClientNames(t *testing.T) {
	clients := []struct{ id, application string }{
		{service.OAuthPiClientID, "Pi"},
		{service.OAuthDshClientID, "DSH"},
		{service.OAuthCLIClientID, "LMM CLI"},
		{service.OAuthCodewhaleClientID, "Codewhale"},
		{service.OAuthOpenCodeClientID, "OpenCode"},
		{service.OAuthVSCodeClientID, "VS Code"},
		{service.OAuthZedClientID, "Zed"},
	}
	for _, client := range clients {
		t.Run(client.id, func(t *testing.T) {
			n := newOAuthEditorNetwork(t, client.id)
			if client.id == service.OAuthCLIClientID {
				n.query.Set("scope", service.OAuthCatalogScope+" "+service.OAuthBalanceScope)
			}
			page := n.request(t, http.MethodGet, "/api/oauth2/authorize?"+n.query.Encode(), "", nil)
			require.Equal(t, http.StatusOK, page.status)
			require.Contains(t, page.body, "<title>Authorize "+client.application+"</title>")
			complete := n.callbackTarget(t, "allow")
			require.NotEmpty(t, complete.Query().Get("code"))
			// A fresh consent invalidated by an account change keeps its client
			// identity on the failure page instead of relabelling it as Pi.
			csrf := n.consent(t)
			n.loginCookie(t, n.h.otherLogin.RefreshToken)
			failed := n.request(t, http.MethodPost, "/api/user/auth/oauth2/consent", url.Values{"csrf": {csrf}, "decision": {"allow"}}.Encode(), nil)
			require.Equal(t, http.StatusBadRequest, failed.status)
			require.Contains(t, failed.body, "<title>Authorize "+client.application+"</title>")
			require.Contains(t, failed.body, "Start login again from "+client.application+".")
		})
	}
}

func TestOAuthNetworkLegacyServerDoesNotAdvertiseEditors(t *testing.T) {
	n := newOAuthEditorNetwork(t, service.OAuthVSCodeClientID)
	core, err := oauthserver.New(n.h.db, oauthserver.Config{Issuer: oauthTestIssuer, Clients: []oauthserver.NativeClient{{ID: service.OAuthPiClientID, Name: service.OAuthPiClientName, RedirectURIs: []string{service.OAuthNativeRedirect}, Resources: []string{n.h.integration.Resource}, Scopes: strings.Fields(n.query.Get("scope"))}}}, n.h.integration)
	require.NoError(t, err)
	n.h.integration.Core = core
	metadata := n.request(t, http.MethodGet, "/.well-known/oauth-authorization-server", "", nil)
	require.Equal(t, http.StatusOK, metadata.status)
	var discovery struct {
		Clients []string `json:"lmm_client_ids_supported"`
	}
	require.NoError(t, json.Unmarshal([]byte(metadata.body), &discovery))
	require.Equal(t, []string{service.OAuthPiClientID}, discovery.Clients)
	page := n.request(t, http.MethodGet, "/api/oauth2/authorize?"+n.query.Encode(), "", nil)
	require.Equal(t, http.StatusBadRequest, page.status)
	require.Contains(t, page.body, "not registered on this LMM server")
	require.NotContains(t, page.body, "Authorize Pi")
}
