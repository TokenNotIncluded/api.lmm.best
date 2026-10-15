// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package remotecontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Compile against the actual RPC client, not only the controlled test double.
var _ Core = (*coreclient.Client)(nil)

const aliceToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const bobToken = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const hostToken = "hhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhh"

type fakeCore struct {
	calls   int
	denied  bool
	offline bool
	kind    pb.CredentialKind
}

func (c *fakeCore) AuthorizeRemote(_ context.Context, token string) (*coreclient.RemoteAuthority, error) {
	c.calls++
	if c.offline {
		return nil, status.Error(codes.Unavailable, "private core error must not leak")
	}
	if c.denied || (token != aliceToken && token != bobToken) {
		return nil, status.Error(codes.Unauthenticated, "revoked secret must not leak")
	}
	id := int64(1)
	if token == bobToken {
		id = 2
	}
	if c.kind != pb.CredentialKind_CREDENTIAL_KIND_SESSION {
		return nil, status.Error(codes.PermissionDenied, "session required")
	}
	return remoteAuthority(id, id), nil
}
func setup(t *testing.T) (*Module, *fakeCore, http.Handler) {
	t.Helper()
	core := &fakeCore{kind: pb.CredentialKind_CREDENTIAL_KIND_SESSION}
	module, err := New(core)
	if err != nil {
		t.Fatal(err)
	}
	host, err := modules.New([]modules.Module{module}, []byte(hostToken))
	if err != nil {
		t.Fatal(err)
	}
	return module, core, host
}
func request(t *testing.T, handler http.Handler, method, path, token string, input any) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, "/extensions/v1/remote-control"+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+hostToken)
	if token != "" {
		req.Header.Set("X-LMM-User-Credential", token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}
func putInput() any { return map[string]any{"device_id": "device_abcdefgh", "metadata": cipher(16)} }
func postInput() any {
	return map[string]any{"sender": "controller", "nonce": cipher(16).Nonce, "ciphertext": cipher(16).Ciphertext}
}
func requireStatus(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status %d != %d: %s", w.Code, code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable private response")
	}
}
func TestHTTPHostAndLiveUserAuthority(t *testing.T) {
	module, core, handler := setup(t)
	requireStatus(t, request(t, handler, "GET", "/pi/sessions", "", nil), 401)
	if core.calls != 0 || len(module.relay.owners) != 0 {
		t.Fatal("host credential granted user authority")
	}
	req := httptest.NewRequest("GET", "/extensions/v1/remote-control/pi/sessions", nil)
	req.Header.Set("X-LMM-User-Credential", aliceToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 || core.calls != 0 {
		t.Fatal("user credential bypassed host")
	}
	requireStatus(t, request(t, handler, "PUT", "/pi/sessions/session_abcdefgh", aliceToken, putInput()), 200)
	core.denied = true
	for _, op := range []struct {
		method, path string
		input        any
	}{
		{"GET", "/pi/sessions", nil}, {"GET", "/pi/sessions/session_abcdefgh/messages", nil},
		{"POST", "/pi/sessions/session_abcdefgh/messages", postInput()}, {"PUT", "/pi/sessions/session_abcdefgh", putInput()},
		{"DELETE", "/pi/sessions/session_abcdefgh", nil},
	} {
		before := core.calls
		response := request(t, handler, op.method, op.path, aliceToken, op.input)
		requireStatus(t, response, 401)
		if core.calls != before+1 || strings.Contains(response.Body.String(), "secret") {
			t.Fatal("cached or leaked core authority")
		}
	}
	if len(module.relay.list(1)) != 1 {
		t.Fatal("revoked request mutated storage")
	}
	core.denied = false
	core.offline = true
	requireStatus(t, request(t, handler, "GET", "/pi/sessions", aliceToken, nil), 503)
	core.offline = false
	core.kind = pb.CredentialKind_CREDENTIAL_KIND_API_KEY
	requireStatus(t, request(t, handler, "GET", "/pi/sessions", aliceToken, nil), 403)
	core.kind = pb.CredentialKind_CREDENTIAL_KIND_UNSPECIFIED
	requireStatus(t, request(t, handler, "GET", "/pi/sessions", aliceToken, nil), 403)
}
func TestHTTPOwnerIsolationAndEnvelopeContract(t *testing.T) {
	_, _, handler := setup(t)
	requireStatus(t, request(t, handler, "PUT", "/pi/sessions/session_abcdefgh", aliceToken, putInput()), 200)
	requireStatus(t, request(t, handler, "POST", "/pi/sessions/session_abcdefgh/messages", aliceToken, postInput()), 200)
	for _, method := range []string{"GET", "POST"} {
		var input any
		if method == "POST" {
			input = postInput()
		}
		requireStatus(t, request(t, handler, method, "/pi/sessions/session_abcdefgh/messages", bobToken, input), 404)
	}
	requireStatus(t, request(t, handler, "DELETE", "/pi/sessions/session_abcdefgh", bobToken, nil), 200)
	list := request(t, handler, "GET", "/pi/sessions", aliceToken, nil)
	requireStatus(t, list, 200)
	var value struct {
		Success bool `json:"success"`
		Data    struct {
			Sessions []Snapshot `json:"sessions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &value); err != nil || !value.Success || len(value.Data.Sessions) != 1 || value.Data.Sessions[0].Generation == "" {
		t.Fatal("invalid snapshot response", err)
	}
	history := request(t, handler, "GET", "/pi/sessions/session_abcdefgh/messages?after=0", aliceToken, nil)
	requireStatus(t, history, 200)
	var rows struct {
		Data struct {
			Messages []Message `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(history.Body.Bytes(), &rows); err != nil || len(rows.Data.Messages) != 1 || rows.Data.Messages[0].Ciphertext != cipher(16) || rows.Data.Messages[0].Sequence != 1 {
		t.Fatal("invalid message response", err)
	}
	empty := request(t, handler, "GET", "/pi/sessions/session_abcdefgh/messages?after=1", aliceToken, nil)
	if !strings.Contains(empty.Body.String(), `"messages":[]`) {
		t.Fatal("empty history is not an array")
	}
	requireStatus(t, request(t, handler, "DELETE", "/pi/sessions/session_abcdefgh", aliceToken, nil), 200)
	requireStatus(t, request(t, handler, "DELETE", "/pi/sessions/session_abcdefgh", aliceToken, nil), 200)
}
func TestHTTPRejectsMalformedAndOversizedInput(t *testing.T) {
	module, _, handler := setup(t)
	for _, input := range []any{
		map[string]any{"device_id": "short", "metadata": cipher(16)},
		map[string]any{"device_id": "device_abcdefgh", "metadata": cipher((16 << 10) + 1)},
		map[string]any{"device_id": "device_abcdefgh", "metadata": Ciphertext{"AAAAAAAAAAAAAAAA=", cipher(16).Ciphertext}},
		map[string]any{"device_id": "device_abcdefgh", "metadata": cipher(16), "user_id": 2},
	} {
		requireStatus(t, request(t, handler, "PUT", "/pi/sessions/session_abcdefgh", aliceToken, input), 400)
	}
	if len(module.relay.owners) != 0 {
		t.Fatal("invalid heartbeat created a session")
	}
	requireStatus(t, request(t, handler, "PUT", "/pi/sessions/session_abcdefgh", aliceToken, putInput()), 200)
	for _, input := range []any{
		map[string]any{"sender": "admin", "nonce": cipher(16).Nonce, "ciphertext": cipher(16).Ciphertext},
		map[string]any{"sender": "plugin", "nonce": cipher(16).Nonce, "ciphertext": cipher((64 << 10) + 1).Ciphertext},
		map[string]any{"sender": "controller", "nonce": cipher(16).Nonce, "ciphertext": cipher(16).Ciphertext, "content": "plaintext"},
		map[string]any{"sender": "controller", "nonce": cipher(16).Nonce, "ciphertext": "AAAAAAAAAAAAAAAAAAAAAB"}, // Nonzero unused padding bits.
	} {
		requireStatus(t, request(t, handler, "POST", "/pi/sessions/session_abcdefgh/messages", aliceToken, input), 400)
	}
	for _, query := range []string{"after=-1", "after=1&after=2", "after=9007199254740992", "after=invalid", "after=%ZZ"} {
		requireStatus(t, request(t, handler, "GET", "/pi/sessions/session_abcdefgh/messages?"+query, aliceToken, nil), 400)
	}
	for _, body := range []string{`{} {}`, `{"device_id":"device_abcdefgh","metadata":`, strings.Repeat(" ", requestLimit+1)} {
		req := httptest.NewRequest("PUT", "/extensions/v1/remote-control/pi/sessions/session_abcdefgh", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+hostToken)
		req.Header.Set("X-LMM-User-Credential", aliceToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		expected := 400
		if len(body) > requestLimit {
			expected = 413
		}
		requireStatus(t, w, expected)
	}
	messages, _ := module.relay.read(1, "session_abcdefgh", 0)
	if len(messages) != 0 {
		t.Fatal("invalid message entered relay")
	}
}
func TestHTTPCanonicalCiphertextBounds(t *testing.T) {
	for _, size := range []int{16, 16 << 10, 64 << 10} {
		if !validCiphertext(cipher(size), size) {
			t.Fatal("valid boundary", size)
		}
	}
	for _, value := range []Ciphertext{{cipher(16).Nonce, cipher(16).Ciphertext + "="}, {cipher(16).Nonce + "\n", cipher(16).Ciphertext}, {cipher(16).Nonce, cipher(16).Ciphertext + "\r\n"}, cipher(15), cipher((64 << 10) + 1)} {
		if validCiphertext(value, 64<<10) {
			t.Fatal("noncanonical/bounded input accepted", fmt.Sprint(len(value.Ciphertext)))
		}
	}
}
func TestHTTPCapacityFailureDoesNotAcknowledge(t *testing.T) {
	module, _, handler := setup(t)
	requireStatus(t, request(t, handler, "PUT", "/pi/sessions/session_abcdefgh", aliceToken, putInput()), 200)
	module.relay.maxRetained = module.relay.retained
	response := request(t, handler, "POST", "/pi/sessions/session_abcdefgh/messages", aliceToken, postInput())
	requireStatus(t, response, 429)
	if strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatal("failed command acknowledged")
	}
	if response.Header().Get("Retry-After") != "1" {
		t.Fatal("no capacity hint")
	}
	module.relay.maxRetained = MaxRetainedBytes
	requireStatus(t, request(t, handler, "POST", "/pi/sessions/session_abcdefgh/messages", aliceToken, postInput()), 200)
}
func TestHTTPRejectsAmbiguousCredentials(t *testing.T) {
	_, core, handler := setup(t)
	for _, credentials := range [][]string{{aliceToken, bobToken}, {strings.Repeat("x", 257)}, {aliceToken + ","}, {"short"}} {
		req := httptest.NewRequest("GET", "/extensions/v1/remote-control/pi/sessions", nil)
		req.Header.Set("Authorization", "Bearer "+hostToken)
		for _, credential := range credentials {
			req.Header.Add("X-LMM-User-Credential", credential)
		}
		before := core.calls
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		requireStatus(t, w, 401)
		if core.calls != before {
			t.Fatal("malformed credentials reached Core")
		}
	}
}

func remoteAuthority(user, owner int64) *coreclient.RemoteAuthority {
	return &coreclient.RemoteAuthority{Source: coreclient.RemoteSource{NativeCredentialID: 1}, UserID: user, OwnerAccountID: owner, AuthVersion: 1, ExpiresAt: time.Now().Add(time.Hour).Unix()}
}

type principalCore struct{ principal *coreclient.RemoteAuthority }

func (c principalCore) AuthorizeRemote(context.Context, string) (*coreclient.RemoteAuthority, error) {
	return c.principal, nil
}
func TestHTTPRejectsIncompleteAndNonSessionPrincipals(t *testing.T) {
	principals := []*coreclient.RemoteAuthority{nil, {}}
	for _, change := range []func(*coreclient.RemoteAuthority){
		func(p *coreclient.RemoteAuthority) { p.Source.NativeCredentialID = 0 },
		func(p *coreclient.RemoteAuthority) { p.UserID = -1 },
		func(p *coreclient.RemoteAuthority) { p.OwnerAccountID = 0 },
		func(p *coreclient.RemoteAuthority) { p.AuthVersion = 0 },
		func(p *coreclient.RemoteAuthority) { p.ExpiresAt = time.Now().Unix() },
		func(p *coreclient.RemoteAuthority) {
			p.Source = coreclient.RemoteSource{GrantID: "lmm_gr_" + strings.Repeat("a", 43), AccessID: "lmm_ac_" + strings.Repeat("a", 43)}
		},
	} {
		p := remoteAuthority(1, 101)
		change(p)
		principals = append(principals, p)
	}
	for _, principal := range principals {
		module, err := New(principalCore{principal})
		if err != nil {
			t.Fatal(err)
		}
		handler, err := modules.New([]modules.Module{module}, []byte(hostToken))
		if err != nil {
			t.Fatal(err)
		}
		requireStatus(t, request(t, handler, "PUT", "/pi/sessions/session_abcdefgh", aliceToken, putInput()), 403)
		if len(module.relay.owners) != 0 {
			t.Fatal("unusable principal created records")
		}
	}
}
func TestHTTPStableOwnerNotUserIDNamespace(t *testing.T) {
	module, _ := New(principalCore{remoteAuthority(1, 101)})
	handler, _ := modules.New([]modules.Module{module}, []byte(hostToken))
	requireStatus(t, request(t, handler, "PUT", "/pi/sessions/session_abcdefgh", aliceToken, putInput()), 200)
	if len(module.relay.list(1)) != 0 || len(module.relay.list(101)) != 1 {
		t.Fatal("relay keyed by user instead of stable owner")
	}
}

func TestNilCoreRejected(t *testing.T) {
	var core *fakeCore
	for _, value := range []Core{nil, core} {
		if _, err := New(value); err == nil {
			t.Fatal("nil core accepted")
		}
	}
}
