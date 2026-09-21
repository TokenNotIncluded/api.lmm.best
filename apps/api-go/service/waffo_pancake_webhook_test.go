package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

func TestWaffoPancakeWebhookActionForEvent(t *testing.T) {
	tests := map[string]WaffoPancakeWebhookAction{
		"order.completed":                WaffoPancakeWebhookActionOrderCompleted,
		"subscription.activated":         WaffoPancakeWebhookActionSubscriptionStateChanged,
		"subscription.renewed":           WaffoPancakeWebhookActionSubscriptionStateChanged,
		"subscription.canceling":         WaffoPancakeWebhookActionSubscriptionStateChanged,
		"subscription.uncanceled":        WaffoPancakeWebhookActionSubscriptionStateChanged,
		"subscription.past_due":          WaffoPancakeWebhookActionSubscriptionStateChanged,
		"subscription.payment_succeeded": WaffoPancakeWebhookActionSubscriptionPaymentSucceeded,
		"subscription.canceled":          WaffoPancakeWebhookActionSubscriptionStateChanged,
		"refund.succeeded":               WaffoPancakeWebhookActionRefundSucceeded,
		"refund.failed":                  WaffoPancakeWebhookActionRefundFailed,
		"":                               WaffoPancakeWebhookActionIgnore,
	}
	for eventType, expected := range tests {
		t.Run(eventType, func(t *testing.T) {
			require.Equal(t, expected, WaffoPancakeWebhookActionForEvent(eventType))
		})
	}
}

func TestWaffoPancakeWebhookAdapterPaymentWithoutPeriodFields(t *testing.T) {
	// Since 2026-09-06 payment events omit orderStatus and all cycle fields.
	const payload = `{
		"id": "ORD_example", "eventId": "PAY_example-succeeded",
		"eventType": "subscription.payment_succeeded", "mode": "prod",
		"storeId": "STO_example", "timestamp": "2026-09-08T04:20:43.300Z",
		"data": {
			"orderId": "ORD_example", "orderMerchantExternalId": "local-order",
			"paymentId": "PAY_example", "paymentStatus": "succeeded",
			"paymentDate": "2026-09-08", "amount": "3.99", "currency": "USD",
			"merchantProvidedBuyerIdentity": "new-api-user-12",
			"orderMetadata": {"lmm_product_id": "PRO_example"}
		}
	}`
	var sdkEvent pancake.TypedWebhookEvent[pancake.WebhookEventData]
	require.NoError(t, json.Unmarshal([]byte(payload), &sdkEvent))
	event := waffoPancakeWebhookEventFromSDK(&sdkEvent)

	require.Equal(t, WaffoPancakeWebhookActionSubscriptionPaymentSucceeded, WaffoPancakeWebhookActionForEvent(event.EventType))
	require.NoError(t, ValidateWaffoPancakeWebhookEvent(event))
	require.Equal(t, "PAY_example", event.Data.PaymentID)
	require.Equal(t, "2026-09-08", event.Data.PaymentDate)
	require.Equal(t, "ORD_example", event.Data.OrderID)
	require.Equal(t, "local-order", event.Data.OrderMerchantExternalID)
	require.Equal(t, "new-api-user-12", event.Data.MerchantProvidedBuyerIdentity)
	require.Equal(t, "PRO_example", event.Data.OrderMetadata[WaffoPancakeOrderMetadataProductID])
	require.Empty(t, event.Data.OrderStatus)
	require.Empty(t, event.Data.BillingPeriod)
	require.Empty(t, event.Data.CurrentPeriodStart)
	require.Empty(t, event.Data.CurrentPeriodEnd)
}

func TestWaffoPancakeWebhookAdapterLifecyclePeriods(t *testing.T) {
	for _, eventType := range []string{"subscription.activated", "subscription.renewed"} {
		t.Run(eventType, func(t *testing.T) {
			payload := fmt.Sprintf(`{
				"id": "ORD_example", "eventId": "ORD_example-renewed-2026-10-08",
				"eventType": %q, "mode": "prod", "storeId": "STO_example",
				"storeName": "Your Store", "timestamp": "2026-09-08T04:20:43.300Z",
				"data": {
					"orderId": "ORD_example", "orderStatus": "active",
					"billingPeriod": "monthly", "currentPeriodStart": "2026-09-08",
					"currentPeriodEnd": "2026-10-08", "amount": "3.99",
					"subtotal": "3.99", "taxAmount": "0.00", "total": "3.99",
					"currency": "USD", "taxName": "None", "taxRate": 0,
					"buyerEmail": "buyer@example.com",
					"merchantProvidedBuyerIdentity": "new-api-user-12",
					"orderMerchantExternalId": "local-order",
					"orderMetadata": {}, "productMetadata": {},
					"productName": "Monthly-VIP", "productDescription": "...",
					"billingDetail": {"country": "US", "isBusiness": false}
				}
			}`, eventType)
			var sdkEvent pancake.TypedWebhookEvent[pancake.WebhookEventData]
			require.NoError(t, json.Unmarshal([]byte(payload), &sdkEvent))
			event := waffoPancakeWebhookEventFromSDK(&sdkEvent)

			require.Equal(t, WaffoPancakeWebhookActionSubscriptionStateChanged, WaffoPancakeWebhookActionForEvent(event.EventType))
			require.NoError(t, ValidateWaffoPancakeWebhookEvent(event))
			require.Equal(t, "ORD_example-renewed-2026-10-08", event.EventID)
			require.Equal(t, "ORD_example", event.Data.OrderID)
			require.Equal(t, "local-order", event.Data.OrderMerchantExternalID)
			require.Equal(t, "active", event.Data.OrderStatus)
			require.Equal(t, "monthly", event.Data.BillingPeriod)
			require.Equal(t, "2026-09-08", event.Data.CurrentPeriodStart)
			require.Equal(t, "2026-10-08", event.Data.CurrentPeriodEnd)
			require.Equal(t, "3.99", event.Data.Total)
			require.Empty(t, event.Data.PaymentID)
			require.Empty(t, event.Data.PaymentDate)
		})
	}
}

func TestValidateWaffoPancakeSubscriptionSettlement(t *testing.T) {
	originalStoreID := setting.WaffoPancakeStoreID
	t.Cleanup(func() { setting.WaffoPancakeStoreID = originalStoreID })
	setting.WaffoPancakeStoreID = "victim-store"

	valid := &WaffoPancakeWebhookEvent{
		StoreID: "victim-store",
		Data:    WaffoPancakeWebhookData{Amount: "99.00", Currency: "USD"},
	}
	require.NoError(t, validateWaffoPancakeSubscriptionSettlement(valid, 99))

	tests := []struct {
		name      string
		storeID   string
		amount    string
		currency  string
		wantError string
	}{
		{name: "cross merchant store", storeID: "attacker-store", amount: "99.00", currency: "USD", wantError: "store mismatch"},
		{name: "underpayment", storeID: "victim-store", amount: "0.01", currency: "USD", wantError: "amount mismatch"},
		{name: "wrong currency", storeID: "victim-store", amount: "99.00", currency: "EUR", wantError: "currency mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := &WaffoPancakeWebhookEvent{StoreID: tt.storeID, Data: WaffoPancakeWebhookData{Amount: tt.amount, Currency: tt.currency}}
			require.ErrorContains(t, validateWaffoPancakeSubscriptionSettlement(event, 99), tt.wantError)
		})
	}
}

func TestValidateWaffoPancakeWebhookEventRejectsContradictoryStatuses(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		data      WaffoPancakeWebhookData
		wantError string
	}{
		{
			name:      "completed order with failed payment",
			eventType: "order.completed",
			data: WaffoPancakeWebhookData{
				OrderStatus:   "completed",
				PaymentStatus: "failed",
			},
			wantError: "paymentStatus mismatch",
		},
		{
			name:      "completed event with pending order",
			eventType: "order.completed",
			data: WaffoPancakeWebhookData{
				OrderStatus:   "pending",
				PaymentStatus: "succeeded",
			},
			wantError: "orderStatus mismatch",
		},
		{
			name:      "successful refund marked failed",
			eventType: "refund.succeeded",
			data:      WaffoPancakeWebhookData{RefundStatus: "failed"},
			wantError: "refundStatus mismatch",
		},
		{
			name:      "failed refund marked successful",
			eventType: "refund.failed",
			data:      WaffoPancakeWebhookData{RefundStatus: "succeeded"},
			wantError: "refundStatus mismatch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWaffoPancakeWebhookEvent(&WaffoPancakeWebhookEvent{
				EventType: tt.eventType,
				Data:      tt.data,
			})
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestValidateWaffoPancakeWebhookEventAllowsOmittedOptionalStatuses(t *testing.T) {
	for _, eventType := range []string{
		"order.completed",
		"subscription.activated",
		"subscription.renewed",
		"subscription.payment_succeeded",
		"refund.succeeded",
		"refund.failed",
	} {
		t.Run(eventType, func(t *testing.T) {
			require.NoError(t, ValidateWaffoPancakeWebhookEvent(&WaffoPancakeWebhookEvent{
				EventType: eventType,
			}))
		})
	}
}

func TestWaffoPancakeCredentialsPreferSettingsAndFallBackToOfficialEnv(t *testing.T) {
	originalMerchantID := setting.WaffoPancakeMerchantID
	originalPrivateKey := setting.WaffoPancakePrivateKey
	t.Cleanup(func() {
		setting.WaffoPancakeMerchantID = originalMerchantID
		setting.WaffoPancakePrivateKey = originalPrivateKey
	})
	t.Setenv("WAFFO_MERCHANT_ID", "env-merchant")
	t.Setenv("WAFFO_PRIVATE_KEY", "env-private-key")

	setting.WaffoPancakeMerchantID = ""
	setting.WaffoPancakePrivateKey = ""
	merchantID, privateKey := WaffoPancakeCredentials()
	require.Equal(t, "env-merchant", merchantID)
	require.Equal(t, "env-private-key", privateKey)

	setting.WaffoPancakeMerchantID = "stored-merchant"
	setting.WaffoPancakePrivateKey = "stored-private-key"
	merchantID, privateKey = WaffoPancakeCredentials()
	require.Equal(t, "stored-merchant", merchantID)
	require.Equal(t, "stored-private-key", privateKey)
}
