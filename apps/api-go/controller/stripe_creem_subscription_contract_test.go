package controller

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
)

func TestStripeCreemSubscriptionSettlementUsesDynamicFiatFX(t *testing.T) {
	originalRate := operation_setting.USDExchangeRate
	originalPlatformRate := operation_setting.TopUpPlatformUnitsPerCNY
	t.Cleanup(func() {
		operation_setting.USDExchangeRate = originalRate
		operation_setting.TopUpPlatformUnitsPerCNY = originalPlatformRate
	})
	operation_setting.USDExchangeRate = 7.2
	operation_setting.TopUpPlatformUnitsPerCNY = 99 // must not affect fiat conversion

	plan := &model.SubscriptionPlan{PriceAmount: 14.4, Currency: "cny"}
	amountMicros, currency, err := subscriptionSettlementSnapshot(plan, "usd")
	require.NoError(t, err)
	require.Equal(t, int64(2_000_000), amountMicros)
	require.Equal(t, "USD", currency)

	operation_setting.USDExchangeRate = 6.8
	plan.PriceAmount = 1
	amountMicros, currency, err = subscriptionSettlementSnapshot(plan, "USD")
	require.NoError(t, err)
	require.Equal(t, int64(150_000), amountMicros)
	require.Equal(t, "USD", currency)
}

func TestStripeSubscriptionCheckoutEvidenceIsStrict(t *testing.T) {
	order := &model.SubscriptionOrder{
		PaymentProvider:      model.PaymentProviderStripe,
		ExpectedAmountMicros: 2_000_000,
		SettlementCurrency:   "USD",
		ProviderProductId:    "price_monthly",
	}
	valid := &stripe.CheckoutSession{
		ClientReferenceID: "sub_ref_1",
		AmountTotal:       200,
		Currency:          stripe.CurrencyUSD,
		Mode:              stripe.CheckoutSessionModeSubscription,
		PaymentStatus:     stripe.CheckoutSessionPaymentStatusPaid,
		Metadata:          map[string]string{stripeSubscriptionPriceMetadataKey: "price_monthly"},
		Subscription:      &stripe.Subscription{ID: "sub_stripe_1"},
	}
	require.NoError(t, validateStripeSubscriptionCheckoutEvidence(order, valid))

	wrongPrice := *valid
	wrongPrice.Metadata = map[string]string{stripeSubscriptionPriceMetadataKey: "price_other"}
	require.Error(t, validateStripeSubscriptionCheckoutEvidence(order, &wrongPrice))

	wrongAmount := *valid
	wrongAmount.AmountTotal = 199
	require.Error(t, validateStripeSubscriptionCheckoutEvidence(order, &wrongAmount))

	wrongCurrency := *valid
	wrongCurrency.Currency = stripe.CurrencyCNY
	require.Error(t, validateStripeSubscriptionCheckoutEvidence(order, &wrongCurrency))
}

func TestStripeSubscriptionInvoiceEvidenceIsStrict(t *testing.T) {
	order := &model.SubscriptionOrder{
		PaymentProvider:      model.PaymentProviderStripe,
		ProviderProductId:    "price_recurring",
		ExpectedAmountMicros: 2_000_000,
		SettlementCurrency:   "USD",
	}
	invoice := &stripe.Invoice{
		ID:       "in_recurring",
		Status:   stripe.InvoiceStatusPaid,
		Currency: stripe.CurrencyUSD,
		Total:    200,
		SubscriptionDetails: &stripe.InvoiceSubscriptionDetails{Metadata: map[string]string{
			subscriptionTradeNoMetadataKey: "trade-recurring",
		}},
		Subscription: &stripe.Subscription{ID: "sub_recurring"},
		Lines: &stripe.InvoiceLineItemList{Data: []*stripe.InvoiceLineItem{{
			Price:  &stripe.Price{ID: "price_recurring"},
			Period: &stripe.Period{Start: 100, End: 200},
		}}},
	}
	start, end, err := validateStripeSubscriptionInvoiceEvidence(order, invoice)
	require.NoError(t, err)
	require.EqualValues(t, 100, start)
	require.EqualValues(t, 200, end)
	require.Equal(t, "trade-recurring", stripeInvoiceTradeNo(invoice))

	wrongAmount := *invoice
	wrongAmount.Total = 1
	_, _, err = validateStripeSubscriptionInvoiceEvidence(order, &wrongAmount)
	require.Error(t, err)

	wrongPrice := *invoice
	wrongPrice.Lines = &stripe.InvoiceLineItemList{Data: []*stripe.InvoiceLineItem{{
		Price:  &stripe.Price{ID: "price_wrong"},
		Period: &stripe.Period{Start: 100, End: 200},
	}}}
	_, _, err = validateStripeSubscriptionInvoiceEvidence(order, &wrongPrice)
	require.Error(t, err)
}

func TestCreemRecurringPaymentEvidenceIsStrict(t *testing.T) {
	order := &model.SubscriptionOrder{
		PaymentProvider:      model.PaymentProviderCreem,
		ProviderProductId:    "prod_recurring",
		ExpectedAmountMicros: 8_990_000,
		SettlementCurrency:   "USD",
	}
	event := &CreemWebhookEvent{Id: "evt_paid", EventType: "subscription.paid"}
	event.Object.Id = "sub_recurring"
	event.Object.Object = "subscription"
	event.Object.Product.Id = "prod_recurring"
	event.Object.Product.BillingType = "recurring"
	event.Object.Product.Currency = "USD"
	event.Object.LastTransaction.Id = "tran_recurring"
	event.Object.LastTransaction.AmountPaid = 899
	event.Object.LastTransaction.Currency = "USD"
	event.Object.LastTransaction.Status = "paid"
	event.Object.CurrentPeriodStartDate = "2026-08-01T00:00:00Z"
	event.Object.CurrentPeriodEndDate = "2026-09-01T00:00:00Z"
	event.Object.Metadata = map[string]string{"reference_id": "trade-recurring"}

	start, end, err := validateCreemSubscriptionPaymentEvent(order, event)
	require.NoError(t, err)
	require.Less(t, start, end)
	require.Equal(t, "trade-recurring", creemSubscriptionTradeNo(event))

	wrongAmount := *event
	wrongAmount.Object = event.Object
	wrongAmount.Object.LastTransaction.AmountPaid = 1
	_, _, err = validateCreemSubscriptionPaymentEvent(order, &wrongAmount)
	require.Error(t, err)
}

func TestCreemRemoteProductMustMatchPriceCurrencyAndCadence(t *testing.T) {
	plan := &model.SubscriptionPlan{
		CreemProductId: "prod_recurring",
		DurationUnit:   model.SubscriptionDurationMonth,
		DurationValue:  1,
	}
	product := &creemRemoteProduct{
		ID:            "prod_recurring",
		Price:         200,
		Currency:      "USD",
		BillingType:   "recurring",
		BillingPeriod: "every-month",
		Status:        "active",
	}
	require.NoError(t, validateCreemRemoteProduct(product, plan, 2_000_000, "USD"))

	wrongPrice := *product
	wrongPrice.Price = 680
	require.Error(t, validateCreemRemoteProduct(&wrongPrice, plan, 2_000_000, "USD"))

	oneTime := *product
	oneTime.BillingType = "onetime"
	require.Error(t, validateCreemRemoteProduct(&oneTime, plan, 2_000_000, "USD"))

	wrongCadence := *product
	wrongCadence.BillingPeriod = "every-year"
	require.Error(t, validateCreemRemoteProduct(&wrongCadence, plan, 2_000_000, "USD"))
}

func TestCreemSubscriptionCheckoutEvidenceIsStrict(t *testing.T) {
	order := &model.SubscriptionOrder{
		PaymentProvider:      model.PaymentProviderCreem,
		ExpectedAmountMicros: 2_000_000,
		SettlementCurrency:   "USD",
		ProviderProductId:    "prod_monthly",
	}
	var event CreemWebhookEvent
	event.Id = "evt_creem_1"
	event.EventType = "checkout.completed"
	event.Object.RequestId = "sub_ref_1"
	event.Object.Order.Status = "paid"
	event.Object.Order.Type = "recurring"
	event.Object.Order.AmountPaid = 200
	event.Object.Order.Currency = "usd"
	event.Object.Order.Product = "prod_monthly"
	event.Object.Order.Transaction = "tran_creem_1"
	event.Object.Product.Id = "prod_monthly"

	require.NoError(t, validateCreemSubscriptionCheckoutEvidence(order, &event))

	wrongProduct := event
	wrongProduct.Object.Order.Product = "prod_other"
	require.Error(t, validateCreemSubscriptionCheckoutEvidence(order, &wrongProduct))

	wrongAmount := event
	wrongAmount.Object.Order.AmountPaid = 199
	require.Error(t, validateCreemSubscriptionCheckoutEvidence(order, &wrongAmount))

	wrongCurrency := event
	wrongCurrency.Object.Order.Currency = "CNY"
	require.Error(t, validateCreemSubscriptionCheckoutEvidence(order, &wrongCurrency))
}
