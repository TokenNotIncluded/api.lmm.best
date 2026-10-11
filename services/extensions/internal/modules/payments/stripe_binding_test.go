package payments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const checkoutObject = `{"id":"cs_fixture","object":"checkout.session","mode":"payment","client_reference_id":"top_1","metadata":{"lmm_order_id":"top_1"},"amount_total":1000,"currency":"usd","livemode":false,"payment_intent":"pi_top_1","url":"https://checkout.stripe.com/c/pay/test"}`
const refundObject = `{"id":"re_fixture","object":"refund","payment_intent":"pi_top_1","amount":300,"currency":"usd","status":"succeeded","metadata":{"lmm_order_id":"top_1","lmm_refund_id":"local_refund"}}`

func bindingOrder() Order {
	return Order{ID: "top_1", PaymentID: "pi_top_1", Intent: Intent{ID: 1, Merchant: "acct_fixture", Currency: "USD", AmountMinor: 1000}}
}

// Both creation and recovery must use the same frozen order and channel fields.
func TestStripeCheckoutBindingCreationAndRecovery(t *testing.T) {
	for _, operation := range []string{"create", "recover"} {
		t.Run(operation, func(t *testing.T) {
			for _, tc := range []struct {
				name, from, to string
				wantConflict   bool
			}{
				{"valid", "", "", false},
				{"no_payment_yet", `"payment_intent":"pi_top_1"`, `"payment_intent":null`, false},
				{"expanded_payment", `"payment_intent":"pi_top_1"`, `"payment_intent":{"id":"pi_top_1"}`, false},
				{"empty_id", `"id":"cs_fixture"`, `"id":""`, true},
				{"wrong_object", `"object":"checkout.session"`, `"object":"payment_intent"`, true},
				{"subscription", `"mode":"payment"`, `"mode":"subscription"`, true},
				{"setup", `"mode":"payment"`, `"mode":"setup"`, true},
				{"missing_mode", `"mode":"payment",`, "", true},
				{"wrong_reference", `"client_reference_id":"top_1"`, `"client_reference_id":"top_other"`, true},
				{"wrong_metadata", `"lmm_order_id":"top_1"`, `"lmm_order_id":"top_other"`, true},
				{"wrong_amount", `"amount_total":1000`, `"amount_total":900`, true},
				{"wrong_currency", `"currency":"usd"`, `"currency":"eur"`, true},
				{"wrong_environment", `"livemode":false`, `"livemode":true`, true},
				{"missing_environment", `"livemode":false,`, "", true},
				{"wrong_payment_kind", `"payment_intent":"pi_top_1"`, `"payment_intent":"seti_fixture"`, true},
				{"empty_payment_reference", `"payment_intent":"pi_top_1"`, `"payment_intent":""`, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					s := stripeFixture(t)
					raw := checkoutObject
					if tc.from != "" {
						raw = strings.Replace(raw, tc.from, tc.to, 1)
					}
					if !json.Valid([]byte(raw)) {
						t.Fatal("invalid fixture")
					}
					paymentReads := 0
					s.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
						switch r.URL.Path {
						case "/v1/account":
							return response(`{"id":"acct_fixture"}`), nil
						case "/v1/checkout/sessions", "/v1/checkout/sessions/cs_fixture":
							return response(raw), nil
						case "/v1/payment_intents/pi_top_1":
							paymentReads++
							return response(paymentObject), nil
						case "/v1/refunds":
							return response(`{"has_more":false,"data":[]}`), nil
						default:
							t.Fatalf("unexpected request: %s", r.URL.Path)
							return nil, ErrInvalid
						}
					})}
					o := bindingOrder()
					o.PaymentID = ""
					var err error
					var checkout Checkout
					var events []Event
					if operation == "create" {
						checkout, err = s.Checkout(context.Background(), o, "stable_request_01")
					} else {
						o.Checkout.ID = "cs_fixture"
						events, err = s.Lookup(context.Background(), o)
					}
					if tc.wantConflict {
						if !errors.Is(err, ErrConflict) || paymentReads != 0 || checkout != (Checkout{}) || len(events) != 0 {
							t.Fatalf("conflicting checkout reached payment lookup: reads=%d err=%v", paymentReads, err)
						}
					} else if err != nil {
						t.Fatal(err)
					} else if operation == "create" {
						wantPayment := "pi_top_1"
						if tc.name == "no_payment_yet" {
							wantPayment = ""
						}
						if checkout.ID != "cs_fixture" || checkout.URL == "" || checkout.PaymentID != wantPayment {
							t.Fatal("valid checkout was not preserved", checkout)
						}
					} else if tc.name == "no_payment_yet" {
						if len(events) != 0 || paymentReads != 0 {
							t.Fatal("unpaid checkout produced payment evidence")
						}
					} else if len(events) != 1 || paymentReads != 1 || events[0].Type != Paid || events[0].OrderID != o.ID || events[0].AmountMinor != o.Intent.AmountMinor || events[0].Evidence.Source != "api" || len(events[0].Evidence.Signature) != 0 {
						t.Fatal("valid payment lookup lost its binding or invented a signature", events)
					}
				})
			}
		})
	}
}

func TestStripeRecoveryKeepsSavedCheckoutIdentity(t *testing.T) {
	s := stripeFixture(t)
	o := bindingOrder()
	o.PaymentID = ""
	o.Checkout.ID = "cs_fixture"
	paymentReads := 0
	s.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/account":
			return response(`{"id":"acct_fixture"}`), nil
		case "/v1/checkout/sessions/cs_fixture":
			return response(strings.Replace(checkoutObject, `"id":"cs_fixture"`, `"id":"cs_other"`, 1)), nil
		case "/v1/payment_intents/pi_top_1":
			paymentReads++
			return response(paymentObject), nil
		case "/v1/refunds":
			return response(`{"has_more":false,"data":[]}`), nil
		default:
			t.Fatal(r.URL.Path)
			return nil, ErrInvalid
		}
	})}
	if events, err := s.Lookup(context.Background(), o); !errors.Is(err, ErrConflict) || len(events) != 0 || paymentReads != 0 {
		t.Fatalf("saved checkout was replaced: events=%d reads=%d err=%v", len(events), paymentReads, err)
	}
}

func TestStripeRefundSubmissionChecksOriginalPayment(t *testing.T) {
	for _, tc := range []struct {
		name, from, to string
		readError      error
		wantError      bool
	}{
		{"valid", "", "", nil, false},
		{"wrong_id", `"id":"pi_top_1"`, `"id":"pi_other"`, nil, true},
		{"wrong_order", `"lmm_order_id":"top_1"`, `"lmm_order_id":"top_other"`, nil, true},
		{"wrong_amount", `"amount":1000`, `"amount":900`, nil, true},
		{"wrong_currency", `"currency":"usd"`, `"currency":"eur"`, nil, true},
		{"wrong_environment", `"livemode":false`, `"livemode":true`, nil, true},
		{"not_paid", `"status":"succeeded"`, `"status":"processing"`, nil, true},
		{"not_fully_received", `"amount_received":1000`, `"amount_received":900`, nil, true},
		{"read_unavailable", "", "", ErrUnavailable, true},
		{"read_canceled", "", "", context.Canceled, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := stripeFixture(t)
			reads, writes := 0, 0
			s.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/v1/account":
					return response(`{"id":"acct_fixture"}`), nil
				case "/v1/payment_intents/pi_top_1":
					reads++
					if tc.readError != nil {
						return nil, tc.readError
					}
					raw := paymentObject
					if tc.from != "" {
						raw = strings.Replace(raw, tc.from, tc.to, 1)
					}
					return response(raw), nil
				case "/v1/refunds":
					writes++
					if reads != 1 || r.Method != "POST" || r.Header.Get("Idempotency-Key") != "stable_request_01" {
						t.Error("refund write lacks original-payment verification or its stable key")
					}
					return response(refundObject), nil
				default:
					t.Fatal(r.URL.Path)
					return nil, ErrInvalid
				}
			})}
			r := Refund{ID: "local_refund", HoldReceipt: "held_by_core", AmountMinor: 300}
			result, err := s.Refund(context.Background(), bindingOrder(), r, "stable_request_01")
			if tc.wantError {
				if err == nil || writes != 0 || result != (RefundResult{}) {
					t.Fatalf("unverified refund was submitted: reads=%d writes=%d result=%+v err=%v", reads, writes, result, err)
				}
			} else if err != nil || result.ID != "re_fixture" || reads != 1 || writes != 1 {
				t.Fatal(reads, writes, result, err)
			}
		})
	}
}

func TestStripeRefundWebhookMetadataDoesNotReplaceOriginalPayment(t *testing.T) {
	for _, tc := range []struct {
		name, object, payment string
		wantError             bool
	}{
		{"matching", refundObject, paymentObject, false},
		{"metadata_absent", strings.Replace(refundObject, `"lmm_order_id":"top_1",`, "", 1), paymentObject, false},
		{"conflicting_order", strings.Replace(refundObject, `"lmm_order_id":"top_1"`, `"lmm_order_id":"top_other"`, 1), paymentObject, true},
		{"unbound_payment", refundObject, strings.Replace(paymentObject, `"lmm_order_id":"top_1"`, `"unrelated":"value"`, 1), true},
		{"wrong_currency", refundObject, strings.Replace(paymentObject, `"currency":"usd"`, `"currency":"eur"`, 1), true},
		{"excess_refund", refundObject, strings.Replace(paymentObject, `"amount":1000`, `"amount":200`, 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := stripeFixture(t)
			now := time.Unix(1791633600, 0)
			raw := envelope("refund.updated", "evt_refund", tc.object)
			reads := 0
			s.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/v1/account":
					return response(`{"id":"acct_fixture"}`), nil
				case "/v1/events/evt_refund":
					return response(raw), nil
				case "/v1/payment_intents/pi_top_1":
					reads++
					return response(tc.payment), nil
				default:
					t.Fatal(r.URL.Path)
					return nil, ErrInvalid
				}
			})}
			e, err := s.Verify(stripeRequest(raw, s.cfg.WebhookSecrets[0], now), now)
			if tc.wantError {
				if !errors.Is(err, ErrConflict) || e.OrderID != "" || e.Evidence.EventID != "" {
					t.Fatalf("conflicting refund binding accepted: order=%q err=%v", e.OrderID, err)
				}
			} else if err != nil || e.OrderID != "top_1" || reads != 1 || string(e.Evidence.Payload) != raw || len(e.Evidence.Signature) == 0 {
				t.Fatal(reads, e.OrderID, err)
			}
		})
	}
}

func TestStripeRefundResponseRequiresProviderID(t *testing.T) {
	for _, id := range []string{"", "pi_not_a_refund", "re_invalid/segment"} {
		t.Run(id, func(t *testing.T) {
			s := stripeFixture(t)
			s.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/v1/account":
					return response(`{"id":"acct_fixture"}`), nil
				case "/v1/payment_intents/pi_top_1":
					return response(paymentObject), nil
				case "/v1/refunds":
					return response(strings.Replace(refundObject, `"id":"re_fixture"`, `"id":"`+id+`"`, 1)), nil
				default:
					t.Fatal(r.URL.Path)
					return nil, ErrInvalid
				}
			})}
			r := Refund{ID: "local_refund", HoldReceipt: "held_by_core", AmountMinor: 300}
			if result, err := s.Refund(context.Background(), bindingOrder(), r, "stable_request_01"); !errors.Is(err, ErrConflict) || result != (RefundResult{}) {
				t.Fatal(result, err)
			}
		})
	}
}

// This is Go service/adapter integration with an in-memory store and a core
// fixture. It does not establish real Rust/PostgreSQL ledger correctness.
func TestStripeWorkerKeepsHoldUntilVerifiedRefundAfterRetry(t *testing.T) {
	h := setup(t)
	o := h.paid()
	adapter := stripeFixture(t)
	service, err := New(h.m, h.c, adapter)
	if err != nil {
		t.Fatal(err)
	}
	service.now = h.s.now
	h.s = service
	r, err := h.s.RequestRefund(context.Background(), "user", o.ID, "refund_binding_001", 300)
	if err != nil || r.State != "held" {
		t.Fatal(r, err)
	}
	jobKey := ""
	for key, job := range h.get(o.ID).Jobs {
		if job.Kind == "refund_submit" && job.RefundID == r.ID {
			jobKey = key
		}
	}
	if jobKey == "" {
		t.Fatal("refund job was not persisted")
	}
	object := strings.Replace(refundObject, "local_refund", r.ID, 1)
	raw := envelope("refund.updated", "evt_refund_binding", object)
	unavailable, writes := true, 0
	adapter.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/account":
			return response(`{"id":"acct_fixture"}`), nil
		case "/v1/payment_intents/pi_top_1":
			if unavailable {
				return nil, ErrUnavailable
			}
			return response(paymentObject), nil
		case "/v1/refunds":
			writes++
			if req.Method != "POST" || req.Header.Get("Idempotency-Key") != jobKey {
				t.Error("refund retry changed its durable job key")
			}
			return response(object), nil
		case "/v1/events/evt_refund_binding":
			return response(raw), nil
		default:
			t.Fatal(req.URL.Path)
			return nil, ErrInvalid
		}
	})}
	h.work()
	pending := h.get(o.ID)
	if writes != 0 || pending.Refunds[r.ID].State != "held" || pending.Jobs[jobKey].State != "pending" ||
		pending.Refunds[r.ID].HoldReceipt != r.HoldReceipt || h.c.count("release") != 0 || h.c.count("commit") != 0 {
		t.Fatal("unavailable provider read submitted a refund, released a hold or created a receipt")
	}
	unavailable = false
	h.advance()
	h.work()
	h.work()
	pending = h.get(o.ID)
	if writes != 1 || pending.Refunds[r.ID].State != "awaiting_evidence" || h.c.count("hold") != 1 || h.c.count("commit") != 0 {
		t.Fatal("unsigned provider response was settled or the retry repeated a hold")
	}
	for attempt := 0; attempt < 2; attempt++ {
		request := stripeRequest(raw, adapter.cfg.WebhookSecrets[0], h.s.now())
		request.URL.Path = "/callbacks/1"
		recorder := httptest.NewRecorder()
		h.s.WebhookHandler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatal(recorder.Code, recorder.Body.String())
		}
		if attempt == 0 && h.c.count("commit") != 0 {
			t.Fatal("callback acknowledgement bypassed the durable worker")
		}
		h.work()
	}
	final := h.get(o.ID)
	if writes != 1 || h.c.count("commit") != 1 || h.c.count("release") != 0 || final.State() != "partially_refunded" || final.Refunds[r.ID].State != "committed" {
		t.Fatal("duplicate callback changed refund settlement", writes, h.c.count("commit"), final.State())
	}
}

func TestStripeRefundRejectsAmountBeyondOriginalBeforeIO(t *testing.T) {
	s := stripeFixture(t)
	calls := 0
	s.client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, ErrUnavailable
	})}
	r := Refund{ID: "local_refund", HoldReceipt: "held_by_core", AmountMinor: 1001}
	if _, err := s.Refund(context.Background(), bindingOrder(), r, "stable_request_01"); !errors.Is(err, ErrInvalid) || calls != 0 {
		t.Fatal("oversized refund reached provider", calls, err)
	}
}
