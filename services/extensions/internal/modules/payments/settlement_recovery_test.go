package payments

import (
	"context"
	"testing"
)

func withoutPaymentAdapters(t *testing.T, h *harness) {
	t.Helper()
	s, err := New(h.m, h.c)
	if err != nil {
		t.Fatal(err)
	}
	s.now = h.s.now
	h.s = s
}

func TestSettlementRecoversCommittedCreditWithoutAdapter(t *testing.T) {
	h := setup(t)
	o := h.create("lost_credit_response_0001")
	h.work()
	h.c.lose["credit"] = true
	h.send(h.event(o, "evt_lost_credit", Paid, 1000))
	h.work()
	if h.get(o.ID).Credited() || h.c.count("credit") != 1 {
		t.Fatal("fixture must lose the response after the first core credit")
	}
	withoutPaymentAdapters(t, h)
	h.advance()
	h.work()
	if !h.get(o.ID).Credited() || h.c.count("credit") != 1 || h.c.count("lookup") != 1 {
		t.Fatal("core receipt recovery must not need a provider adapter or repeat credit")
	}
	if h.a.checkoutCalls.Load() != 1 || h.a.refundCalls.Load() != 0 {
		t.Fatal("receipt recovery performed provider IO")
	}
}

func TestSettlementRecoversLostHoldWithoutAdapter(t *testing.T) {
	h := setup(t)
	o := h.paid()
	h.c.lose["hold"] = true
	r, err := h.s.RequestRefund(context.Background(), "user", o.ID, "lost_hold_response_0001", 100)
	if err != nil || r.State != "hold_pending" {
		t.Fatalf("fixture hold: state=%s err=%v", r.State, err)
	}
	withoutPaymentAdapters(t, h)
	h.work()
	r = h.get(o.ID).Refunds[r.ID]
	if r.State != "held" || r.HoldReceipt == "" || h.c.count("hold") != 1 {
		t.Fatal("lost hold receipt did not recover independently of provider configuration")
	}
	h.work()
	j := h.get(o.ID).Jobs[stableKey("submit", r.ID)]
	if j.Attempts != 0 || j.State != "pending" || h.a.refundCalls.Load() != 0 {
		t.Fatal("missing provider must still prevent the first external refund attempt")
	}
}

func TestSettlementUsesAcceptedProofWithoutAdapter(t *testing.T) {
	h := setup(t)
	o := h.create("accepted_credit_0001")
	h.work()
	h.send(h.event(o, "evt_accepted_credit", Paid, 1000))
	if err := h.s.Drain(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	withoutPaymentAdapters(t, h)
	h.work()
	if !h.get(o.ID).Credited() || h.c.count("credit") != 1 {
		t.Fatal("a queued core-accepted proof was blocked by missing Go adapter configuration")
	}
}

func TestSettlementStillRejectsUnacceptedEvidenceWithoutAdapter(t *testing.T) {
	h := setup(t)
	o := h.create("unaccepted_credit_0001")
	h.work()
	e := h.event(o, "evt_unsigned_credit", Paid, 1000)
	e.Evidence.Source, e.Evidence.Signature = "api", nil
	h.send(e)
	if err := h.s.Drain(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	withoutPaymentAdapters(t, h)
	h.work()
	if h.get(o.ID).Credited() || h.c.count("credit") != 0 {
		t.Fatal("provider observation was treated as an accepted core proof")
	}
	for _, j := range h.get(o.ID).Jobs {
		if j.Kind == "credit" && (j.State != "review" || j.ErrorCode != ErrEvidence.Error()) {
			t.Fatal("unsigned evidence must remain in review")
		}
	}
}
