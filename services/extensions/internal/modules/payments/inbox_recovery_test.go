package payments

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInboxRecoveryBeforeJobWithoutOriginalAdapter(t *testing.T) {
	for _, mode := range []string{"missing", "replaced", "legacy_proof", "transaction_lookup"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			o := h.create("inbox_before_job_0001")
			h.work()
			if err := h.m.Mutate(context.Background(), o.ID, func(o *Order) error { o.PaymentID = "pi_" + o.ID; return nil }); err != nil {
				t.Fatal(err)
			}
			e := h.event(o, "evt_inbox_restart", Paid, 1000)
			if mode == "transaction_lookup" {
				e.OrderID = ""
			}
			h.send(e) // Stop after durable inbox, before the credit job exists.
			if mode == "legacy_proof" {
				old := h.m.events[eventKey(e)]
				old.Evidence.AcceptedProofID = ""
				h.m.events[eventKey(e)] = old
			}
			if mode == "replaced" {
				channel := h.a.Channel()
				channel.Merchant = "acct_replacement"
				h.s = a05Service(t, h, &a05ChannelAdapter{Adapter: h.a, channel: channel})
			} else {
				h.s = a05Service(t, h)
			}
			h.work()
			h.work()
			if !h.get(o.ID).Credited() || h.c.count("credit") != 1 || h.c.count("accept_proof") != 1 {
				t.Fatal("inbox recovery needed the old Go adapter or repeated proof acceptance/credit")
			}
			if h.a.checkoutCalls.Load() != 1 || h.a.refundCalls.Load() != 0 {
				t.Fatal("inbox settlement dispatched a provider write")
			}
		})
	}
}

func TestInboxRecoveryRejectsChangedStoredBindings(t *testing.T) {
	for _, field := range []string{"channel", "provider", "merchant", "environment", "currency", "transaction", "amount", "type", "refund_id", "source"} {
		t.Run(field, func(t *testing.T) {
			h := setup(t)
			o := h.create("inbox_binding_000001")
			h.work()
			if err := h.m.Mutate(context.Background(), o.ID, func(o *Order) error { o.PaymentID = "pi_" + o.ID; return nil }); err != nil {
				t.Fatal(err)
			}
			e := h.event(o, "evt_inbox_binding", Paid, 1000)
			h.send(e)
			key := eventKey(e)
			e = h.m.events[key]
			switch field {
			case "channel":
				e.ChannelID++
			case "provider":
				e.Evidence.Provider = "epay"
			case "merchant":
				e.Evidence.Merchant = "acct_other"
			case "environment":
				e.Evidence.Environment = "live"
			case "currency":
				e.Currency = "CNY"
			case "transaction":
				e.Evidence.Transaction = "pi_other"
			case "amount":
				e.AmountMinor++
			case "type":
				e.Type = "invented_success"
			case "refund_id":
				e.Type = RefundSucceeded
				e.RefundID = ""
			case "source":
				e.Evidence.Source = "api" // A proof ID cannot turn an API observation into a webhook.
			}
			h.m.events[key] = e
			h.s = a05Service(t, h)
			h.work()
			if h.get(o.ID).Credited() || h.c.count("credit") != 0 || h.c.count("external") != 0 || h.c.count("accept_proof") != 1 {
				t.Fatal("stored event escaped its immutable payment binding")
			}
			if h.m.events[key].Status == "done" {
				t.Fatal("invalid stored event was acknowledged as applied")
			}
		})
	}
}

func TestInboxRecoveryDoesNotAcceptUnknownProof(t *testing.T) {
	h := setup(t)
	o := h.create("inbox_unknown_000001")
	h.work()
	e := h.event(o, "evt_never_accepted", Paid, 1000)
	e.ChannelID = o.ChannelID
	e.Key, e.Digest, e.Status, e.NextAttempt = eventKey(e), eventDigest(e), "pending", h.s.now()
	if err := h.m.PutEvent(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	h.s = a05Service(t, h)
	h.work()
	if h.c.count("accept_proof") != 0 || h.c.count("credit") != 0 || h.c.count("lookup_proof") != 1 || h.get(o.ID).Credited() {
		t.Fatal("recovery accepted previously unverified evidence")
	}
	if h.m.events[e.Key].Status == "done" {
		t.Fatal("unaccepted event was finalized")
	}
}

func TestInboxRecoveryUnsignedObservationStillNeedsAdapter(t *testing.T) {
	h := setup(t)
	o := h.create("inbox_observation_01")
	h.work()
	e := h.event(o, "evt_api_observation", Paid, 1000)
	e.Evidence.Source, e.Evidence.Signature = "api", nil
	h.send(e)
	h.s = a05Service(t, h)
	h.work()
	if h.get(o.ID).Credited() || h.c.count("credit") != 0 || h.c.count("accept_proof") != 0 {
		t.Fatal("unsigned observation was promoted to an accepted payment")
	}
	if got := h.m.events[eventKey(e)]; got.Status != "pending" || got.ErrorCode != ErrUnavailable.Error() {
		t.Fatal("unsigned observation did not remain pending")
	}
}

func TestInboxRecoveryRefundBeforeCreditKeepsFence(t *testing.T) {
	h := setup(t)
	o := h.create("inbox_refund_first01")
	h.work()
	e := h.event(o, "evt_refund_first", RefundSucceeded, 1000)
	e.RefundID = "re_external_first"
	h.send(e)
	h.send(h.event(o, "evt_later_paid", Paid, 1000))
	h.s = a05Service(t, h)
	h.work()
	h.work()
	got := h.get(o.ID)
	if !got.RefundFence || got.Credited() || h.c.count("credit") != 0 || h.c.count("external") != 1 {
		t.Fatal("inbox recovery removed refund fence or credited a refunded payment")
	}
	if h.a.refundCalls.Load() != 0 {
		t.Fatal("provider refund observation dispatched another refund")
	}
}

func TestInboxRecoveryHeldRefundDoesNotRedispatch(t *testing.T) {
	h := setup(t)
	o := h.paid()
	r, err := h.s.RequestRefund(context.Background(), "user", o.ID, "inbox_refund_held01", 100)
	if err != nil {
		t.Fatal(err)
	}
	h.work()
	e := h.event(o, "evt_refund_terminal", RefundSucceeded, 100)
	e.RefundID, e.LocalRefundID = "re_"+r.ID, r.ID
	h.send(e)
	h.s = a05Service(t, h)
	h.work()
	h.work()
	if h.get(o.ID).Refunds[r.ID].State != "committed" || h.c.count("commit") != 1 || h.c.count("release") != 0 || h.a.refundCalls.Load() != 1 {
		t.Fatal("accepted refund did not settle once from the stored proof")
	}
}

type failInboxFinish struct {
	Store
	fail bool
}

func (s *failInboxFinish) FinishEvent(ctx context.Context, key, status, code string, next time.Time) error {
	if s.fail {
		s.fail = false
		return ErrUnavailable
	}
	return s.Store.FinishEvent(ctx, key, status, code, next)
}

func TestInboxRecoveryAfterJobBeforeInboxFinish(t *testing.T) {
	h := setup(t)
	o := h.create("inbox_lost_finish01")
	h.work()
	h.send(h.event(o, "evt_lost_finish", Paid, 1000))
	h.s = a05Service(t, h)
	h.s.store = &failInboxFinish{Store: h.m, fail: true}
	if err := h.s.Work(context.Background(), 100); !errors.Is(err, ErrUnavailable) {
		t.Fatal("fixture did not interrupt after saving the job")
	}
	key := stableKey("credit", o.ID)
	if j := h.get(o.ID).Jobs[key]; j.State != "pending" || j.Evidence.AcceptedProofID == "" {
		t.Fatal("job was not persisted before inbox acknowledgement")
	}
	h.s = a05Service(t, h)
	h.work()
	h.work()
	if !h.get(o.ID).Credited() || h.c.count("credit") != 1 || h.c.count("accept_proof") != 1 {
		t.Fatal("interrupted inbox finalization duplicated or lost the payment")
	}
}
