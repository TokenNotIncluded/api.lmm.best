package payments

import (
	"context"
	"errors"
	"testing"
	"time"
)

// These tests use the existing in-memory fixtures. Constructing a Service again
// is not a PostgreSQL durability test or an operating-system process restart.
func a05Service(t *testing.T, h *harness, adapters ...Adapter) *Service {
	t.Helper()
	s, err := New(h.m, h.c, adapters...)
	if err != nil {
		t.Fatal(err)
	}
	s.now = h.s.now
	return s
}

type a05ChannelAdapter struct {
	Adapter
	channel     Channel
	lookupCalls int
}

func (a *a05ChannelAdapter) Channel() Channel { return a.channel }
func (a *a05ChannelAdapter) Lookup(ctx context.Context, o Order) ([]Event, error) {
	a.lookupCalls++
	return a.Adapter.Lookup(ctx, o)
}

func TestA05AcceptedInboxSurvivesMissingChannel(t *testing.T) {
	for _, explicitOrder := range []bool{false, true} {
		name := "transaction_lookup"
		if explicitOrder {
			name = "explicit_order"
		}
		t.Run(name, func(t *testing.T) {
			h := setup(t)
			o := h.create("a05_missing_channel_01")
			h.work()
			if err := h.m.Mutate(context.Background(), o.ID, func(o *Order) error {
				o.PaymentID = "pi_" + o.ID
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			e := h.event(o, "evt_a05_accepted", Paid, 1000)
			if !explicitOrder {
				e.OrderID = ""
			}
			h.send(e)
			key := eventKey(e)
			before := h.m.events[key]
			if before.Evidence.AcceptedProofID == "" {
				t.Fatal("fixture did not obtain accepted proof")
			}

			h.s = a05Service(t, h)
			if err := h.s.Drain(context.Background(), 100); err != nil {
				t.Fatal(err)
			}
			after := h.m.events[key]
			if after.Status != "pending" || after.ErrorCode != ErrUnavailable.Error() ||
				!after.NextAttempt.After(h.s.now()) || after.Key != before.Key ||
				after.Evidence.AcceptedProofID != before.Evidence.AcceptedProofID ||
				eventHash(after.Evidence.Payload) != eventHash(before.Evidence.Payload) {
				t.Fatal("missing channel lost, finalized or changed the accepted event")
			}
			if h.c.count("credit") != 0 || h.c.count("accept_proof") != 1 {
				t.Fatal("missing channel changed Core payment state")
			}

			h.advance()
			h.s = a05Service(t, h, h.a)
			h.work()
			h.work()
			if !h.get(o.ID).Credited() || h.c.count("credit") != 1 ||
				h.c.count("accept_proof") != 1 || h.m.events[key].Status != "done" {
				t.Fatal("restoring the original channel did not recover the same proof once")
			}
		})
	}
}

func TestA05MissingChannelDoesNotConsumeFirstAttempt(t *testing.T) {
	h := setup(t)
	o := h.create("a05_first_attempt_001")
	key := stableKey("checkout", o.ID)
	h.a.window = 0 // A synthetic send-once provider, not live ePay acceptance.
	h.s = a05Service(t, h)
	h.work()
	j := h.get(o.ID).Jobs[key]
	if j.State != "pending" || j.Attempts != 0 || !j.FirstAttempt.IsZero() ||
		j.Lease != "" || !j.LeaseUntil.IsZero() || !j.NextAttempt.After(h.s.now()) ||
		j.ErrorCode != ErrUnavailable.Error() || h.a.checkoutCalls.Load() != 0 {
		t.Fatal("unavailable adapter consumed a provider attempt or retained a claim")
	}
	h.advance()
	h.s = a05Service(t, h, h.a)
	h.work()
	j = h.get(o.ID).Jobs[key]
	if j.State != "done" || j.Attempts != 1 || j.Key != key || h.a.checkoutCalls.Load() != 1 {
		t.Fatal("a never-dispatched send-once operation did not recover")
	}
}

func TestA05MissingChannelDoesNotStopHealthyOrder(t *testing.T) {
	h := setup(t)
	first := h.create("a05_missing_first_001")
	channel := h.a.Channel()
	channel.ID = 2
	channel.Merchant = "acct_a05_other"
	other := copyOrder(first)
	other.ID = "top_2"
	other.ChannelID = channel.ID
	other.Namespace = channel.namespace()
	other.Intent.ID = 2
	other.Intent.Key = "a05_healthy_second_01"
	other.Intent.Merchant = channel.Merchant
	other.Jobs = map[string]Job{}
	enqueue(&other, Job{Key: stableKey("checkout", other.ID), Kind: "checkout"}, h.s.now())
	// Synthetic stored state only. This does not seed a real ledger or prove auth.
	if err := h.m.Insert(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	a := &a05ChannelAdapter{Adapter: h.a, channel: channel}
	h.s = a05Service(t, h, a)
	h.work()
	if h.get(first.ID).Jobs[stableKey("checkout", first.ID)].Attempts != 0 ||
		h.get(other.ID).Checkout.ID == "" || h.a.checkoutCalls.Load() != 1 {
		t.Fatal("one missing channel stopped a healthy order in the same batch")
	}
}

func TestA05ChannelIdentityCannotRedirectStoredOrder(t *testing.T) {
	for _, field := range []string{"provider", "merchant", "environment", "currency"} {
		for _, operation := range []string{"checkout", "request_refund", "refund_submit", "reconcile"} {
			t.Run(field+"/"+operation, func(t *testing.T) {
				h := setup(t)
				var o Order
				if operation == "request_refund" || operation == "refund_submit" {
					o = h.paid()
				} else {
					o = h.create("a05_binding_check_001")
				}
				var refund Refund
				if operation == "refund_submit" {
					var err error
					refund, err = h.s.RequestRefund(context.Background(), "user", o.ID, "a05_bound_refund_001", 1000)
					if err != nil {
						t.Fatal(err)
					}
				}
				channel := h.a.Channel()
				switch field {
				case "provider":
					channel.Provider = "epay"
				case "merchant":
					channel.Merchant = "acct_a05_replaced"
				case "environment":
					channel.Environment = "live"
				case "currency":
					channel.Currency = "CNY"
				}
				a := &a05ChannelAdapter{Adapter: h.a, channel: channel}
				h.s = a05Service(t, h, a)
				checkoutCalls := h.a.checkoutCalls.Load()
				holdCalls := h.c.count("hold")
				switch operation {
				case "request_refund":
					_, err := h.s.RequestRefund(context.Background(), "user", o.ID, "a05_bound_refund_002", 1000)
					if !errors.Is(err, ErrConflict) || len(h.get(o.ID).Refunds) != 0 {
						t.Fatal("changed channel created a refund", err)
					}
				case "reconcile":
					if err := h.s.Reconcile(context.Background(), o.ID); !errors.Is(err, ErrConflict) {
						t.Fatal("changed channel reached provider lookup", err)
					}
				default:
					h.work()
					key := stableKey("checkout", o.ID)
					if operation == "refund_submit" {
						key = stableKey("submit", refund.ID)
						if h.get(o.ID).Refunds[refund.ID].State != "held" {
							t.Fatal("configuration conflict released or completed held funds")
						}
					}
					j := h.get(o.ID).Jobs[key]
					if j.State != "review" || j.Attempts != 0 || j.ErrorCode != ErrConflict.Error() {
						t.Fatal("changed channel was not stopped before dispatch")
					}
				}
				if h.a.checkoutCalls.Load() != checkoutCalls || h.a.refundCalls.Load() != 0 ||
					a.lookupCalls != 0 || h.c.count("hold") != holdCalls || h.c.count("release") != 0 {
					t.Fatal("configuration conflict caused provider IO or a new hold/release")
				}
			})
		}
	}
}

func TestA05StoredNamespaceMustMatchIntent(t *testing.T) {
	h := setup(t)
	o := h.create("a05_namespace_check_01")
	o.Namespace = "stripe/acct_unrelated/test"
	if _, err := h.s.orderAdapter(o); !errors.Is(err, ErrConflict) {
		t.Fatal("stored namespace mismatch accepted", err)
	}
}

func TestA05UnknownRefundKeepsOriginalDispatchHistory(t *testing.T) {
	h := setup(t)
	o := h.paid()
	r, err := h.s.RequestRefund(context.Background(), "user", o.ID, "a05_unknown_refund_01", 1000)
	if err != nil {
		t.Fatal(err)
	}
	h.a.window = 0
	h.a.refundErr = ErrUnavailable
	h.work()
	key := stableKey("submit", r.ID)
	before := h.get(o.ID).Jobs[key]
	if before.Attempts != 1 || h.a.refundCalls.Load() != 1 {
		t.Fatal("fixture did not produce an ambiguous dispatch")
	}
	h.advance()
	h.s = a05Service(t, h)
	h.work()
	after := h.get(o.ID).Jobs[key]
	if after.Key != before.Key || after.Attempts != before.Attempts ||
		!after.FirstAttempt.Equal(before.FirstAttempt) ||
		h.get(o.ID).Refunds[r.ID].HoldReceipt != r.HoldReceipt {
		t.Fatal("missing channel erased prior dispatch or hold history")
	}
	h.advance()
	h.s = a05Service(t, h, h.a)
	h.work()
	if h.a.refundCalls.Load() != 1 || h.c.count("release") != 0 || h.c.count("commit") != 0 ||
		h.get(o.ID).Jobs[key].State != "review" || h.get(o.ID).Refunds[r.ID].State != "held" {
		t.Fatal("unknown send-once refund was repeated or its hold released")
	}
}

func TestA05DisabledChannelStillCompletesExistingPayment(t *testing.T) {
	h := setup(t)
	o := h.create("a05_disabled_channel_01")
	h.work()
	channel := h.a.Channel()
	channel.Enabled = false
	a := &a05ChannelAdapter{Adapter: h.a, channel: channel}
	h.s = a05Service(t, h, a)
	e := h.event(o, "evt_a05_disabled", Paid, 1000)
	if err := h.s.receiveEvent(context.Background(), a, e); err != nil {
		t.Fatal(err)
	}
	h.work()
	if !h.get(o.ID).Credited() || h.c.count("credit") != 1 {
		t.Fatal("disabled channel discarded an already accepted payment")
	}
	_, err := h.s.Create(context.Background(), "user", PrepareRequest{
		Key: "a05_disabled_new_order", AccountID: 99, ChannelID: 1, Currency: "USD", AmountMinor: 1000,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("disabled channel accepted a new payment", err)
	}
}

func TestA05MissingChannelPreservesActiveLease(t *testing.T) {
	h := setup(t)
	o := h.create("a05_active_lease_001")
	key := stableKey("checkout", o.ID)
	until := h.s.now().Add(time.Minute)
	if err := h.m.Mutate(context.Background(), o.ID, func(o *Order) error {
		j := o.Jobs[key]
		j.State, j.Lease, j.LeaseUntil = "running", "existing_claim", until
		j.Attempts, j.FirstAttempt = 1, h.s.now()
		o.Jobs[key] = j
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.s = a05Service(t, h)
	if err := h.s.Step(context.Background(), o.ID); err != nil {
		t.Fatal(err)
	}
	j := h.get(o.ID).Jobs[key]
	if j.State != "running" || j.Lease != "existing_claim" || !j.LeaseUntil.Equal(until) || j.Attempts != 1 {
		t.Fatal("missing channel stole an active claim")
	}
}
