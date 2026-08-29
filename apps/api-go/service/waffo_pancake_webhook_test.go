package service

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
)

func TestWaffoPancakeWebhookActionForEvent(t *testing.T) {
	tests := map[string]WaffoPancakeWebhookAction{
		"order.completed":                WaffoPancakeWebhookActionOrderCompleted,
		"subscription.activated":         WaffoPancakeWebhookActionSubscriptionStateChanged,
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
