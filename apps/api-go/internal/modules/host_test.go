package modules

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testModule struct {
	name    string
	handler http.Handler
}

func (m testModule) Name() string          { return m.name }
func (m testModule) Handler() http.Handler { return m.handler }

var testCredential = []byte(strings.Repeat("test-only-", 4))

func request(h http.Handler, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestModulePathsAndAuthentication(t *testing.T) {
	called := 0
	handler, err := New([]Module{testModule{"catalog", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.URL.Path != "/entry" || r.URL.RawQuery != "q=value" {
			t.Fatalf("unexpected module URL: %s", r.URL)
		}
		w.WriteHeader(http.StatusNoContent)
	})}}, testCredential)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "incorrect"} {
		if got := request(handler, "/extensions/v1/catalog/entry?q=value", token).Code; got != http.StatusUnauthorized {
			t.Fatalf("status=%d", got)
		}
	}
	if called != 0 {
		t.Fatal("unauthorized request reached a module")
	}
	if got := request(handler, "/extensions/v1/catalog/entry?q=value", string(testCredential)).Code; got != http.StatusNoContent {
		t.Fatalf("status=%d", got)
	}
	if called != 1 {
		t.Fatal("authorized request did not reach module exactly once")
	}
	for _, path := range []string{"/v1/chat/completions", "/api/user/self", "/internal/v1/billing", "/catalog/entry", "/extensions/v1/missing/"} {
		if got := request(handler, path, string(testCredential)).Code; got != http.StatusNotFound {
			t.Fatalf("%s status=%d", path, got)
		}
	}
}

func TestModuleRegistrationRejectsUnsafeBoundaries(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, name := range []string{"", "modules", "../billing", "v1/test", "Billing", strings.Repeat("a", 33)} {
		t.Run(name, func(t *testing.T) {
			if _, err := New([]Module{testModule{name, handler}}, testCredential); err == nil {
				t.Fatal("accepted unsafe name")
			}
		})
	}
	for _, registered := range [][]Module{{nil}, {testModule{"foo", nil}}, {testModule{"foo", handler}, testModule{"foo", handler}}} {
		if _, err := New(registered, testCredential); err == nil {
			t.Fatal("accepted invalid registration")
		}
	}
	for _, credential := range [][]byte{nil, []byte("short"), make([]byte, 4097)} {
		if _, err := New(nil, credential); err == nil {
			t.Fatal("accepted invalid credential")
		}
	}
}

func TestModuleInventoryIsAuthenticatedAndDeterministic(t *testing.T) {
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	credential := append([]byte(nil), testCredential...)
	handler, err := New([]Module{testModule{"zeta", noop}, testModule{"alpha", noop}}, credential)
	if err != nil {
		t.Fatal(err)
	}
	credential[0] ^= 1
	response := request(handler, "/extensions/v1/modules", string(testCredential))
	if response.Code != http.StatusOK || response.Body.String() != `{"contract_version":1,"modules":["alpha","zeta"]}`+"\n" {
		t.Fatalf("inventory: %d %s", response.Code, response.Body)
	}
}

func TestHealthNeedsNeitherCoreNorModuleCalls(t *testing.T) {
	handler, err := New(nil, testCredential)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health/live", "/health/ready"} {
		if got := request(handler, path, "").Code; got != http.StatusOK {
			t.Fatalf("status=%d", got)
		}
	}
	response := request(handler, "/extensions/v1/modules", string(testCredential))
	if !strings.Contains(response.Body.String(), `"modules":[]`) {
		t.Fatalf("empty inventory=%s", response.Body)
	}
}

func TestModuleReceivesCancellation(t *testing.T) {
	handler, err := New([]Module{testModule{"cancel", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != context.Canceled {
			t.Fatal("module lost request cancellation")
		}
	})}}, testCredential)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodGet, "/extensions/v1/cancel/", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+string(testCredential))
	handler.ServeHTTP(httptest.NewRecorder(), r)
}
