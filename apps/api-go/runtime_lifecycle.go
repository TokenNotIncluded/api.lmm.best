package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	// The package-owned systemd unit allows 45 seconds. Keep one process-wide
	// drain budget below that supervisor deadline so flush/exit retains margin.
	httpShutdownTimeout    = 30 * time.Second
	maxHTTPShutdownTimeout = 40 * time.Second
	loopShutdownTimeout    = 10 * time.Second
)

func configuredShutdownTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return httpShutdownTimeout
	}
	maxSeconds := int(maxHTTPShutdownTimeout / time.Second)
	if seconds > maxSeconds {
		return maxHTTPShutdownTimeout
	}
	return time.Duration(seconds) * time.Second
}

// runtimeLoops owns goroutines started by the application. Stop closes
// admission before cancelling the root context, and Wait is always bounded by
// its caller-provided context.
type runtimeLoops struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	stopping bool
	active   int
	idle     chan struct{}
}

func newRuntimeLoops(parent context.Context) *runtimeLoops {
	ctx, cancel := context.WithCancel(parent)
	idle := make(chan struct{})
	close(idle)
	return &runtimeLoops{ctx: ctx, cancel: cancel, idle: idle}
}

func (r *runtimeLoops) Go(fn func(context.Context)) bool {
	if fn == nil {
		return false
	}
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		return false
	}
	if r.active == 0 {
		r.idle = make(chan struct{})
	}
	r.active++
	r.mu.Unlock()

	gopool.Go(func() {
		defer r.done()
		fn(r.ctx)
	})
	return true
}

func (r *runtimeLoops) done() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active--
	if r.active == 0 {
		close(r.idle)
	}
}

func (r *runtimeLoops) Stop() {
	r.mu.Lock()
	if !r.stopping {
		r.stopping = true
		r.cancel()
	}
	r.mu.Unlock()
}

func (r *runtimeLoops) Wait(ctx context.Context) error {
	r.mu.Lock()
	idle := r.idle
	r.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type shutdownSteps struct {
	markUnready  func()
	stopLoops    func()
	drainSockets func(context.Context) error
	shutdownHTTP func(context.Context) error
	waitLoops    func(context.Context) error
	flushQuota   func()
	flushBatch   func()
	flushPerf    func()
	closeValkey  func() error
	closeDB      func() error
}

// shutdownRuntime makes the process unavailable first, stops background work
// before draining HTTP, then persists in-memory state before closing its data
// stores. Every drain shares one process-wide deadline; bounded substeps may
// use a shorter cap but can never extend the supervisor-safe total budget.
func shutdownRuntime(steps shutdownSteps, totalTimeout, waitTimeout time.Duration) error {
	if steps.markUnready != nil {
		steps.markUnready()
	}
	if steps.stopLoops != nil {
		steps.stopLoops()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), totalTimeout)
	defer shutdownCancel()
	boundedStep := func(run func(context.Context) error) error {
		if run == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(shutdownCtx, waitTimeout)
		defer cancel()
		return run(ctx)
	}

	var result error
	result = errors.Join(result, boundedStep(steps.drainSockets))
	if steps.shutdownHTTP != nil {
		result = errors.Join(result, steps.shutdownHTTP(shutdownCtx))
	}
	result = errors.Join(result, boundedStep(steps.waitLoops))
	if steps.flushQuota != nil {
		steps.flushQuota()
	}
	if steps.flushBatch != nil {
		steps.flushBatch()
	}
	if steps.flushPerf != nil {
		steps.flushPerf()
	}
	if steps.closeValkey != nil {
		result = errors.Join(result, steps.closeValkey())
	}
	if steps.closeDB != nil {
		result = errors.Join(result, steps.closeDB())
	}
	return result
}
