package controller

// This exporter exercises the current controller and pinned Stripe SDK against
// an HTTP fixture. Its output is a shared input/output contract for Rust/PG.
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
)

type stripeSubscriptionCheckoutFixture struct {
	Name             string `json:"name"`
	Body             string `json:"body,omitempty"`
	PlanCurrency     string `json:"plan_currency,omitempty"`
	PriceMode        string `json:"price_mode,omitempty"`
	Customer         string `json:"customer,omitempty"`
	Disabled         bool   `json:"disabled,omitempty"`
	Purchased        bool   `json:"purchased,omitempty"`
	FailOrder        bool   `json:"fail_order,omitempty"`
	FailCheckout     bool   `json:"fail_checkout,omitempty"`
	NoWebhook        bool   `json:"no_webhook,omitempty"`
	Restricted       bool   `json:"restricted,omitempty"`
	ExplicitAudience bool   `json:"explicit_audience,omitempty"`
}

func TestRustStripeSubscriptionCheckoutCurrentGoOracle(t *testing.T) {
	output := os.Getenv("LMM_STRIPE_SUBSCRIPTION_CHECKOUT_GO_ORACLE_OUTPUT")
	if output == "" {
		t.Skip("set LMM_STRIPE_SUBSCRIPTION_CHECKOUT_GO_ORACLE_OUTPUT to export checkout evidence")
	}
	input := os.Getenv("LMM_STRIPE_SUBSCRIPTION_CHECKOUT_FIXTURES")
	if input == "" {
		input = "../../api-rust/tests/fixtures/stripe-subscription-checkout-current-go-input.json"
	}
	raw, err := os.ReadFile(input)
	require.NoError(t, err)
	var fixtures []stripeSubscriptionCheckoutFixture
	require.NoError(t, json.Unmarshal(raw, &fixtures))
	require.NotEmpty(t, fixtures)
	results := make([]map[string]any, 0, len(fixtures))
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			preserveChannelPricing(t)
			confirmPaymentComplianceForTest(t)
			setupTopupInfoUser(t, 7, "default")
			previousSecret, previousWebhook, previousWalletPrice := setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId
			previousServer, previousBackend := system_setting.ServerAddress, stripe.GetBackend(stripe.APIBackend)
			t.Cleanup(func() {
				setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = previousSecret, previousWebhook, previousWalletPrice
				system_setting.ServerAddress = previousServer
				stripe.SetBackend(stripe.APIBackend, previousBackend)
				model.InvalidateSubscriptionPlanCache(3)
			})
			require.NoError(t, model.DB.AutoMigrate(&model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.UserSubscription{}))
			setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = "sk_subscription_checkout_fixture", "whsec_fixture", ""
			if fixture.NoWebhook {
				setting.StripeWebhookSecret = ""
			}
			system_setting.ServerAddress = "https://console.example/"
			operation_setting.USDExchangeRate = 6.8
			operation_setting.TopUpPlatformUnitsPerCNY = 17 // This ratio must not affect a fiat plan.
			operation_setting.PayMethods = nil
			if fixture.ExplicitAudience {
				operation_setting.PayMethods = []map[string]string{{"type": "stripe", "audience_mode": "all"}}
			}
			email := "payer@example.test"
			if fixture.Restricted {
				email = "payer@linux.do"
			}
			require.NoError(t, model.DB.Model(&model.User{}).Where("id=7").Updates(map[string]any{"email": email, "stripe_customer": fixture.Customer}).Error)
			currency := fixture.PlanCurrency
			if currency == "" {
				currency = "USD"
			}
			plan := model.SubscriptionPlan{Id: 3, Title: "Stripe recurring checkout", PriceAmount: 1, Currency: currency, Enabled: !fixture.Disabled, StripePriceId: "price_plan", DurationUnit: "day", DurationValue: 1, TotalAmount: 1000}
			if fixture.Purchased {
				plan.MaxPurchasePerUser = 1
				require.NoError(t, model.DB.Create(&model.UserSubscription{UserId: 7, PlanId: 3, Status: "expired"}).Error)
			}
			plan.NormalizeDefaults()
			require.NoError(t, model.DB.Create(&plan).Error)
			// GORM's default:true tag replaces a zero bool during Create.
			// Explicitly persist the requested fixture state, then read it back.
			require.NoError(t, model.DB.Model(&plan).Update("enabled", !fixture.Disabled).Error)
			require.NoError(t, model.DB.First(&plan, 3).Error)
			require.Equal(t, !fixture.Disabled, plan.Enabled)
			model.InvalidateSubscriptionPlanCache(3)
			if fixture.FailOrder {
				require.NoError(t, model.DB.Exec("CREATE TRIGGER fail_checkout_order BEFORE INSERT ON subscription_orders BEGIN SELECT RAISE(ABORT, 'fixture order failed'); END").Error)
			}
			var mu sync.Mutex
			priceCalls, sessionCalls := 0, 0
			persistedBeforeCheckout, stableKey := true, true
			var firstKey string
			fields := map[string]string{}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/v1/prices/price_plan" {
					priceCalls++
					if fixture.PriceMode == "read_failure" {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{"error":{"message":"fixture price unavailable"}}`))
						return
					}
					minor := int64(100)
					if currency == "CNY" {
						minor = 15
					}
					price := map[string]any{"id": "price_plan", "active": true, "currency": "usd", "unit_amount": minor, "recurring": map[string]string{"interval": "month"}, "product": "prod_plan"}
					switch fixture.PriceMode {
					case "inactive":
						price["active"] = false
					case "one_time":
						delete(price, "recurring")
					case "wrong_id":
						price["id"] = "price_other"
					case "wrong_currency":
						price["currency"] = "cny"
					case "wrong_amount":
						price["unit_amount"] = minor + 1
					}
					_ = json.NewEncoder(w).Encode(price)
					return
				}
				if r.URL.Path != "/v1/checkout/sessions" {
					w.WriteHeader(404)
					return
				}
				sessionCalls++
				if err := r.ParseForm(); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				for key := range r.PostForm {
					fields[key] = r.PostForm.Get(key)
				}
				var count int64
				if err := model.DB.Model(&model.SubscriptionOrder{}).Where("trade_no=? AND status='pending' AND expected_amount_micros>0 AND plan_snapshot<>''", fields["client_reference_id"]).Count(&count).Error; err != nil {
					t.Error(err)
				}
				persistedBeforeCheckout = persistedBeforeCheckout && count == 1
				key := r.Header.Get("Idempotency-Key")
				if sessionCalls == 1 {
					firstKey = key
				}
				stableKey = stableKey && key != "" && key == firstKey
				if fixture.FailCheckout {
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{"error":{"message":"fixture checkout unavailable"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":"cs_subscription","url":"https://checkout.stripe.test/cs_subscription"}`))
			}))
			t.Cleanup(provider.Close)
			stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{URL: stripe.String(provider.URL), HTTPClient: provider.Client()}))
			body := fixture.Body
			if body == "" {
				body = `{"plan_id":3}`
			}
			writer := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(writer)
			ctx.Set("id", 7)
			ctx.Request = httptest.NewRequest("POST", "/api/subscription/stripe/pay", strings.NewReader(body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			SubscriptionRequestStripePay(ctx)
			var response map[string]any
			require.NoError(t, json.Unmarshal(writer.Body.Bytes(), &response))
			var orders []model.SubscriptionOrder
			require.NoError(t, model.DB.Find(&orders).Error)
			snapshots := make([]map[string]any, 0, len(orders))
			for _, order := range orders {
				var snapshot map[string]any
				require.NoError(t, json.Unmarshal([]byte(order.PlanSnapshot), &snapshot))
				delete(snapshot, "created_at")
				delete(snapshot, "updated_at")
				snapshots = append(snapshots, map[string]any{"status": order.Status, "money": order.Money, "payment_method": order.PaymentMethod, "payment_provider": order.PaymentProvider, "expected_amount_micros": order.ExpectedAmountMicros, "settlement_currency": order.SettlementCurrency, "provider_product_id": order.ProviderProductId, "plan_snapshot": snapshot})
			}
			mu.Lock()
			for _, key := range []string{"client_reference_id", "metadata[subscription_trade_no]", "subscription_data[metadata][subscription_trade_no]"} {
				if _, ok := fields[key]; ok {
					fields[key] = "<order>"
				}
			}
			result := map[string]any{"name": fixture.Name, "input": fixture, "http_status": writer.Code, "response": response, "orders": snapshots, "price_calls": priceCalls, "session_calls": sessionCalls, "checkout_fields": fields, "persisted_before_checkout": persistedBeforeCheckout, "idempotency_key_stable": stableKey}
			mu.Unlock()
			results = append(results, result)
		})
	}
	encoded, err := json.MarshalIndent(results, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, append(encoded, '\n'), 0600))
}
