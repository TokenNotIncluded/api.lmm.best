package payments

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// RuntimeOptions bounds a payment worker and its HTTP admission. Server socket
// deadlines and ingress rate limits remain the composition root's responsibility.
type RuntimeOptions struct {
	Interval    time.Duration
	Timeout     time.Duration
	BatchSize   int
	MaxInFlight int
}

// Runtime starts no work during construction. The required probe must check
// payment storage write access and the independently configured Core payment
// capability; a healthy extension process alone is not a payment readiness proof.
// Call Start only after the host listener and its admission gate are ready.
type Runtime struct {
	service *Service
	probe   func(context.Context) error
	options RuntimeOptions
	mu      sync.Mutex
	phase   string
	parent  context.Context
	checked time.Time
	active  int
	idle    chan struct{}
	done    chan struct{}
	drain   chan struct{}
}

func NewRuntime(service *Service, probe func(context.Context) error, options RuntimeOptions) (*Runtime, error) {
	if service == nil || service.store == nil || service.core == nil || probe == nil {
		return nil, ErrInvalid
	}
	if options.Interval == 0 {
		options.Interval = time.Second
	}
	if options.Timeout == 0 {
		options.Timeout = 20 * time.Second
	}
	if options.BatchSize == 0 {
		options.BatchSize = 8
	}
	if options.MaxInFlight == 0 {
		options.MaxInFlight = 8
	}
	if options.Interval <= 0 || options.Interval > time.Minute || options.Timeout <= 0 || options.Timeout > 30*time.Second || options.BatchSize < 1 || options.BatchSize > 100 || options.MaxInFlight < 1 || options.MaxInFlight > 100 {
		return nil, ErrInvalid
	}
	idle, done := make(chan struct{}), make(chan struct{})
	close(idle)
	close(done)
	return &Runtime{service: service, probe: probe, options: options, phase: "preparing", idle: idle, done: done, drain: make(chan struct{})}, nil
}

// Start launches exactly one serial worker. acquire is the existing host.Acquire
// gate: false means the host is not accepting new work. Every accepted batch
// retains that permit until the bounded batch actually returns.
func (r *Runtime) Start(parent context.Context, acquire func() (func(), bool)) error {
	if parent == nil || acquire == nil {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != "preparing" {
		return ErrConflict
	}
	if err := parent.Err(); err != nil {
		return err
	}
	r.phase = "running"
	r.parent = parent
	r.done = make(chan struct{})
	go r.run(parent, acquire)
	return nil
}

func (r *Runtime) run(ctx context.Context, acquire func() (func(), bool)) {
	defer func() {
		r.mu.Lock()
		r.checked = time.Time{}
		if r.phase != "draining" {
			r.phase = "stopped"
		}
		close(r.done)
		r.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.drain:
			return
		default:
		}
		release, ok := acquire()
		if !ok || release == nil {
			return
		}
		r.mu.Lock()
		accepting := r.phase == "running"
		r.mu.Unlock()
		if !accepting {
			release()
			return
		}
		err := r.batch(ctx, release)
		r.mu.Lock()
		r.checked = time.Time{}
		if err == nil && r.phase == "running" {
			r.checked = time.Now()
		}
		r.mu.Unlock()
		timer := time.NewTimer(r.options.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-r.drain:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *Runtime) batch(parent context.Context, release func()) (err error) {
	defer release()
	// A broken provider must not panic the process from a worker goroutine.
	// Preserve its durable lease/unknown result; a later batch can reconcile it.
	defer func() {
		if recover() != nil {
			err = ErrUnavailable
		}
	}()
	ctx, cancel := context.WithTimeout(parent, r.options.Timeout)
	defer cancel()
	if err = r.probe(ctx); err == nil && ctx.Err() == nil {
		err = r.service.Work(ctx, r.options.BatchSize)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (r *Runtime) readyLocked() bool {
	return r.phase == "running" && r.parent.Err() == nil && !r.checked.IsZero() && time.Since(r.checked) <= r.options.Interval+r.options.Timeout
}

func (r *Runtime) Ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readyLocked()
}

// Wrap applies the same bounded admission to private routes and the separate
// callback-only handler. It does not route between them or replace their auth.
// Keep the permit until the handler really exits, including after cancellation;
// releasing it when a timer fires would allow unbounded abandoned handlers.
func (r *Runtime) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		status := http.StatusServiceUnavailable
		if next != nil && r.readyLocked() {
			status = http.StatusTooManyRequests
			if r.active < r.options.MaxInFlight {
				status = http.StatusOK
				if r.active == 0 {
					r.idle = make(chan struct{})
				}
				r.active++
			}
		}
		r.mu.Unlock()
		if status != http.StatusOK {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "1")
			http.Error(w, ErrUnavailable.Error(), status)
			return
		}
		defer func() {
			r.mu.Lock()
			r.active--
			if r.active == 0 {
				close(r.idle)
			}
			r.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(req.Context(), r.options.Timeout)
		defer cancel()
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

// BeginDrain closes admission and stops polling without canceling accepted work.
// Wait must succeed before the owner closes the Core connection or payment DB.
func (r *Runtime) BeginDrain() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != "draining" {
		r.phase = "draining"
		r.checked = time.Time{}
		close(r.drain)
	}
}

func (r *Runtime) Wait(ctx context.Context) error {
	r.mu.Lock()
	if r.phase != "draining" {
		r.mu.Unlock()
		return ErrConflict
	}
	done, idle := r.done, r.idle
	r.mu.Unlock()
	for _, ch := range []<-chan struct{}{done, idle} {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
