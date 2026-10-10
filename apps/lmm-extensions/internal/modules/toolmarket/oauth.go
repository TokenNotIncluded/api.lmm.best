package toolmarket

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OAuthMeta struct {
	Issuer        string   `json:"issuer"`
	Authorization string   `json:"authorization_endpoint"`
	Token         string   `json:"token_endpoint"`
	Revocation    string   `json:"revocation_endpoint,omitempty"`
	PKCE          []string `json:"code_challenge_methods_supported"`
	ResponseTypes []string `json:"response_types_supported"`
	AuthMethods   []string `json:"token_endpoint_auth_methods_supported"`
}
type resourceMeta struct {
	Resource string   `json:"resource"`
	Servers  []string `json:"authorization_servers"`
}
type oauthStart struct {
	URL     string `json:"authorization_url"`
	State   string `json:"state"`
	binding string
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// challengeMetadata accepts a single Bearer resource_metadata parameter. MIME's
// quoted-string parser handles quoted commas; ambiguous duplicates are rejected.
func challengeMetadata(headers []string) (string, error) {
	found := ""
	for _, h := range headers {
		if len(h) > 8192 {
			return "", ErrInvalid
		}
		scheme, params, ok := strings.Cut(strings.TrimSpace(h), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			continue
		}
		quoted, escaped := false, false
		var b strings.Builder
		b.WriteString("bearer;")
		for _, c := range params {
			if escaped {
				escaped = false
				b.WriteRune(c)
				continue
			}
			if c == '\\' && quoted {
				escaped = true
				b.WriteRune(c)
				continue
			}
			if c == '"' {
				quoted = !quoted
			}
			if c == ',' && !quoted {
				b.WriteByte(';')
			} else {
				b.WriteRune(c)
			}
		}
		if quoted || escaped {
			return "", ErrInvalid
		}
		_, values, e := mime.ParseMediaType(b.String())
		if e != nil {
			return "", ErrInvalid
		}
		if value := values["resource_metadata"]; value != "" {
			if found != "" {
				return "", ErrInvalid
			}
			found = value
		}
	}
	return found, nil
}
func (m *Module) oauthMetadata(ctx context.Context, s Server) (OAuthMeta, error) {
	endpoint, e := m.net.url(s.Endpoint)
	if e != nil {
		return OAuthMeta{}, e
	}
	// Do not send any stored token during discovery.
	r, e := m.net.request(ctx, "GET", s.Endpoint, nil, http.Header{"Accept": []string{"application/json, text/event-stream"}})
	if e != nil {
		return OAuthMeta{}, e
	}
	r.Body.Close()
	metadata := ""
	if r.StatusCode == 401 {
		metadata, e = challengeMetadata(r.Header.Values("WWW-Authenticate"))
		if e != nil {
			return OAuthMeta{}, e
		}
	}
	candidates := []string{origin(endpoint) + "/.well-known/oauth-protected-resource" + endpoint.EscapedPath(), origin(endpoint) + "/.well-known/oauth-protected-resource"}
	if metadata != "" {
		candidates = []string{metadata}
	}
	var resource resourceMeta
	loaded := false
	for _, address := range candidates {
		e = m.net.getJSON(ctx, address, &resource)
		if e == ErrMissing {
			continue
		}
		if e != nil {
			return OAuthMeta{}, e
		}
		loaded = true
		break
	}
	if !loaded || resource.Resource != s.Endpoint || len(resource.Servers) == 0 || len(resource.Servers) > 10 {
		return OAuthMeta{}, ErrAuth
	}
	issuer := s.Issuer
	if issuer == "" {
		issuer = resource.Servers[0]
	}
	if !contains(resource.Servers, issuer) {
		return OAuthMeta{}, ErrAuth
	}
	u, e := m.net.url(issuer)
	if e != nil {
		return OAuthMeta{}, e
	}
	candidates = []string{origin(u) + "/.well-known/oauth-authorization-server" + u.EscapedPath(), origin(u) + "/.well-known/openid-configuration" + u.EscapedPath()}
	if u.Path != "" && u.Path != "/" {
		candidates = append(candidates, strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration")
	}
	var meta OAuthMeta
	loaded = false
	for _, address := range candidates {
		meta = OAuthMeta{}
		e = m.net.getJSON(ctx, address, &meta)
		if e == ErrMissing {
			continue
		}
		if e != nil {
			return OAuthMeta{}, e
		}
		loaded = true
		break
	}
	if !loaded || meta.Issuer != issuer || !contains(meta.PKCE, "S256") || !contains(meta.ResponseTypes, "code") || !contains(meta.AuthMethods, "none") {
		return OAuthMeta{}, ErrAuth
	}
	// This release supports pre-registered public clients. Endpoints are pinned to
	// the discovered issuer origin; cross-origin token endpoints are not accepted.
	for _, address := range []string{meta.Authorization, meta.Token, meta.Revocation} {
		if address == "" {
			continue
		}
		v, e := m.net.url(address)
		if e != nil || origin(v) != origin(u) {
			return OAuthMeta{}, ErrAuth
		}
	}
	if meta.Authorization == "" || meta.Token == "" {
		return OAuthMeta{}, ErrAuth
	}
	return meta, nil
}
func (m *Module) startOAuth(ctx context.Context, p Principal, id string) (oauthStart, error) {
	i, r, _, e := m.snapshot(p, id)
	if e != nil {
		return oauthStart{}, e
	}
	if r.Server.Auth != "oauth" {
		return oauthStart{}, ErrInvalid
	}
	meta, e := m.oauthMetadata(ctx, r.Server)
	if e != nil {
		return oauthStart{}, e
	}
	state, e := randomID()
	if e != nil {
		return oauthStart{}, e
	}
	verifier, e := randomID()
	if e != nil {
		return oauthStart{}, e
	}
	binding, e := randomID()
	if e != nil {
		return oauthStart{}, e
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	u, _ := url.Parse(meta.Authorization)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", r.Server.ClientID)
	q.Set("redirect_uri", m.callback)
	q.Set("resource", r.Server.Endpoint)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if r.Server.Scopes != "" {
		q.Set("scope", r.Server.Scopes)
	}
	u.RawQuery = q.Encode()
	e = m.store.update(func(d *database) error {
		current, _, _, e := selected(*d, p, id)
		if e != nil {
			return e
		}
		if current.Generation != i.Generation {
			return ErrConflict
		}
		for k, s := range d.States {
			if s.Expires <= m.now().Unix() || s.Install == id {
				delete(d.States, k)
			}
		}
		if len(d.States) >= 10000 {
			return ErrLimit
		}
		// A new authorization attempt invalidates older in-flight callbacks.
		invalidate(d, &current)
		d.Installs[id] = current
		d.States[digest(state)] = OAuthState{Owner: p, Install: id, Generation: current.Generation, Verifier: verifier, Binding: digest(binding), Expires: m.now().Add(5 * time.Minute).Unix(), Meta: meta}
		d.audit(p, "oauth.start", id, "ok")
		return nil
	})
	return oauthStart{URL: u.String(), State: state, binding: binding}, e
}
func (m *Module) tokenRequest(ctx context.Context, endpoint string, form url.Values) (Secret, error) {
	r, e := m.net.request(ctx, "POST", endpoint, []byte(form.Encode()), http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}, "Accept": []string{"application/json"}})
	if e != nil {
		return Secret{}, e
	}
	b, e := readBody(r)
	if e != nil || r.StatusCode != 200 {
		return Secret{}, ErrAuth
	}
	var response struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Type    string `json:"token_type"`
		Expires int64  `json:"expires_in"`
	}
	if uniqueJSON(b) != nil || json.Unmarshal(b, &response) != nil || !strings.EqualFold(response.Type, "Bearer") || response.Access == "" || len(response.Access) > 8192 || len(response.Refresh) > 8192 || strings.ContainsAny(response.Access+response.Refresh, "\r\n\x00") || response.Expires < 1 || response.Expires > 31536000 {
		return Secret{}, ErrAuth
	}
	return Secret{Access: response.Access, Refresh: response.Refresh, Expires: m.now().Unix() + response.Expires}, nil
}
func (m *Module) callbackOAuth(ctx context.Context, p Principal, state, binding, code, issuer string) error {
	if len(state) != 43 || len(binding) != 43 || len(code) == 0 || len(code) > 4096 || len(issuer) > 2048 {
		return ErrInvalid
	}
	var flow OAuthState
	var srv Server
	e := m.store.update(func(d *database) error {
		s, ok := d.States[digest(state)]
		if !ok || s.Owner != p || s.Expires <= m.now().Unix() || subtle.ConstantTimeCompare([]byte(s.Binding), []byte(digest(binding))) != 1 {
			return ErrAuth
		}
		if issuer != "" && issuer != s.Meta.Issuer {
			return ErrAuth
		}
		i, r, _, e := selected(*d, p, s.Install)
		if e != nil {
			return e
		}
		if i.Generation != s.Generation {
			return ErrConflict
		}
		flow = s
		srv = r.Server
		delete(d.States, digest(state))
		d.audit(p, "oauth.callback", i.ID, "consumed")
		return nil
	})
	if e != nil {
		return e
	}
	// Consume state before exchange. A timeout never permits replay of the code.
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {srv.ClientID}, "redirect_uri": {m.callback}, "code_verifier": {flow.Verifier}, "resource": {srv.Endpoint}}
	token, e := m.tokenRequest(ctx, flow.Meta.Token, form)
	if e != nil {
		return e
	}
	token.Meta = flow.Meta
	return m.store.update(func(d *database) error {
		i, _, _, e := selected(*d, p, flow.Install)
		if e != nil {
			return e
		}
		if i.Generation != flow.Generation {
			return ErrConflict
		}
		invalidate(d, &i)
		d.Secrets[i.ID] = token
		d.Installs[i.ID] = i
		d.audit(p, "oauth.callback", i.ID, "ok")
		return nil
	})
}
func (m *Module) refreshOAuth(ctx context.Context, p Principal, id string) error {
	var saved Secret
	var inst Installation
	var srv Server
	e := m.store.update(func(d *database) error {
		i, r, s, e := selected(*d, p, id)
		if e != nil {
			return e
		}
		if r.Server.Auth != "oauth" || s.Refresh == "" || s.Refreshing {
			return ErrAuth
		}
		s.Refreshing = true
		d.Secrets[id] = s
		saved = s
		inst = i
		srv = r.Server
		d.audit(p, "oauth.refresh", id, "started")
		return nil
	})
	if e != nil {
		return e
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {saved.Refresh}, "client_id": {srv.ClientID}, "resource": {srv.Endpoint}}
	token, e := m.tokenRequest(ctx, saved.Meta.Token, form)
	if e != nil {
		return ErrAuth
	} // Explicit reauthorization after uncertain refresh; do not reuse a rotating token.
	token.Meta = saved.Meta
	if token.Refresh == "" {
		token.Refresh = saved.Refresh
	}
	return m.store.update(func(d *database) error {
		i, _, s, e := selected(*d, p, id)
		if e != nil {
			return e
		}
		if i.Generation != inst.Generation || !s.Refreshing {
			return ErrConflict
		}
		invalidate(d, &i)
		d.Installs[id] = i
		d.Secrets[id] = token
		d.audit(p, "oauth.refresh", id, "ok")
		return nil
	})
}
func (m *Module) revoke(ctx context.Context, p Principal, id string) (any, error) {
	var secret Secret
	var srv Server
	e := m.store.update(func(d *database) error {
		i, ok := d.Installs[id]
		if !ok || i.Owner != p {
			return ErrMissing
		}
		secret = d.Secrets[id]
		if i.Release != "" {
			srv = d.Releases[i.Release].Server
		} else {
			srv = d.Servers[i.Server]
		}
		invalidate(d, &i)
		d.Installs[id] = i
		delete(d.Secrets, id)
		d.audit(p, "revoke", id, "local")
		return nil
	})
	if e != nil {
		return nil, e
	}
	status := "not_supported"
	if secret.Meta.Revocation != "" {
		status = "ok"
		for _, token := range []string{secret.Refresh, secret.Access} {
			if token == "" {
				continue
			}
			form := url.Values{"token": {token}, "client_id": {srv.ClientID}}
			r, e := m.net.request(ctx, "POST", secret.Meta.Revocation, []byte(form.Encode()), http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}})
			if e != nil {
				status = "failed"
				continue
			}
			r.Body.Close()
			if r.StatusCode != 200 {
				status = "failed"
			}
		}
	}
	return map[string]any{"local_revoked": true, "upstream_revocation": status}, nil
}

// Provider denial consumes the flow but never stores or changes an access token.
func (m *Module) denyOAuth(p Principal, state, binding, issuer string) error {
	if len(state) != 43 || len(binding) != 43 || len(issuer) > 2048 {
		return ErrInvalid
	}
	return m.store.update(func(d *database) error {
		flow, ok := d.States[digest(state)]
		if !ok || flow.Owner != p || flow.Expires <= m.now().Unix() || subtle.ConstantTimeCompare([]byte(flow.Binding), []byte(digest(binding))) != 1 {
			return ErrAuth
		}
		if issuer != "" && issuer != flow.Meta.Issuer {
			return ErrAuth
		}
		i, _, _, err := selected(*d, p, flow.Install)
		if err != nil {
			return err
		}
		if i.Generation != flow.Generation {
			return ErrConflict
		}
		delete(d.States, digest(state))
		d.audit(p, "oauth.callback", flow.Install, "denied")
		return nil
	})
}
