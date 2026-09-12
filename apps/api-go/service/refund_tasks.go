package service

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/bytedance/gopkg/util/gopool"
)

// RefundTaskReport describes execution, NOT proof that money was refunded.
type RefundTaskReport struct {
	Accepted, Finished, Failed, Active uint64
}

type refundTaskTracker struct {
	mu     sync.Mutex
	sealed bool
	idle   chan struct{}
	report RefundTaskReport
}

func (t *refundTaskTracker) begin() (func(bool), error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sealed {
		return nil, errors.New("refund task admission closed; refund not executed")
	}
	if t.report.Active == 0 {
		t.idle = make(chan struct{})
	}
	t.report.Active++
	t.report.Accepted++
	return func(failed bool) {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.report.Active--
		t.report.Finished++
		if failed {
			t.report.Failed++
		}
		if t.report.Active == 0 {
			close(t.idle)
		}
	}, nil
}

func executeRefundTask(fn func() error, done func(bool)) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("refund task panicked; financial outcome unknown")
		}
		done(err != nil)
	}()
	return fn()
}

func (t *refundTaskTracker) run(fn func() error) error {
	done, err := t.begin()
	if err != nil {
		return err
	}
	return executeRefundTask(fn, done)
}

func (t *refundTaskTracker) goRun(fn func() error, reportError func(error)) error {
	done, err := t.begin()
	if err != nil {
		return err
	}
	gopool.Go(func() {
		// Error reporting is part of the tracked execution too.
		_ = executeRefundTask(func() error {
			err := executeRefundTask(fn, func(bool) {})
			if err != nil {
				reportError(err)
			}
			return err
		}, done)
	})
	return nil
}

// Called only after HTTP, sockets and loops have stopped producing work.
// Submissions racing an active drain are admitted. Zero and sealing share a
// mutex, avoiding WaitGroup Add/Wait races and stale idle-channel snapshots.
func (t *refundTaskTracker) drain(ctx context.Context) (RefundTaskReport, error) {
	for {
		t.mu.Lock()
		if err := ctx.Err(); err != nil {
			r := t.report
			t.mu.Unlock()
			return r, fmt.Errorf("refund tasks not drained: %w", err)
		}
		if t.report.Active == 0 {
			t.sealed = true
			r := t.report
			t.mu.Unlock()
			if r.Failed != 0 {
				return r, fmt.Errorf("refund execution ended with %d failed tasks; financial reconciliation required", r.Failed)
			}
			return r, nil
		}
		idle := t.idle
		t.mu.Unlock()
		select {
		case <-idle:
		case <-ctx.Done():
		}
	}
}

var billingRefundTasks = &refundTaskTracker{}

func DrainBillingRefundTasks(ctx context.Context) (RefundTaskReport, error) {
	return billingRefundTasks.drain(ctx)
}
