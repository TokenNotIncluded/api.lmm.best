package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/form"
)

func TestWaffoPancakeProductBindingMustMatchPlanPriceTypeAndCadence(t *testing.T) {
	existing := &model.SubscriptionPlan{
		PriceAmount:             6.8,
		Currency:                "CNY",
		DurationUnit:            model.SubscriptionDurationMonth,
		DurationValue:           1,
		WaffoPancakeProductId:   "PROD_monthly",
		WaffoPancakeProductType: model.WaffoPancakeProductTypeSubscription,
	}
	next := *existing
	require.False(t, waffoPancakeProductMustBeRecreated(existing, &next))

	next.PriceAmount = 7.8
	require.True(t, waffoPancakeProductMustBeRecreated(existing, &next))

	next = *existing
	next.WaffoPancakeProductType = model.WaffoPancakeProductTypeOneTime
	require.True(t, waffoPancakeProductMustBeRecreated(existing, &next))

	next = *existing
	next.WaffoPancakeProductId = "PROD_monthly_new_price"
	require.False(t, waffoPancakeProductMustBeRecreated(existing, &next))

	oneTime := *existing
	oneTime.WaffoPancakeProductType = model.WaffoPancakeProductTypeOneTime
	next = oneTime
	next.DurationValue = 2
	require.False(t, waffoPancakeProductMustBeRecreated(&oneTime, &next))
}

func TestWaffoPancakePlanProductTypeAndSettlementEventContract(t *testing.T) {
	productType, err := parseWaffoPancakePlanProductType("")
	require.NoError(t, err)
	require.Equal(t, model.WaffoPancakeProductTypeSubscription, productType)
	_, err = parseWaffoPancakePlanProductType("invalid")
	require.Error(t, err)

	oneTimeOrder := &model.SubscriptionOrder{
		PlanSnapshot:         `{"waffo_pancake_product_type":"one_time"}`,
		ExpectedAmountMicros: 1000000,
	}
	require.True(t, waffoPancakeSubscriptionOrderAcceptsSettlementAction(
		oneTimeOrder,
		service.WaffoPancakeWebhookActionOrderCompleted,
	))
	require.False(t, waffoPancakeSubscriptionOrderAcceptsSettlementAction(
		oneTimeOrder,
		service.WaffoPancakeWebhookActionSubscriptionPaymentSucceeded,
	))

	subscriptionOrder := &model.SubscriptionOrder{
		PlanSnapshot:         `{"waffo_pancake_product_type":"subscription"}`,
		ExpectedAmountMicros: 1000000,
	}
	require.False(t, waffoPancakeSubscriptionOrderAcceptsSettlementAction(
		subscriptionOrder,
		service.WaffoPancakeWebhookActionOrderCompleted,
	))
	require.True(t, waffoPancakeSubscriptionOrderAcceptsSettlementAction(
		subscriptionOrder,
		service.WaffoPancakeWebhookActionSubscriptionPaymentSucceeded,
	))

	legacyOneTimeOrder := &model.SubscriptionOrder{}
	require.True(t, waffoPancakeSubscriptionOrderAcceptsSettlementAction(
		legacyOneTimeOrder,
		service.WaffoPancakeWebhookActionOrderCompleted,
	))
}

func TestNormalizeSubscriptionFiatCurrency(t *testing.T) {
	currency, err := normalizeSubscriptionFiatCurrency(" cny ")
	require.NoError(t, err)
	require.Equal(t, "CNY", currency)
	currency, err = normalizeSubscriptionFiatCurrency("USD")
	require.NoError(t, err)
	require.Equal(t, "USD", currency)
	currency, err = normalizeSubscriptionFiatCurrency("")
	require.NoError(t, err)
	require.Equal(t, "CNY", currency)
	_, err = normalizeSubscriptionFiatCurrency("EUR")
	require.Error(t, err)
}

type subscriptionStripeCheckoutBackend struct {
	orderVisibleDuringCall bool
}

func (b *subscriptionStripeCheckoutBackend) Call(_ string, _ string, _ string, _ stripe.ParamsContainer, v stripe.LastResponseSetter) error {
	if providerPrice, ok := v.(*stripe.Price); ok {
		providerPrice.ID = "price_order_first"
		providerPrice.Active = true
		providerPrice.Currency = stripe.CurrencyUSD
		providerPrice.UnitAmount = 1000
		providerPrice.Recurring = &stripe.PriceRecurring{}
		return nil
	}
	checkout, ok := v.(*stripe.CheckoutSession)
	if !ok {
		return nil
	}
	var count int64
	if err := model.DB.Model(&model.SubscriptionOrder{}).
		Where("status = ?", common.TopUpStatusPending).Count(&count).Error; err == nil {
		b.orderVisibleDuringCall = count > 0
	}
	checkout.ID = "cs_order_first_test"
	checkout.URL = "https://checkout.example.test/cs_order_first_test"
	return nil
}

func (*subscriptionStripeCheckoutBackend) CallStreaming(_ string, _ string, _ string, _ stripe.ParamsContainer, _ stripe.StreamingLastResponseSetter) error {
	return nil
}

func (*subscriptionStripeCheckoutBackend) CallRaw(_ string, _ string, _ string, _ *form.Values, _ *stripe.Params, _ stripe.LastResponseSetter) error {
	return nil
}

func (*subscriptionStripeCheckoutBackend) CallMultipart(_ string, _ string, _ string, _ string, _ *bytes.Buffer, _ *stripe.Params, _ stripe.LastResponseSetter) error {
	return nil
}

func (*subscriptionStripeCheckoutBackend) SetMaxNetworkRetries(_ int64) {}

func TestSubscriptionPaymentMethodsUsePlanStripeProductWithoutWalletProduct(t *testing.T) {
	paymentSetting := operation_setting.GetPaymentSetting()
	oldConfirmed := paymentSetting.ComplianceConfirmed
	oldTerms := paymentSetting.ComplianceTermsVersion
	oldAPISecret := setting.StripeApiSecret
	oldWebhookSecret := setting.StripeWebhookSecret
	oldWalletPriceID := setting.StripePriceId
	t.Cleanup(func() {
		paymentSetting.ComplianceConfirmed = oldConfirmed
		paymentSetting.ComplianceTermsVersion = oldTerms
		setting.StripeApiSecret = oldAPISecret
		setting.StripeWebhookSecret = oldWebhookSecret
		setting.StripePriceId = oldWalletPriceID
	})

	paymentSetting.ComplianceConfirmed = true
	paymentSetting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	setting.StripeApiSecret = "sk_test_subscription" // gitleaks:allow
	setting.StripeWebhookSecret = "whsec_subscription"
	// The wallet's global Stripe price is intentionally absent. Subscription
	// plans own their Stripe product and must not depend on this setting.
	setting.StripePriceId = ""

	allowBalance := false
	methods := subscriptionPaymentMethods(
		&model.User{Username: "subscription-user", Status: common.UserStatusEnabled},
		&model.SubscriptionPlan{
			PriceAmount:     10,
			AllowBalancePay: &allowBalance,
			StripePriceId:   "price_plan_specific",
		},
		time.Now(),
	)

	require.Equal(t, []string{model.PaymentMethodStripe}, methods)
}

func TestSubscriptionConfiguredPaymentMethodsRequireUsableGatewayConfiguration(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	preservePaymentGatewaySettings(t)

	originalAPISecret := setting.StripeApiSecret
	originalWebhookSecret := setting.StripeWebhookSecret
	t.Cleanup(func() {
		setting.StripeApiSecret = originalAPISecret
		setting.StripeWebhookSecret = originalWebhookSecret
	})

	operation_setting.PayAddress = "https://epay.example.test"
	operation_setting.EpayId = "merchant"
	operation_setting.EpayKey = "secret"
	operation_setting.PayMethods = []map[string]string{
		{"name": "Alipay", "type": "alipay"},
		{"name": "Duplicate", "type": "alipay"},
		{"name": "Dedicated", "type": model.PaymentMethodStripe},
	}
	setting.StripeApiSecret = "not-a-secret"
	setting.StripeWebhookSecret = "whsec_subscription"

	allowBalance := false
	plan := &model.SubscriptionPlan{
		Enabled:         true,
		AllowBalancePay: &allowBalance,
		StripePriceId:   "price_plan_specific",
	}
	require.Equal(t, []string{"alipay"}, subscriptionConfiguredPaymentMethods(plan))

	setting.StripeApiSecret = "sk_test_subscription" // gitleaks:allow
	require.Equal(t,
		[]string{model.PaymentMethodStripe, "alipay"},
		subscriptionConfiguredPaymentMethods(plan),
	)
}

func TestSubscriptionConfiguredPaymentMethodsGrowsPastConfigurationCapacity(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	preservePaymentGatewaySettings(t)

	operation_setting.PayAddress = "https://epay.example.test"
	operation_setting.EpayId = "merchant"
	operation_setting.EpayKey = "secret"
	const configuredCount = 4096
	operation_setting.PayMethods = make([]map[string]string, configuredCount)
	expected := make([]string, 0, configuredCount+1)
	expected = append(expected, model.PaymentMethodBalance)
	for index := range configuredCount {
		method := fmt.Sprintf("custom-%04d", index)
		operation_setting.PayMethods[index] = map[string]string{"name": method, "type": method}
		expected = append(expected, method)
	}

	require.Equal(t, expected, subscriptionConfiguredPaymentMethods(&model.SubscriptionPlan{Enabled: true}))
}

func TestEnabledSubscriptionPlanRequiresConfiguredPaymentMethod(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	preservePaymentGatewaySettings(t)
	operation_setting.PayAddress = ""
	operation_setting.EpayId = ""
	operation_setting.EpayKey = ""
	operation_setting.PayMethods = nil

	allowBalance := false
	plan := &model.SubscriptionPlan{Enabled: true, AllowBalancePay: &allowBalance}
	require.False(t, enabledSubscriptionPlanHasConfiguredPaymentMethod(plan))

	plan.Enabled = false
	require.True(t, enabledSubscriptionPlanHasConfiguredPaymentMethod(plan))

	plan.Enabled = true
	allowBalance = true
	require.True(t, enabledSubscriptionPlanHasConfiguredPaymentMethod(plan))
}

func TestStripeSubscriptionPersistsOrderBeforeCreatingCheckout(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.SubscriptionPlan{}, &model.SubscriptionOrder{}))
	confirmPaymentComplianceForTest(t)

	originalAPISecret := setting.StripeApiSecret
	originalWebhookSecret := setting.StripeWebhookSecret
	originalBackend := stripe.GetBackend(stripe.APIBackend)
	t.Cleanup(func() {
		setting.StripeApiSecret = originalAPISecret
		setting.StripeWebhookSecret = originalWebhookSecret
		stripe.SetBackend(stripe.APIBackend, originalBackend)
	})
	setting.StripeApiSecret = "sk_test_order_first"
	setting.StripeWebhookSecret = "whsec_order_first"
	backend := &subscriptionStripeCheckoutBackend{}
	stripe.SetBackend(stripe.APIBackend, backend)

	user := model.User{
		Username: "stripe-order-first-user",
		Password: "password",
		Email:    "stripe-order-first@example.test",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)
	plan := model.SubscriptionPlan{
		Title:         "Order-first Stripe plan",
		PriceAmount:   10,
		Currency:      "USD",
		Enabled:       true,
		StripePriceId: "price_order_first",
	}
	require.NoError(t, db.Create(&plan).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", user.Id)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/subscription/stripe/pay", bytes.NewBufferString(fmt.Sprintf(`{"plan_id":%d}`, plan.Id)))
	c.Request.Header.Set("Content-Type", "application/json")
	SubscriptionRequestStripePay(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "pay_link")
	require.True(t, backend.orderVisibleDuringCall, "the provider must not receive a checkout request before the local order is durable")
	var order model.SubscriptionOrder
	require.NoError(t, db.Where("trade_no LIKE ?", "sub_ref_%").First(&order).Error)
	require.Equal(t, common.TopUpStatusPending, order.Status)
}
