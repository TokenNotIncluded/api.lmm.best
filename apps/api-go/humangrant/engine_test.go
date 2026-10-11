package humangrant

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This store is a unit-test seam, not a substitute for the GORM host adapter or
// evidence that the host's real login cookie / database / browser flow ran.
// Each write serializes JSON, so private binding loss across persistence is real.
type testRecord struct {
	Owner   string
	Expires int64
	Raw     json.RawMessage
}
type testPrincipal struct {
	Human   Human
	Account Account
	Active  bool
}
type testStore struct {
	mu     sync.Mutex
	rows   map[string]testRecord
	people map[string]testPrincipal
	now    int64
	fail   bool
}
type testTx struct {
	s    *testStore
	rows map[string]testRecord
}

func newTestStore() *testStore {
	return &testStore{rows: map[string]testRecord{}, people: map[string]testPrincipal{
		"alice": {Human{"1", "session-alice", 1, 1}, Account{"1", "alice", "Alice", false}, true},
		"bob":   {Human{"2", "session-bob", 1, 1}, Account{"2", "bob", "Bob", false}, true},
		"root":  {Human{"3", "session-root", 1, 1}, Account{"3", "root", "Root", true}, true},
	}, now: 1700000000}
}
func (s *testStore) Transaction(_ context.Context, fn func(Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := map[string]testRecord{}
	for k, v := range s.rows {
		rows[k] = v
	}
	tx := &testTx{s, rows}
	if err := fn(tx); err != nil {
		return err
	}
	s.rows = rows
	return nil
}
func (t *testTx) Get(kind, id string, value any) (bool, error) {
	r, ok := t.rows[kind+":"+id]
	if !ok || (r.Expires > 0 && r.Expires <= t.s.now) {
		return false, nil
	}
	return true, json.Unmarshal(r.Raw, value)
}
func (t *testTx) Put(kind, id, owner string, expires int64, value any) error {
	if t.s.fail {
		return errors.New("injected write failure")
	}
	raw, e := json.Marshal(value)
	t.rows[kind+":"+id] = testRecord{owner, expires, raw}
	return e
}
func (t *testTx) Delete(kind, id string) error { delete(t.rows, kind+":"+id); return nil }
func (t *testTx) List(kind, owner string, limit int) ([]json.RawMessage, error) {
	keys := []string{}
	for k, v := range t.rows {
		if strings.HasPrefix(k, kind+":") && v.Owner == owner && (v.Expires == 0 || v.Expires > t.s.now) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := []json.RawMessage{}
	for _, k := range keys[:min(limit, len(keys))] {
		out = append(out, t.rows[k].Raw)
	}
	return out, nil
}
func (t *testTx) Authenticate(r *http.Request) (Human, Account, bool, error) {
	if r.Header.Get("Authorization") != "" {
		return Human{}, Account{}, false, nil
	}
	c, e := r.Cookie("test-host-login")
	if e != nil {
		return Human{}, Account{}, false, nil
	}
	p, ok := t.s.people[c.Value]
	return p.Human, p.Account, ok && p.Active, nil
}
func (t *testTx) CurrentHuman(h Human) (Account, bool, error) {
	for _, p := range t.s.people {
		if p.Active && p.Human == h {
			return p.Account, true, nil
		}
	}
	return Account{}, false, nil
}

type testActor struct {
	public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func actor(t *testing.T) testActor {
	t.Helper()
	p, k, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return testActor{p, k}
}
func (a testActor) request(method, path string, body []byte, token string, now int64) *http.Request {
	r := httptest.NewRequest(method, "https://auth.example.test"+path, bytes.NewReader(body))
	r.RequestURI = r.URL.RequestURI()
	r.RemoteAddr = "127.0.0.1:12345"
	nonce := make([]byte, 24)
	_, _ = rand.Read(nonce)
	pub := base64.RawURLEncoding.EncodeToString(a.public)
	id := "ed25519:" + fingerprint(a.public)
	params := fmt.Sprintf(`%s;created=%d;expires=%d;nonce="%s";keyid="%s";alg="ed25519";tag="humangrant-0.1"`, signatureComponents, now, now+60, base64.RawURLEncoding.EncodeToString(nonce), id)
	digest := sha256.Sum256(body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Content-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(digest[:])+":")
	r.Header.Set("X-HG-Key", pub)
	r.Header.Set("Authorization", "HumanGrant "+token)
	r.Header.Set("Signature-Input", "hg="+params)
	base := `"@method": ` + method + "\n" + `"@target-uri": https://auth.example.test` + r.URL.Path + "\n" + `"content-digest": ` + r.Header.Get("Content-Digest") + "\n" + `"content-type": application/json` + "\n" + `"x-hg-key": ` + pub + "\n" + `"authorization": HumanGrant ` + token + "\n" + `"@signature-params": ` + params
	r.Header.Set("Signature", "hg=:"+base64.StdEncoding.EncodeToString(ed25519.Sign(a.private, []byte(base)))+":")
	return r
}

type rig struct {
	t *testing.T
	s *testStore
	e *Engine
	a testActor
}

func setup(t *testing.T) *rig {
	s := newTestStore()
	return &rig{t, s, &Engine{Store: s, Origin: "https://auth.example.test", Now: func() time.Time { return time.Unix(s.now, 0) }}, actor(t)}
}
func (h *rig) run(r *http.Request, status int) *httptest.ResponseRecorder {
	h.t.Helper()
	w := httptest.NewRecorder()
	h.e.ServeHTTP(w, r)
	if w.Code != status {
		h.t.Fatalf("%s %s: got %d want %d: %s", r.Method, r.URL.Path, w.Code, status, w.Body.String())
	}
	return w
}
func jsonBody(v any) []byte { b, _ := json.Marshal(v); return b }
func (h *rig) signed(method, path string, value any, token string, status int) *httptest.ResponseRecorder {
	h.t.Helper()
	var b []byte
	if value != nil {
		b = jsonBody(value)
	}
	return h.run(h.a.request(method, path, b, token, h.s.now), status)
}
func object(w *httptest.ResponseRecorder) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}
func (h *rig) create(input map[string]any) map[string]any {
	h.t.Helper()
	if input == nil {
		input = map[string]any{"client_id": ClientID, "target_service": ServiceID, "purpose": "Read profile", "requested_permissions": []Permission{{ProfileRead, "1"}}, "expected_human_id": "1"}
	}
	return object(h.signed("POST", "/v1/requests", input, "anonymous", 201))
}
func (h *rig) get(path string, status int) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	return h.run(r, status)
}

type browser struct {
	cookie    *http.Cookie
	csrf      string
	principal string
	link      string
}

func (h *rig) review(state map[string]any, principal string) *browser {
	h.t.Helper()
	path := strings.TrimPrefix(state["human_authorization_url"].(string), h.e.Origin)
	w := h.get(path, 200)
	page := w.Body.String()
	a := strings.Index(page, `<pre hidden id="hg-data">`) + len(`<pre hidden id="hg-data">`)
	b := strings.Index(page[a:], "</pre>") + a
	var data struct {
		Data map[string]any `json:"data"`
	}
	if e := json.Unmarshal([]byte(html.UnescapeString(page[a:b])), &data); e != nil {
		h.t.Fatal(e)
	}
	return &browser{cookie: w.Result().Cookies()[0], csrf: data.Data["csrf"].(string), principal: principal, link: strings.TrimPrefix(path, "/human/")}
}
func (h *rig) human(b *browser, method, path string, value any, status int) *httptest.ResponseRecorder {
	h.t.Helper()
	var raw []byte
	if value != nil {
		raw = jsonBody(value)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", h.e.Origin)
	r.Header.Set("X-HG-CSRF", b.csrf)
	if b.cookie != nil {
		r.AddCookie(b.cookie)
	}
	r.AddCookie(&http.Cookie{Name: "test-host-login", Value: b.principal})
	w := h.run(r, status)
	for _, cookie := range w.Result().Cookies() {
		b.cookie = cookie
	}
	if strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		if csrf, ok := object(w)["csrf"].(string); ok {
			b.csrf = csrf
		}
	}
	return w
}
func (h *rig) verify(b *browser, status int) {
	h.t.Helper()
	base := BrowserPrefix + "/human/" + b.link
	h.human(b, "POST", base+"/begin", map[string]any{}, 200)
	h.human(b, "POST", base+"/verify", map[string]any{}, status)
}
func (h *rig) approve(state map[string]any) *browser {
	h.t.Helper()
	b := h.review(state, "alice")
	h.verify(b, 200)
	h.human(b, "POST", BrowserPrefix+"/human/"+b.link+"/approve", Approval{[]Permission{{ProfileRead, "1"}}, h.s.now + 300}, 200)
	return b
}
func (h *rig) claim(state map[string]any, status int) map[string]any {
	h.t.Helper()
	return object(h.signed("POST", "/v1/requests/"+state["request_id"].(string)+"/claim", map[string]any{}, "anonymous", status))
}
func (h *rig) advance(seconds int64) { h.s.mu.Lock(); h.s.now += seconds; h.s.mu.Unlock() }

func TestPublicSignatureVector(t *testing.T) {
	raw, e := os.ReadFile("testdata/signature.json")
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Method, Origin, Path, Body string
		Created                    int64
		Headers                    map[string]string
	}
	if e = json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest(v.Method, v.Origin+v.Path, strings.NewReader(v.Body))
	r.RequestURI = v.Path
	for k, val := range v.Headers {
		r.Header.Set(k, val)
	}
	p, invalid := VerifySignature(r, []byte(v.Body), v.Origin, v.Created)
	if invalid != nil || p.Actor != "ed25519:If4x36FUomFia_hUBG_SJxt77UtqvkWqWId-9H-XIbk" {
		t.Fatalf("vector: %+v %v", p, invalid)
	}
}
func TestSignatureRejectsTampering(t *testing.T) {
	cases := map[string]func(*http.Request){"body digest": func(r *http.Request) { r.Header.Set("Content-Digest", "wrong") }, "target": func(r *http.Request) { r.URL.Path = "/v1/other"; r.RequestURI = r.URL.Path }, "credential": func(r *http.Request) { r.Header.Set("Authorization", "HumanGrant "+strings.Repeat("A", 43)) }, "duplicate": func(r *http.Request) { r.Header.Add("Signature", r.Header.Get("Signature")) }, "case duplicate": func(r *http.Request) { r.Header["signature"] = []string{r.Header.Get("Signature")} }, "query": func(r *http.Request) { r.URL.RawQuery = "x=1"; r.RequestURI = r.URL.RequestURI() }, "unknown signature parameter": func(r *http.Request) { r.Header.Set("Signature-Input", r.Header.Get("Signature-Input")+`;foo="bar"`) }, "wrong key": func(r *http.Request) { r.Header.Set("X-HG-Key", strings.Repeat("A", 43)) }, "base64 padding": func(r *http.Request) { r.Header.Set("X-HG-Key", r.Header.Get("X-HG-Key")+"=") }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			a := actor(t)
			r := a.request("POST", "/v1/requests", []byte("{}"), "anonymous", 1700000000)
			change(r)
			if _, e := VerifySignature(r, []byte("{}"), "https://auth.example.test", 1700000000); e == nil {
				t.Fatal("accepted tampering")
			}
		})
	}
	a := actor(t)
	r := a.request("GET", "/v1/session", nil, "anonymous", 1700000000)
	for _, now := range []int64{1699999969, 1700000060} {
		if _, e := VerifySignature(r, nil, "https://auth.example.test", now); e == nil {
			t.Fatal("accepted expired/future signature")
		}
	}
	if _, e := VerifySignature(r, nil, "https://wrong.example.test", 1700000000); e == nil {
		t.Fatal("accepted different origin")
	}
}
func TestStrictJSONAndDefaults(t *testing.T) {
	for _, raw := range []string{" ", "null", "[]", `{"x":1}`, `{"enabled":true,"enabled":false}`, `{"enabled":true} {}`, "{\"enabled\":\"\xff\"}"} {
		var v struct {
			Enabled bool `json:"enabled"`
		}
		if strictJSON([]byte(raw), &v) {
			t.Fatalf("accepted %q", raw)
		}
	}
	h := setup(t)
	config := object(h.get("/humangrant/config", 200))
	if config["enabled"] != true {
		t.Fatal(config)
	}
	for _, ttl := range []any{nil, 0, 59, 86401} {
		input := map[string]any{"client_id": ClientID, "target_service": ServiceID, "purpose": "read", "requested_permissions": []Permission{{ProfileRead, "1"}}, "session_ttl_seconds": ttl}
		h.signed("POST", "/v1/requests", input, "anonymous", 400)
	}
	h.create(nil)
}
func TestRealSignatureScopedFlowAndNoCredentialLeak(t *testing.T) {
	h := setup(t)
	state := h.create(nil)
	id := state["request_id"].(string)
	h.claim(state, 409)
	h.approve(state)
	status := object(h.signed("GET", "/v1/requests/"+id, nil, "anonymous", 200))
	if status["status"] != "human_login" || status["phase"] != "approved" {
		t.Fatal(status)
	}
	claim := h.claim(state, 200)
	token := claim["access_token"].(string)
	profile := h.signed("GET", "/v1/accounts/1/profile", nil, token, 200)
	if strings.Contains(profile.Body.String(), "session-alice") || strings.Contains(profile.Body.String(), "Root") || strings.Contains(profile.Body.String(), token) {
		t.Fatal("private state leaked")
	}
	h.signed("GET", "/v1/accounts/2/profile", nil, token, 403)
	h.signed("PUT", "/v1/accounts/1/profile", map[string]any{}, token, 403)
	session := object(h.signed("GET", "/v1/session", nil, token, 200))
	if session["grantor_id"] != "1" || session["actor"].(map[string]any)["kind"] != "ai" {
		t.Fatal(session)
	}
	h.advance(5)
	markdown := h.signed("GET", "/v1/requests/"+id+"/status.md", nil, "anonymous", 200)
	if !strings.HasPrefix(markdown.Header().Get("Content-Type"), "text/markdown") || strings.Contains(markdown.Body.String(), token) {
		t.Fatal("invalid Markdown or leaked token")
	}
	h.claim(state, 409)
}
func TestReplayAcrossEngineRestartAndStolenToken(t *testing.T) {
	h := setup(t)
	state := h.create(nil)
	h.approve(state)
	token := h.claim(state, 200)["access_token"].(string)
	r := h.a.request("GET", "/v1/session", nil, token, h.s.now)
	h.run(r, 200)
	h.e = &Engine{Store: h.s, Origin: h.e.Origin, Now: h.e.Now}
	h.run(r, 401)
	other := actor(t)
	h.run(other.request("GET", "/v1/session", nil, token, h.s.now), 401)
	h.run(other.request("GET", "/v1/requests/"+state["request_id"].(string), nil, "anonymous", h.s.now), 403)
}
func TestBrowserCSRFExpectedAccountAndPermissionSubset(t *testing.T) {
	h := setup(t)
	state := h.create(nil)
	b := h.review(state, "bob")
	h.verify(b, 403)
	b = h.review(state, "alice")
	base := BrowserPrefix + "/human/" + b.link
	wrong := *b
	wrong.csrf = "bad"
	h.human(&wrong, "POST", base+"/begin", map[string]any{}, 403)
	h.human(b, "POST", base+"/approve", Approval{[]Permission{{ProfileRead, "1"}}, h.s.now + 300}, 403)
	before := b.cookie.Value
	h.verify(b, 200)
	if before == b.cookie.Value {
		t.Fatal("cookie not rotated")
	}
	h.human(b, "POST", base+"/approve", Approval{[]Permission{{ProfileRead, "2"}}, h.s.now + 300}, 403)
	h.human(b, "POST", base+"/approve", Approval{[]Permission{}, h.s.now + 300}, 400)
	h.human(b, "POST", base+"/approve", Approval{[]Permission{{"admin.write", "1"}}, h.s.now + 300}, 400)
	h.human(b, "POST", base+"/approve", Approval{[]Permission{{ProfileRead, "1"}}, h.s.now + 99999}, 400)
	changed := *b
	changed.principal = "bob"
	h.human(&changed, "POST", base+"/approve", Approval{[]Permission{{ProfileRead, "1"}}, h.s.now + 300}, 403)
	h.human(b, "POST", base+"/approve", Approval{[]Permission{{ProfileRead, "1"}}, h.s.now + 300}, 200)
	h.human(b, "POST", base+"/approve", Approval{[]Permission{{ProfileRead, "1"}}, h.s.now + 300}, 403)
}
func TestGETDoesNotConsentOrChangePhaseAndEscapesPurpose(t *testing.T) {
	h := setup(t)
	state := h.create(map[string]any{"client_id": ClientID, "target_service": ServiceID, "purpose": "</pre><script>alert(1)</script>", "requested_permissions": []Permission{{ProfileRead, "1"}}})
	path := strings.TrimPrefix(state["human_authorization_url"].(string), h.e.Origin)
	for i := 0; i < 3; i++ {
		w := h.get(path, 200)
		if strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
			t.Fatal("purpose became executable")
		}
	}
	after := object(h.signed("GET", "/v1/requests/"+state["request_id"].(string), nil, "anonymous", 200))
	if after["phase"] != "waiting_human" || after["status_updated_at"] != state["status_updated_at"] {
		t.Fatal(after)
	}
}
func TestConcurrentClaimExactlyOnce(t *testing.T) {
	h := setup(t)
	state := h.create(nil)
	h.approve(state)
	path := "/v1/requests/" + state["request_id"].(string) + "/claim"
	var successes atomic.Int32
	var failures atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := h.a.request("POST", path, []byte("{}"), "anonymous", h.s.now)
			w := httptest.NewRecorder()
			h.e.ServeHTTP(w, r)
			if w.Code == 200 {
				successes.Add(1)
			} else if w.Code == 409 {
				failures.Add(1)
			} else {
				t.Errorf("unexpected %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 || failures.Load() != 11 {
		t.Fatalf("claims %d/%d", successes.Load(), failures.Load())
	}
}
func TestDisableInvalidatesPendingAndIssuedWithoutRevival(t *testing.T) {
	h := setup(t)
	state := h.create(nil)
	h.approve(state)
	token := h.claim(state, 200)["access_token"].(string)
	pending := h.create(nil)
	admin := &browser{principal: "root"}
	h.human(admin, "GET", BrowserPrefix+"/settings", nil, 200)
	nonroot := &browser{principal: "alice"}
	h.human(nonroot, "GET", BrowserPrefix+"/settings", nil, 403)
	h.human(admin, "PUT", BrowserPrefix+"/settings", map[string]any{"enabled": false}, 200)
	if object(h.get("/humangrant/config", 200))["enabled"] != false {
		t.Fatal("switch not visible")
	}
	h.signed("GET", "/v1/session", nil, token, 403)
	h.get("/humangrant", 403)
	h.e = &Engine{Store: h.s, Origin: h.e.Origin, Now: h.e.Now}
	if object(h.get("/humangrant/config", 200))["enabled"] != false {
		t.Fatal("restart reenabled feature")
	}
	h.human(admin, "PUT", BrowserPrefix+"/settings", map[string]any{"enabled": true}, 200)
	h.signed("GET", "/v1/session", nil, token, 401)
	p := object(h.signed("GET", "/v1/requests/"+pending["request_id"].(string), nil, "anonymous", 200))
	if p["phase"] != "cancelled" {
		t.Fatal(p)
	}
}
func TestRevocationAndCurrentHostRights(t *testing.T) {
	for _, change := range []string{"revoke", "disabled", "version", "expiry"} {
		t.Run(change, func(t *testing.T) {
			h := setup(t)
			state := h.create(nil)
			h.approve(state)
			claim := h.claim(state, 200)
			token := claim["access_token"].(string)
			if change == "revoke" {
				b := &browser{principal: "bob"}
				h.human(b, "GET", BrowserPrefix+"/grants", nil, 200)
				h.human(b, "POST", BrowserPrefix+"/grants/"+claim["grant_id"].(string)+"/revoke", map[string]any{}, 403)
				b = &browser{principal: "alice"}
				h.human(b, "GET", BrowserPrefix+"/grants", nil, 200)
				h.human(b, "POST", BrowserPrefix+"/grants/"+claim["grant_id"].(string)+"/revoke", map[string]any{}, 200)
			} else if change == "expiry" {
				h.advance(301)
			} else {
				p := h.s.people["alice"]
				if change == "disabled" {
					p.Active = false
				} else {
					p.Human.AuthVersion++
				}
				h.s.people["alice"] = p
			}
			h.signed("GET", "/v1/accounts/1/profile", nil, token, 401)
			h.advance(5)
			s := object(h.signed("GET", "/v1/requests/"+state["request_id"].(string), nil, "anonymous", 200))
			if s["phase"] != "succeeded" {
				t.Fatal("historical result rewritten")
			}
		})
	}
}
func TestPollingAndRequestExpiry(t *testing.T) {
	h := setup(t)
	state := h.create(nil)
	p := "/v1/requests/" + state["request_id"].(string)
	h.signed("GET", p, nil, "anonymous", 200)
	w := h.signed("GET", p, nil, "anonymous", 429)
	if w.Header().Get("Retry-After") != "10" {
		t.Fatal(w.Header())
	}
	h.advance(601)
	s := object(h.signed("GET", p, nil, "anonymous", 200))
	if s["phase"] != "expired" {
		t.Fatal(s)
	}
	h.claim(state, 410)
}
func TestTransactionalFailureDoesNotIssueState(t *testing.T) {
	h := setup(t)
	h.s.fail = true
	h.signed("POST", "/v1/requests", map[string]any{}, "anonymous", 500)
	if len(h.s.rows) != 0 {
		t.Fatal("failed transaction persisted")
	}
}
func TestBodyBoundAndOriginConfiguration(t *testing.T) {
	h := setup(t)
	h.run(httptest.NewRequest("POST", "/v1/requests", strings.NewReader(strings.Repeat("x", BodyLimit+1))), 413)
	for _, raw := range []string{"https://example.test/path", "http://example.test", "https://user@example.test", "https://example.test?x=1"} {
		if _, e := Origin(raw, false); e == nil {
			t.Fatal(raw)
		}
	}
	if _, e := Origin("http://127.0.0.1:8787", true); e != nil {
		t.Fatal(e)
	}
}
func TestMailPersistentLimitsIdempotencyAndNoDeliveryClaim(t *testing.T) {
	h := setup(t)
	s := h.create(nil)
	p := "/v1/requests/" + s["request_id"].(string) + "/email"
	input := EmailRequest{"person@example.invalid", "first"}
	first := object(h.signed("POST", p, input, "anonymous", 202))
	same := object(h.signed("POST", p, input, "anonymous", 202))
	if first["operation_id"] != same["operation_id"] {
		t.Fatal("idempotency lost")
	}
	h.signed("POST", p, EmailRequest{"other@example.invalid", "first"}, "anonymous", 409)
	h.signed("POST", p, EmailRequest{"person@example.invalid\r\nBcc:x@example.invalid", "bad"}, "anonymous", 400)
	h.e = &Engine{Store: h.s, Origin: h.e.Origin, Now: h.e.Now}
	h.signed("POST", p, EmailRequest{"person@example.invalid", "second"}, "anonymous", 202)
	count := 0
	var out localMail
	for k, v := range h.s.rows {
		if strings.HasPrefix(k, "outbox:") {
			count++
			_ = json.Unmarshal(v.Raw, &out)
		}
	}
	if count != 1 || out.State != "local_spooled" || strings.Contains(first["message"].(string), "delivered") {
		t.Fatalf("outbox %d: %+v", count, out)
	}
	start := strings.Index(out.Message, "/invite/") + len("/invite/")
	end := strings.Index(out.Message[start:], "\r\n") + start
	token := out.Message[start:end]
	w := h.get("/invite/"+token, 200)
	b := &browser{cookie: w.Result().Cookies()[0]}
	page := w.Body.String()
	start = strings.Index(page, `<pre hidden id="hg-data">`) + len(`<pre hidden id="hg-data">`)
	end = strings.Index(page[start:], "</pre>") + start
	var d struct{ Data map[string]any }
	_ = json.Unmarshal([]byte(html.UnescapeString(page[start:end])), &d)
	b.csrf = d.Data["csrf"].(string)
	h.human(b, "POST", "/invite/"+token+"/block", map[string]any{}, 200)
	h.advance(61)
	next := h.create(nil)
	h.signed("POST", "/v1/requests/"+next["request_id"].(string)+"/email", EmailRequest{"PERSON@example.invalid", "third"}, "anonymous", 202)
	count = 0
	for k := range h.s.rows {
		if strings.HasPrefix(k, "outbox:") {
			count++
		}
	}
	if count != 1 {
		t.Fatal("block bypassed")
	}
}
func TestStoredGrantKeepsBindings(t *testing.T) {
	g := Grant{Human: Human{"1", "sid", 2, 3}, Requested: []Permission{{ProfileRead, "1"}}, PublicKey: "key", TokenHash: "hash", Epoch: 9}
	raw, _ := json.Marshal(saveGrant(g))
	var s storedGrant
	_ = json.Unmarshal(raw, &s)
	got := s.grant()
	if got.Human != g.Human || got.Epoch != 9 || got.TokenHash != "hash" || got.PublicKey != "key" || len(got.Requested) != 1 {
		t.Fatal("lost private authority fields")
	}
}

func TestShortSessionTTLStartsAtConsentNotRequest(t *testing.T) {
	h := setup(t)
	state := h.create(map[string]any{"client_id": ClientID, "target_service": ServiceID, "purpose": "read", "requested_permissions": []Permission{{ProfileRead, "1"}}, "session_ttl_seconds": 60})
	if !strings.Contains(state["forwarding_prompt"].(string), state["human_authorization_url"].(string)) {
		t.Fatal("forwarding prompt lost the human link")
	}
	h.advance(120)
	b := h.review(state, "alice")
	h.verify(b, 200)
	h.human(b, "POST", BrowserPrefix+"/human/"+b.link+"/approve", Approval{[]Permission{{ProfileRead, "1"}}, h.s.now + 60}, 200)
	token := h.claim(state, 200)["access_token"].(string)
	h.signed("GET", "/v1/session", nil, token, 200)
	h.advance(60)
	h.signed("GET", "/v1/session", nil, token, 401)
}
