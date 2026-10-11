package payments

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	lab "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/testkit/mocklab"
)

func openMocklab(t *testing.T) *lab.Client {
	t.Helper()
	path := os.Getenv("LMM_MOCKLAB_MANIFEST")
	if path == "" {
		t.Skip("NOT RUN: standalone mocklab manifest is not configured")
	}
	c, err := lab.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// This is an adapter contract test. It neither calls a fake Core implementation
// nor claims to credit a wallet. Full payment/ledger acceptance remains separate.
func TestMocklabStripeAdapter(t *testing.T) {
	c := openMocklab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := NewStripe(StripeConfig{
		Channel: Channel{ID: 1, Provider: "stripe", Merchant: lab.StripeMerchant, Environment: "test", Currency: "USD", Enabled: true},
		APIKey:  lab.StripeKey, APIVersion: lab.StripeVersion,
		SuccessURL: "https://return.mock.invalid/paid", CancelURL: "https://return.mock.invalid/canceled",
	})
	if err != nil {
		t.Fatal(err)
	}
	s.client, err = c.HTTP("stripe", "api.stripe.com")
	if err != nil {
		t.Fatal(err)
	}
	order := Order{ID: "mock_adapter_" + c.Manifest.RunID, Intent: Intent{ID: 1, Merchant: lab.StripeMerchant, Currency: "USD", AmountMinor: 1000}}
	key := "mock_checkout_" + c.Manifest.RunID
	checkout, err := s.Checkout(ctx, order, key)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Checkout(ctx, order, key)
	if err != nil || checkout != again || checkout.PaymentID == "" {
		t.Fatal("provider checkout identity mismatch", err)
	}
	order.Checkout, order.PaymentID = checkout, checkout.PaymentID
	failed, err := c.Transition(ctx, "stripe", order.PaymentID, "failed")
	if err != nil {
		t.Fatal(err)
	}
	paid, err := c.Transition(ctx, "stripe", order.PaymentID, "succeeded")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(c.Manifest.Epoch, 0)
	// The old failure is delivered after success. Verify must preserve the
	// immutable provider events; the application service owns order precedence.
	for i, id := range []string{paid, failed, paid} {
		request, err := c.Delivery(ctx, "stripe", id)
		if err != nil {
			t.Fatal(err)
		}
		event, err := s.Verify(request, now)
		want := Paid
		if i == 1 {
			want = PaymentFailed
		}
		if err != nil || event.Type != want || event.OrderID != order.ID || event.AmountMinor != 1000 ||
			event.Evidence.Source != "webhook" || len(event.Evidence.Signature) == 0 {
			t.Fatal("signed provider event contract failed", err)
		}
	}
	request, err := c.Delivery(ctx, "stripe", paid)
	if err != nil {
		t.Fatal(err)
	}
	request.Body = io.NopCloser(strings.NewReader(`{"not":"the signed body"}`))
	if _, err = s.Verify(request, now); !errors.Is(err, ErrInvalid) {
		t.Fatal("changed body was not rejected", err)
	}
	observations, err := s.Lookup(ctx, order)
	if err != nil || len(observations) != 1 || observations[0].Evidence.Source != "api" || len(observations[0].Evidence.Signature) != 0 {
		t.Fatal("API observation must not be fabricated webhook proof", err)
	}
	// This value is an adapter-input fixture, NOT a real or fake ledger receipt.
	// Do not use this direct adapter test to mark refund settlement PASS.
	refund := Refund{ID: "mock_refund_" + c.Manifest.RunID, AmountMinor: 400, HoldReceipt: "adapter-input-only-not-a-ledger-receipt"}
	refundKey := "mock_refund_key_" + c.Manifest.RunID
	result, err := s.Refund(ctx, order, refund, refundKey)
	if err != nil || result.ID == "" {
		t.Fatal("partial provider refund", err)
	}
	repeat, err := s.Refund(ctx, order, refund, refundKey)
	if err != nil || repeat != result {
		t.Fatal("provider refund replay changed identity", err)
	}
	eventID, err := c.Transition(ctx, "stripe", result.ID, "succeeded")
	if err != nil {
		t.Fatal(err)
	}
	request, err = c.Delivery(ctx, "stripe", eventID)
	if err != nil {
		t.Fatal(err)
	}
	event, err := s.Verify(request, now)
	if err != nil || event.Type != RefundSucceeded || event.AmountMinor != 400 || event.LocalRefundID != refund.ID {
		t.Fatal("partial refund evidence", err)
	}
	observations, err = s.Lookup(ctx, order)
	if err != nil || len(observations) != 2 {
		t.Fatal("payment plus partial refund lookup", err)
	}
}

func TestMocklabEPayAdapter(t *testing.T) {
	c := openMocklab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e, err := NewEPay(EPayConfig{
		Channel:  Channel{ID: 2, Provider: "epay", Merchant: lab.EPayMerchant, Environment: "test", Currency: "CNY", Enabled: true},
		Endpoint: "https://epay.mock.invalid", MerchantKey: lab.EPayKey, PaymentType: "alipay",
		NotifyURL: "https://callback.mock.invalid/callbacks/2", ReturnURL: "https://return.mock.invalid/paid",
	})
	if err != nil {
		t.Fatal(err)
	}
	e.client, err = c.HTTP("epay", "epay.mock.invalid")
	if err != nil {
		t.Fatal(err)
	}
	order := Order{ID: "epay_adapter_" + c.Manifest.RunID, Intent: Intent{Currency: "CNY", AmountMinor: 1001}}
	checkout, err := e.Checkout(ctx, order, "mock_epay_checkout_key")
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, "GET", checkout.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := e.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("mock provider rejected real checkout form", response.StatusCode)
	}
	id, err := c.Transition(ctx, "epay", order.ID, "succeeded")
	if err != nil {
		t.Fatal(err)
	}
	request, err = c.Delivery(ctx, "epay", id)
	if err != nil {
		t.Fatal(err)
	}
	event, err := e.Verify(request, time.Unix(c.Manifest.Epoch, 0))
	if err != nil || event.OrderID != order.ID || event.Currency != "CNY" || event.AmountMinor != 1001 {
		t.Fatal("ePay signed decimal contract", err)
	}
	observations, err := e.Lookup(ctx, order)
	if err != nil || len(observations) != 1 || observations[0].AmountMinor != 1001 || observations[0].Evidence.Source != "api" {
		t.Fatal("ePay authenticated lookup", err)
	}
	if e.RefundEnabled() || e.PartialRefunds() {
		t.Fatal("legacy refunds unexpectedly enabled")
	}
	if _, err := e.Refund(ctx, order, Refund{}, "mock_refund_key_0001"); !errors.Is(err, ErrUnsupported) {
		t.Fatal("legacy refund must remain unsupported", err)
	}
}
