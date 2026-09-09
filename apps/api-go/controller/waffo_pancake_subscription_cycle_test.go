package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type pancakeCycleFixture struct {
	db    *gorm.DB
	order *model.SubscriptionOrder
	start time.Time
	end   time.Time
}

func newPancakeCycleFixture(t *testing.T) pancakeCycleFixture {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.SubscriptionPlan{}, &model.UserSubscription{}, &model.TopUp{}, &model.Log{},
		&model.FinanceLedgerEntry{}, &model.WaffoPancakeSubscriptionPayment{}, &model.WaffoPancakeSubscriptionPeriod{},
	))
	user := model.User{Username: "cycle-user", Password: "password", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	plan := model.SubscriptionPlan{
		Title: "Monthly VIP", PriceAmount: 3.99, Currency: "USD", Enabled: true,
		DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1,
		TotalAmount: 1000, QuotaResetPeriod: model.SubscriptionResetMonthly,
		WaffoPancakeProductId: "PROD_cycle", WaffoPancakeProductType: model.WaffoPancakeProductTypeSubscription,
	}
	require.NoError(t, db.Create(&plan).Error)
	order := &model.SubscriptionOrder{
		UserId: user.Id, PlanId: plan.Id, Money: plan.PriceAmount, TradeNo: "WAFFO_PANCAKE_SUB-cycle",
		PaymentProvider: model.PaymentProviderWaffoPancake, PaymentMethod: model.PaymentMethodWaffoPancake,
		Status: common.TopUpStatusPending, ExpectedAmountMicros: 3_990_000, SettlementCurrency: "USD",
		ProviderStoreId: "STO_cycle", ProviderProductId: "PROD_cycle", ProviderSubscriptionId: "ORD_cycle",
	}
	require.NoError(t, order.Insert())
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return pancakeCycleFixture{db: db, order: order, start: start, end: start.AddDate(0, 1, 0)}
}

func (f pancakeCycleFixture) event(kind, key string, start, end time.Time) *service.WaffoPancakeWebhookEvent {
	event := &service.WaffoPancakeWebhookEvent{
		ID: "ORD_cycle", EventID: key, EventType: kind, Mode: "test", StoreID: "STO_cycle",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data: service.WaffoPancakeWebhookData{
			OrderID: "ORD_cycle", OrderMerchantExternalID: f.order.TradeNo,
			MerchantProvidedBuyerIdentity: service.WaffoPancakeBuyerIdentityFromUserID(f.order.UserId),
			Amount:                        "3.99", Currency: "USD",
			OrderMetadata: map[string]string{
				service.WaffoPancakeOrderMetadataProductID: "PROD_cycle",
				service.WaffoPancakeOrderMetadataPlanID:    strconv.Itoa(f.order.PlanId),
			},
		},
	}
	if kind == "subscription.payment_succeeded" {
		event.Data.PaymentID = "PAY_" + key
		event.Data.PaymentStatus = "succeeded"
		event.Data.PaymentDate = start.Format(time.DateOnly)
		// Deliberately omit all four fields removed on 2026-09-06.
	} else {
		event.Data.OrderStatus = "active"
		event.Data.BillingPeriod = "monthly"
		event.Data.CurrentPeriodStart = start.Format(time.DateOnly)
		event.Data.CurrentPeriodEnd = end.Format(time.DateOnly)
	}
	return event
}

func deliverPancakeCycle(event *service.WaffoPancakeWebhookEvent) int {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/waffo-pancake/webhook/test", nil)
	handleVerifiedWaffoPancakeWebhook(ctx, event, []byte(`{"verified_fixture":true}`))
	return recorder.Code
}

func (f pancakeCycleFixture) subscription(t *testing.T) model.UserSubscription {
	t.Helper()
	order := model.GetSubscriptionOrderByTradeNo(f.order.TradeNo)
	require.NotNil(t, order)
	require.Equal(t, common.TopUpStatusSuccess, order.Status)
	var sub model.UserSubscription
	require.NoError(t, f.db.First(&sub, order.UserSubscriptionId).Error)
	return sub
}

func TestWaffoPancakeSplitSubscriptionEventsSettleInEitherOrder(t *testing.T) {
	for _, paymentFirst := range []bool{true, false} {
		t.Run(strconv.FormatBool(paymentFirst), func(t *testing.T) {
			f := newPancakeCycleFixture(t)
			payment := f.event("subscription.payment_succeeded", "initial-payment", f.start, f.end)
			activation := f.event("subscription.activated", "initial-activation", f.start, f.end)
			first, second := payment, activation
			if !paymentFirst {
				first, second = activation, payment
			}
			require.Equal(t, http.StatusOK, deliverPancakeCycle(first))
			order := model.GetSubscriptionOrderByTradeNo(f.order.TradeNo)
			require.Equal(t, common.TopUpStatusPending, order.Status)
			require.Zero(t, order.UserSubscriptionId, "one event alone must not grant access")
			var payments, periods int64
			require.NoError(t, f.db.Model(&model.WaffoPancakeSubscriptionPayment{}).Count(&payments).Error)
			require.NoError(t, f.db.Model(&model.WaffoPancakeSubscriptionPeriod{}).Count(&periods).Error)
			require.EqualValues(t, 1, payments+periods, "acknowledged unmatched evidence must be durable")
			require.Equal(t, http.StatusOK, deliverPancakeCycle(second))
			sub := f.subscription(t)
			require.Equal(t, f.end.Unix(), sub.EndTime)
			require.EqualValues(t, 1000, sub.AmountTotal)
			require.NoError(t, f.db.Model(&sub).Update("amount_used", 125).Error)
			for _, duplicate := range []*service.WaffoPancakeWebhookEvent{first, second} {
				require.Equal(t, http.StatusOK, deliverPancakeCycle(duplicate))
			}
			sub = f.subscription(t)
			require.EqualValues(t, 125, sub.AmountUsed)
			var ledgerCount int64
			require.NoError(t, f.db.Model(&model.SubscriptionPaymentEvent{}).Count(&ledgerCount).Error)
			require.EqualValues(t, 1, ledgerCount)
		})
	}
}

func TestWaffoPancakeRenewedAndPaymentResetQuotaOnceInEitherOrder(t *testing.T) {
	for _, paymentFirst := range []bool{true, false} {
		t.Run(strconv.FormatBool(paymentFirst), func(t *testing.T) {
			f := newPancakeCycleFixture(t)
			require.Equal(t, http.StatusOK, deliverPancakeCycle(f.event("subscription.activated", "activation", f.start, f.end)))
			require.Equal(t, http.StatusOK, deliverPancakeCycle(f.event("subscription.payment_succeeded", "initial", f.start, f.end)))
			sub := f.subscription(t)
			require.NoError(t, f.db.Model(&sub).Update("amount_used", 700).Error)
			nextEnd := f.end.AddDate(0, 1, 0)
			payment := f.event("subscription.payment_succeeded", "renewal-payment", f.end, nextEnd)
			renewed := f.event("subscription.renewed", "renewal-period", f.end, nextEnd)
			first, second := payment, renewed
			if !paymentFirst {
				first, second = renewed, payment
			}
			require.Equal(t, http.StatusOK, deliverPancakeCycle(first))
			sub = f.subscription(t)
			require.EqualValues(t, 700, sub.AmountUsed)
			require.Equal(t, f.end.Unix(), sub.EndTime)
			require.Equal(t, http.StatusOK, deliverPancakeCycle(second))
			sub = f.subscription(t)
			require.Zero(t, sub.AmountUsed)
			require.Equal(t, nextEnd.Unix(), sub.EndTime)
			require.NoError(t, f.db.Model(&sub).Update("amount_used", 51).Error)
			require.Equal(t, http.StatusOK, deliverPancakeCycle(first))
			require.Equal(t, http.StatusOK, deliverPancakeCycle(second))
			sub = f.subscription(t)
			require.EqualValues(t, 51, sub.AmountUsed)
			var ledger []model.SubscriptionPaymentEvent
			require.NoError(t, f.db.Order("period_end").Find(&ledger).Error)
			require.Len(t, ledger, 2)
			require.Equal(t, f.start.Unix(), ledger[0].PeriodStart)
			require.Equal(t, f.end.Unix(), ledger[1].PeriodStart)
		})
	}
}

func TestWaffoPancakeCycleRejectsConflictingEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*service.WaffoPancakeWebhookEvent){
		"store":          func(e *service.WaffoPancakeWebhookEvent) { e.StoreID = "STO_other" },
		"amount":         func(e *service.WaffoPancakeWebhookEvent) { e.Data.Amount = "0.01" },
		"currency":       func(e *service.WaffoPancakeWebhookEvent) { e.Data.Currency = "CNY" },
		"provider order": func(e *service.WaffoPancakeWebhookEvent) { e.Data.OrderID = "ORD_other" },
		"product": func(e *service.WaffoPancakeWebhookEvent) {
			e.Data.OrderMetadata[service.WaffoPancakeOrderMetadataProductID] = "PROD_other"
		},
		"missing payment date": func(e *service.WaffoPancakeWebhookEvent) { e.Data.PaymentDate = "" },
		"invalid payment date": func(e *service.WaffoPancakeWebhookEvent) { e.Data.PaymentDate = "2026-02-30" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newPancakeCycleFixture(t)
			event := f.event("subscription.payment_succeeded", "bad-payment", f.start, f.end)
			mutate(event)
			require.Equal(t, http.StatusInternalServerError, deliverPancakeCycle(event))
			order := model.GetSubscriptionOrderByTradeNo(f.order.TradeNo)
			require.Equal(t, common.TopUpStatusPending, order.Status)
			require.Zero(t, order.UserSubscriptionId)
		})
	}
}

func TestWaffoPancakeLegacyInlinePeriodRemainsSupported(t *testing.T) {
	f := newPancakeCycleFixture(t)
	event := f.event("subscription.payment_succeeded", "legacy-payment", f.start, f.end)
	event.Data.PaymentDate = ""
	event.Data.CurrentPeriodStart = f.start.Format(time.RFC3339)
	event.Data.CurrentPeriodEnd = f.end.Format(time.RFC3339)
	require.Equal(t, http.StatusOK, deliverPancakeCycle(event))
	sub := f.subscription(t)
	require.Equal(t, f.end.Unix(), sub.EndTime)
}

func TestWaffoPancakeBillingDatesAcceptUTCDateOnly(t *testing.T) {
	for _, field := range []string{"currentPeriodStart", "currentPeriodEnd", "paymentDate"} {
		actual, err := parseWaffoPancakeTimestamp("2026-09-08", field, true)
		require.NoError(t, err)
		require.Equal(t, time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC).Unix(), actual)
	}
	_, err := parseWaffoPancakeTimestamp("2026-09-08", "canceledAt", true)
	require.Error(t, err, "cancellation instants still require an explicit timezone")
}

func TestWaffoPancakeCycleDatabaseFailureRetriesWithoutPartialGrant(t *testing.T) {
	f := newPancakeCycleFixture(t)
	activation := f.event("subscription.activated", "activation", f.start, f.end)
	payment := f.event("subscription.payment_succeeded", "payment", f.start, f.end)
	require.Equal(t, http.StatusOK, deliverPancakeCycle(activation))
	const callback = "test:fail_subscription_ledger"
	require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "subscription_payment_events" {
			tx.AddError(errors.New("injected ledger write failure"))
		}
	}))
	t.Cleanup(func() { f.db.Callback().Create().Remove(callback) })
	require.Equal(t, http.StatusInternalServerError, deliverPancakeCycle(payment))
	order := model.GetSubscriptionOrderByTradeNo(f.order.TradeNo)
	require.Equal(t, common.TopUpStatusPending, order.Status)
	require.Zero(t, order.UserSubscriptionId)
	var grantCount int64
	require.NoError(t, f.db.Model(&model.UserSubscription{}).Count(&grantCount).Error)
	require.Zero(t, grantCount)
	require.NoError(t, f.db.Callback().Create().Remove(callback))
	require.Equal(t, http.StatusOK, deliverPancakeCycle(payment))
	require.Equal(t, f.end.Unix(), f.subscription(t).EndTime)
}

func TestWaffoPancakeRenewedCannotGrantOneTimePlan(t *testing.T) {
	f := newPancakeCycleFixture(t)
	require.NoError(t, f.db.Model(f.order).Update("plan_snapshot", `{"waffo_pancake_product_type":"one_time"}`).Error)
	require.Equal(t, http.StatusOK, deliverPancakeCycle(f.event("subscription.renewed", "renewal", f.start, f.end)))
	order := model.GetSubscriptionOrderByTradeNo(f.order.TradeNo)
	require.Equal(t, common.TopUpStatusPending, order.Status)
	require.Zero(t, order.UserSubscriptionId)
	var count int64
	require.NoError(t, f.db.Model(&model.WaffoPancakeSubscriptionPeriod{}).Count(&count).Error)
	require.Zero(t, count)
}
