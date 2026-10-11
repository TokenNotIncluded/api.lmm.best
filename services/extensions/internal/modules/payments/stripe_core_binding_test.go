package payments

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

// These tests inspect requests to a local transport. They make no Stripe
// requests and do not establish Rust signature or ledger correctness.
func TestStripeCheckoutCarriesFrozenCoreIntentOnSessionAndPayment(t *testing.T) {
	s := stripeFixture(t)
	o := bindingOrder()
	// Above the exact-integer range of a float64: preserve the Rust ID as text.
	o.Intent.ID = 9007199254740993
	writes := 0
	s.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/account":
			return response(`{"id":"acct_fixture"}`), nil
		case "/v1/checkout/sessions":
			writes++
			if req.Method != http.MethodPost || req.Header.Get("Idempotency-Key") != "frozen_checkout_key" {
				t.Fatal("checkout lost its mutation method or durable key")
			}
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			for _, prefix := range []string{"metadata", "payment_intent_data[metadata]"} {
				assertStripeMetadata(t, req.PostForm, prefix, map[string]string{
					"lmm_order_id":       o.ID,
					"lmm_core_intent_id": strconv.FormatInt(o.Intent.ID, 10),
					"lmm_merchant":       o.Intent.Merchant,
				})
			}
			return response(checkoutObject), nil
		default:
			t.Fatalf("unexpected Stripe request: %s", req.URL.Path)
			return nil, ErrInvalid
		}
	})}
	if _, err := s.Checkout(context.Background(), o, "frozen_checkout_key"); err != nil || writes != 1 {
		t.Fatal("checkout submission failed", writes, err)
	}
}

func TestStripeRefundCarriesFrozenCoreIntentAndHold(t *testing.T) {
	s := stripeFixture(t)
	o := bindingOrder()
	o.Intent.ID = 9007199254740993
	r := Refund{ID: "local_refund", HoldReceipt: "core_hold_receipt_001", AmountMinor: 300}
	writes := 0
	s.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v1/account":
			return response(`{"id":"acct_fixture"}`), nil
		case "/v1/payment_intents/pi_top_1":
			return response(paymentObject), nil
		case "/v1/refunds":
			writes++
			if req.Method != http.MethodPost || req.Header.Get("Idempotency-Key") != "frozen_refund_key" {
				t.Fatal("refund lost its mutation method or durable key")
			}
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			assertStripeMetadata(t, req.PostForm, "metadata", map[string]string{
				"lmm_order_id":        o.ID,
				"lmm_refund_id":       r.ID,
				"lmm_core_intent_id":  strconv.FormatInt(o.Intent.ID, 10),
				"lmm_merchant":        o.Intent.Merchant,
				"lmm_hold_receipt_id": r.HoldReceipt,
			})
			return response(refundObject), nil
		default:
			t.Fatalf("unexpected Stripe request: %s", req.URL.Path)
			return nil, ErrInvalid
		}
	})}
	if _, err := s.Refund(context.Background(), o, r, "frozen_refund_key"); err != nil || writes != 1 {
		t.Fatal("refund submission failed", writes, err)
	}
}

func TestStripeMutationsRejectUnboundCoreIntentBeforeIO(t *testing.T) {
	for _, operation := range []string{"checkout", "refund"} {
		for _, tc := range []struct {
			name   string
			change func(*Order)
		}{
			{"missing_id", func(o *Order) { o.Intent.ID = 0 }},
			{"negative_id", func(o *Order) { o.Intent.ID = -1 }},
			{"missing_merchant", func(o *Order) { o.Intent.Merchant = "" }},
			{"different_merchant", func(o *Order) { o.Intent.Merchant = "acct_someone_else" }},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				s := stripeFixture(t)
				o := bindingOrder()
				tc.change(&o)
				calls := 0
				s.client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
					calls++
					return nil, ErrUnavailable
				})}
				var err error
				if operation == "checkout" {
					_, err = s.Checkout(context.Background(), o, "frozen_checkout_key")
				} else {
					_, err = s.Refund(context.Background(), o, Refund{ID: "local_refund", HoldReceipt: "core_hold_receipt_001", AmountMinor: 300}, "frozen_refund_key")
				}
				if !errors.Is(err, ErrInvalid) || calls != 0 {
					t.Fatalf("unbound core intent reached Stripe: calls=%d err=%v", calls, err)
				}
			})
		}
	}
}

func assertStripeMetadata(t *testing.T, values url.Values, prefix string, expected map[string]string) {
	t.Helper()
	for name, want := range expected {
		key := prefix + "[" + name + "]"
		if got := values[key]; len(got) != 1 || got[0] != want {
			t.Errorf("%s must contain one frozen value; got %q, want %q", key, got, want)
		}
	}
}
