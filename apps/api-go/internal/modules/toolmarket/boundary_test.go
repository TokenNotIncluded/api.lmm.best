package toolmarket

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHTTPNativeOAuthCookieAndCallback(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i := registered(t, m, mock, "oauth")
	w := post(t, m, "/oauth/start", "alice", `{"installation":"`+i.ID+`"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var start oauthStart
	if e := json.Unmarshal(w.Body.Bytes(), &start); e != nil {
		t.Fatal(e)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/" {
		t.Fatal("unsafe binding cookie", cookies)
	}
	if strings.Contains(w.Body.String(), cookies[0].Value) {
		t.Fatal("binding token in JSON")
	}
	callback := func(credential, extra string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/oauth/callback?state="+start.State+extra, nil)
		r.Header.Set("X-LMM-User-Credential", credential)
		r.Header.Set("X-LMM-Account-ID", alice.Account)
		r.AddCookie(cookies[0])
		out := httptest.NewRecorder()
		m.Handler().ServeHTTP(out, r)
		return out
	}
	if out := callback("bob", "&code=code"); out.Code != 401 {
		t.Fatal("wrong callback identity", out.Code, out.Body.String())
	}
	out := callback("alice", "&code=code&iss="+url.QueryEscape(mock.server.URL+"/tenant"))
	if out.Code != 200 || strings.Contains(out.Body.String(), "access-") {
		t.Fatal(out.Code, out.Body.String())
	}
	if cs := out.Result().Cookies(); len(cs) != 1 || cs[0].MaxAge != -1 {
		t.Fatal("binding cookie not removed")
	}
	if out = callback("alice", "&code=code"); out.Code != 401 {
		t.Fatal("callback replay", out.Code)
	}
}
func TestHTTPProviderDenialConsumesFlow(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i := registered(t, m, mock, "oauth")
	start, e := m.startOAuth(context.Background(), alice, i.ID)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "/oauth/callback?state="+start.State+"&error=access_denied&error_description=not-approved", nil)
	r.Header.Set("X-LMM-User-Credential", "alice")
	r.Header.Set("X-LMM-Account-ID", alice.Account)
	r.AddCookie(&http.Cookie{Name: cookieName(start.State), Value: start.binding})
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 401 || mock.tokens.Load() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	if e = m.callbackOAuth(context.Background(), alice, start.State, start.binding, "code", ""); !errors.Is(e, ErrAuth) {
		t.Fatal("denied flow reused", e)
	}
}
func TestNewOAuthFlowInvalidatesEarlierCallback(t *testing.T) {
	mock := newMock(t)
	mock.blockToken = make(chan struct{})
	mock.enteredToken = make(chan struct{}, 1)
	m := testModule(t, mock)
	_, i := registered(t, m, mock, "oauth")
	first, e := m.startOAuth(context.Background(), alice, i.ID)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- m.callbackOAuth(context.Background(), alice, first.State, first.binding, "code", "") }()
	<-mock.enteredToken
	second, e := m.startOAuth(context.Background(), alice, i.ID)
	if e != nil {
		t.Fatal(e)
	}
	close(mock.blockToken)
	if e = <-done; !errors.Is(e, ErrConflict) {
		t.Fatal("old callback overwrote new authorization", e)
	}
	if e = m.callbackOAuth(context.Background(), alice, second.State, second.binding, "code", ""); e != nil {
		t.Fatal(e)
	}
}
func TestUncertainRefreshRequiresReauthorization(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i := registered(t, m, mock, "oauth")
	doOAuth(t, m, alice, i.ID)
	mock.failToken = true
	for k := 0; k < 2; k++ {
		if e := m.refreshOAuth(context.Background(), alice, i.ID); !errors.Is(e, ErrAuth) {
			t.Fatal(e)
		}
	}
	if mock.tokens.Load() != 2 {
		t.Fatal("rotating refresh token replayed")
	}
}
func TestClosedSchemaSubsetNestedConstraints(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"items":{"type":"array","minItems":1,"maxItems":2,"items":{"type":"integer","enum":[1,2]}},"flag":{"type":"boolean"},"nothing":{"type":"null"}},"required":["items"],"additionalProperties":false}`)
	for _, args := range []string{`{"items":[1.0],"flag":true,"nothing":null}`, `{"items":[1,2]}`} {
		if e := validateArgs(raw, json.RawMessage(args)); e != nil {
			t.Fatal(args, e)
		}
	}
	for _, args := range []string{`{"items":[]}`, `{"items":[1,2,1]}`, `{"items":[3]}`, `{"items":[1],"flag":"true"}`, `{"items":[1],"nothing":1}`} {
		if e := validateArgs(raw, json.RawMessage(args)); e == nil {
			t.Fatal("invalid nested args", args)
		}
	}
	if e := validateArgs(json.RawMessage(`{"type":"object"}`), json.RawMessage(`{"provider_allows_extra":true}`)); e != nil {
		t.Fatal("JSON Schema additionalProperties default", e)
	}
	for _, schema := range []string{`null`, `{"type":"string","enum":[]}`, `{"type":"string","maxLength":null}`} {
		if _, e := readSchema(json.RawMessage(schema), 0); e == nil {
			t.Fatal("invalid schema", schema)
		}
	}
	for _, a := range []any{map[string]any{"n": json.Number("1")}, []any{json.Number("1")}} {
		b, _ := json.Marshal(a)
		var copy any
		_ = decodeStrict(b, &copy)
		if !jsonEqual(a, copy) {
			t.Fatal("enum equality")
		}
	}
}
func TestHTTPMetaExecutionAndStatus(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	in := invocation(t, m, alice, i.ID, "native-call")
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "request", "method": "tools/call", "params": map[string]any{"name": "market.execute", "arguments": in}})
	w := post(t, m, "/mcp", "alice", string(body))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "mock result") || strings.Contains(w.Body.String(), `"error"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = post(t, m, "/executions/status", "alice", `{"idempotency_key":"native-call"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"done"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = post(t, m, "/executions/status", "bob", `{"idempotency_key":"native-call"}`); w.Code != 404 {
		t.Fatal("cross-user execution record", w.Code)
	}
}
func TestMCPInitializationAndRequestValidation(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	w := post(t, m, "/mcp", "alice", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	if !strings.Contains(w.Body.String(), `"serverInfo"`) {
		t.Fatal(w.Body.String())
	}
	w = post(t, m, "/mcp", "alice", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	for _, raw := range []string{`{}`, `{"jsonrpc":"2.0","id":{},"method":"tools/list"}`, `{"jsonrpc":"2.0","method":"tools/list"}`, `{"jsonrpc":"2.0","id":1,"method":"unknown"}`, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"market.load","arguments":{}}}`} {
		w = post(t, m, "/mcp", "alice", raw)
		if !strings.Contains(w.Body.String(), `"error"`) {
			t.Fatal("bad RPC accepted", w.Body.String())
		}
	}
}
func TestCallerCannotMutateCommittedStore(t *testing.T) {
	s := &Store{data: emptyDB()}
	var escaped map[string]Secret
	if e := s.update(func(d *database) error { d.Secrets["id"] = Secret{Access: "original"}; escaped = d.Secrets; return nil }); e != nil {
		t.Fatal(e)
	}
	escaped["id"] = Secret{Access: "changed"}
	if e := s.view(func(d database) error {
		if d.Secrets["id"].Access != "original" {
			t.Fatal("transaction leaked mutable data")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestAllErrorsAreSanitized(t *testing.T) {
	for _, e := range []error{ErrInvalid, ErrDenied, ErrMissing, ErrConflict, ErrUnavailable, ErrFunds, ErrPrice, ErrAuth, ErrUpstream, ErrLimit, ErrPending, context.Canceled, context.DeadlineExceeded, errors.New("access_token=secret")} {
		w := httptest.NewRecorder()
		respond(w, nil, e)
		if w.Code < 400 || strings.Contains(w.Body.String(), "access_token") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func TestHostCredentialDoesNotAuthorizeUser(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	r := httptest.NewRequest("POST", "/meta/search", strings.NewReader(`{"query":""}`))
	r.Header.Set("Authorization", "Bearer host-service-token")
	r.Header.Set("X-LMM-Account-ID", alice.Account)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("service credential granted user authority", w.Code)
	}
}

func TestListIncludesDisabledButNeverForeignTokens(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "api_key", "free")
	if e := m.enable(alice, EnableInput{i.ID, false}); e != nil {
		t.Fatal(e)
	}
	w := post(t, m, "/installations/list", "alice", `{"query":""}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) || strings.Contains(w.Body.String(), "secret-alice") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = post(t, m, "/installations/list", "bob", `{"query":""}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), i.ID) {
		t.Fatal("foreign installation", w.Code, w.Body.String())
	}
	w = post(t, m, "/catalog", "bob", `{"query":""}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), mock.server.URL) {
		t.Fatal("catalog lacks provider information", w.Code, w.Body.String())
	}
}
