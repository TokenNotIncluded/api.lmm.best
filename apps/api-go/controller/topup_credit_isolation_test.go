package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81/webhook"
	"gorm.io/gorm"
)

// These tests call main's signature verifier, handler, transaction, and cache
// code with SQLite, miniredis, synthetic events, and an injected write failure.
// Orders are seeded: provider checkout creation and real merchant behavior
// are not covered. Do not run these global-setting fixtures with t.Parallel.
const (
	bi05AmountMinor   int64 = 123
	bi05AmountMicros  int64 = 1_230_000
	bi05Credit        int64 = 9_876
	bi05WebhookSecret       = "whsec_bi05_local_fixture" // gitleaks:allow
)

type bi05DenyDefaultHTTP struct{ attempts atomic.Int64 }

func (guard *bi05DenyDefaultHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	guard.attempts.Add(1)
	return nil, errors.New("BI-05 forbids outbound HTTP")
}

type bi05Fixture struct {
	db     *gorm.DB
	router *gin.Engine
	owner  model.User
	other  model.User
	order  model.TopUp
}

func bi05Setup(t *testing.T) bi05Fixture {
	t.Helper()
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldRDB := common.RedisEnabled, common.RDB
	oldTransport := http.DefaultTransport
	oldAPI, oldSecret, oldPrice := setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId
	oldQuotaPerUnit := common.QuotaPerUnit
	guard := &bi05DenyDefaultHTTP{}
	// Registered first so fixture handles close before the globals are restored.
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.RDB = oldRedis, oldRDB
		http.DefaultTransport = oldTransport
		setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = oldAPI, oldSecret, oldPrice
		common.QuotaPerUnit = oldQuotaPerUnit
		require.Zero(t, guard.attempts.Load(), "callback must not make default-client HTTP requests")
	})
	http.DefaultTransport = guard

	// This existing helper opens its own in-memory SQLite database, never a DSN
	// from the environment, and installs the persisted credit denomination.
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.Log{}))
	confirmPaymentComplianceForTest(t)
	setting.StripeApiSecret = "sk_test_bi05_local_fixture" // gitleaks:allow
	setting.StripeWebhookSecret = bi05WebhookSecret
	setting.StripePriceId = "price_bi05_frozen"

	cache := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: cache.Addr()})
	require.NoError(t, client.Ping(context.Background()).Err())
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() { _ = client.Close() })

	owner := model.User{Username: "bi05-owner", Password: "fixture", AffCode: "bi05-owner", Status: common.UserStatusEnabled, AuthVersion: 1, Quota: 100}
	other := model.User{Username: "bi05-other", Password: "fixture", AffCode: "bi05-other", Status: common.UserStatusEnabled, AuthVersion: 1, Quota: 7}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&other).Error)
	order := model.TopUp{
		UserId: owner.Id, TradeNo: "bi05-order", Amount: 1, CreditedQuota: bi05Credit,
		ExpectedAmountMicros: bi05AmountMicros, SettlementCurrency: "USD", Money: 1.23,
		PaymentProvider: model.PaymentProviderStripe, PaymentMethod: model.PaymentMethodStripe,
		Status: common.TopUpStatusPending, ReferralExcluded: true,
	}
	require.NoError(t, db.Create(&order).Error)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.POST("/api/stripe/webhook", StripeWebhook)
	fixture := bi05Fixture{db: db, router: router, owner: owner, other: other, order: order}
	fixture.assertQuota(t, owner, owner.Quota)
	require.True(t, cache.Exists(fmt.Sprintf("user:%d", owner.Id)), "prime the actual user cache before settlement")
	return fixture
}

func bi05Event(t *testing.T, order, eventID, transaction string, mutate func(map[string]any, map[string]any)) []byte {
	t.Helper()
	checkout := map[string]any{
		"id": "cs_" + order, "object": "checkout.session", "mode": "payment",
		"client_reference_id": order, "payment_intent": transaction,
		"status": "complete", "payment_status": "paid", "currency": "usd",
		"amount_total": bi05AmountMinor, "amount_subtotal": bi05AmountMinor,
	}
	event := map[string]any{
		"id": eventID, "object": "event", "type": "checkout.session.completed",
		"livemode": false, "data": map[string]any{"object": checkout},
	}
	if mutate != nil {
		mutate(event, checkout)
	}
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	return payload
}

func bi05Request(payload []byte) *http.Request {
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload, Secret: bi05WebhookSecret, Timestamp: time.Now(),
	})
	// These client values are not payment evidence and must not select a payee.
	request := httptest.NewRequest(http.MethodPost, "/api/stripe/webhook?user_id=999999&amount=999999&payment_status=paid", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Stripe-Signature", signed.Header)
	request.RemoteAddr = "127.0.0.1:12345"
	return request
}

func (fixture bi05Fixture) deliver(request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	fixture.router.ServeHTTP(response, request)
	return response
}

func (fixture bi05Fixture) concurrentStatus(requests ...*http.Request) []int {
	codes := make([]int, len(requests))
	var workers sync.WaitGroup
	for index, request := range requests {
		workers.Add(1)
		go func(index int, request *http.Request) {
			defer workers.Done()
			codes[index] = fixture.deliver(request).Code
		}(index, request)
	}
	workers.Wait()
	return codes
}

func (fixture bi05Fixture) assertQuota(t *testing.T, user model.User, expected int) {
	t.Helper()
	var persisted model.User
	require.NoError(t, fixture.db.First(&persisted, user.Id).Error)
	require.Equal(t, expected, persisted.Quota, "database balance")
	cached, err := model.GetUserQuota(user.Id, false)
	require.NoError(t, err)
	require.Equal(t, expected, cached, "cache-backed balance must agree with database")
}

func (fixture bi05Fixture) assertPending(t *testing.T, order model.TopUp) {
	t.Helper()
	var persisted model.TopUp
	require.NoError(t, fixture.db.First(&persisted, order.Id).Error)
	require.Equal(t, common.TopUpStatusPending, persisted.Status)
	require.Zero(t, persisted.CompleteTime)
	require.Zero(t, persisted.SettledAmountMicros)
	require.Nil(t, persisted.ProviderTransactionId)
	require.Nil(t, persisted.ProviderEventId)
	require.Equal(t, order.ExpectedAmountMicros, persisted.ExpectedAmountMicros)
	require.Equal(t, order.CreditedQuota, persisted.CreditedQuota)
}

func (fixture bi05Fixture) assertPaid(t *testing.T, order model.TopUp, transaction string) model.TopUp {
	t.Helper()
	var persisted model.TopUp
	require.NoError(t, fixture.db.First(&persisted, order.Id).Error)
	require.Equal(t, common.TopUpStatusSuccess, persisted.Status)
	require.Equal(t, order.UserId, persisted.UserId)
	require.Equal(t, bi05AmountMicros, persisted.ExpectedAmountMicros)
	require.Equal(t, bi05AmountMicros, persisted.SettledAmountMicros)
	require.Equal(t, "USD", persisted.SettlementCurrency)
	require.Equal(t, bi05Credit, persisted.CreditedQuota)
	require.NotNil(t, persisted.ProviderTransactionId)
	require.Equal(t, transaction, *persisted.ProviderTransactionId)
	require.Positive(t, persisted.CompleteTime)
	return persisted
}

func TestBI05StripeCallbackRollbackAndReplay(t *testing.T) {
	fixture := bi05Setup(t)
	payload := bi05Event(t, fixture.order.TradeNo, "evt_bi05_paid", "pi_bi05_paid", nil)

	// The provider signature covers the original bytes, not the changed body.
	tampered := bi05Request(payload)
	changed := bytes.Replace(payload, []byte(`"amount_total":123`), []byte(`"amount_total":124`), 1)
	require.NotEqual(t, payload, changed)
	tampered.Body = io.NopCloser(bytes.NewReader(changed))
	require.Equal(t, http.StatusBadRequest, fixture.deliver(tampered).Code)
	fixture.assertPending(t, fixture.order)
	fixture.assertQuota(t, fixture.owner, fixture.owner.Quota)

	var failCredit, faultReached atomic.Bool
	failCredit.Store(true)
	const callback = "bi05:fail-wallet-write"
	require.NoError(t, fixture.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if failCredit.Load() && tx.Statement.Schema != nil && tx.Statement.Schema.Table == "users" {
			faultReached.Store(true)
			tx.AddError(errors.New("BI-05 injected wallet persistence failure"))
		}
	}))
	t.Cleanup(func() { _ = fixture.db.Callback().Update().Remove(callback) })
	require.Equal(t, http.StatusInternalServerError, fixture.deliver(bi05Request(payload)).Code)
	require.True(t, faultReached.Load(), "failure must occur inside the real settlement transaction")
	fixture.assertPending(t, fixture.order)
	fixture.assertQuota(t, fixture.owner, fixture.owner.Quota)

	failCredit.Store(false)
	// Later price/display settings must not replace this order's frozen quote.
	setting.StripePriceId = "price_bi05_changed_after_order"
	common.QuotaPerUnit *= 2
	// Discard the response to model a missing provider acknowledgement. This
	// tests replay after commit, not a real TCP disconnect or process crash.
	ackLost := bi05Request(payload)
	ackLost.URL.RawQuery = fmt.Sprintf("user_id=%d&amount=999999&payment_status=paid", fixture.other.Id)
	fixture.deliver(ackLost)
	first := fixture.assertPaid(t, fixture.order, "pi_bi05_paid")
	fixture.assertQuota(t, fixture.owner, fixture.owner.Quota+int(bi05Credit))

	requests := make([]*http.Request, 8)
	for index := range requests {
		eventID := "evt_bi05_paid"
		if index%2 == 1 {
			eventID = fmt.Sprintf("evt_bi05_same_transaction_%d", index)
		}
		requests[index] = bi05Request(bi05Event(t, fixture.order.TradeNo, eventID, "pi_bi05_paid", nil))
	}
	for _, code := range fixture.concurrentStatus(requests...) {
		require.Equal(t, http.StatusOK, code)
	}
	last := fixture.assertPaid(t, fixture.order, "pi_bi05_paid")
	require.Equal(t, first.CompleteTime, last.CompleteTime)
	require.Equal(t, first.ProviderEventId, last.ProviderEventId)
	fixture.assertQuota(t, fixture.owner, fixture.owner.Quota+int(bi05Credit))
	fixture.assertQuota(t, fixture.other, fixture.other.Quota)

	var paidOrders int64
	require.NoError(t, fixture.db.Model(&model.TopUp{}).Where("status = ?", common.TopUpStatusSuccess).Count(&paidOrders).Error)
	require.EqualValues(t, 1, paidOrders)
	// TopUp rows are durable payment evidence. Diagnostic Log rows are not
	// an append-only wallet ledger and are not used as a duplicate-credit oracle.
}

func TestBI05StripeRejectsTransactionReuseAcrossUsers(t *testing.T) {
	fixture := bi05Setup(t)
	second := fixture.order
	second.Id, second.UserId, second.TradeNo = 0, fixture.other.Id, "bi05-other-order"
	require.NoError(t, fixture.db.Create(&second).Error)

	payload := bi05Event(t, fixture.order.TradeNo, "evt_bi05_owner", "pi_bi05_owned", nil)
	// Race the first valid callbacks while the order is still pending. This
	// remains a single-process test; it does not prove multi-instance locking.
	for _, code := range fixture.concurrentStatus(bi05Request(payload), bi05Request(payload), bi05Request(payload)) {
		require.Equal(t, http.StatusOK, code)
	}
	fixture.assertPaid(t, fixture.order, "pi_bi05_owned")

	foreign := bi05Event(t, second.TradeNo, "evt_bi05_foreign_order", "pi_bi05_owned", nil)
	// An HTTP acknowledgement of rejected evidence is not a payment receipt.
	require.Equal(t, http.StatusOK, fixture.deliver(bi05Request(foreign)).Code)
	fixture.assertPending(t, second)
	fixture.assertQuota(t, fixture.other, fixture.other.Quota)
	fixture.assertQuota(t, fixture.owner, fixture.owner.Quota+int(bi05Credit))

	valid := bi05Event(t, second.TradeNo, "evt_bi05_other_valid", "pi_bi05_other_valid", nil)
	require.Equal(t, http.StatusOK, fixture.deliver(bi05Request(valid)).Code)
	fixture.assertPaid(t, second, "pi_bi05_other_valid")
	fixture.assertQuota(t, fixture.other, fixture.other.Quota+int(bi05Credit))
	fixture.assertQuota(t, fixture.owner, fixture.owner.Quota+int(bi05Credit))
}

func TestBI05StripeRejectsInvalidPaymentEvidence(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{"unpaid checkout", func(_ map[string]any, checkout map[string]any) { checkout["payment_status"] = "unpaid" }},
		{"wrong quote", func(_ map[string]any, checkout map[string]any) { checkout["amount_subtotal"] = bi05AmountMinor + 1 }},
		{"amount above quote", func(_ map[string]any, checkout map[string]any) { checkout["amount_total"] = bi05AmountMinor + 1 }},
		{"wrong currency", func(_ map[string]any, checkout map[string]any) { checkout["currency"] = "eur" }},
		{"unknown order", func(_ map[string]any, checkout map[string]any) { checkout["client_reference_id"] = "bi05-missing" }},
		// These are acceptance requirements, not claims of reproduced defects.
		// A valid signature alone must not authorize a foreign environment/account.
		{"wrong environment", func(event map[string]any, _ map[string]any) { event["livemode"] = true }},
		{"foreign connected account", func(event map[string]any, _ map[string]any) { event["account"] = "acct_bi05_foreign" }},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			fixture := bi05Setup(t)
			payload := bi05Event(t, fixture.order.TradeNo, "evt_bi05_invalid", "pi_bi05_invalid", item.mutate)
			fixture.deliver(bi05Request(payload))
			fixture.assertPending(t, fixture.order)
			fixture.assertQuota(t, fixture.owner, fixture.owner.Quota)
			fixture.assertQuota(t, fixture.other, fixture.other.Quota)
			// A disabled route or broken fixture must not make a rejection pass.
			control := bi05Event(t, fixture.order.TradeNo, "evt_bi05_control", "pi_bi05_control", nil)
			require.Equal(t, http.StatusOK, fixture.deliver(bi05Request(control)).Code)
			fixture.assertPaid(t, fixture.order, "pi_bi05_control")
			fixture.assertQuota(t, fixture.owner, fixture.owner.Quota+int(bi05Credit))
			fixture.assertQuota(t, fixture.other, fixture.other.Quota)
		})
	}
}
