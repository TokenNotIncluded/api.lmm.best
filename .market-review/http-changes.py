from pathlib import Path
p=Path('.')
def edit(f,a,b):
 q=p/f;s=q.read_text();assert s.count(a)==1,(f,s.count(a),a[:90]);q.write_text(s.replace(a,b))
f='apps/api-go/controller/tool_market_meta_mcp.go'
s=(p/f).read_text();start=s.index('\tserver.AddTool(&mcp.Tool{',s.index('func addToolMarketMetaMCP'));end=s.index(', func(ctx context.Context',start)
definition=s[start+len('\tserver.AddTool('):end]
s=s[:start]+'\tserver.AddTool(toolMarketMetaDefinition()'+s[end:]
pos=s.index('func addToolMarketMetaMCP')
s=s[:pos]+'func toolMarketMetaDefinition() *mcp.Tool {\n\treturn '+definition+'\n}\n\n'+s[pos:];(p/f).write_text(s)
edit('apps/api-go/controller/tool_market_remote.go','"mcp_oauth": service.CurrentOAuthIntegration() != nil}', '"mcp_oauth": service.CurrentOAuthIntegration() != nil, "metamcp": true}')
edit('apps/api-go/controller/tool_market_remote.go','\tdata["provider_presets"] = marketprovider.Presets()', '''	// This tool is part of the gateway, not a published/paid service that needs
	// installation. Use its actual protocol descriptor so UI/schema cannot drift.
	data["meta_tool"] = toolMarketMetaDefinition()
	data["provider_presets"] = marketprovider.Presets()''')
f='apps/api-go/controller/oauth_server_mcp.go'
edit(f,'import (\n','import (\n\t"bytes"\n\t"strings"\n\t"unicode/utf8"\n')
edit(f,'c.GetHeader("Authorization") != "" {', 'len(c.Request.Header.Values("Authorization")) != 0 || service.OAuthAlternateCredentials(c.Request) {')
edit(f, '''	decoder := json.NewDecoder(c.Request.Body)
	var input service.MCPClientRegistration
	var trailing any
	if decoder.Decode(&input) != nil || decoder.Decode(&trailing) != io.EOF {''','''	input, err := decodeMCPClientRegistration(c.Request.Body)
	if err != nil {''')
with (p/f).open('a') as out:out.write('''
// Unknown standard metadata is ignored, not fetched or treated as identity.
// Reject duplicate/case-alias keys and null policy fields before unmarshalling.
func decodeMCPClientRegistration(body io.Reader) (service.MCPClientRegistration, error) {
	var input service.MCPClientRegistration
	raw, err := io.ReadAll(io.LimitReader(body, (16<<10)+1))
	if err != nil || len(raw) > 16<<10 || !utf8.Valid(raw) {
		return input, service.ErrMCPRegistration
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return input, service.ErrMCPRegistration
	}
	known := map[string]bool{"client_name": true, "application_type": true, "redirect_uris": true, "token_endpoint_auth_method": true, "grant_types": true, "response_types": true, "scope": true}
	seen := map[string]bool{}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		folded := strings.ToLower(key)
		if err != nil || !ok || seen[folded] || (known[folded] && key != folded) {
			return input, service.ErrMCPRegistration
		}
		seen[folded] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return input, service.ErrMCPRegistration
		}
		if known[key] {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return input, service.ErrMCPRegistration
			}
			fields[key] = value
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return input, service.ErrMCPRegistration
	}
	if _, err := decoder.Token(); err != io.EOF {
		return input, service.ErrMCPRegistration
	}
	filtered, err := json.Marshal(fields)
	if err != nil || json.Unmarshal(filtered, &input) != nil {
		return input, service.ErrMCPRegistration
	}
	return input, nil
}

// Public OAuth endpoints use no cookies. Browser clients may read discovery
// and exchange their own PKCE codes; consent/login routes never use this CORS
// policy and retain same-origin authentication and CSRF checks.
func OAuthPublicClientCORS(c *gin.Context) {
	service.MCPPublicClientHeaders(c.Writer.Header())
	if c.Request.Method == http.MethodOptions {
		c.AbortWithStatus(http.StatusNoContent)
		return
	}
	c.Next()
}
''')
(p/'apps/api-go/service/mcp_http.go').write_text('''package service

import "net/http"

// MCPPublicClientHeaders permits browser-based public clients without giving
// cross-origin scripts access to a user's cookies or browser login session.
func MCPPublicClientHeaders(header http.Header) {
	header.Set("Access-Control-Allow-Origin", "*")
	header.Del("Access-Control-Allow-Credentials")
	header.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	header.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, MCP-Protocol-Version, MCP-Session-Id, Last-Event-ID")
	header.Set("Access-Control-Expose-Headers", "WWW-Authenticate, MCP-Protocol-Version, MCP-Session-Id")
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
}
''')
f='apps/api-go/router/oauth_server.go'
for path,guard,handler in [('/.well-known/oauth-authorization-server','discovery','Metadata'),('/.well-known/oauth-protected-resource/api/oauth2','discovery','ResourceMetadata'),('/.well-known/oauth-protected-resource/mcp/market','discovery','MarketResourceMetadata')]:
 edit(f,f'router.GET("{path}", {guard}, h.{handler})',f'router.GET("{path}", {guard}, controller.OAuthPublicClientCORS, h.{handler})\n\trouter.OPTIONS("{path}", {guard}, controller.OAuthPublicClientCORS)')
for path,guard,handler in [('/api/oauth2/register','h.Guard(10, "registration")','RegisterMCPClient'),('/api/oauth2/token','tokens','Token'),('/api/oauth2/revoke','tokens','Revoke')]:
 edit(f,f'router.POST("{path}", {guard}, h.{handler})',f'router.POST("{path}", {guard}, controller.OAuthPublicClientCORS, h.{handler})\n\trouter.OPTIONS("{path}", {guard}, controller.OAuthPublicClientCORS)')
f='apps/api-go/controller/tool_market_mcp.go'
edit(f,'\t"net/http"\n','\t"net/http"\n\t"net/url"\n\t"io"\n')
edit(f,'type marketMCPIdentity struct {','var errMarketMCPScope = errors.New("market discovery scope is required")\n\ntype marketMCPIdentity struct {')
edit(f,'integration.ValidateMarketResource(ctx, raw, service.OAuthMarketDiscoverScope)','integration.ValidateMarketResource(ctx, raw)')
edit(f, '''		invoke, manage := slices.Contains(grant.Scopes, service.OAuthMarketInvokeScope)''','''		if !slices.Contains(grant.Scopes, service.OAuthMarketDiscoverScope) {
			return marketMCPIdentity{}, errMarketMCPScope
		}
		invoke, manage := slices.Contains(grant.Scopes, service.OAuthMarketInvokeScope)''')
s=(p/f).read_text();start=s.index('func NewToolMarketMCPHandler() http.Handler {')
s=s[:start]+'''func marketMCPAuthChallenge(scopes []string, code string) string {
	var parameters []string
	if integration := service.CurrentOAuthIntegration(); integration != nil {
		parameters = append(parameters, "resource_metadata="+strconv.Quote(integration.Issuer+"/.well-known/oauth-protected-resource/mcp/market"))
	}
	if len(scopes) != 0 {
		parameters = append(parameters, "scope="+strconv.Quote(strings.Join(scopes, " ")))
	}
	if code != "" {
		parameters = append(parameters, "error="+strconv.Quote(code))
	}
	if len(parameters) == 0 {
		return "Bearer"
	}
	return "Bearer " + strings.Join(parameters, ", ")
}

// Inspect only enough of an authenticated OAuth call to request missing
// protocol scopes. This does not load tools, grant payment rights or execute
// anything. The normal SDK and action decoder remain the final validators.
func marketMCPCallScopes(raw []byte) []string {
	var rpc struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &rpc) != nil || rpc.Method != "tools/call" {
		return nil
	}
	if strings.HasPrefix(rpc.Params.Name, "market_tool_") {
		return []string{service.OAuthMarketInvokeScope}
	}
	if rpc.Params.Name == "lmm_market_load" {
		return []string{service.OAuthMarketManageScope}
	}
	if rpc.Params.Name != "metamcp" {
		return nil
	}
	input, err := decodeToolMarketMetaInput(rpc.Params.Arguments)
	if err != nil { return nil }
	switch input.Action {
	case "invoke":
		return []string{service.OAuthMarketInvokeScope}
	case "load", "unload", "set_tool_budget", "set_client_budget":
		return []string{service.OAuthMarketManageScope}
	case "authorize":
		return []string{service.OAuthMarketManageScope, service.OAuthMarketInvokeScope}
	default:
		return nil
	}
}

func NewToolMarketMCPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		service.MCPPublicClientHeaders(w.Header())
		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		values := req.Header.Values("Authorization")
		var identity marketMCPIdentity
		err := model.ErrToolMarketDenied
		if len(values) == 1 && len(values[0]) > 7 && strings.EqualFold(values[0][:7], "Bearer ") {
			identity, err = marketMCPAuthenticate(req.Context(), values[0][7:])
		}
		if err != nil {
			code := ""
			var requiredScopes []string
			status := http.StatusUnauthorized
			message := "MCP authorization required"
			if errors.Is(err, errMarketMCPScope) {
				code = "insufficient_scope"
				requiredScopes = []string{service.OAuthMarketDiscoverScope}
				status, message = http.StatusForbidden, "MCP discovery permission is required"
			} else if len(values) != 0 {
				code = "invalid_token"
			}
			w.Header().Set("WWW-Authenticate", marketMCPAuthChallenge(requiredScopes, code))
			http.Error(w, message, status)
			return
		}
		// Do not accept query tokens, duplicate mode fields or legacy credential
		// headers alongside the bearer. Identity has exactly one source.
		query, queryErr := url.ParseQuery(req.URL.RawQuery)
		mode := query.Get("mode")
		if queryErr != nil || len(query) > 1 || (len(query) == 1 && len(query["mode"]) != 1) || (mode != "" && mode != "full" && mode != "compact") || service.OAuthAlternateCredentials(req) || len(req.Header.Values(service.OAuthGroupHeader)) != 0 {
			http.Error(w, "MCP accepts one bearer token and an optional full or compact mode", http.StatusBadRequest)
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, 256<<10)
		if identity.metaSubject.CredentialKind == "oauth" && req.Method == http.MethodPost && (!identity.invoke || !identity.manage) {
			raw, readErr := io.ReadAll(req.Body)
			if readErr != nil {
				http.Error(w, "MCP request body is invalid or too large", http.StatusRequestEntityTooLarge)
				return
			}
			req.Body = io.NopCloser(bytes.NewReader(raw))
			required := marketMCPCallScopes(raw)
			missing := (!identity.invoke && slices.Contains(required, service.OAuthMarketInvokeScope)) || (!identity.manage && slices.Contains(required, service.OAuthMarketManageScope))
			if missing {
				scopes := []string{service.OAuthMarketDiscoverScope}
				if identity.invoke || slices.Contains(required, service.OAuthMarketInvokeScope) { scopes = append(scopes, service.OAuthMarketInvokeScope) }
				if identity.manage || slices.Contains(required, service.OAuthMarketManageScope) { scopes = append(scopes, service.OAuthMarketManageScope) }
				w.Header().Set("WWW-Authenticate", marketMCPAuthChallenge(scopes, "insufficient_scope"))
				http.Error(w, "MCP client needs additional tool permissions", http.StatusForbidden)
				return
			}
		}
		server, err := newToolMarketMCPServerWithMode(identity, mode == "compact")
		if err != nil {
			http.Error(w, "Tool set temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true, PropagateRequestCancellation: true})
		handler.ServeHTTP(w, req)
	})
}
'''
(p/f).write_text(s)
