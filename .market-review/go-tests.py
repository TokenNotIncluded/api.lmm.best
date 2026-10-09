from pathlib import Path
p=Path('.')
def edit(f,a,b):
 q=p/f;s=q.read_text();assert s.count(a)==1,(f,s.count(a),a[:110]);q.write_text(s.replace(a,b))
f='apps/api-go/router/oauth_server_mcp_test.go'
edit(f,'\t"context"\n','\t"context"\n\t"fmt"\n')
edit(f, '''	for _, host := range []string{"127.0.0.1", "[::1]"} {
		t.Run(host, func(t *testing.T) { testOAuthMCPBrowserFlow(t, host) })
	}
}

func testOAuthMCPBrowserFlow(t *testing.T, host string) {''','''	for _, callback := range []string{"http://127.0.0.1:35679/callback/codex-test", "http://[::1]:35679/callback/codex-test", "http://localhost:35679/callback/codex-test", "https://client.example/callback/mcp"} {
		for _, codeOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/code_only=%v", callback, codeOnly), func(t *testing.T) { testOAuthMCPBrowserFlow(t, callback, codeOnly, false) })
		}
	}
}

func TestOAuthMCPDiscoveryOnlyLoginCanRequestScopedStepUp(t *testing.T) {
	testOAuthMCPBrowserFlow(t, "https://client.example/callback/mcp", false, true)
}

func testOAuthMCPBrowserFlow(t *testing.T, callback string, codeOnly, discoveryOnly bool) {''')
edit(f, '''	callback := "http://" + host + ":35679/callback/codex-test"
	response := h.request("POST", "/api/oauth2/register", `{"client_name":"Codex","redirect_uris":["`+callback+`"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`, map[string]string{"Content-Type": "application/json"})''','''	grants := `["authorization_code","refresh_token"]`
	if codeOnly {
		grants = `["authorization_code"]`
	}
	response := h.request("POST", "/api/oauth2/register", `{"client_name":"Codex","redirect_uris":["`+callback+`"],"token_endpoint_auth_method":"none","grant_types":`+grants+`,"response_types":["code"]}`, map[string]string{"Content-Type": "application/json"})''')
edit(f, '''	query.Del("scope")
	h.query = query.Encode()''','''	query.Del("scope")
	if discoveryOnly { query.Set("scope", service.OAuthMarketDiscoverScope) }
	h.query = query.Encode()''')
old='''	// The older editor fixture matches only IPv4. Inspect both supported
	// address families, then require the exact registered callback below.
	match := regexp.MustCompile(`<a[^>]*href="(http://(?:127\\.0\\.0\\.1|\\[::1\\]):[^"]+)"`).FindStringSubmatch(response.Body.String())'''
new='''	// Consent must return only to the exact authorized callback, never an
	// origin or path alias, for both browser and native clients.
	match := regexp.MustCompile(`<a[^>]*href="(` + regexp.QuoteMeta(callback) + `\\?[^"]+)"`).FindStringSubmatch(response.Body.String())'''
edit(f,old,new)
edit(f, '''	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &token))
	grant, user, err :=''','''	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &token))
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
	response = h.request("POST", "/mcp/market?mode=compact", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + token.AccessToken, "Content-Type": "application/json", "Accept": "application/json, text/event-stream", "MCP-Protocol-Version": "2026-07-28"})
	require.Equal(t, 200, response.Code, response.Body.String())
	var list struct { Result struct { Tools []struct { Name string `json:"name"` } `json:"tools"` } `json:"result"` }
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	require.Len(t, list.Result.Tools, 1)
	require.Equal(t, "metamcp", list.Result.Tools[0].Name)
	if discoveryOnly {
		for action, argument := range map[string]string{
			"load": `{"action":"load","tool_id":"fixture-tool","version_id":"fixture-version"}`,
			"invoke": `{"action":"invoke","tool_id":"fixture-tool","version_id":"fixture-version","request_id":"fixture-request","arguments":{}}`,
		} {
			response = h.request("POST", "/mcp/market?mode=compact", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"metamcp","arguments":`+argument+`}}`, map[string]string{"Authorization":"Bearer "+token.AccessToken,"Content-Type":"application/json","Accept":"application/json, text/event-stream","MCP-Protocol-Version":"2026-07-28"})
			require.Equal(t, 403, response.Code, response.Body.String())
			require.Contains(t, response.Header().Get("WWW-Authenticate"), `, error="insufficient_scope"`)
			require.Contains(t, response.Header().Get("WWW-Authenticate"), `, scope="`)
			needed := service.OAuthMarketManageScope
			if action == "invoke" { needed = service.OAuthMarketInvokeScope }
			require.Contains(t, response.Header().Get("WWW-Authenticate"), needed)
		}
		grant, _, err := h.integration.ValidateMarketResource(context.Background(), token.AccessToken)
		require.NoError(t, err)
		require.Equal(t, []string{service.OAuthMarketDiscoverScope}, grant.Scopes, "a challenge must not silently expand access")
		return
	}
	grant, user, err :=''')
edit(f,'[]string{"https://attacker.example/callback", "http://localhost:1234/callback", "http://127.0.0.1.evil.example/callback"','[]string{"http://attacker.example/callback", "https://attacker.example/callback?next=x", "http://127.0.0.1.evil.example/callback"')
f='apps/api-go/router/tool_market_capabilities_test.go'
edit(f,'\t\t\tCapabilities map[string]bool `json:"capabilities"`','\t\t\tCapabilities map[string]bool `json:"capabilities"`\n\t\t\tMetaTool struct { Name string `json:"name"`; Schema map[string]any `json:"inputSchema"` } `json:"meta_tool"`')
edit(f,'require.Len(t, body.Data.Capabilities, 4)','''require.Len(t, body.Data.Capabilities, 5)
	require.True(t, body.Data.Capabilities["metamcp"])
	require.Equal(t, "metamcp", body.Data.MetaTool.Name)
	require.NotEmpty(t, body.Data.MetaTool.Schema["oneOf"])''')
with (p/'apps/api-go/oauthserver/native_redirect_test.go').open('a') as out:out.write('''
func TestMCPRedirectPolicyIsExplicitAndExactForWebClients(t *testing.T) {
	for _, callback := range []string{"https://client.example/callback", "http://localhost:3210/callback"} {
		client := NativeClient{ID: "public-mcp", Name: "External MCP", RedirectURIs: []string{callback}, Resources: []string{"https://issuer.example/mcp/market"}, Scopes: []string{"market:discover"}, MCPRedirects: true}
		if validateClient(client) != nil || !validRedirect(callback, client) {
			t.Fatal("registered MCP callback rejected", callback)
		}
		for _, other := range []string{callback + "/", callback + "?next=1", callback + "#fragment", "https://other.example/callback", "https://client.example:443/callback", "http://localhost:3211/callback", "http://127.0.0.1:3210/callback"} {
			if validRedirect(other, client) { t.Errorf("accepted callback alias %q for %q", other, callback) }
		}
		client.MCPRedirects = false
		if validateClient(client) == nil || validRedirect(callback, client) { t.Fatal("MCP policy leaked into legacy native clients") }
	}
	for _, bad := range []string{"https://client.example", "http://client.example/callback", "https://user@client.example/callback", "https://client.example/%2e%2e/callback", "https://client.example/../callback", "https://127.0.0.1/callback", "http://localhost.evil.example/callback", "http://localhost:0/callback", "http://localhost:0123/callback", "http://localhost:65536/callback", "http://localhost:/callback", "http://LOCALHOST:123/callback"} {
		if _, ok := MCPRedirectTemplate(bad); ok { t.Errorf("accepted unsafe callback %q", bad) }
	}
}
''')
with (p/'apps/api-go/router/oauth_server_mcp_test.go').open('a') as out:out.write('''
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
			response := h.request("POST", "/api/oauth2/register", body, map[string]string{"Content-Type":"application/json"})
			require.Equal(t, 400, response.Code, body)
		})
	}
	h := setupOAuthHTTP(t)
	// Extra standard fields are data only and never trigger outbound requests.
	response := h.request("POST", "/api/oauth2/register", `{"redirect_uris":["https://client.example/callback"],"application_type":"web","logo_uri":"http://127.0.0.1/private","client_uri":"https://untrusted.example"}`, map[string]string{"Content-Type":"application/json"})
	require.Equal(t, 201, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"application_type":"web"`)
	require.NotContains(t, response.Body.String(), "logo_uri")
}

func TestOAuthMCPPublicCORSDoesNotExposeBrowserConsent(t *testing.T) {
	h := setupOAuthHTTP(t)
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource/mcp/market", "/api/oauth2/register", "/api/oauth2/token", "/api/oauth2/revoke"} {
		response := h.request("OPTIONS", path, "", map[string]string{"Origin":"https://client.example", "Access-Control-Request-Method":"POST", "Access-Control-Request-Headers":"content-type"})
		require.Equal(t, 204, response.Code, path)
		require.Equal(t, "*", response.Header().Get("Access-Control-Allow-Origin"))
		require.Empty(t, response.Header().Get("Access-Control-Allow-Credentials"))
	}
	response := h.request("GET", "/.well-known/oauth-protected-resource/mcp/market", "", map[string]string{"Origin":"https://client.example"})
	var metadata struct { Scopes []string `json:"scopes_supported"` }
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &metadata))
	require.Equal(t, service.OAuthMarketScopes(), metadata.Scopes)
	response = h.request("GET", "/api/oauth2/authorize?"+h.query, "", map[string]string{"Origin":"https://client.example"})
	require.Empty(t, response.Header().Get("Access-Control-Allow-Origin"))
	SetToolMarketMCPRouter(h.engine)
	response = h.request("GET", "/mcp/market", "", nil)
	require.Equal(t, 401, response.Code)
	require.Contains(t, response.Header().Get("WWW-Authenticate"), "resource_metadata=")
	require.Contains(t, response.Header().Get("Access-Control-Expose-Headers"), "WWW-Authenticate")
	// A valid model-only grant is not an invalid token: request more scope,
	// but never silently turn it into marketplace access.
	token, _ := h.approve(t)
	response = h.request("POST", "/mcp/market?mode=compact", `{}`, map[string]string{"Authorization":"Bearer "+token.AccessToken})
	require.Equal(t, 403, response.Code, response.Body.String())
	require.Contains(t, response.Header().Get("WWW-Authenticate"), `error="insufficient_scope"`)
}
''')
with (p/'apps/api-go/controller/tool_market_meta_discovery_test.go').open('a') as out:out.write('''
func TestToolMarketMetaHTTPRejectsAmbiguousCredentialsAndModes(t *testing.T) {
	_, user := setupToolMarketBuiltinControllerTest(t)
	token, _, err := model.CreateToolMarketToken(user.Id, "http-boundaries", false, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	for _, query := range []string{"?mode=compact&mode=full", "?mode=compact&token=ignored", "?access_token=ignored", "?mode=%XX", "?mode=unknown"} {
		request := httptest.NewRequest(http.MethodPost, "/mcp/market"+query, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		NewToolMarketMCPHandler().ServeHTTP(response, request)
		require.Equal(t, 400, response.Code, query)
	}
	for _, header := range []string{"Cookie", "X-Api-Key", "X-Goog-Api-Key", "New-Api-User"} {
		request := httptest.NewRequest(http.MethodPost, "/mcp/market?mode=compact", strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set(header, "ignored")
		response := httptest.NewRecorder()
		NewToolMarketMCPHandler().ServeHTTP(response, request)
		require.Equal(t, 400, response.Code, header)
	}
	request := httptest.NewRequest(http.MethodOptions, "/mcp/market", nil)
	request.Header.Set("Origin", "https://client.example")
	response := httptest.NewRecorder()
	NewToolMarketMCPHandler().ServeHTTP(response, request)
	require.Equal(t, 204, response.Code)
	require.Empty(t, response.Header().Get("Access-Control-Allow-Credentials"))
}

func TestToolMarketMetaDescriptorIsSharedWithProtocol(t *testing.T) {
	implementation, err := newToolMarketMCPServerWithMode(marketMCPIdentity{}, true)
	require.NoError(t, err)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := implementation.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name:"descriptor-check", Version:"1"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer cs.Close()
	list, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, list.Tools, 1)
	actual, err := json.Marshal(list.Tools[0])
	require.NoError(t, err)
	expected, err := json.Marshal(toolMarketMetaDefinition())
	require.NoError(t, err)
	require.JSONEq(t, string(expected), string(actual))
}
''')
with (p/'apps/api-go/oauthserver/token_test.go').open('a') as out:out.write('''
// Code-only clients must not receive or retain an unusable refresh credential.
func TestOAuthCodeOnlyClientPersistsOnlyAccessTokens(t *testing.T) {
	forDatabases(t, func(t *testing.T, db, _ *gorm.DB) {
		s, _, _ := testServer(t, db)
		client := s.clients["pi-native"]
		client.RefreshDisabled = true
		s.clients[client.ID] = client
		tokens, _ := issueTokens(t, s)
		require.Empty(t, tokens.RefreshToken)
		raw, err := json.Marshal(tokens)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "refresh_token")
		grant := verify(t, s, tokens.AccessToken)
		var rows []model.OAuthServerToken
		require.NoError(t, db.Where("family_id = ?", grant.FamilyID).Find(&rows).Error)
		require.Len(t, rows, 1)
		require.Equal(t, "access", rows[0].Kind)
		_, err = s.Exchange(context.Background(), refreshValues(tokens.AccessToken).Encode(), SenderBinding{})
		expectProtocol(t, err, "unauthorized_client")
		verify(t, s, tokens.AccessToken)
		require.NoError(t, s.Revoke(context.Background(), url.Values{"client_id": {client.ID}, "token": {tokens.AccessToken}}.Encode()))
		_, err = s.ValidateAccess(context.Background(), AccessRequest{Token: tokens.AccessToken, Resource: testResource})
		expectProtocol(t, err, "invalid_token")
	})
}
''')
