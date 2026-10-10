package toolmarket

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var alice = Principal{"user-1", "account-1"}
var bob = Principal{"user-2", "account-1"} // Same team/account does not share tokens.
type testAuthority struct {
	unavailable  bool
	wrongAccount bool
}

func (a *testAuthority) Authorize(_ context.Context, credential, account, action string) (Principal, error) {
	if a.unavailable {
		return Principal{}, ErrUnavailable
	}
	if a.wrongAccount {
		return Principal{"user-1", "other-account"}, nil
	}
	if account != "account-1" {
		return Principal{}, ErrDenied
	}
	switch credential {
	case "alice":
		return alice, nil
	case "bob":
		return bob, nil
	case "read-only":
		if action == "toolmarket.read" {
			return alice, nil
		}
		return Principal{}, ErrDenied
	default:
		return Principal{}, ErrAuth
	}
}

type mockMCP struct {
	server           *httptest.Server
	calls            atomic.Int32
	tokens           atomic.Int32
	revokes          atomic.Int32
	mu               sync.Mutex
	seen             []string
	verifier         string
	resourceOverride string
	tokenOverride    string
	badStateID       bool
	sse              bool
	cycle            bool
	badSchema        bool
	oidcOnly         bool
	failRevoke       bool
	failToken        bool
	blockToken       chan struct{}
	enteredToken     chan struct{}
	blockCall        chan struct{}
	enteredCall      chan struct{}
}

const echoSchema = `{"type":"object","properties":{"q":{"type":"string","minLength":1,"maxLength":32},"n":{"type":"integer","minimum":1,"maximum":10}},"required":["q"],"additionalProperties":false}`

func newMock(t *testing.T) *mockMCP {
	t.Helper()
	mock := &mockMCP{}
	mock.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := mock.server.URL
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/resource", scope="read,write"`)
			w.WriteHeader(401)
			return
		case r.URL.Path == "/resource" || strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource"):
			resource := base + "/mcp"
			if mock.resourceOverride != "" {
				resource = mock.resourceOverride
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": []string{base + "/tenant"}})
			return
		case strings.HasPrefix(r.URL.Path, "/.well-known/oauth-authorization-server") || strings.HasPrefix(r.URL.Path, "/.well-known/openid-configuration"):
			if mock.oidcOnly && strings.HasPrefix(r.URL.Path, "/.well-known/oauth-authorization-server") {
				w.WriteHeader(404)
				return
			}
			token := base + "/token"
			if mock.tokenOverride != "" {
				token = mock.tokenOverride
			}
			_ = json.NewEncoder(w).Encode(OAuthMeta{Issuer: base + "/tenant", Authorization: base + "/authorize", Token: token, Revocation: base + "/revoke", PKCE: []string{"S256"}, ResponseTypes: []string{"code"}, AuthMethods: []string{"none"}})
			return
		case r.URL.Path == "/token":
			count := mock.tokens.Add(1)
			if mock.enteredToken != nil {
				mock.enteredToken <- struct{}{}
			}
			if mock.blockToken != nil {
				select {
				case <-mock.blockToken:
				case <-r.Context().Done():
					return
				}
			}
			if mock.failToken {
				w.WriteHeader(400)
				return
			}
			if r.ParseForm() != nil || r.PostForm.Get("resource") != base+"/mcp" || r.PostForm.Get("client_id") != "client" {
				w.WriteHeader(400)
				return
			}
			if r.PostForm.Get("grant_type") == "authorization_code" {
				mock.mu.Lock()
				mock.verifier = r.PostForm.Get("code_verifier")
				mock.mu.Unlock()
				if r.PostForm.Get("redirect_uri") != "https://app.example/callback" {
					w.WriteHeader(400)
					return
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("access-%d", count), "refresh_token": fmt.Sprintf("refresh-%d", count), "token_type": "Bearer", "expires_in": 3600})
			return
		case r.URL.Path == "/revoke":
			mock.revokes.Add(1)
			if mock.failRevoke {
				w.WriteHeader(500)
			}
			return
		case r.Method == "DELETE":
			w.WriteHeader(204)
			return
		case r.Method == "POST" && r.URL.Path == "/mcp":
			var req struct {
				ID     any            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.NewDecoder(r.Body).Decode(&req) != nil {
				w.WriteHeader(400)
				return
			}
			auth := r.Header.Get("Authorization")
			if auth == "" {
				auth = r.Header.Get("X-Api-Key")
			}
			mock.mu.Lock()
			mock.seen = append(mock.seen, auth)
			mock.mu.Unlock()
			sid := "s-" + digest(auth)[:16]
			if req.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != sid {
				w.WriteHeader(400)
				return
			}
			var result any
			switch req.Method {
			case "initialize":
				w.Header().Set("Mcp-Session-Id", sid)
				result = map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "mock", "version": "1"}}
			case "notifications/initialized":
				w.WriteHeader(202)
				return
			case "tools/list":
				schema := json.RawMessage(echoSchema)
				if mock.badSchema {
					schema = json.RawMessage(`{"type":"object","$ref":"https://evil.example/schema"}`)
				}
				result = map[string]any{"tools": []Tool{{Name: "echo", Description: strings.Repeat("description ", 20), InputSchema: schema}}}
				if mock.cycle {
					result.(map[string]any)["nextCursor"] = "again"
				}
			case "tools/call":
				mock.calls.Add(1)
				if mock.enteredCall != nil {
					mock.enteredCall <- struct{}{}
				}
				if mock.blockCall != nil {
					select {
					case <-mock.blockCall:
					case <-r.Context().Done():
						return
					}
				}
				result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "mock result"}}}
			default:
				w.WriteHeader(400)
				return
			}
			if mock.badStateID && req.Method == "tools/call" {
				req.ID = "wrong-id"
			}
			body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
			if mock.sse {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, ": keepalive\n\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\ndata: %s\n\n", body)
			} else {
				_, _ = w.Write(body)
			}
			return
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(mock.server.Close)
	return mock
}
func testModule(t *testing.T, mock *mockMCP) *Module {
	t.Helper()
	store := &Store{data: emptyDB()}
	m, e := New(store, &testAuthority{}, Config{CallbackURL: "https://app.example/callback", CallsPerMinute: 600})
	if e != nil {
		t.Fatal(e)
	}
	useMock(t, m, mock)
	t.Cleanup(func() { _ = store.Close() })
	return m
}
func useMock(t *testing.T, m *Module, mock *mockMCP) {
	t.Helper()
	client := mock.server.Client()
	client.Timeout = 2 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	m.net = &network{client: client, test: true}
	t.Cleanup(client.CloseIdleConnections)
}
func doOAuth(t *testing.T, m *Module, p Principal, id string) oauthStart {
	t.Helper()
	start, e := m.startOAuth(context.Background(), p, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.callbackOAuth(context.Background(), p, start.State, start.binding, "code", ""); e != nil {
		t.Fatal(e)
	}
	return start
}
func registered(t *testing.T, m *Module, mock *mockMCP, auth string) (Server, Installation) {
	t.Helper()
	in := RegisterInput{Endpoint: mock.server.URL + "/mcp", Auth: auth}
	if auth == "oauth" {
		in.ClientID = "client"
		in.Scopes = "read"
	}
	value, e := m.register(alice, in)
	if e != nil {
		t.Fatal(e)
	}
	v := value.(map[string]any)
	return v["server"].(Server), v["installation"].(Installation)
}
func price(m *Module, kind string) Price {
	p := Price{Kind: kind, Currency: "USD", Multiplier: 12500, Source: "https://provider.example/pricing", ValidUntil: m.now().Add(time.Hour).Unix()}
	if kind == "fixed" {
		p.Amount = 101
	}
	return p
}
func prepared(t *testing.T, m *Module, mock *mockMCP, auth, kind string) (Server, Installation, Release) {
	t.Helper()
	srv, inst := registered(t, m, mock, auth)
	authorize := func() {
		if auth == "api_key" {
			if e := m.setKey(alice, KeyInput{inst.ID, "secret-alice"}); e != nil {
				t.Fatal(e)
			}
		}
		if auth == "oauth" {
			doOAuth(t, m, alice, inst.ID)
		}
	}
	authorize()
	if _, e := m.discover(context.Background(), alice, inst.ID); e != nil {
		t.Fatal(e)
	}
	value, e := m.release(alice, ReleaseInput{srv.ID, "v1", map[string]Price{"echo": price(m, kind)}})
	if e != nil {
		t.Fatal(e)
	}
	r := value.(Release)
	if e = m.publish(alice, PublishInput{r.ID, true}); e != nil {
		t.Fatal(e)
	}
	if e = m.upgrade(alice, UpgradeInput{inst.ID, r.ID}); e != nil {
		t.Fatal(e)
	}
	authorize()
	return srv, inst, r
}
func invocation(t *testing.T, m *Module, p Principal, id, key string) ExecuteInput {
	t.Helper()
	v, e := m.details(p, ToolInput{id, "echo"}, true)
	if e != nil {
		t.Fatal(e)
	}
	out := v.(map[string]any)
	return ExecuteInput{Installation: id, Name: "echo", Handle: out["handle"].(string), Quote: out["quote"].(Quote).ID, IdempotencyKey: key, Arguments: json.RawMessage(`{"q":"hello"}`)}
}
func post(t *testing.T, m *Module, path, credential, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-LMM-User-Credential", credential)
	r.Header.Set("X-LMM-Account-ID", "account-1")
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	return w
}

func TestLifecycleSearchLoadExecute(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	srv, inst, r := prepared(t, m, mock, "none", "free")
	v, e := m.search(alice, SearchInput{Query: "echo"})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(v)
	if bytes.Contains(b, []byte("inputSchema")) || bytes.Contains(b, []byte("properties")) {
		t.Fatal("search leaked schema")
	}
	in := invocation(t, m, alice, inst.ID, "job1")
	if _, e = m.execute(context.Background(), alice, "alice", in); e != nil {
		t.Fatal(e)
	}
	if mock.calls.Load() != 1 {
		t.Fatal("missing call")
	}
	if e = m.enable(alice, EnableInput{inst.ID, false}); e != nil {
		t.Fatal(e)
	}
	if _, e = m.details(alice, ToolInput{inst.ID, "echo"}, false); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	if e = m.enable(alice, EnableInput{inst.ID, true}); e != nil {
		t.Fatal(e)
	}
	if _, e = m.execute(context.Background(), alice, "alice", in); !errors.Is(e, ErrDenied) {
		t.Fatal("disabled generation did not invalidate handle", e)
	}
	if _, e = m.release(alice, ReleaseInput{srv.ID, "v1", r.Prices}); !errors.Is(e, ErrConflict) {
		t.Fatal("mutable version", e)
	}
	r2, e := m.release(alice, ReleaseInput{srv.ID, "v2", r.Prices})
	if e != nil {
		t.Fatal(e)
	}
	if e = m.upgrade(alice, UpgradeInput{inst.ID, r2.(Release).ID}); e != nil {
		t.Fatal(e)
	}
}
func TestUnknownAndPaidFailClosed(t *testing.T) {
	for _, kind := range []string{"unknown", "fixed"} {
		t.Run(kind, func(t *testing.T) {
			mock := newMock(t)
			m := testModule(t, mock)
			_, inst, _ := prepared(t, m, mock, "none", kind)
			in := invocation(t, m, alice, inst.ID, "job")
			_, e := m.execute(context.Background(), alice, "alice", in)
			want := ErrPrice
			if kind == "fixed" {
				want = ErrFunds
			}
			if !errors.Is(e, want) || mock.calls.Load() != 0 {
				t.Fatalf("%v calls=%d", e, mock.calls.Load())
			}
		})
	}
}
func TestPricePrecisionExpiryAndNoDefault(t *testing.T) {
	m := &Module{now: time.Now}
	p := price(m, "fixed")
	n, e := p.total(time.Now())
	if e != nil || n != 127 {
		t.Fatal(n, e)
	}
	for _, p := range []Price{{}, {Kind: "free", Multiplier: 10000}, {Kind: "fixed", Multiplier: 10000, Amount: 1}, {Kind: "fixed", Multiplier: 1, Amount: 1}, {Kind: "fixed", Multiplier: 1000000, Amount: 9223372036854775807}} {
		if _, e := p.total(time.Now()); e == nil {
			t.Fatal("invalid price accepted")
		}
	}
	p = price(m, "free")
	p.ValidUntil = time.Now().Unix() - 1
	if _, e := p.total(time.Now()); e == nil {
		t.Fatal("expired price accepted")
	}
}
func TestSchemaValidation(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{}`, `{"q":""}`, `{"q":1}`, `{"q":"x","extra":true}`, `{"q":"x","n":1.5}`, `{"q":"x","n":11}`, `{"q":"x","q":"y"}`, `{"q":"x"} {}`, `{"q":"x","n":1e999999999}`} {
		t.Run(raw, func(t *testing.T) {
			if e := validateArgs(json.RawMessage(echoSchema), json.RawMessage(raw)); e == nil {
				t.Fatal("accepted")
			}
		})
	}
	for _, raw := range []string{`{"q":"ok"}`, `{"q":"ok","n":1}`, `{"q":"ok","n":1e1}`} {
		if e := validateArgs(json.RawMessage(echoSchema), json.RawMessage(raw)); e != nil {
			t.Fatal(raw, e)
		}
	}
	for _, raw := range []string{`{"type":"object","$ref":"https://example/schema"}`, `{"type":"object","oneOf":[]}`, `{"type":"string","pattern":".*"}`, `{"type":"integer","minimum":1e999999999}`, `{"type":"object","required":["missing"]}`} {
		if _, e := readSchema(json.RawMessage(raw), 0); e == nil {
			t.Fatal("unsupported constraint ignored", raw)
		}
	}
}
func TestBadArgumentsNeverReachProvider(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	in := invocation(t, m, alice, i.ID, "bad")
	in.Arguments = json.RawMessage(`{"q":false}`)
	if _, e := m.execute(context.Background(), alice, "alice", in); !errors.Is(e, ErrInvalid) || mock.calls.Load() != 0 {
		t.Fatal(e)
	}
}
func TestDiscoveryRejectsCycleAndUnsupportedSchema(t *testing.T) {
	for _, bad := range []string{"cycle", "schema"} {
		t.Run(bad, func(t *testing.T) {
			mock := newMock(t)
			mock.cycle = bad == "cycle"
			mock.badSchema = bad == "schema"
			m := testModule(t, mock)
			srv, inst := registered(t, m, mock, "none")
			if _, e := m.discover(context.Background(), alice, inst.ID); e == nil {
				t.Fatal("bad discovery accepted")
			}
			_ = m.store.view(func(d database) error {
				if len(d.Servers[srv.ID].Tools) != 0 {
					t.Fatal("partial discovery committed")
				}
				return nil
			})
		})
	}
}
func TestSSEResponseAndSessionHeaders(t *testing.T) {
	mock := newMock(t)
	mock.sse = true
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	if _, e := m.execute(context.Background(), alice, "alice", invocation(t, m, alice, i.ID, "sse")); e != nil {
		t.Fatal(e)
	}
}
func TestProtocolErrorIsNotRetried(t *testing.T) {
	mock := newMock(t)
	mock.badStateID = true
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	in := invocation(t, m, alice, i.ID, "wrong-id")
	for k := 0; k < 2; k++ {
		if _, e := m.execute(context.Background(), alice, "alice", in); !errors.Is(e, ErrPending) {
			t.Fatal(e)
		}
	}
	if mock.calls.Load() != 1 {
		t.Fatal("uncertain request retried")
	}
}
func TestTimeoutDoesNotRetryTool(t *testing.T) {
	mock := newMock(t)
	mock.blockCall = make(chan struct{})
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	in := invocation(t, m, alice, i.ID, "timeout")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, e := m.execute(ctx, alice, "alice", in); !errors.Is(e, ErrPending) {
		t.Fatal(e)
	}
	close(mock.blockCall)
	if _, e := m.execute(context.Background(), alice, "alice", in); !errors.Is(e, ErrPending) {
		t.Fatal(e)
	}
	if mock.calls.Load() != 1 {
		t.Fatal("timeout retried")
	}
}

func TestAPIKeyAndTokenIsolation(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, a, r := prepared(t, m, mock, "api_key", "free")
	v, e := m.install(bob, InstallInput{r.ID})
	if e != nil {
		t.Fatal(e)
	}
	b := v.(Installation)
	if _, e = m.details(bob, ToolInput{a.ID, "echo"}, true); !errors.Is(e, ErrMissing) {
		t.Fatal("cross-user access", e)
	}
	if e = m.setKey(bob, KeyInput{a.ID, "attacker"}); !errors.Is(e, ErrMissing) {
		t.Fatal("cross-user key write", e)
	}
	in := invocation(t, m, bob, b.ID, "bob")
	if _, e = m.execute(context.Background(), bob, "bob", in); !errors.Is(e, ErrAuth) {
		t.Fatal("seller token inherited", e)
	}
	if e = m.setKey(bob, KeyInput{b.ID, "secret-bob"}); e != nil {
		t.Fatal(e)
	}
	for _, x := range []struct {
		p          Principal
		id         string
		credential string
	}{{alice, a.ID, "alice"}, {bob, b.ID, "bob"}} {
		if _, e = m.execute(context.Background(), x.p, x.credential, invocation(t, m, x.p, x.id, x.credential)); e != nil {
			t.Fatal(e)
		}
	}
	mock.mu.Lock()
	seen := strings.Join(mock.seen, " ")
	mock.mu.Unlock()
	if !strings.Contains(seen, "Bearer secret-alice") || !strings.Contains(seen, "Bearer secret-bob") {
		t.Fatal("missing isolated credentials")
	}
	if _, e = m.revoke(context.Background(), bob, b.ID); e != nil {
		t.Fatal(e)
	}
	_, _, s, e := m.snapshot(alice, a.ID)
	if e != nil || s.Access != "secret-alice" {
		t.Fatal("cross-user revoke", e)
	}
}
func TestOAuthPKCECallbackReplayAndIdentity(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i := registered(t, m, mock, "oauth")
	start, e := m.startOAuth(context.Background(), alice, i.ID)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(start.URL)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("resource") != mock.server.URL+"/mcp" || q.Get("redirect_uri") != m.callback {
		t.Fatal(q)
	}
	for _, x := range []struct {
		p                      Principal
		state, binding, issuer string
	}{{bob, start.State, start.binding, ""}, {alice, strings.Repeat("x", 43), start.binding, ""}, {alice, start.State, strings.Repeat("x", 43), ""}, {alice, start.State, start.binding, "https://evil.example"}} {
		if e = m.callbackOAuth(context.Background(), x.p, x.state, x.binding, "code", x.issuer); e == nil {
			t.Fatal("malicious callback accepted")
		}
	}
	if mock.tokens.Load() != 0 {
		t.Fatal("token exchange before state verification")
	}
	if e = m.callbackOAuth(context.Background(), alice, start.State, start.binding, "code", mock.server.URL+"/tenant"); e != nil {
		t.Fatal(e)
	}
	mock.mu.Lock()
	verifier := mock.verifier
	mock.mu.Unlock()
	sum := sha256.Sum256([]byte(verifier))
	if q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatal("PKCE mismatch")
	}
	if e = m.callbackOAuth(context.Background(), alice, start.State, start.binding, "code", ""); !errors.Is(e, ErrAuth) {
		t.Fatal("state reused", e)
	}
	if mock.tokens.Load() != 1 {
		t.Fatal("duplicate exchange")
	}
}
func TestOAuthDiscoveryOIDCFallbackAndMaliciousMetadata(t *testing.T) {
	for _, kind := range []string{"oidc", "resource", "endpoint"} {
		t.Run(kind, func(t *testing.T) {
			mock := newMock(t)
			mock.oidcOnly = kind == "oidc"
			if kind == "resource" {
				mock.resourceOverride = "https://other.example/mcp"
			}
			if kind == "endpoint" {
				mock.tokenOverride = "https://evil.example/token"
			}
			m := testModule(t, mock)
			_, i := registered(t, m, mock, "oauth")
			_, e := m.startOAuth(context.Background(), alice, i.ID)
			if (e == nil) != (kind == "oidc") {
				t.Fatal(e)
			}
		})
	}
}
func TestOAuthExpiryRefreshAndRevocation(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i := registered(t, m, mock, "oauth")
	doOAuth(t, m, alice, i.ID)
	_, _, before, e := m.snapshot(alice, i.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.refreshOAuth(context.Background(), alice, i.ID); e != nil {
		t.Fatal(e)
	}
	_, _, after, e := m.snapshot(alice, i.ID)
	if e != nil || after.Access == before.Access || after.Refresh == before.Refresh {
		t.Fatal("refresh not rotated", e)
	}
	mock.failRevoke = true
	v, e := m.revoke(context.Background(), alice, i.ID)
	if e != nil || v.(map[string]any)["upstream_revocation"] != "failed" {
		t.Fatal(v, e)
	}
	_, _, after, e = m.snapshot(alice, i.ID)
	if e != nil || after.Access != "" {
		t.Fatal("local revoke failed", e)
	}
	if mock.revokes.Load() != 2 {
		t.Fatal("did not revoke both tokens")
	}
}
func TestExpiredOAuthStateCannotExchange(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i := registered(t, m, mock, "oauth")
	start, e := m.startOAuth(context.Background(), alice, i.ID)
	if e != nil {
		t.Fatal(e)
	}
	now := m.now()
	m.now = func() time.Time { return now.Add(6 * time.Minute) }
	if e = m.callbackOAuth(context.Background(), alice, start.State, start.binding, "code", ""); !errors.Is(e, ErrAuth) || mock.tokens.Load() != 0 {
		t.Fatal(e)
	}
}
func TestRevokeDuringCallbackCannotRestoreToken(t *testing.T) {
	mock := newMock(t)
	mock.blockToken = make(chan struct{})
	mock.enteredToken = make(chan struct{}, 1)
	m := testModule(t, mock)
	_, i := registered(t, m, mock, "oauth")
	start, e := m.startOAuth(context.Background(), alice, i.ID)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- m.callbackOAuth(context.Background(), alice, start.State, start.binding, "code", "") }()
	<-mock.enteredToken
	if _, e = m.revoke(context.Background(), alice, i.ID); e != nil {
		t.Fatal(e)
	}
	close(mock.blockToken)
	if e = <-done; !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	_, _, s, e := m.snapshot(alice, i.ID)
	if e != nil || s.Access != "" {
		t.Fatal("callback restored revoked token", e)
	}
}
func TestOAuthFailureConsumesState(t *testing.T) {
	mock := newMock(t)
	mock.failToken = true
	m := testModule(t, mock)
	_, i := registered(t, m, mock, "oauth")
	start, e := m.startOAuth(context.Background(), alice, i.ID)
	if e != nil {
		t.Fatal(e)
	}
	for k := 0; k < 2; k++ {
		if e = m.callbackOAuth(context.Background(), alice, start.State, start.binding, "code", ""); e == nil {
			t.Fatal("failed token accepted")
		}
	}
	if mock.tokens.Load() != 1 {
		t.Fatal("failed exchange replayed")
	}
}

func TestSSRFAndRedirectBoundaries(t *testing.T) {
	n := newNetwork()
	for _, raw := range []string{"http://example.com/mcp", "https://localhost/mcp", "https://127.0.0.1/mcp", "https://[::1]/mcp", "https://169.254.169.254/mcp", "https://example.com:8443/mcp", "https://u:p@example.com/mcp", "https://example.com/mcp?key=secret", "https://example.com/mcp#fragment"} {
		if _, e := n.url(raw); e == nil {
			t.Fatal("unsafe URL", raw)
		}
	}
	for _, raw := range []string{"10.0.0.1", "127.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "198.18.0.1", "240.0.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "64:ff9b::a00:1", "2002:7f00:1::1", "2001:db8::1"} {
		if publicIP(netip.MustParseAddr(raw)) {
			t.Fatal("unsafe IP", raw)
		}
	}
	if !publicIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public IPv4 blocked")
	}
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	n.test = true
	n.client = source.Client()
	n.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := n.request(context.Background(), "POST", source.URL, []byte("secret"), http.Header{"Authorization": []string{"Bearer secret"}})
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if leaked.Load() != 0 || resp.StatusCode != 307 {
		t.Fatal("redirect leaked credentials")
	}
}
func TestHTTPConstraintsAndAuthority(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	for _, x := range []struct{ path, body string }{{"/meta/search", `{"query":"echo","limit":21}`}, {"/meta/search", `{"query":"x","unknown":1}`}, {"/meta/search", `{"query":"x","query":"y"}`}, {"/meta/search", `null`}, {"/installations/enable", `{"installation":"` + i.ID + `"}`}} {
		w := post(t, m, x.path, "alice", x.body)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := post(t, m, "/meta/search", "bad", `{"query":"echo"}`); w.Code != 401 {
		t.Fatal(w.Code)
	}
	m.authority = &testAuthority{wrongAccount: true}
	if w := post(t, m, "/meta/search", "alice", `{"query":"echo"}`); w.Code != 403 {
		t.Fatal(w.Code)
	}
	m.authority = &testAuthority{unavailable: true}
	if w := post(t, m, "/meta/search", "alice", `{"query":"echo"}`); w.Code != 503 {
		t.Fatal(w.Code)
	}
}
func TestMCPMetaToolsAndExecutePermission(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	w := post(t, m, "/mcp", "alice", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "market.execute") {
		t.Fatal(w.Body.String())
	}
	for _, tool := range metaTools() {
		if _, e := readSchema(tool.InputSchema, 0); e != nil {
			t.Fatal(tool.Name, e)
		}
	}
	in := invocation(t, m, alice, i.ID, "not-allowed")
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "market.execute", "arguments": in}})
	w = post(t, m, "/mcp", "read-only", string(b))
	if !strings.Contains(w.Body.String(), "forbidden") || mock.calls.Load() != 0 {
		t.Fatal(w.Body.String())
	}
	w = post(t, m, "/mcp", "alice", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"market.search","arguments":{"query":"echo"}}}`)
	if strings.Contains(w.Body.String(), "inputSchema") || strings.Contains(w.Body.String(), `"error"`) {
		t.Fatal(w.Body.String())
	}
}
func TestMaliciousHTTPCallback(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i := registered(t, m, mock, "oauth")
	start, e := m.startOAuth(context.Background(), alice, i.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, suffix := range []string{"&redirect_uri=https://evil.example", "&state=duplicate", "&code=second"} {
		r := httptest.NewRequest("GET", "/oauth/callback?state="+start.State+"&code=code"+suffix, nil)
		r.Header.Set("X-LMM-User-Credential", "alice")
		r.Header.Set("X-LMM-Account-ID", alice.Account)
		r.AddCookie(&http.Cookie{Name: cookieName(start.State), Value: start.binding})
		w := httptest.NewRecorder()
		m.Handler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if mock.tokens.Load() != 0 {
		t.Fatal("malicious callback exchanged token")
	}
}
func TestRateLimitAndOrigin(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	m.perMinute = 1
	if w := post(t, m, "/meta/search", "alice", `{"query":""}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := post(t, m, "/meta/search", "alice", `{"query":""}`); w.Code != 429 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("POST", "/meta/search", strings.NewReader(`{"query":""}`))
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestEncryptedStoreLockRestartAndRollback(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "market.db")
	key := bytes.Repeat([]byte{7}, 32)
	s, e := InitStore(path, key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = OpenStore(path, key); !errors.Is(e, ErrConflict) {
		t.Fatal("second process writer", e)
	}
	if e = s.update(func(d *database) error { d.Secrets["i"] = Secret{Access: "top-secret-token"}; return nil }); e != nil {
		t.Fatal(e)
	}
	if e = s.update(func(d *database) error { delete(d.Secrets, "i"); return ErrConflict }); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	b, e := os.ReadFile(path)
	if e != nil || bytes.Contains(b, []byte("top-secret-token")) {
		t.Fatal("plaintext secret", e)
	}
	_ = s.Close()
	s, e = OpenStore(path, key)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.view(func(d database) error {
		if d.Secrets["i"].Access != "top-secret-token" {
			t.Fatal("lost state/rollback")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	_ = s.Close()
	if _, e = OpenStore(path, bytes.Repeat([]byte{8}, 32)); e == nil {
		t.Fatal("wrong encryption key accepted")
	}
	if _, e = InitStore(path, key); !errors.Is(e, ErrConflict) {
		t.Fatal("reinitialization accepted", e)
	}
}
func TestAuditContainsNoSecrets(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i := registered(t, m, mock, "api_key")
	if w := post(t, m, "/credentials/key", "alice", `{"installation":"`+i.ID+`","key":"sensitive-api-key"}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if e := m.store.view(func(d database) error {
		b, _ := json.Marshal(d.Audit)
		if bytes.Contains(b, []byte("sensitive-api-key")) || bytes.Contains(b, []byte("Authorization")) {
			t.Fatal("audit leaked secret")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
