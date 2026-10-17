package payments

import (
	"context"
	"testing"
)

// These are development projection tests with existing in-memory fixtures.
// They exercise Step transitions, not provider or money acceptance.
func TestOrderStateStoppedCheckoutFromStepNeedsReview(t *testing.T) {
	h := setup(t)
	o := h.create("state_review_checkout_01")
	h.a.c.Merchant = "acct_changed"
	h.work()
	got := h.get(o.ID)
	if got.Jobs[stableKey("checkout", o.ID)].State != "review" || got.State() != "review" || got.Credited() || h.a.checkoutCalls.Load() != 0 {
		t.Fatal("stopped checkout promised automatic progress", got.State())
	}
}

type stateRejectedCore struct{ Core }

func (c stateRejectedCore) Credit(_ context.Context, id int64, key string, _ Evidence) (Receipt, error) {
	return Receipt{ID: "rejected_receipt", IntentID: id, Key: key, RequestHash: make([]byte, 32), State: Rejected, RejectionCode: "account_inactive"}, nil
}
func TestOrderStateRejectedCreditFromStepNeedsReview(t *testing.T) {
	h := setup(t)
	o := h.create("state_reject_credit_01")
	h.work()
	h.s.core = stateRejectedCore{Core: h.c}
	h.send(h.event(o, "evt_state_rejected", Paid, 1000))
	h.work()
	got := h.get(o.ID)
	if got.CreditState != "rejected" || got.Jobs[stableKey("credit", o.ID)].State != "rejected" || got.State() != "review" || got.Credited() {
		t.Fatal("rejected credit promised automatic progress", got.State())
	}
}
func TestOrderStateUnknownRefundFromStepRemainsHeldForReview(t *testing.T) {
	h := setup(t)
	o := h.paid()
	r, err := h.s.RequestRefund(context.Background(), "user", o.ID, "state_unknown_refund_01", 1000)
	if err != nil {
		t.Fatal(err)
	}
	h.a.window = 0
	h.a.refundErr = ErrUnavailable
	h.work()
	h.advance()
	h.work()
	got := h.get(o.ID)
	if got.Jobs[stableKey("submit", r.ID)].State != "review" || got.State() != "review" || got.Refunds[r.ID].Dispatch != DispatchUnknown || got.Refunds[r.ID].State != "held" || h.c.count("release") != 0 || h.c.count("commit") != 0 || h.a.refundCalls.Load() != 1 {
		t.Fatal("unknown refund was hidden or money state invented", got.State())
	}
}
func TestOrderStateConfirmedFullRefundStillWinsOverOldStoppedJobs(t *testing.T) {
	h := setup(t)
	o := h.paid()
	e := h.event(o, "evt_state_full_refund", RefundSucceeded, 1000)
	e.RefundID = "re_state_full"
	h.send(e)
	h.work()
	got := h.get(o.ID)
	if got.State() != "refunded" {
		t.Fatal("fixture did not confirm refund", got.State())
	}
	got.Jobs["old_stopped_job"] = Job{State: "review"}
	if got.State() != "refunded" {
		t.Fatal("confirmed full refund lost its meaning", got.State())
	}
}

type stateUnavailableSettlementCore struct{ Core }

func (c stateUnavailableSettlementCore) Commit(context.Context, int64, string, string, Evidence) (Receipt, error) {
	return Receipt{}, ErrUnavailable
}
func (c stateUnavailableSettlementCore) Release(context.Context, int64, string, string, Evidence) (Receipt, error) {
	return Receipt{}, ErrUnavailable
}
func TestOrderStateRefundSettlementAfterStoppedSubmitUsesCoreResult(t *testing.T) {
	for _, outcome := range []string{RefundSucceeded, RefundFailed} {
		t.Run(outcome, func(t *testing.T) {
			h := setup(t)
			o := h.paid()
			r, err := h.s.RequestRefund(context.Background(), "user", o.ID, "state_settle_after_review_01", 400)
			if err != nil {
				t.Fatal(err)
			}
			h.a.window, h.a.refundErr = 0, ErrUnavailable
			h.work()
			h.advance()
			h.work()
			if got := h.get(o.ID); got.State() != "review" || got.Refunds[r.ID].Dispatch != DispatchUnknown {
				t.Fatal("ambiguous submit did not stop for review", got.State())
			}
			e := h.event(o, "evt_state_settlement", outcome, 400)
			e.RefundID, e.LocalRefundID = "re_state_settlement", r.ID
			h.send(e)
			h.s.core = stateUnavailableSettlementCore{Core: h.c}
			h.work()
			if got := h.get(o.ID); got.State() != "review" || got.Refunds[r.ID].State != "held" {
				t.Fatal("provider status alone settled held funds", got.State(), got.Refunds[r.ID].State)
			}
			h.s.core = h.c
			h.advance()
			h.work()
			got := h.get(o.ID)
			wantState, wantRefund := "partially_refunded", "committed"
			if outcome == RefundFailed {
				wantState, wantRefund = "credited", "released"
			}
			if got.State() != wantState || got.Refunds[r.ID].State != wantRefund || got.Jobs[stableKey("submit", r.ID)].State != "review" || got.Refunds[r.ID].Dispatch != DispatchUnknown || h.a.refundCalls.Load() != 1 {
				t.Fatal("completed settlement hidden or unknown dispatch erased", got.State(), got.Refunds[r.ID])
			}
		})
	}
}
func TestOrderStatePaidCheckoutIgnoresHistoricalStoppedCheckout(t *testing.T) {
	h := setup(t)
	o := h.create("state_checkout_paid_01")
	channel := h.a.c
	h.a.c.Merchant = "acct_changed"
	h.work()
	if got := h.get(o.ID); got.State() != "review" {
		t.Fatal("checkout was not stopped", got.State())
	}
	h.a.c = channel
	h.send(h.event(o, "evt_state_checkout_paid", Paid, 1000))
	h.work()
	got := h.get(o.ID)
	if got.State() != "credited" || !got.Credited() || got.Jobs[stableKey("checkout", o.ID)].State != "review" || h.a.checkoutCalls.Load() != 0 {
		t.Fatal("completed credit hidden by old checkout review", got.State())
	}
}
