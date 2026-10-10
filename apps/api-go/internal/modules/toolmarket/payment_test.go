package toolmarket

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

type fakeFunds struct {
	mu           sync.Mutex
	reserves     map[string]Charge
	payments     map[string]Charge
	reserveCalls int
	commitCalls  int
	failReserve  bool
	failCommit   atomic.Bool
}

func newFunds() *fakeFunds {
	return &fakeFunds{reserves: map[string]Charge{}, payments: map[string]Charge{}}
}
func (f *fakeFunds) Reserve(_ context.Context, credential string, p Principal, c Charge) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if credential != "alice" || p != alice {
		return ErrDenied
	}
	f.reserveCalls++
	if previous, ok := f.reserves[c.ID]; ok && previous != c {
		return ErrConflict
	}
	f.reserves[c.ID] = c
	if f.failReserve {
		return ErrUnavailable
	}
	return nil
}
func (f *fakeFunds) Commit(_ context.Context, credential string, p Principal, c Charge) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if credential != "alice" || p != alice {
		return ErrDenied
	}
	f.commitCalls++
	if f.failCommit.Load() {
		return ErrUnavailable
	}
	if f.reserves[c.ID] != c {
		return ErrConflict
	}
	f.payments[c.ID] = c
	return nil
}
func TestConcurrentDuplicatePayment(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	f := newFunds()
	m.funds = f
	_, i, _ := prepared(t, m, mock, "none", "fixed")
	in := invocation(t, m, alice, i.ID, "same-payment")
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for k := 0; k < 24; k++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := m.execute(context.Background(), alice, "alice", in); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil && !errors.Is(e, ErrPending) {
			t.Fatal(e)
		}
	}
	if _, e := m.execute(context.Background(), alice, "alice", in); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reserveCalls != 1 || len(f.payments) != 1 || mock.calls.Load() != 1 {
		t.Fatalf("reserve=%d payments=%d calls=%d", f.reserveCalls, len(f.payments), mock.calls.Load())
	}
	for _, c := range f.payments {
		if c.Amount != 127 {
			t.Fatal("incorrect charge", c)
		}
	}
}
func TestDuplicateKeyChangedArgumentsRejected(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	f := newFunds()
	m.funds = f
	_, i, _ := prepared(t, m, mock, "none", "fixed")
	in := invocation(t, m, alice, i.ID, "key")
	if _, e := m.execute(context.Background(), alice, "alice", in); e != nil {
		t.Fatal(e)
	}
	in.Arguments = []byte(`{"q":"different"}`)
	if _, e := m.execute(context.Background(), alice, "alice", in); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if mock.calls.Load() != 1 || f.reserveCalls != 1 {
		t.Fatal("duplicate debit")
	}
}
func TestSettlementRetryNeverRepeatsProviderCall(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	f := newFunds()
	m.funds = f
	f.failCommit.Store(true)
	_, i, _ := prepared(t, m, mock, "none", "fixed")
	in := invocation(t, m, alice, i.ID, "commit")
	if _, e := m.execute(context.Background(), alice, "alice", in); !errors.Is(e, ErrPending) {
		t.Fatal(e)
	}
	f.failCommit.Store(false)
	if _, e := m.execute(context.Background(), alice, "alice", in); e != nil {
		t.Fatal(e)
	}
	if mock.calls.Load() != 1 || f.reserveCalls != 1 || f.commitCalls != 2 || len(f.payments) != 1 {
		t.Fatal("settlement replay incorrect")
	}
}
func TestReserveUncertaintyNeverCallsTool(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	f := newFunds()
	f.failReserve = true
	m.funds = f
	_, i, _ := prepared(t, m, mock, "none", "fixed")
	in := invocation(t, m, alice, i.ID, "reserve")
	for k := 0; k < 2; k++ {
		if _, e := m.execute(context.Background(), alice, "alice", in); !errors.Is(e, ErrPending) {
			t.Fatal(e)
		}
	}
	if mock.calls.Load() != 0 || f.reserveCalls != 1 {
		t.Fatal("uncertain reserve repeated")
	}
}
func TestPaymentReplayAfterStoreReopen(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "market.db")
	key := bytes.Repeat([]byte{9}, 32)
	store, e := InitStore(path, key)
	if e != nil {
		t.Fatal(e)
	}
	m.store = store
	f := newFunds()
	m.funds = f
	_, i, _ := prepared(t, m, mock, "none", "fixed")
	in := invocation(t, m, alice, i.ID, "durable")
	if _, e = m.execute(context.Background(), alice, "alice", in); e != nil {
		t.Fatal(e)
	}
	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	store, e = OpenStore(path, key)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	m.store = store
	if _, e = m.execute(context.Background(), alice, "alice", in); e != nil {
		t.Fatal(e)
	}
	if mock.calls.Load() != 1 || f.reserveCalls != 1 || len(f.payments) != 1 {
		t.Fatal("reopen lost idempotency")
	}
}
