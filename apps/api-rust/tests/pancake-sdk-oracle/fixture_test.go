package pancakeoracle

import (
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRustOpenSSLFixtures(t *testing.T) {
	root := "../fixtures/pancake"
	key, err := os.ReadFile(filepath.Join(root, "test-public.pem"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"test", "prod"} {
		body, err := os.ReadFile(filepath.Join(root, mode+".json"))
		if err != nil {
			t.Fatal(err)
		}
		header, err := os.ReadFile(filepath.Join(root, mode+".header"))
		if err != nil {
			t.Fatal(err)
		}
		event, err := pancake.VerifyWebhookTyped[pancake.WebhookEventData](string(body), string(header), &pancake.VerifyWebhookOptions{PublicKey: string(key), ToleranceMS: -1})
		if err != nil {
			t.Fatal(err)
		}
		if event.EventID != "payment-event-1" || event.EventType != "subscription.payment_succeeded" || event.Data.CurrentPeriodEnd != nil || event.Data.BillingPeriod != nil {
			t.Fatalf("unexpected event: %+v", event)
		}
		if _, err := pancake.VerifyWebhook(string(body)+" ", string(header), &pancake.VerifyWebhookOptions{PublicKey: string(key), ToleranceMS: -1}); err == nil {
			t.Fatal("tampered body accepted")
		}
		if !strings.Contains(string(header), "t=1700000000000,v1=") {
			t.Fatal("unexpected fixture signature format")
		}
	}
}
