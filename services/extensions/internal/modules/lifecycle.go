package modules

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

const DefaultMaxInFlight = 16
const MaximumMaxInFlight = 64

var ErrNotAccepting = errors.New("extension host not accepting")
var ErrSaturated = errors.New("extension host busy")

// Host adds process admission to the existing authenticated module router.
// Readiness describes this Go host, not availability of Rust or payment APIs.
// Construction is not readiness. Call MarkReady only after local preparation
// and listener binding succeed. No runtime module loading is involved.
type Host struct {
	limit    int
	handler  http.Handler
	mu       sync.Mutex
	phase    string
	active   int
	idle     chan struct{}
	drainers []interface{ BeginDrain() }
}

func NewHost(registered []Module, credential []byte) (*Host, error) {
	return NewHostWithLimit(registered, credential, DefaultMaxInFlight)
}

// NewHostWithLimit fixes a process-wide bound for this host lifetime.
// This is an admission limit, not a measured machine capacity guarantee.
func NewHostWithLimit(registered []Module, credential []byte, limit int) (*Host, error) {
	if limit < 1 || limit > MaximumMaxInFlight {
		return nil, errors.New("extension max_in_flight must be between 1 and 64")
	}
	handler, err := New(registered, credential)
	if err != nil {
		return nil, err
	}
	idle := make(chan struct{})
	close(idle)
	host := &Host{handler: handler, phase: "preparing", idle: idle, limit: limit}
	for _, module := range registered {
		if drainer, ok := module.(interface{ BeginDrain() }); ok {
			host.drainers = append(host.drainers, drainer)
		}
	}
	return host, nil
}

func (h *Host) MarkReady() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.phase != "preparing" {
		return errors.New("extension host cannot become ready from " + h.phase)
	}
	h.phase = "ready"
	return nil
}

// BeginDrain closes admission permanently. It does not cancel accepted work.
// Stop worker polling through the same Acquire gate before closing its storage.
func (h *Host) BeginDrain() {
	h.mu.Lock()
	first := h.phase != "draining"
	h.phase = "draining"
	h.mu.Unlock()
	if first {
		for _, drainer := range h.drainers {
			drainer.BeginDrain()
		}
	}
}

// Acquire admits one HTTP request or one bounded worker operation. The caller
// must defer the returned release function. Keep the permit until the operation
// has durably recorded its outcome, including an unknown external outcome.
func (h *Host) Acquire() (release func(), ok bool) {
	release, err := h.TryAcquire()
	return release, err == nil
}

// TryAcquire rejects immediately without creating a waiter. Saturation is
// temporary; callers must not confuse it with permanent lifecycle shutdown.
func (h *Host) TryAcquire() (func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.phase != "ready" {
		return nil, ErrNotAccepting
	}
	if h.active >= h.limit {
		return nil, ErrSaturated
	}
	if h.active == 0 {
		h.idle = make(chan struct{})
	}
	h.active++
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.active--
			if h.active == 0 {
				close(h.idle)
			}
		})
	}, nil
}

// Wait is valid after BeginDrain. It never releases ownership or reports success
// on timeout. A caller must not close module storage while accepted work runs.
func (h *Host) Wait(ctx context.Context) error {
	h.mu.Lock()
	if h.phase != "draining" {
		h.mu.Unlock()
		return errors.New("extension host must begin draining before wait")
	}
	idle := h.idle
	h.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Do not wrap ResponseWriter: streaming, flush and hijack support remain
	// with the existing HTTP server. Upgraded/background work must use Acquire.
	if r.URL.Path == "/health/live" {
		h.handler.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/health/ready" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		h.mu.Lock()
		phase := h.phase
		h.mu.Unlock()
		status := http.StatusServiceUnavailable
		if phase == "ready" {
			status = http.StatusOK
		}
		writeJSON(w, status, map[string]string{"service": "lmm-extensions", "status": phase})
		return
	}
	release, err := h.TryAcquire()
	if err != nil {
		w.Header().Set("Retry-After", "1")
		label := "extension_not_accepting"
		if errors.Is(err, ErrSaturated) {
			label = "extension_busy"
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": label})
		return
	}
	defer release()
	h.handler.ServeHTTP(w, r)
}
