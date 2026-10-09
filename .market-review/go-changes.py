from pathlib import Path
p=Path('.')
def edit(f,a,b):
 q=p/f;s=q.read_text();assert s.count(a)==1,(f,s.count(a),a[:120]);q.write_text(s.replace(a,b))
edit('apps/api-go/oauthserver/types.go','\tScopes       []string\n}', '''\tScopes       []string
	// Only validated marketplace registrations opt in to exact HTTPS and
	// localhost callbacks. Existing native adapters keep their IP-only policy.
	MCPRedirects bool
	// The zero value preserves existing code + refresh clients.
	RefreshDisabled bool
}''')
edit('apps/api-go/oauthserver/types.go','`json:"refresh_token"`','`json:"refresh_token,omitempty"`')
edit('apps/api-go/oauthserver/types.go','// are portless http://127.0.0.1/<path> or http://[::1]/<path> templates. Only\n// the port may vary; address families and registered paths are not aliases.', '// default to portless http://127.0.0.1/<path> or http://[::1]/<path> templates.\n// Only the port may vary; address families and registered paths are not aliases.\n// Explicit MCP registrations may also use exact HTTPS/localhost callbacks.')
edit('apps/api-go/oauthserver/validation.go', '''	template, ok := NativeRedirectTemplate(raw)
	return ok && contains(client.RedirectURIs, template)''','''	template, ok := NativeRedirectTemplate(raw)
	if client.MCPRedirects {
		template, ok = MCPRedirectTemplate(raw)
	}
	return ok && contains(client.RedirectURIs, template)''')
with (p/'apps/api-go/oauthserver/validation.go').open('a') as out:out.write('''
// MCPRedirectTemplate retains exact web and localhost callback URLs. Only
// loopback IP listeners may change ports. No URL is fetched during validation.
// Query/fragment callbacks, credentials, encodings, private IPs and ambiguous
// host/path spellings stay excluded by the common canonical URL checks.
func MCPRedirectTemplate(raw string) (string, bool) {
	if template, ok := NativeRedirectTemplate(raw); ok {
		return template, true
	}
	u, ok := strictURL(raw)
	if !ok || !canonicalPath(u.Path) {
		return "", false
	}
	if validHTTPS(raw) {
		return raw, true
	}
	if u.Scheme != "http" || u.Hostname() != "localhost" {
		return "", false
	}
	if u.Host == "localhost" {
		return raw, true
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || u.Host != "localhost:"+strconv.Itoa(port) {
		return "", false
	}
	return raw, true
}
''')
edit('apps/api-go/oauthserver/server.go', '''		template, ok := NativeRedirectTemplate(redirect)''','''		template, ok := NativeRedirectTemplate(redirect)
		if client.MCPRedirects {
			template, ok = MCPRedirectTemplate(redirect)
		}''')
edit('apps/api-go/model/oauth_server_mcp.go','\tCreatedAtMs  int64    `gorm:"not null"`','''\tCreatedAtMs  int64    `gorm:"not null"`
	// Existing registrations keep their refresh support during migration.
	RefreshDisabled bool `gorm:"not null;default:false"`''')
edit('apps/api-go/model/oauth_server_mcp.go','public native-client registrations','public MCP client registrations')
f='apps/api-go/service/oauth_server_mcp.go'
edit(f,'\tClientName              string   `json:"client_name"`','\tClientName              string   `json:"client_name"`\n\tApplicationType         string   `json:"application_type,omitempty"`')
edit(f,'// RegisterMCPClient accepts only public native clients, code + PKCE and fixed', '// RegisterMCPClient accepts public MCP clients, code + PKCE and fixed')
edit(f,'if !slices.Equal(grants, []string{"authorization_code", "refresh_token"}) {','if !slices.Equal(grants, []string{"authorization_code", "refresh_token"}) && !slices.Equal(grants, []string{"authorization_code"}) {')
edit(f,'\tname := strings.TrimSpace(in.ClientName)','''	if in.ApplicationType != "" && in.ApplicationType != "native" && in.ApplicationType != "web" {
		return nil, ErrMCPRegistration
	}
	name := strings.TrimSpace(in.ClientName)''')
edit(f,'\tredirects := make([]string, 0, len(in.RedirectURIs))','\tredirects := make([]string, 0, len(in.RedirectURIs))\n\tapplicationType := ""')
edit(f,'\t\ttemplate, ok := oauthserver.NativeRedirectTemplate(raw)','\t\ttemplate, ok := oauthserver.MCPRedirectTemplate(raw)')
edit(f,''' 		if !slices.Contains(redirects, template) {'''.lstrip(' '),'''		kind := "native"
		if strings.HasPrefix(template, "https://") {
			kind = "web"
		}
		if (applicationType != "" && applicationType != kind) || (in.ApplicationType != "" && in.ApplicationType != kind) {
			return nil, ErrMCPRegistration
		}
		applicationType = kind
		if !slices.Contains(redirects, template) {''')
edit(f,'CreatedAtMs: time.Now().UnixMilli()}','CreatedAtMs: time.Now().UnixMilli(), RefreshDisabled: len(in.GrantTypes) == 1}')
edit(f,'\tidentity, _ := json.Marshal([]any{row.Issuer, row.Name, row.RedirectURIs, row.Scope})','''	identityFields := []any{row.Issuer, row.Name, row.RedirectURIs, row.Scope}
	if row.RefreshDisabled {
		identityFields = append(identityFields, "authorization_code_only")
	}
	identity, _ := json.Marshal(identityFields)''')
edit(f,'\treturn &MCPRegisteredClient{ClientID: row.ID,','''	grantTypes := []string{"authorization_code", "refresh_token"}
	if row.RefreshDisabled {
		grantTypes = []string{"authorization_code"}
	}
	return &MCPRegisteredClient{ClientID: row.ID,''')
edit(f,'MCPClientRegistration{ClientName: row.Name, RedirectURIs:', 'MCPClientRegistration{ClientName: row.Name, ApplicationType: applicationType, RedirectURIs:')
edit(f,'GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes:', 'GrantTypes: grantTypes, ResponseTypes:')
edit(f,'Resources: []string{s.MarketResource()}, Scopes: scopes}', 'Resources: []string{s.MarketResource()}, Scopes: scopes, MCPRedirects: true, RefreshDisabled: row.RefreshDisabled}')
f='apps/api-go/oauthserver/token.go'
edit(f,''' 	if _, exists := s.resolveClient(s.db.WithContext(ctx), values.Get("client_id")); !exists {
		return nil, protocolError("invalid_client")
	}'''.lstrip(' '),'''	client, exists := s.resolveClient(s.db.WithContext(ctx), values.Get("client_id"))
	if !exists {
		return nil, protocolError("invalid_client")
	}
	if client.RefreshDisabled && values.Get("grant_type") == "refresh_token" {
		return nil, protocolError("unauthorized_client")
	}''')
edit(f,''' 	refresh, err := newSecret(refreshPrefix)
	if err != nil {
		return nil, err
	}'''.lstrip(' '),'''	client, exists := s.resolveClient(tx, family.ClientID)
	if !exists {
		return nil, protocolError("invalid_client")
	}
	refresh := ""
	if !client.RefreshDisabled {
		refresh, err = newSecret(refreshPrefix)
		if err != nil {
			return nil, err
		}
	}''')
edit(f,''' 		{Digest: digest(refresh), Issuer: s.issuer, FamilyID: family.ID, Kind: "refresh", Scope: scope, CreatedAtMs: now.UnixMilli(), ExpiresAtMs: refreshExpiry},
	}
	if err := tx.Create(&rows).Error; err != nil {'''.lstrip(' '),'''	}
	if refresh != "" {
		rows = append(rows, model.OAuthServerToken{Digest: digest(refresh), Issuer: s.issuer, FamilyID: family.ID, Kind: "refresh", Scope: scope, CreatedAtMs: now.UnixMilli(), ExpiresAtMs: refreshExpiry})
	}
	if err := tx.Create(&rows).Error; err != nil {''')
