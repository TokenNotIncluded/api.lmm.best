package modules

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type moduleProbe struct {
	name string
	h    http.Handler
}

func (m *moduleProbe) Name() string          { return m.name }
func (m *moduleProbe) Handler() http.Handler { return m.h }

func TestHostRejectsTypedNilModuleAndHandler(t *testing.T) {
	var absent *moduleProbe
	var handler http.HandlerFunc
	for _, registration := range [][]Module{{absent}, {&moduleProbe{"test", handler}}} {
		if _, err := New(registration, []byte(strings.Repeat("x", 32))); err == nil {
			t.Fatal("typed nil must be rejected")
		}
	}
}

func TestServiceCredentialDoesNotReachModule(t *testing.T) {
	token := strings.Repeat("x", 32)
	calls := 0
	host, err := New([]Module{&moduleProbe{"inspect", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-LMM-User-Credential") != "user-secret" {
			t.Error("credential boundary was not preserved")
		}
		w.WriteHeader(http.StatusNoContent)
	})}}, []byte(token))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/extensions/v1/inspect/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-LMM-User-Credential", "user-secret")
	w := httptest.NewRecorder()
	host.ServeHTTP(w, r)
	if w.Code != 204 || calls != 1 {
		t.Fatalf("status=%d calls=%d", w.Code, calls)
	}
	if r.Header.Get("Authorization") != "Bearer "+token {
		t.Fatal("caller request was mutated")
	}
	r.Header.Add("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	host.ServeHTTP(w, r)
	if w.Code != 401 || calls != 1 {
		t.Fatal("duplicate service credentials accepted")
	}
}

func TestBusyModuleDoesNotBlockHealthOrOtherModules(t *testing.T) {
	entered := make(chan struct{}, MaxInFlightPerModule)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	blocked := &moduleProbe{"slow", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	})}
	fast := &moduleProbe{"fast", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })}
	token := strings.Repeat("x", 32)
	host, err := New([]Module{blocked, fast}, []byte(token))
	if err != nil {
		t.Fatal(err)
	}
	call := func(path string) int {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		host.ServeHTTP(w, r)
		return w.Code
	}
	var wg sync.WaitGroup
	for range MaxInFlightPerModule {
		wg.Add(1)
		go func() { defer wg.Done(); call("/extensions/v1/slow/") }()
	}
	for range MaxInFlightPerModule {
		<-entered
	}
	if got := call("/extensions/v1/slow/"); got != 503 {
		t.Fatalf("overflow=%d", got)
	}
	if got := call("/extensions/v1/fast/"); got != 204 {
		t.Fatalf("unrelated module=%d", got)
	}
	if got := call("/health/live"); got != 200 {
		t.Fatalf("health=%d", got)
	}
	releaseOnce.Do(func() { close(release) })
	wg.Wait()
	if got := call("/extensions/v1/slow/"); got != 204 {
		t.Fatalf("released capacity=%d", got)
	}
}
