//go:build rt20 && linux

package admission

// This opt-in probe runs the real limiter over loopback TCP. The HTTP handler is
// a test fixture, NOT main's router. A/B are client labels, NOT authenticated
// accounts. There is no database, upstream, billing, decoder or task worker.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	rt20Requests    = 16
	rt20Connections = 4
	rt20Input       = 16 << 20
	rt20Body        = 4 << 20
)

type rt20Budget struct {
	mu                 sync.Mutex
	opened, live, peak int
	written            int64
}

type rt20Conn struct {
	net.Conn
	budget *rt20Budget
	once   sync.Once
}

func (c *rt20Conn) Write(p []byte) (int, error) {
	c.budget.mu.Lock()
	if c.budget.written+int64(len(p)) > rt20Input {
		c.budget.mu.Unlock()
		return 0, fmt.Errorf("RT20 total input budget reached")
	}
	// Reserve before writing. Partial writes still consume the reservation.
	c.budget.written += int64(len(p))
	c.budget.mu.Unlock()
	return c.Conn.Write(p)
}

func (c *rt20Conn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		c.budget.mu.Lock()
		c.budget.live--
		c.budget.mu.Unlock()
	})
	return err
}

func (b *rt20Budget) dial(address string) (*rt20Conn, error) {
	b.mu.Lock()
	if b.opened >= rt20Requests || b.live >= rt20Connections {
		b.mu.Unlock()
		return nil, fmt.Errorf("RT20 connection/request budget reached")
	}
	b.opened++
	b.live++
	if b.live > b.peak {
		b.peak = b.live
	}
	b.mu.Unlock()
	conn, err := net.DialTimeout("tcp4", address, time.Second)
	if err != nil {
		b.mu.Lock()
		b.live--
		b.mu.Unlock()
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	return &rt20Conn{Conn: conn, budget: b}, nil
}

func TestRT20ProcessRecovery(t *testing.T) {
	if os.Getenv("RT20_BOUNDED_RUNNER") != "1" {
		t.Fatal("run with scripts/resource-audit/rt20.py; hard process limits are required")
	}
	for _, bound := range []struct {
		resource int
		maximum  uint64
	}{
		{syscall.RLIMIT_AS, 2 << 30}, {syscall.RLIMIT_CPU, 10}, {syscall.RLIMIT_NOFILE, 64},
	} {
		var limit syscall.Rlimit
		if err := syscall.Getrlimit(bound.resource, &limit); err != nil || limit.Max > bound.maximum {
			t.Fatalf("missing hard resource bound %d: limit=%+v error=%v", bound.resource, limit, err)
		}
	}
	start := time.Now()
	emit := func(kind string, fields map[string]any) {
		fields["kind"] = kind
		fields["ms"] = float64(time.Since(start).Microseconds()) / 1000
		fields["scope"] = "admission_component_not_main"
		data, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("RT20 %s", data)
	}
	limiter := NewLargeRequestLimiter(2, DefaultLargeRequestThreshold)
	budget := &rt20Budget{}
	var serverConnections atomic.Int64
	var handled atomic.Int64
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handled.Add(1)
			release, ok := limiter.TryAcquire(r)
			w.Header().Set("Connection", "close")
			if !ok {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			defer release()
			hash := sha256.New()
			n, err := io.Copy(hash, io.LimitReader(r.Body, rt20Body+1))
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if n > rt20Body {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			w.Header().Set("X-RT20-SHA256", hex.EncodeToString(hash.Sum(nil)))
			w.WriteHeader(http.StatusOK)
		}),
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				serverConnections.Add(1)
			}
			if state == http.StateClosed {
				serverConnections.Add(-1)
			}
		},
	}
	server.SetKeepAlivesEnabled(false)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		select {
		case err := <-serveDone:
			if err != nil && err != http.ErrServerClosed {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	})
	wait := func(condition func() bool, reason string) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for !condition() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if !condition() {
			t.Fatal(reason)
		}
	}
	snapshot := func(phase string) {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		budget.mu.Lock()
		fields := map[string]any{
			"phase": phase, "admission_active": limiter.active.Load(),
			"server_connections": serverConnections.Load(), "goroutines": runtime.NumGoroutine(),
			"heap_alloc_bytes": mem.HeapAlloc, "go_sys_bytes": mem.Sys,
			"opened_requests": budget.opened, "client_connections": budget.live,
			"peak_client_connections": budget.peak, "reserved_wire_bytes": budget.written,
			"handled": handled.Load(), "application_queue": nil, "frozen_funds": nil,
		}
		budget.mu.Unlock()
		emit("snapshot", fields)
	}
	header := func(c net.Conn, label string, length int) {
		t.Helper()
		_, err := fmt.Fprintf(c, "POST /rt20-body-probe HTTP/1.1\r\nHost: loopback.invalid\r\nX-RT20-Client: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", label, length)
		if err != nil {
			t.Fatal(err)
		}
	}
	probe := func(label string, body string, declared, want int, pause time.Duration) {
		t.Helper()
		begin := time.Now()
		c, err := budget.dial(listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		header(c, label, declared)
		if pause > 0 && len(body) > 0 {
			if _, err = io.WriteString(c, body[:1]); err != nil {
				t.Fatal(err)
			}
			time.Sleep(pause)
			bodyTail := body[1:]
			if _, err = io.WriteString(c, bodyTail); err != nil {
				t.Fatal(err)
			}
		} else if body != "" {
			if _, err = io.WriteString(c, body); err != nil {
				t.Fatal(err)
			}
		}
		response, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: http.MethodPost})
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != want {
			t.Fatalf("%s status=%d want=%d", label, response.StatusCode, want)
		}
		if want == http.StatusOK {
			expected := sha256.Sum256([]byte(body))
			if response.Header.Get("X-RT20-SHA256") != hex.EncodeToString(expected[:]) {
				t.Fatal("successful response did not process the complete body")
			}
		}
		emit("request", map[string]any{"client": label, "status": response.StatusCode,
			"latency_ms": float64(time.Since(begin).Microseconds()) / 1000, "body_bytes": len(body),
			"declared_bytes": declared, "intentional_pause_ms": pause.Milliseconds()})
	}
	probe("B-baseline", "normal", 6, 200, 0)
	wait(func() bool { return serverConnections.Load() == 0 }, "baseline did not settle")
	snapshot("baseline")

	var stalled []*rt20Conn
	for i := 0; i < 2; i++ {
		c, err := budget.dial(listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		stalled = append(stalled, c)
		t.Cleanup(func() { _ = c.Close() })
		header(c, "A-stalled", rt20Body)
		if _, err = io.WriteString(c, "x"); err != nil {
			t.Fatal(err)
		}
	}
	wait(func() bool { return limiter.active.Load() == 2 }, "two slots were not held")
	snapshot("A_holds_two_slots")
	for i := 0; i < 4; i++ {
		probe("B-small-during-A", "normal", 6, 200, 0)
	}
	// Send only headers on the known-full pool. This checks early rejection
	// without wasting another 4 MiB of input or draining a rejected body.
	probe("B-large-during-A", "", rt20Body, 429, 0)
	for i := 0; i < 5; i++ {
		time.Sleep(100 * time.Millisecond)
		snapshot("holding")
	}
	stop := time.Now()
	emit("stop", map[string]any{"action": "close_two_stalled_clients", "server_shutdown": false})
	for _, c := range stalled {
		_ = c.Close()
	}
	for _, offset := range []time.Duration{0, 100 * time.Millisecond, 300 * time.Millisecond, 700 * time.Millisecond, 1500 * time.Millisecond} {
		if remaining := time.Until(stop.Add(offset)); remaining > 0 {
			time.Sleep(remaining)
		}
		snapshot("recovery")
	}
	wait(func() bool { return limiter.active.Load() == 0 && serverConnections.Load() == 0 }, "resources did not recover while listener remained running")
	large := strings.Repeat("x", rt20Body)
	probe("B-large-after-recovery", large, len(large), 200, 0)
	probe("normal-slow-large", large, len(large), 200, 200*time.Millisecond)
	wait(func() bool { return limiter.active.Load() == 0 && serverConnections.Load() == 0 }, "normal controls did not settle")
	snapshot("final_before_server_shutdown")
	budget.mu.Lock()
	opened, peak, written, live := budget.opened, budget.peak, budget.written, budget.live
	budget.mu.Unlock()
	if opened != 10 || peak > 3 || written > rt20Input || live != 0 {
		t.Fatalf("budget/counter mismatch: opened=%d peak=%d bytes=%d live=%d", opened, peak, written, live)
	}
	emit("result", map[string]any{"component_pass": true, "main_acceptance": "not_run",
		"requests": opened, "peak_client_connections": peak, "reserved_wire_bytes": written,
		"frozen_funds_delta": nil, "funds_status": "not_measured", "automatic_timeout_tested": false})
}
