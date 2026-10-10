package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

func TestWaffoPancakeSDKAmountSubjects(t *testing.T) {
	for _, tc := range []struct {
		name, eventType, data, amount, total, tax string
	}{
		{"legacy payment", "order.completed", `{"amount":"10.00","total":"10.00","taxAmount":"1.00"}`, "10.00", "10.00", "1.00"},
		{"actual charge", "order.completed", `{"amount":"10.00","chargedAmount":"4.50","listPrice":{"total":"10.00","taxAmount":"1.00"}}`, "4.50", "10.00", "1.00"},
		{"zero is not list price", "subscription.payment_succeeded", `{"amount":"10.00","chargedAmount":"0.00","listPrice":{"total":"10.00","taxAmount":"1.00"}}`, "0.00", "10.00", "1.00"},
		{"missing charge", "order.completed", `{"amount":"10.00","listPrice":{"total":"10.00","taxAmount":"1.00"}}`, "", "10.00", "1.00"},
		{"partial refund", "refund.succeeded", `{"amount":"10.00","refundedAmount":"2.00","originalChargedAmount":"4.50","originalPayment":{"total":"10.00","taxAmount":"1.00"}}`, "2.00", "10.00", "1.00"},
		{"missing refund", "refund.succeeded", `{"amount":"10.00","originalChargedAmount":"4.50","originalPayment":{"total":"10.00","taxAmount":"1.00"}}`, "", "10.00", "1.00"},
		{"plan price is not a payment", "subscription.renewed", `{"amount":"1.00","planPrice":{"total":"10.00","taxAmount":"1.00"}}`, "10.00", "10.00", "1.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data pancake.WebhookEventData
			if err := json.Unmarshal([]byte(tc.data), &data); err != nil {
				t.Fatal(err)
			}
			event := waffoPancakeWebhookEventFromSDK(&pancake.TypedWebhookEvent[pancake.WebhookEventData]{EventType: tc.eventType, Data: data})
			if event.Data.Amount != tc.amount || event.Data.Total != tc.total || event.Data.TaxAmount != tc.tax {
				t.Fatalf("amount/total/tax = %q/%q/%q, want %q/%q/%q", event.Data.Amount, event.Data.Total, event.Data.TaxAmount, tc.amount, tc.total, tc.tax)
			}
		})
	}
}

func TestWaffoPancakeSDKRefundChargeCeiling(t *testing.T) {
	for _, tc := range []struct {
		name, amount string
		original     *string
		invalid      bool
	}{
		{"legacy without ceiling", "2.00", nil, false},
		{"partial refund", "2.00", pancake.Ptr("4.50"), false},
		{"full actual charge", "4.50", pancake.Ptr("4.50"), false},
		{"list price is not ceiling", "10.00", pancake.Ptr("4.50"), true},
		{"zero original charge", "2.00", pancake.Ptr("0.00"), true},
		{"malformed original charge", "2.00", pancake.Ptr("invalid"), true},
		{"unknown refund stays retryable", "", pancake.Ptr("4.50"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := &WaffoPancakeWebhookEvent{EventType: "refund.succeeded", Data: WaffoPancakeWebhookData{Amount: tc.amount, OriginalChargedAmount: tc.original}}
			if err := ValidateWaffoPancakeWebhookEvent(event); (err != nil) != tc.invalid {
				t.Fatalf("validation error = %v, invalid = %t", err, tc.invalid)
			}
		})
	}
}

func TestWaffoPancakeSDKAuthorizationPeriodDoesNotGrantPayment(t *testing.T) {
	for _, period := range []*int{nil, pancake.Ptr(0), pancake.Ptr(1), pancake.Ptr(-1)} {
		event := waffoPancakeWebhookEventFromSDK(&pancake.TypedWebhookEvent[pancake.WebhookEventData]{
			EventType: "subscription.payment_succeeded",
			Data:      pancake.WebhookEventData{PaymentStatus: pancake.Ptr("succeeded"), PeriodNumber: period, ChargedAmount: pancake.Ptr("10.00")},
		})
		if event.Data.PeriodNumber != period {
			t.Fatal("adapter lost the optional billing period")
		}
		invalid := period != nil && *period <= 0
		if err := ValidateWaffoPancakeWebhookEvent(event); (err != nil) != invalid {
			t.Fatalf("period %v: validation error = %v", period, err)
		}
	}
}

func TestMerchantStoreRefundTransportUsesBoundOperationKey(t *testing.T) {
	key := waffoPancakeRequestKey("create-refund-ticket", "merchant", "order", "refund")
	calls := 0
	transport := merchantStoreRefundCustomerTransport{
		token: "BOUND", environment: "prod", idempotencyKey: key,
		base: merchantStoreTestTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Header.Get("X-Idempotency-Key") != key || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
				t.Fatal("refund transport did not enforce its bound operation key and credential boundary")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}),
	}
	r, err := http.NewRequest(http.MethodPost, pancake.DefaultBaseURL+"/v1/actions/refund-ticket/create-ticket", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer BOUND")
	r.Header.Set("X-Environment", "prod")
	r.Header.Set("Cookie", "PRIVATE")
	r.Header.Set("Proxy-Authorization", "PRIVATE")
	r.Header.Set("X-Idempotency-Key", "UNTRUSTED")
	for range 2 {
		response, err := transport.RoundTrip(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	if calls != 2 || r.Header.Get("X-Idempotency-Key") != "UNTRUSTED" {
		t.Fatal("transport mutated the caller request or did not reach the fake provider")
	}
}
