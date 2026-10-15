package composition

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/payments"
)

// Real production StartWorkers and payments.Service.Work; only storage and Core
// ports are synthetic. No database or money-transfer acceptance is claimed.
type admissionStore struct {
	payments.Store
	calls                     atomic.Int32
	entered, finish, canceled chan struct{}
}

func (s *admissionStore) Events(ctx context.Context, now time.Time, limit int) ([]payments.Event, error) {
	s.calls.Add(1)
	return nil, nil
}
func (s *admissionStore) DueOrders(ctx context.Context, now time.Time, limit int) ([]string, error) {
	close(s.entered)
	<-ctx.Done()
	close(s.canceled)
	<-s.finish
	return nil, ctx.Err()
}

// @mark extensions.recovery role=test
func TestRealPaymentWorkerRetriesSaturationAndJoinsOwner(t *testing.T) {
	s := &admissionStore{entered: make(chan struct{}), finish: make(chan struct{}), canceled: make(chan struct{})}
	svc, e := payments.New(s, structCore{})
	if e != nil {
		t.Fatal(e)
	}
	h, e := modules.NewHostWithLimit(nil, []byte(strings.Repeat("x", 32)), 1)
	if e != nil {
		t.Fatal(e)
	}
	_ = h.MarkReady()
	release, e := h.TryAcquire()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Runtime{Payments: svc}
	r.StartWorkers(ctx, h)
	var finishOnce sync.Once
	finish := func() { finishOnce.Do(func() { close(s.finish) }) }
	t.Cleanup(func() { cancel(); finish(); _ = r.Close() })
	// First actual production tick must see saturation and leave recovery alive.
	time.Sleep(5500 * time.Millisecond)
	if s.calls.Load() != 0 {
		t.Fatal("busy worker entered payment storage")
	}
	select {
	case <-r.workerDone:
		t.Fatal("busy worker stopped permanently")
	default:
	}
	release()
	select {
	case <-s.entered:
	case <-time.After(6 * time.Second):
		t.Fatal("payment recovery never resumed")
	}
	h.BeginDrain()
	cancel()
	select {
	case <-s.canceled:
	case <-time.After(time.Second):
		t.Fatal("worker did not observe cancellation")
	}
	closed := make(chan struct{})
	r.close = []func() error{func() error { close(closed); return nil }}
	done := make(chan struct{})
	go func() { _ = r.Close(); close(done) }()
	select {
	case <-closed:
		t.Fatal("closed storage before actual worker return")
	case <-time.After(20 * time.Millisecond):
	}
	finish()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker owner did not join")
	}
	if e = h.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
}

// The empty embedded port panics if Work unexpectedly attempts financial RPC.
type structCore struct{ payments.Core }
