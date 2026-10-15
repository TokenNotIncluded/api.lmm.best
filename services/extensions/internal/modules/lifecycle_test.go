package modules

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type rolloutModule struct{ handler http.Handler }

func (m rolloutModule) Name() string          { return "rollout" }
func (m rolloutModule) Handler() http.Handler { return m.handler }

func makeRolloutHost(t *testing.T, handler http.Handler) *Host {
	t.Helper()
	h, err := NewHost([]Module{rolloutModule{handler}}, []byte(strings.Repeat("x", 32)))
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func status(h http.Handler, path string) int {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
	h.ServeHTTP(w, r)
	return w.Code
}
func TestRolloutReadinessAndDrain(t *testing.T) {
	h := makeRolloutHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	if status(h, "/health/ready") != 503 || status(h, "/extensions/v1/rollout/x") != 503 || status(h, "/health/live") != 200 {
		t.Fatal("preparing admission")
	}
	if err := h.Wait(context.Background()); err == nil {
		t.Fatal("wait accepted before drain")
	}
	if err := h.MarkReady(); err != nil {
		t.Fatal(err)
	}
	if status(h, "/health/ready") != 200 || status(h, "/extensions/v1/rollout/x") != 204 {
		t.Fatal("ready admission")
	}
	// Existing host authentication must still run after the admission gate.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/extensions/v1/rollout/x", nil))
	if w.Code != 401 {
		t.Fatal("host authentication bypass")
	}
	h.BeginDrain()
	if status(h, "/health/ready") != 503 || status(h, "/extensions/v1/rollout/x") != 503 || status(h, "/health/live") != 200 {
		t.Fatal("draining admission")
	}
	if h.MarkReady() == nil {
		t.Fatal("drained host revived")
	}
	if err := h.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestRolloutAcceptedStreamCompletes(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	h := makeRolloutHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		close(entered)
		<-finish
		_, _ = io.WriteString(w, "event: done\ndata: complete\n\n")
	}))
	if err := h.MarkReady(); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	defer s.Close()
	req, _ := http.NewRequest("GET", s.URL+"/extensions/v1/rollout/stream", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
	response, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	<-entered
	h.BeginDrain()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err = h.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("early wait: %v", err)
	}
	if status(h, "/extensions/v1/rollout/stream") != 503 {
		t.Fatal("accepted new stream")
	}
	close(finish)
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "data: first\n\nevent: done\ndata: complete\n\n" {
		t.Fatalf("truncated stream: %q", body)
	}
	if err = h.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestRolloutWorkerPermitAndDrainRace(t *testing.T) {
	for n := 0; n < 100; n++ {
		h := makeRolloutHost(t, http.NotFoundHandler())
		if err := h.MarkReady(); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if release, ok := h.Acquire(); ok {
					release()
					release()
				}
			}()
		}
		h.BeginDrain()
		wg.Wait()
		if release, ok := h.Acquire(); ok {
			release()
			t.Fatal("admitted after drain")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := h.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
	}
}

// @mark extensions.admission role=test
func TestGlobalAdmissionStrictBoundAndRelease(t *testing.T) {
	for _, n := range []int{0, -1, MaximumMaxInFlight + 1} {
		if _, err := NewHostWithLimit(nil, []byte(strings.Repeat("x", 32)), n); err == nil {
			t.Fatal("invalid limit", n)
		}
	}
	h, err := NewHostWithLimit(nil, []byte(strings.Repeat("x", 32)), 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.TryAcquire(); !errors.Is(err, ErrNotAccepting) {
		t.Fatal(err)
	}
	if err = h.MarkReady(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	releases := make(chan func(), 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, e := h.TryAcquire()
			if e == nil {
				releases <- r
			} else if !errors.Is(e, ErrSaturated) {
				t.Error(e)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(releases)
	if len(releases) != 3 {
		t.Fatal("accepted", len(releases))
	}
	if status(h, "/health/live") != 200 || status(h, "/health/ready") != 200 {
		t.Fatal("health admission")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/extensions/v1/unknown/", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "extension_busy") || w.Header().Get("Retry-After") != "1" {
		t.Fatal(w)
	}
	release := <-releases
	release()
	release()
	replacement, e := h.TryAcquire()
	if e != nil {
		t.Fatal(e)
	}
	h.BeginDrain()
	if _, e = h.TryAcquire(); !errors.Is(e, ErrNotAccepting) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(h.Wait(ctx), context.Canceled) {
		t.Fatal("drain lost outstanding ownership")
	}
	replacement()
	for release = range releases {
		release()
	}
	if e = h.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestCanceledAndPanickingHandlerKeepPermitUntilReturn(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	h, err := NewHostWithLimit([]Module{rolloutModule{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		<-finish
		panic("synthetic")
	})}}, []byte(strings.Repeat("x", 32)), 1)
	if err != nil {
		t.Fatal(err)
	}
	_ = h.MarkReady()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		r := httptest.NewRequest("GET", "/extensions/v1/rollout/x", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
		h.ServeHTTP(httptest.NewRecorder(), r)
	}()
	<-entered
	cancel()
	if _, err = h.TryAcquire(); !errors.Is(err, ErrSaturated) {
		t.Fatal("cancel released permit early", err)
	}
	close(finish)
	<-done
	release, err := h.TryAcquire()
	if err != nil {
		t.Fatal("panic leaked permit", err)
	}
	release()
}
