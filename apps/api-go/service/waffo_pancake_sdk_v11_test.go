package service

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestWaffoPancakeV11CheckoutUsesDedicatedPriceSnapshot(t *testing.T) {
	params, err := buildWaffoPancakeSDKCheckoutParams(&WaffoPancakeCreateSessionParams{
		ProductID: "PROD_example", ProductType: model.WaffoPancakeProductTypeOneTime,
		Currency: "CNY", PriceSnapshot: &WaffoPancakePriceSnapshot{Amount: "9.90", TaxCategory: "saas"},
	})
	require.NoError(t, err)
	encoded, err := json.Marshal(params.CreateCheckoutSessionParams)
	require.NoError(t, err)
	var request map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &request))
	require.JSONEq(t, `"CNY"`, string(request["currency"]))
	require.JSONEq(t, `{"amount":"9.90","taxCategory":"saas"}`, string(request["priceSnapshot"]))
	require.NotContains(t, request, "withTrial", "the migration must not silently enable a trial")
}

func TestWaffoPancakeV11LifecycleAndPurePaymentContracts(t *testing.T) {
	for _, eventType := range []string{
		"subscription.renewed", "subscription.recovered", "subscription.plan_changed",
		"subscription.plan_change_scheduled", "subscription.plan_change_failed",
	} {
		require.Equal(t, WaffoPancakeWebhookActionSubscriptionStateChanged, WaffoPancakeWebhookActionForEvent(eventType))
	}
	// v0.11 deliberately omits order status and period from this receipt.
	event := &WaffoPancakeWebhookEvent{EventType: "subscription.payment_succeeded", Data: WaffoPancakeWebhookData{
		PaymentID: "PAY_fixture", PaymentStatus: "succeeded", Amount: "9.90", Currency: "USD",
	}}
	require.NoError(t, ValidateWaffoPancakeWebhookEvent(event))
	for _, eventType := range []string{"subscription.renewed", "subscription.recovered"} {
		require.ErrorContains(t, ValidateWaffoPancakeWebhookEvent(&WaffoPancakeWebhookEvent{
			EventType: eventType, Data: WaffoPancakeWebhookData{OrderStatus: "canceled"},
		}), "orderStatus mismatch")
	}
}

func TestWaffoPancakeV11SignatureRetriesAndEnvironmentIsolation(t *testing.T) {
	keys := make(map[string]*rsa.PrivateKey)
	for _, environment := range []string{"PROD", "TEST"} {
		privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
		require.NoError(t, err)
		t.Setenv("WAFFO_WEBHOOK_"+environment+"_PUBLIC_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})))
		keys[environment] = privateKey
	}
	t.Setenv("WAFFO_WEBHOOK_PUBLIC_KEY", "")
	payload := `{"id":"delivery-fixture","eventId":"payment-fixture","eventType":"subscription.payment_succeeded","mode":"prod","storeId":"store-fixture","data":{"orderId":"ORD_fixture","paymentId":"PAY_fixture","paymentStatus":"succeeded","amount":"9.90","currency":"USD"}}`
	for _, tc := range []struct {
		name        string
		delta       time.Duration
		key         string
		environment string
		mutate      bool
		wantError   bool
	}{
		{name: "current production signature", key: "PROD", environment: "prod"},
		{name: "delivery retry after thirty one minutes", delta: -31 * time.Minute, key: "PROD", environment: "prod"},
		{name: "old delivery outside retry window", delta: -46 * time.Minute, key: "PROD", environment: "prod", wantError: true},
		{name: "small receiver clock skew", delta: 30 * time.Second, key: "PROD", environment: "prod"},
		{name: "large future timestamp rejected", delta: 2 * time.Minute, key: "PROD", environment: "prod", wantError: true},
		{name: "test signature cannot claim prod mode", key: "TEST", environment: "prod", wantError: true},
		{name: "verified key cannot contradict mode", key: "PROD", environment: "test", wantError: true},
		{name: "raw body tamper rejected", key: "PROD", environment: "prod", mutate: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			timestamp := strconv.FormatInt(time.Now().Add(tc.delta).UnixMilli(), 10)
			digest := sha256.Sum256([]byte(timestamp + "." + payload))
			signature, err := rsa.SignPKCS1v15(rand.Reader, keys[tc.key], crypto.SHA256, digest[:])
			require.NoError(t, err)
			header := fmt.Sprintf("t=%s,v1=%s", timestamp, base64.StdEncoding.EncodeToString(signature))
			body := payload
			if tc.mutate {
				body += " "
			}
			event, err := VerifyConfiguredWaffoPancakeWebhook(body, header, tc.environment)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "PAY_fixture", event.Data.PaymentID)
			require.Empty(t, event.Data.CurrentPeriodStart)
			require.Empty(t, event.Data.CurrentPeriodEnd)
		})
	}
}
