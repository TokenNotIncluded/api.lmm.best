package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Deliberately test-only: production has no volatile-storage fallback.
type memoryStore struct {
	mu     sync.Mutex
	orders map[string]Order
	events map[string]Event
}

func newMemory() *memoryStore {
	return &memoryStore{orders: map[string]Order{}, events: map[string]Event{}}
}
func copyOrder(o Order) Order {
	b, _ := json.Marshal(o)
	var out Order
	_ = json.Unmarshal(b, &out)
	return out
}
func (m *memoryStore) Insert(ctx context.Context, o Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := m.orders[o.ID]; ok {
		return nil
	}
	o.Version = 1
	if _, err := encodeOrder(o); err != nil {
		return err
	}
	m.orders[o.ID] = copyOrder(o)
	return nil
}
func (m *memoryStore) Get(ctx context.Context, id string) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Order{}, err
	}
	o, ok := m.orders[id]
	if !ok {
		return Order{}, ErrNotFound
	}
	return copyOrder(o), nil
}
func (m *memoryStore) FindPayment(ctx context.Context, ns, tx string) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.orders {
		if o.Namespace == ns && o.PaymentID == tx {
			return copyOrder(o), nil
		}
	}
	return Order{}, ErrNotFound
}
func (m *memoryStore) Mutate(ctx context.Context, id string, fn func(*Order) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	old, ok := m.orders[id]
	if !ok {
		return ErrNotFound
	}
	o := copyOrder(old)
	if err := fn(&o); err != nil {
		return err
	}
	if !sameIdentity(old, o) {
		return ErrConflict
	}
	if o.PaymentID != "" {
		for key, other := range m.orders {
			if key != id && other.Namespace == o.Namespace && other.PaymentID == o.PaymentID {
				return ErrConflict
			}
		}
	}
	o.Version++
	if _, err := encodeOrder(o); err != nil {
		return err
	}
	m.orders[id] = copyOrder(o)
	return nil
}
func (m *memoryStore) PutEvent(ctx context.Context, e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.events[e.Key]; ok {
		if old.Digest != e.Digest {
			return ErrConflict
		}
		return nil
	}
	m.events[e.Key] = e
	return nil
}
func (m *memoryStore) Events(ctx context.Context, now time.Time, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for _, e := range m.events {
		if e.Status == "pending" && !e.NextAttempt.After(now) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (m *memoryStore) FinishEvent(ctx context.Context, key, status, code string, next time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.events[key]
	if e.Status == "done" {
		return nil
	}
	e.Status = status
	e.ErrorCode = code
	e.NextAttempt = next
	m.events[key] = e
	return nil
}
func (m *memoryStore) DueOrders(ctx context.Context, now time.Time, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for id, o := range m.orders {
		d := o.due()
		if !d.IsZero() && !d.After(now) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type fakeCore struct {
	mu                        sync.Mutex
	channel                   Channel
	intents                   map[string]Intent
	requests                  map[string]PrepareRequest
	receipts                  map[string]Receipt
	calls                     map[string]int
	lose                      map[string]bool
	deny                      bool
	offline                   bool
	invalidReceipt            bool
	creditGate, creditStarted chan struct{}
}

func newCore(c Channel) *fakeCore {
	return &fakeCore{channel: c, intents: map[string]Intent{}, requests: map[string]PrepareRequest{}, receipts: map[string]Receipt{}, calls: map[string]int{}, lose: map[string]bool{}}
}
func (f *fakeCore) Prepare(ctx context.Context, user string, r PrepareRequest) (Intent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls["prepare"]++
	if f.deny || user == "denied" {
		return Intent{}, ErrDenied
	}
	if f.offline {
		return Intent{}, ErrUnavailable
	}
	if old, ok := f.intents[r.Key]; ok {
		if f.requests[r.Key] != r {
			return Intent{}, ErrConflict
		}
		return old, nil
	}
	v := Intent{ID: int64(len(f.intents) + 1), AccountID: r.AccountID, CreatedBy: 7, Provider: f.channel.Provider, Merchant: f.channel.Merchant, Environment: f.channel.Environment, Currency: r.Currency, AmountMinor: r.AmountMinor, CreditUnits: 12345, CreditUnit: "credit_500k_usd", Key: r.Key, RequestHash: make([]byte, 32)}
	f.intents[r.Key] = v
	f.requests[r.Key] = r
	return v, nil
}
func (f *fakeCore) Read(ctx context.Context, user string, i Intent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls["read"]++
	if f.deny || user == "denied" {
		return ErrDenied
	}
	if f.offline {
		return ErrUnavailable
	}
	return nil
}
func (f *fakeCore) post(kind string, id int64, key, state string, e Evidence) (Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[kind]++
	if f.offline {
		return Receipt{}, ErrUnavailable
	}
	if kind != "hold" && (e.Source != "webhook" || len(e.Signature) == 0) {
		return Receipt{}, ErrEvidence
	}
	if old, ok := f.receipts[key]; ok {
		return old, nil
	}
	r := Receipt{ID: "receipt_" + key, IntentID: id, Key: key, RequestHash: make([]byte, 32), State: state, JournalID: int64(len(f.receipts) + 1)}
	if f.invalidReceipt {
		r.State = "queued"
		return r, nil
	}
	f.receipts[key] = r
	if f.lose[kind] {
		delete(f.lose, kind)
		return Receipt{}, ErrUnavailable
	}
	return r, nil
}
func (f *fakeCore) Credit(ctx context.Context, id int64, key string, e Evidence) (Receipt, error) {
	if f.creditStarted != nil {
		f.creditStarted <- struct{}{}
		<-f.creditGate
	}
	return f.post("credit", id, key, CreditApplied, e)
}
func (f *fakeCore) Hold(ctx context.Context, user string, id int64, key string, n int64) (Receipt, error) {
	f.mu.Lock()
	denied := f.deny || user == "denied"
	f.mu.Unlock()
	if denied {
		return Receipt{}, ErrDenied
	}
	return f.post("hold", id, key, RefundHeld, Evidence{})
}
func (f *fakeCore) Commit(ctx context.Context, id int64, key, hold string, e Evidence) (Receipt, error) {
	if hold == "" {
		return Receipt{}, ErrInvalid
	}
	return f.post("commit", id, key, RefundCommitted, e)
}
func (f *fakeCore) Release(ctx context.Context, id int64, key, hold string, e Evidence) (Receipt, error) {
	if hold == "" {
		return Receipt{}, ErrInvalid
	}
	return f.post("release", id, key, RefundReleased, e)
}
func (f *fakeCore) ExternalRefund(ctx context.Context, id int64, key string, e Evidence) (Receipt, error) {
	return f.post("external", id, key, ExternalRefundApplied, e)
}
func (f *fakeCore) Receipt(ctx context.Context, id int64, key string) (Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls["lookup"]++
	if f.offline {
		return Receipt{}, ErrUnavailable
	}
	r, ok := f.receipts[key]
	if !ok {
		return Receipt{}, ErrNotFound
	}
	return r, nil
}
func (f *fakeCore) count(k string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls[k] }

type fakeAdapter struct {
	c                  Channel
	refundCalls        atomic.Int64
	checkoutCalls      atomic.Int64
	window             time.Duration
	refundErr          error
	disabled, fullOnly bool
	observations       []Event
}

func (f *fakeAdapter) Channel() Channel { return f.c }
func (f *fakeAdapter) Checkout(ctx context.Context, o Order, k string) (Checkout, error) {
	f.checkoutCalls.Add(1)
	return Checkout{ID: "cs_" + o.ID, URL: "https://checkout.stripe.com/test"}, nil
}
func (f *fakeAdapter) Verify(r *http.Request, now time.Time) (Event, error) {
	return Event{}, ErrUnsupported
}
func (f *fakeAdapter) Lookup(ctx context.Context, o Order) ([]Event, error) {
	return f.observations, nil
}
func (f *fakeAdapter) Refund(ctx context.Context, o Order, r Refund, k string) (RefundResult, error) {
	if r.HoldReceipt == "" {
		return RefundResult{}, ErrInvalid
	}
	f.refundCalls.Add(1)
	return RefundResult{ID: "re_" + r.ID, Status: "succeeded"}, f.refundErr
}
func (f *fakeAdapter) RefundEnabled() bool               { return !f.disabled }
func (f *fakeAdapter) PartialRefunds() bool              { return !f.fullOnly }
func (f *fakeAdapter) ReplayWindow(string) time.Duration { return f.window }

type harness struct {
	t     *testing.T
	s     *Service
	m     *memoryStore
	c     *fakeCore
	a     *fakeAdapter
	clock atomic.Int64
}

func setup(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, m: newMemory()}
	h.a = &fakeAdapter{c: Channel{ID: 1, Provider: "stripe", Merchant: "acct_fixture", Environment: "test", Currency: "USD", Enabled: true}, window: 23 * time.Hour}
	h.c = newCore(h.a.c)
	var err error
	h.s, err = New(h.m, h.c, h.a)
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Store(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC).Unix())
	h.s.now = func() time.Time { return time.Unix(h.clock.Load(), 0).UTC() }
	return h
}
func (h *harness) create(key string) Order {
	h.t.Helper()
	o, err := h.s.Create(context.Background(), "user", PrepareRequest{Key: key, AccountID: 99, ChannelID: 1, Currency: "USD", AmountMinor: 1000})
	if err != nil {
		h.t.Fatal(err)
	}
	return o
}
func (h *harness) get(id string) Order {
	h.t.Helper()
	o, err := h.m.Get(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return o
}
func (h *harness) work() {
	h.t.Helper()
	if err := h.s.Work(context.Background(), 100); err != nil {
		h.t.Fatal(err)
	}
}
func (h *harness) advance() { h.clock.Add(65) }
func (h *harness) event(o Order, id, kind string, n int64) Event {
	return Event{OrderID: o.ID, Type: kind, Currency: "USD", AmountMinor: n, Evidence: Evidence{Provider: "stripe", Merchant: "acct_fixture", Environment: "test", Transaction: "pi_" + o.ID, EventID: id, Payload: []byte(`{"fixture":true}`), Signature: []byte("signed_fixture"), Source: "webhook"}}
}
func (h *harness) send(e Event) {
	h.t.Helper()
	if err := h.s.receiveEvent(context.Background(), h.a, e); err != nil {
		h.t.Fatal(err)
	}
}
func (h *harness) paid() Order {
	o := h.create("create_fixture_0001")
	h.work()
	h.send(h.event(o, "evt_paid", Paid, 1000))
	h.work()
	o = h.get(o.ID)
	if !o.Credited() {
		h.t.Fatal(fmt.Sprint(o.State(), o.Jobs))
	}
	return o
}
