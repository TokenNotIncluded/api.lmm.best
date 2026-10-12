package payments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func runtimeForTest(t *testing.T, h *harness, probe func(context.Context) error, options RuntimeOptions) *Runtime {
	t.Helper()
	r, err := NewRuntime(h.s, probe, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		r.BeginDrain()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := r.Wait(ctx); err != nil {
			t.Errorf("payment runtime leaked accepted work: %v", err)
		}
	})
	return r
}

func waitPaymentRuntime(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("payment runtime did not reach expected state")
		}
		time.Sleep(time.Millisecond)
	}
}

func admitPaymentBatch() (func(), bool)     { return func() {}, true }
func healthyPayments(context.Context) error { return nil }

func TestRuntimeDoesNotStartOrAdvertiseReadinessOnConstruction(t *testing.T) {
	h := setup(t)
	var probes atomic.Int64
	r := runtimeForTest(t, h, func(context.Context) error { probes.Add(1); return nil }, RuntimeOptions{})
	if probes.Load() != 0 || r.Ready() {
		t.Fatal("construction performed IO or reported payment readiness")
	}
	rr := httptest.NewRecorder()
	r.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unstarted handler called") })).ServeHTTP(rr, httptest.NewRequest("GET", "/orders", nil))
	if rr.Code != 503 {
		t.Fatal("unstarted runtime accepted traffic")
	}
	if !errors.Is(r.Wait(context.Background()), ErrConflict) {
		t.Fatal("wait before drain must fail")
	}
}

func TestRuntimeRequiresProbeAndBounds(t *testing.T) {
	h := setup(t)
	if _, err := NewRuntime(h.s, nil, RuntimeOptions{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing readiness probe accepted")
	}
	for _, opts := range []RuntimeOptions{{BatchSize: 101}, {BatchSize: -1}, {MaxInFlight: 101}, {MaxInFlight: -1}, {Timeout: -1}, {Timeout: 31 * time.Second}, {Interval: -1}, {Interval: 2 * time.Minute}} {
		if _, err := NewRuntime(h.s, healthyPayments, opts); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid runtime bounds accepted: %+v", opts)
		}
	}
}

func TestRuntimeRunsActualPaymentWorkerAndTracksDependencies(t *testing.T) {
	h := setup(t)
	o := h.create("runtime_real_worker_0001")
	var unavailable atomic.Bool
	unavailable.Store(true)
	r := runtimeForTest(t, h, func(context.Context) error {
		if unavailable.Load() {
			return ErrUnavailable
		}
		return nil
	}, RuntimeOptions{Interval: time.Millisecond, BatchSize: 1})
	if err := r.Start(context.Background(), admitPaymentBatch); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(context.Background(), admitPaymentBatch); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate worker accepted")
	}
	if r.Ready() || h.a.checkoutCalls.Load() != 0 {
		t.Fatal("failed dependency probe permitted work")
	}
	unavailable.Store(false)
	waitPaymentRuntime(t, r.Ready)
	if h.get(o.ID).Checkout.ID == "" || h.a.checkoutCalls.Load() != 1 {
		t.Fatal("readiness preceded first real worker batch")
	}
	unavailable.Store(true)
	waitPaymentRuntime(t, func() bool { return !r.Ready() })
}

func TestRuntimeDrainWaitsForAcceptedRequestsWithoutCancelingThem(t *testing.T) {
	h := setup(t)
	r := runtimeForTest(t, h, healthyPayments, RuntimeOptions{MaxInFlight: 1})
	if err := r.Start(context.Background(), admitPaymentBatch); err != nil {
		t.Fatal(err)
	}
	waitPaymentRuntime(t, r.Ready)
	entered, finish, returned := make(chan context.Context, 1), make(chan struct{}), make(chan struct{})
	handler := r.Wrap(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		entered <- req.Context()
		<-finish
		w.WriteHeader(http.StatusAccepted)
	}))
	rr := httptest.NewRecorder()
	go func() { defer close(returned); handler.ServeHTTP(rr, httptest.NewRequest("POST", "/callbacks/1", nil)) }()
	requestCtx := <-entered
	t.Cleanup(func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
		<-returned
	})
	full := httptest.NewRecorder()
	handler.ServeHTTP(full, httptest.NewRequest("POST", "/callbacks/1", nil))
	if full.Code != 429 {
		t.Fatal("in-flight limit was not enforced")
	}
	r.BeginDrain()
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest("POST", "/callbacks/1", nil))
	if denied.Code != 503 || r.Ready() {
		t.Fatal("draining runtime accepted new work")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if !errors.Is(r.Wait(ctx), context.DeadlineExceeded) {
		t.Fatal("drain reported success while handler still ran")
	}
	if requestCtx.Err() != nil {
		t.Fatal("drain canceled accepted callback")
	}
	close(finish)
	<-returned
	if err := r.Wait(context.Background()); err != nil || rr.Code != 202 {
		t.Fatal("accepted callback did not finish before drain")
	}
	if err := r.Start(context.Background(), admitPaymentBatch); !errors.Is(err, ErrConflict) {
		t.Fatal("drained runtime restarted")
	}
}

func TestRuntimeRetainsPermitAfterRequestDeadline(t *testing.T) {
	h := setup(t)
	r := runtimeForTest(t, h, healthyPayments, RuntimeOptions{Interval: time.Millisecond, Timeout: 10 * time.Millisecond, MaxInFlight: 1})
	if err := r.Start(context.Background(), admitPaymentBatch); err != nil {
		t.Fatal(err)
	}
	waitPaymentRuntime(t, r.Ready)
	deadline, finish, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := r.Wrap(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		<-req.Context().Done()
		close(deadline)
		<-finish
	}))
	go func() {
		defer close(returned)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/callbacks/1", nil))
	}()
	t.Cleanup(func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
		<-returned
	})
	<-deadline
	r.mu.Lock()
	active := r.active
	r.mu.Unlock()
	if active != 1 {
		t.Fatal("timeout released a still-running handler's permit")
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("POST", "/callbacks/1", nil))
	if rr.Code != 429 && rr.Code != 503 {
		t.Fatal("timed-out active handler allowed extra work")
	}
	close(finish)
	<-returned
}

func TestRuntimeContainsWorkerPanicAndReleasesHostPermit(t *testing.T) {
	h := setup(t)
	var releases atomic.Int64
	r := runtimeForTest(t, h, func(context.Context) error { panic("synthetic failure") }, RuntimeOptions{Interval: time.Millisecond})
	if err := r.Start(context.Background(), func() (func(), bool) { return func() { releases.Add(1) }, true }); err != nil {
		t.Fatal(err)
	}
	waitPaymentRuntime(t, func() bool { return releases.Load() > 0 })
	if r.Ready() {
		t.Fatal("worker panic reported readiness")
	}
}

func TestRuntimeStopsWithHostAdmissionAndParent(t *testing.T) {
	for _, reject := range []bool{true, false} {
		t.Run(map[bool]string{true: "host_gate", false: "parent_context"}[reject], func(t *testing.T) {
			h := setup(t)
			r := runtimeForTest(t, h, healthyPayments, RuntimeOptions{Interval: time.Millisecond})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := r.Start(ctx, func() (func(), bool) { return func() {}, !reject }); err != nil {
				t.Fatal(err)
			}
			if !reject {
				waitPaymentRuntime(t, r.Ready)
				cancel()
			}
			select {
			case <-r.done:
			case <-time.After(time.Second):
				t.Fatal("worker did not stop")
			}
			if r.Ready() {
				t.Fatal("stopped worker remained ready")
			}
		})
	}
}

func TestRuntimeDrainDoesNotStartLateBatch(t *testing.T) {
	h := setup(t)
	var probes, releases atomic.Int64
	r := runtimeForTest(t, h, func(context.Context) error { probes.Add(1); return nil }, RuntimeOptions{})
	acquiring, grant := make(chan struct{}), make(chan struct{})
	if err := r.Start(context.Background(), func() (func(), bool) {
		close(acquiring)
		<-grant
		return func() { releases.Add(1) }, true
	}); err != nil {
		t.Fatal(err)
	}
	<-acquiring
	r.BeginDrain()
	close(grant)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if probes.Load() != 0 || releases.Load() != 1 {
		t.Fatal("drain admitted a late batch or leaked the host permit")
	}
}

func TestRuntimeDrainWaitsForAcceptedWorker(t *testing.T) {
	h := setup(t)
	entered, finish := make(chan context.Context, 1), make(chan struct{})
	var releases atomic.Int64
	r := runtimeForTest(t, h, func(ctx context.Context) error { entered <- ctx; <-finish; return nil }, RuntimeOptions{})
	t.Cleanup(func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
	})
	if err := r.Start(context.Background(), func() (func(), bool) { return func() { releases.Add(1) }, true }); err != nil {
		t.Fatal(err)
	}
	batchCtx := <-entered
	r.BeginDrain()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if !errors.Is(r.Wait(ctx), context.DeadlineExceeded) || releases.Load() != 0 || batchCtx.Err() != nil {
		t.Fatal("drain abandoned accepted worker ownership")
	}
	close(finish)
	if err := r.Wait(context.Background()); err != nil || releases.Load() != 1 {
		t.Fatal("accepted worker did not release before drain completed")
	}
}
