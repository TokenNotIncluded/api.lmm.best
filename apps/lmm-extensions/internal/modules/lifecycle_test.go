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
