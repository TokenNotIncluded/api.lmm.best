// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package payments

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

type callbackRejectProofCore struct{ Core }

func (callbackRejectProofCore) LookupProof(context.Context, int64, Evidence) (ProofReceipt, error) {
	return ProofReceipt{}, ErrNotFound
}
func (callbackRejectProofCore) AcceptProof(context.Context, int64, Evidence) (ProofReceipt, error) {
	return ProofReceipt{}, ErrEvidence
}

func TestStripePublicCallbackRequiresCoreProofBeforeProviderIO(t *testing.T) {
	for _, mode := range []string{"orphan", "rejected-proof", "offline-core"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			if mode != "orphan" {
				h.create("callback_gate_reject_0001")
			}
			stripe := stripeFixture(t)
			calls := 0
			stripe.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("provider must not be called before proof")
			})}
			h.s.adapters[1] = stripe
			if mode == "rejected-proof" {
				h.s.core = callbackRejectProofCore{Core: h.c}
			}
			if mode == "offline-core" {
				h.c.offline = true
			}
			raw := envelope("payment_intent.succeeded", "evt_callback_gate_reject", paymentObject)
			req := stripeRequest(raw, "whsec_fixture_current_secret", time.Now())
			if err := h.s.Receive(context.Background(), 1, req); err == nil {
				t.Fatal("callback without accepted proof succeeded")
			}
			if calls != 0 || len(h.m.events) != 0 || h.c.count("credit") != 0 {
				t.Fatal("unaccepted callback reached provider, inbox or credit", calls, len(h.m.events), h.c.count("credit"))
			}
		})
	}
}

func TestStripeProviderObservationRemainsRequiredAfterProof(t *testing.T) {
	for _, mode := range []string{"accepted", "merchant-mismatch", "event-mismatch", "refund-payment-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			h.create("callback_gate_observe_0001")
			stripe := stripeFixture(t)
			h.s.adapters[1] = stripe
			raw := envelope("payment_intent.succeeded", "evt_callback_gate_observe", paymentObject)
			if mode == "refund-payment-mismatch" {
				raw = envelope("refund.updated", "evt_callback_gate_observe", refundObject)
			}
			calls := 0
			stripe.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if h.c.count("accept_proof") != 1 || h.c.count("credit") != 0 {
					t.Error("provider IO preceded durable proof or credit preceded inbox")
				}
				switch r.URL.Path {
				case "/v1/account":
					if mode == "merchant-mismatch" {
						return response(`{"id":"acct_other"}`), nil
					}
					return response(`{"id":"acct_fixture"}`), nil
				case "/v1/events/evt_callback_gate_observe":
					if mode == "event-mismatch" {
						return response(strings.Replace(raw, `"amount":1000`, `"amount":1001`, 1)), nil
					}
					return response(raw), nil
				case "/v1/payment_intents/pi_top_1":
					return response(strings.Replace(paymentObject, `"lmm_order_id":"top_1"`, `"lmm_order_id":"top_other"`, 1)), nil
				default:
					t.Error("unexpected provider call", r.URL.Path)
					return nil, ErrInvalid
				}
			})}
			err := h.s.Receive(context.Background(), 1, stripeRequest(raw, "whsec_fixture_current_secret", time.Now()))
			if mode != "accepted" {
				if err == nil || calls == 0 || len(h.m.events) != 0 || h.c.count("credit") != 0 {
					t.Fatal("provider mismatch was ignored or enqueued", err, calls, len(h.m.events), h.c.count("credit"))
				}
				return
			}
			if err != nil || calls != 2 || len(h.m.events) != 1 || h.c.count("credit") != 0 || h.c.count("accept_proof") != 1 {
				t.Fatal("proof, observation and inbox sequence failed", err, calls, len(h.m.events))
			}
			if err := h.s.Work(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			if h.c.count("credit") != 1 {
				t.Fatal("accepted and observed event did not reach worker credit")
			}
		})
	}
}

func TestStripeCallbackRecoversMissingOrderMetadataBeforeCheckoutAck(t *testing.T) {
	for _, mode := range []string{"matching", "provider-order-mismatch", "ambiguous-intent"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			o := h.create("callback_gate_recovery_0001")
			if o.PaymentID != "" {
				t.Fatal("fixture unexpectedly has a checkout acknowledgement")
			}
			stripe := stripeFixture(t)
			h.s.adapters[1] = stripe
			object := strings.Replace(paymentObject, `"lmm_order_id":"top_1",`, "", 1)
			if mode == "ambiguous-intent" {
				object = strings.Replace(object, `"lmm_core_intent_id":"1"`, `"lmm_core_intent_id":"01"`, 1)
			}
			raw := envelope("payment_intent.succeeded", "evt_callback_gate_recovery", object)
			calls := 0
			stripe.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if h.c.count("accept_proof") != 1 {
					t.Error("provider IO before Core admitted the intent-bound proof")
				}
				switch r.URL.Path {
				case "/v1/account":
					return response(`{"id":"acct_fixture"}`), nil
				case "/v1/events/evt_callback_gate_recovery":
					return response(raw), nil
				case "/v1/payment_intents/pi_top_1":
					if mode == "provider-order-mismatch" {
						return response(strings.Replace(paymentObject, `"lmm_order_id":"top_1"`, `"lmm_order_id":"top_other"`, 1)), nil
					}
					return response(paymentObject), nil
				default:
					t.Error("unexpected provider request", r.URL.Path)
					return nil, ErrInvalid
				}
			})}
			err := h.s.Receive(context.Background(), 1, stripeRequest(raw, "whsec_fixture_current_secret", time.Now()))
			if mode == "matching" {
				if err != nil || calls != 3 || len(h.m.events) != 1 || h.c.count("credit") != 0 {
					t.Fatal("missing order reference did not recover safely", err, calls, len(h.m.events))
				}
				for _, event := range h.m.events {
					if event.OrderID != o.ID {
						t.Fatal("recovered a different order", event.OrderID)
					}
				}
				return
			}
			if err == nil || len(h.m.events) != 0 || h.c.count("credit") != 0 {
				t.Fatal("conflicting recovery was accepted", err, len(h.m.events))
			}
			if mode == "ambiguous-intent" && (calls != 0 || h.c.count("accept_proof") != 0) {
				t.Fatal("ambiguous intent lookup reached Core/provider", calls)
			}
		})
	}
}
