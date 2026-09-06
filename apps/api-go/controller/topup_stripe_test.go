package controller

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81/webhook"
)

func TestStripeWebhookReceiptLogOmitsSensitiveValues(t *testing.T) {
	logLine := stripeWebhookReceiptLog("/api/stripe/webhook", "198.51.100.7", len(`{"secret":"payload"}`))

	require.Contains(t, logLine, "body_bytes=")
	require.NotContains(t, logLine, "secret")
	require.NotContains(t, logLine, "payload")
	require.NotContains(t, logLine, "signature")
}

func TestStripeWebhookRetriesWhenLocalSettlementCannotPersist(t *testing.T) {
	setupTokenControllerTestDB(t)
	confirmPaymentComplianceForTest(t)

	originalAPISecret := setting.StripeApiSecret
	originalWebhookSecret := setting.StripeWebhookSecret
	originalPriceID := setting.StripePriceId
	t.Cleanup(func() {
		setting.StripeApiSecret = originalAPISecret
		setting.StripeWebhookSecret = originalWebhookSecret
		setting.StripePriceId = originalPriceID
	})
	setting.StripeApiSecret = "sk_test_settlement_retry"
	setting.StripeWebhookSecret = "whsec_settlement_retry"
	setting.StripePriceId = "price_settlement_retry"

	payload := []byte(`{
		"id":"evt_settlement_retry",
		"object":"event",
		"type":"checkout.session.completed",
		"data":{"object":{
			"id":"cs_settlement_retry",
			"object":"checkout.session",
			"client_reference_id":"missing-local-order",
			"status":"complete",
			"payment_status":"paid",
			"amount_total":"100",
			"amount_subtotal":"100",
			"currency":"usd"
		}}
	}`)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   payload,
		Secret:    setting.StripeWebhookSecret,
		Timestamp: time.Now(),
	})

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	request := httptest.NewRequest(http.MethodPost, "/api/stripe/webhook", http.NoBody)
	request.Body = io.NopCloser(bytes.NewReader(payload))
	request.Header.Set("Stripe-Signature", signed.Header)
	ctx.Request = request

	StripeWebhook(ctx)
	require.Equal(t, http.StatusInternalServerError, response.Code)
}

func TestStripeWebhookAcceptsSubscriptionOnlyCredentials(t *testing.T) {
	setupTokenControllerTestDB(t)
	confirmPaymentComplianceForTest(t)

	originalAPISecret := setting.StripeApiSecret
	originalWebhookSecret := setting.StripeWebhookSecret
	originalPriceID := setting.StripePriceId
	t.Cleanup(func() {
		setting.StripeApiSecret = originalAPISecret
		setting.StripeWebhookSecret = originalWebhookSecret
		setting.StripePriceId = originalPriceID
	})
	setting.StripeApiSecret = "sk_test_subscription_only" // gitleaks:allow
	setting.StripeWebhookSecret = "whsec_subscription_only"
	setting.StripePriceId = ""

	payload := []byte(`{
		"id":"evt_subscription_only",
		"object":"event",
		"type":"checkout.session.completed",
		"data":{"object":{
			"id":"cs_subscription_only",
			"object":"checkout.session",
			"client_reference_id":"missing-local-order",
			"status":"complete",
			"payment_status":"paid",
			"amount_total":"100",
			"amount_subtotal":"100",
			"currency":"usd"
		}}
	}`)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   payload,
		Secret:    setting.StripeWebhookSecret,
		Timestamp: time.Now(),
	})

	disabled := httptest.NewRecorder()
	disabledCtx, _ := gin.CreateTestContext(disabled)
	disabledCtx.Request = httptest.NewRequest(http.MethodPost, "/api/stripe/webhook", http.NoBody)
	disabledCtx.Request.Body = io.NopCloser(bytes.NewReader(payload))
	setting.StripeWebhookSecret = ""
	StripeWebhook(disabledCtx)
	require.Equal(t, http.StatusForbidden, disabled.Code)

	setting.StripeWebhookSecret = "whsec_subscription_only"
	accepted := httptest.NewRecorder()
	acceptedCtx, _ := gin.CreateTestContext(accepted)
	acceptedRequest := httptest.NewRequest(http.MethodPost, "/api/stripe/webhook", http.NoBody)
	acceptedRequest.Body = io.NopCloser(bytes.NewReader(payload))
	acceptedRequest.Header.Set("Stripe-Signature", signed.Header)
	acceptedCtx.Request = acceptedRequest
	StripeWebhook(acceptedCtx)
	require.NotEqual(t, http.StatusForbidden, accepted.Code)
	require.Equal(t, http.StatusInternalServerError, accepted.Code)
}

func TestStripeWebhookRetriesOnlyPersistableFailures(t *testing.T) {
	require.True(t, stripeWebhookRetryable(errors.New("database unavailable")))
	for _, err := range []error{
		model.ErrSubscriptionOrderStatusInvalid,
		model.ErrTopUpNotFound,
		model.ErrTopUpStatusInvalid,
		model.ErrPaymentEvidenceConflict,
		model.ErrPaymentMethodMismatch,
		model.ErrRefundAmountInvalid,
	} {
		require.False(t, stripeWebhookRetryable(fmt.Errorf("wrapped: %w", err)))
	}
}

func TestStripeWebhookAppliesOneTimeTopUpRefundsExactlyOnce(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.FinanceLedgerEntry{}))
	confirmPaymentComplianceForTest(t)

	originalAPISecret := setting.StripeApiSecret
	originalWebhookSecret := setting.StripeWebhookSecret
	originalPriceID := setting.StripePriceId
	t.Cleanup(func() {
		setting.StripeApiSecret = originalAPISecret
		setting.StripeWebhookSecret = originalWebhookSecret
		setting.StripePriceId = originalPriceID
	})
	setting.StripeApiSecret = "sk_test_refund"
	setting.StripeWebhookSecret = "whsec_refund"
	setting.StripePriceId = "price_refund"

	user := model.User{Username: "stripe-refund-owner", Password: "password", Status: common.UserStatusEnabled, Quota: 100_000}
	require.NoError(t, db.Create(&user).Error)
	paymentIntent := "pi_stripe_refund_owner"
	topUp := model.TopUp{
		UserId: user.Id, TradeNo: "STRIPE-refund-owner", Amount: 100, CreditedQuota: 100_000,
		Money: 10, SettledAmountMicros: 10_000_000, SettlementCurrency: "USD",
		PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe,
		ProviderTransactionId: stringPointer(paymentIntent), Status: common.TopUpStatusSuccess,
	}
	require.NoError(t, db.Create(&topUp).Error)

	apply := func(eventID, eventType, refundID string, amount int64) {
		payload := []byte(fmt.Sprintf(`{"id":%q,"object":"event","type":%q,"data":{"object":{"id":%q,"object":"refund","payment_intent":%q,"amount":%d,"currency":"usd","status":"succeeded"}}}`,
			eventID, eventType, refundID, paymentIntent, amount))
		signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
			Payload: payload, Secret: setting.StripeWebhookSecret, Timestamp: time.Now(),
		})
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		request := httptest.NewRequest(http.MethodPost, "/api/stripe/webhook", bytes.NewReader(payload))
		request.Header.Set("Stripe-Signature", signed.Header)
		ctx.Request = request
		StripeWebhook(ctx)
		require.Equal(t, http.StatusOK, response.Code)
	}

	apply("evt_stripe_refund_partial", "refund.created", "re_stripe_refund_partial", 250)
	apply("evt_stripe_refund_partial", "refund.created", "re_stripe_refund_partial", 250) // provider retry
	apply("evt_stripe_refund_full", "refund.updated", "re_stripe_refund_full", 750)

	var refreshedUser model.User
	require.NoError(t, db.First(&refreshedUser, user.Id).Error)
	assert.Zero(t, refreshedUser.Quota)
	var refreshedTopUp model.TopUp
	require.NoError(t, db.First(&refreshedTopUp, topUp.Id).Error)
	assert.Equal(t, int64(10_000_000), refreshedTopUp.RefundedAmountMicros)
	assert.Equal(t, int64(100_000), refreshedTopUp.RefundedQuota)
	var entries []model.FinanceLedgerEntry
	require.NoError(t, db.Order("id").Find(&entries).Error)
	require.Len(t, entries, 2)
	assert.Equal(t, model.PaymentProviderStripe, entries[0].PaymentProvider)
	assert.Equal(t, model.PaymentMethodStripe, entries[0].PaymentMethod)
}

func TestStripeWebhookRefundRejectsUnboundOrMismatchedSettlement(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.FinanceLedgerEntry{}))
	confirmPaymentComplianceForTest(t)

	originalAPISecret := setting.StripeApiSecret
	originalWebhookSecret := setting.StripeWebhookSecret
	originalPriceID := setting.StripePriceId
	t.Cleanup(func() {
		setting.StripeApiSecret = originalAPISecret
		setting.StripeWebhookSecret = originalWebhookSecret
		setting.StripePriceId = originalPriceID
	})
	setting.StripeApiSecret = "sk_test_refund_invalid"
	setting.StripeWebhookSecret = "whsec_refund_invalid"
	setting.StripePriceId = "price_refund_invalid"

	user := model.User{Username: "stripe-refund-invalid", Password: "password", Status: common.UserStatusEnabled, Quota: 100_000}
	require.NoError(t, db.Create(&user).Error)
	paymentIntent := "pi_stripe_refund_invalid"
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: "STRIPE-refund-invalid", Amount: 100, CreditedQuota: 100_000,
		Money: 10, SettledAmountMicros: 10_000_000, SettlementCurrency: "USD",
		PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe,
		ProviderTransactionId: stringPointer(paymentIntent), Status: common.TopUpStatusSuccess,
	}).Error)

	for _, event := range []struct {
		id, intent, currency string
		amount               int64
	}{
		{id: "evt_stripe_refund_unknown", intent: "pi_not_ours", currency: "usd", amount: 100},
		{id: "evt_stripe_refund_currency", intent: paymentIntent, currency: "eur", amount: 100},
		{id: "evt_stripe_refund_too_large", intent: paymentIntent, currency: "usd", amount: 1001},
	} {
		payload := []byte(fmt.Sprintf(`{"id":%q,"object":"event","type":"refund.updated","data":{"object":{"id":%q,"object":"refund","payment_intent":%q,"amount":%d,"currency":%q,"status":"succeeded"}}}`,
			event.id, "re_"+event.id, event.intent, event.amount, event.currency))
		signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
			Payload: payload, Secret: setting.StripeWebhookSecret, Timestamp: time.Now(),
		})
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		request := httptest.NewRequest(http.MethodPost, "/api/stripe/webhook", bytes.NewReader(payload))
		request.Header.Set("Stripe-Signature", signed.Header)
		ctx.Request = request
		StripeWebhook(ctx)
		require.Equal(t, http.StatusOK, response.Code)
	}

	var refreshed model.User
	require.NoError(t, db.First(&refreshed, user.Id).Error)
	assert.Equal(t, 100_000, refreshed.Quota)
	var entries []model.FinanceLedgerEntry
	require.NoError(t, db.Find(&entries).Error)
	assert.Empty(t, entries)
}
