package payments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type proofFaultCore struct {
	Core
	change func(*ProofReceipt)
}

func (c proofFaultCore) AcceptProof(ctx context.Context, id int64, e Evidence) (ProofReceipt, error) {
	p, err := c.Core.AcceptProof(ctx, id, e)
	if err == nil {
		c.change(&p)
	}
	return p, err
}

type proofFaultStore struct {
	Store
	fail bool
}

func (s *proofFaultStore) PutEvent(ctx context.Context, e Event) error {
	if s.fail {
		return ErrUnavailable
	}
	return s.Store.PutEvent(ctx, e)
}

func proofStripe(t *testing.T) (*Stripe, string) {
	t.Helper()
	s := stripeFixture(t)
	raw := envelope("payment_intent.succeeded", "evt_proof", paymentObject)
	s.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v1/account" {
			return response(`{"id":"acct_fixture"}`), nil
		}
		return response(raw), nil
	})}
	return s, raw
}

func proofCallback(s *Service, raw string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/callbacks/1", strings.NewReader(raw))
	r.Header.Set("Stripe-Signature", "untrusted-delivery-header")
	w := httptest.NewRecorder()
	s.WebhookHandler().ServeHTTP(w, r)
	return w
}

func TestWebhookAckRequiresRustProofAndDurableInbox(t *testing.T) {
	h := setup(t)
	o := h.create("create_fixture_0001")
	stripe, raw := proofStripe(t)
	store := &proofFaultStore{Store: h.m}
	service, err := New(store, h.c, stripe)
	if err != nil {
		t.Fatal(err)
	}
	service.now = h.s.now
	h.c.offline = true
	if w := proofCallback(service, raw); w.Code != 503 || len(h.m.events) != 0 {
		t.Fatal("offline Rust was acknowledged", w.Code)
	}
	h.c.offline = false
	store.fail = true
	if w := proofCallback(service, raw); w.Code != 503 || len(h.m.events) != 0 || h.c.count("accept_proof") != 1 {
		t.Fatal("lost inbox write acknowledged", w.Code)
	}
	store.fail = false
	h.clock.Add(600)
	if w := proofCallback(service, raw); w.Code != 200 || len(h.m.events) != 1 || h.c.count("accept_proof") != 1 {
		t.Fatal("accepted proof not recovered before freshness check", w.Code)
	}
	if h.get(o.ID).Credited() || h.c.count("credit") != 0 {
		t.Fatal("proof ACK claimed settlement")
	}
	if err = service.Work(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	if !h.get(o.ID).Credited() || h.c.count("credit") != 1 {
		t.Fatal("accepted proof failed to settle")
	}
}

func TestWebhookRejectsMismatchedRustProofReceipt(t *testing.T) {
	for name, change := range map[string]func(*ProofReceipt){
		"intent":      func(p *ProofReceipt) { p.IntentID++ },
		"event":       func(p *ProofReceipt) { p.EventID = "evt_other" },
		"payload":     func(p *ProofReceipt) { p.PayloadSHA256 = []byte("wrong") },
		"merchant":    func(p *ProofReceipt) { p.Payment.Merchant = "acct_other" },
		"transaction": func(p *ProofReceipt) { p.Payment.Transaction = "pi_other" },
		"acceptance":  func(p *ProofReceipt) { p.AcceptedUnixSeconds = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			h := setup(t)
			h.create("create_fixture_0001")
			stripe, raw := proofStripe(t)
			service, err := New(h.m, proofFaultCore{Core: h.c, change: change}, stripe)
			if err != nil {
				t.Fatal(err)
			}
			if w := proofCallback(service, raw); w.Code != 409 || len(h.m.events) != 0 {
				t.Fatal("unbound proof acknowledged", w.Code)
			}
		})
	}
}

func TestUnsupportedWebhookNeverReturnsSuccessfulAck(t *testing.T) {
	h := setup(t)
	w := proofCallback(h.s, `{}`)
	if w.Code != 422 || len(h.m.events) != 0 || h.c.count("accept_proof") != 0 {
		t.Fatal("unsupported webhook acknowledged", w.Code)
	}
}

func TestOldQueuedJobLooksUpAcceptedProofAfterDeliveryExpires(t *testing.T) {
	h := setup(t)
	o := h.create("create_fixture_0001")
	h.work()
	e := h.event(o, "evt_accepted_before_crash", Paid, 1000)
	p, err := h.c.AcceptProof(context.Background(), o.Intent.ID, e.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	key := stableKey("credit", o.ID)
	if err = h.m.Mutate(context.Background(), o.ID, func(o *Order) error {
		enqueue(o, Job{Key: key, Kind: "credit", Evidence: e.Evidence}, h.s.now())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.clock.Add(600)
	h.work()
	got := h.get(o.ID)
	if !got.Credited() || got.Jobs[key].Evidence.AcceptedProofID != p.ID || h.c.count("accept_proof") != 1 || h.c.count("lookup_proof") != 1 {
		t.Fatal("worker did not recover durable proof")
	}
}

func TestDoneInboxWithoutProofRepairsQuarantinedCreditJob(t *testing.T) {
	h := setup(t)
	o := h.create("create_fixture_0001")
	h.work()
	e := h.event(o, "evt_legacy_done", Paid, 1000)
	e.ChannelID = h.a.c.ID
	e.Key = eventKey(e)
	e.Digest = eventDigest(e)
	e.Status = "done"
	e.NextAttempt = h.s.now()
	if err := h.m.PutEvent(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	key := stableKey("credit", o.ID)
	if err := h.m.Mutate(context.Background(), o.ID, func(o *Order) error {
		o.ProviderPaid = true
		o.PaymentID = e.Evidence.Transaction
		o.Jobs[key] = Job{Key: key, Kind: "credit", State: "review", ErrorCode: ErrEvidence.Error(), Evidence: e.Evidence}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.clock.Add(600)
	h.send(e)
	if h.m.events[e.Key].Status != "pending" || h.m.events[e.Key].Evidence.AcceptedProofID == "" {
		t.Fatal("done inbox blocked stronger proof")
	}
	if err := h.s.Drain(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	j := h.get(o.ID).Jobs[key]
	if j.State != "pending" || j.Evidence.AcceptedProofID == "" {
		t.Fatal("quarantined credit job not repaired")
	}
	h.work()
	if !h.get(o.ID).Credited() || h.c.count("credit") != 1 {
		t.Fatal("repaired proof did not settle exactly once")
	}
	h.send(e)
	h.work()
	if h.c.count("credit") != 1 {
		t.Fatal("final credit reopened")
	}
}

func TestDurableProofRepairPreservesFinalJobsAndOriginalBytes(t *testing.T) {
	for _, state := range []string{"done", "rejected", "fenced"} {
		o := Order{Jobs: map[string]Job{"job": {Key: "job", Kind: "credit", State: state, Evidence: Evidence{Provider: "stripe", Merchant: "acct_test", Environment: "test", Transaction: "pi_test", EventID: "evt_test", Payload: []byte("old")}}}}
		next := o.Jobs["job"]
		next.Evidence.AcceptedProofID = "proof_test"
		enqueue(&o, next, setup(t).s.now())
		if o.Jobs["job"].State != state || o.Jobs["job"].Evidence.AcceptedProofID != "" {
			t.Fatal("final job reopened", state)
		}
	}
	h := setup(t)
	o := h.create("create_fixture_0001")
	e := h.event(o, "evt_test", Paid, 1000)
	h.send(e)
	e.Evidence.Payload = []byte("different raw payload")
	if err := h.s.receiveEvent(context.Background(), h.a, e); !errors.Is(err, ErrConflict) {
		t.Fatal("same event changed raw bytes", err)
	}
}
