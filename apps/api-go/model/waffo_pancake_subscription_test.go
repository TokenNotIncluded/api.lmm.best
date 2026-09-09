package model

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func newWaffoSubscriptionFixture(t *testing.T) *SubscriptionOrder {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&WaffoPancakeSubscriptionPayment{}, &WaffoPancakeSubscriptionPeriod{}))
	user := &User{Username: t.Name(), AffCode: t.Name(), Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)
	plan := &SubscriptionPlan{
		Title: t.Name(), PriceAmount: 3.99, Currency: "USD", DurationUnit: SubscriptionDurationMonth,
		DurationValue: 1, Enabled: true, TotalAmount: 1000, UpgradeGroup: "vip",
		QuotaResetPeriod: SubscriptionResetDaily,
	}
	require.NoError(t, DB.Create(plan).Error)
	order := &SubscriptionOrder{
		UserId: user.Id, PlanId: plan.Id, Money: plan.PriceAmount, TradeNo: "WAFFO_PANCAKE_SUB-" + t.Name(),
		PaymentProvider: PaymentProviderWaffoPancake, PaymentMethod: PaymentMethodWaffoPancake,
		Status: common.TopUpStatusPending, ExpectedAmountMicros: 3_990_000, SettlementCurrency: "USD",
	}
	require.NoError(t, order.Insert())
	t.Cleanup(func() {
		DB.Where("subscription_order_id = ?", order.Id).Delete(&WaffoPancakeSubscriptionPayment{})
		DB.Where("subscription_order_id = ?", order.Id).Delete(&WaffoPancakeSubscriptionPeriod{})
		DB.Where("subscription_order_id = ?", order.Id).Delete(&SubscriptionPaymentEvent{})
		DB.Where("trade_no = ?", order.TradeNo).Delete(&TopUp{})
		DB.Where("user_id = ?", user.Id).Delete(&UserSubscription{})
		DB.Delete(order)
		DB.Delete(plan)
		DB.Delete(user)
	})
	return order
}

func waffoSubscriptionFixtureEvents(order *SubscriptionOrder, suffix, kind string, start, end int64) (WaffoPancakeSubscriptionEvent, WaffoPancakeSubscriptionEvent) {
	base := WaffoPancakeSubscriptionEvent{
		ProviderOrderID: fmt.Sprintf("ORD_model_%d", order.Id), Currency: "USD", AmountMicros: order.ExpectedAmountMicros,
	}
	payment := base
	payment.EventID, payment.PaymentID = order.TradeNo+suffix+"-paid", order.TradeNo+suffix+"-payment"
	payment.EventType, payment.PaymentDate = "subscription.payment_succeeded", start+10
	period := base
	period.EventID, period.EventType = order.TradeNo+suffix+"-period", kind
	period.BillingPeriod, period.PeriodStart, period.PeriodEnd = "monthly", start, end
	return payment, period
}

func readWaffoSubscription(t *testing.T, order *SubscriptionOrder) UserSubscription {
	t.Helper()
	stored := GetSubscriptionOrderByTradeNo(order.TradeNo)
	require.NotNil(t, stored)
	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, stored.UserSubscriptionId).Error)
	return subscription
}

func TestWaffoSubscriptionPairsEitherDeliveryOrderAndReplays(t *testing.T) {
	for _, paymentFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("payment_first_%t", paymentFirst), func(t *testing.T) {
			order := newWaffoSubscriptionFixture(t)
			now := time.Now().Unix()
			payment, period := waffoSubscriptionFixtureEvents(order, "initial", "subscription.activated", now-100, now+3600)
			first, second := &payment, &period
			if !paymentFirst {
				first, second = second, first
			}
			settled, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, first)
			require.NoError(t, err)
			require.False(t, settled)
			require.Equal(t, common.TopUpStatusPending, GetSubscriptionOrderByTradeNo(order.TradeNo).Status)
			var count int64
			require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", order.UserId).Count(&count).Error)
			require.Zero(t, count)
			settled, err = RecordWaffoPancakeSubscriptionEvent(order.TradeNo, second)
			require.NoError(t, err)
			require.True(t, settled)
			subscription := readWaffoSubscription(t, order)
			require.Equal(t, period.PeriodStart, subscription.StartTime)
			require.Equal(t, period.PeriodEnd, subscription.EndTime)
			require.LessOrEqual(t, subscription.NextResetTime, subscription.EndTime)
			require.NoError(t, DB.Model(&subscription).Update("amount_used", 321).Error)
			for _, event := range []*WaffoPancakeSubscriptionEvent{first, second} {
				settled, err = RecordWaffoPancakeSubscriptionEvent(order.TradeNo, event)
				require.NoError(t, err)
				require.False(t, settled)
			}
			require.EqualValues(t, 321, readWaffoSubscription(t, order).AmountUsed)
			require.NoError(t, DB.Model(&SubscriptionPaymentEvent{}).Where("subscription_order_id = ?", order.Id).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestWaffoSubscriptionHistoricalPairPreservesCurrentEntitlement(t *testing.T) {
	order := newWaffoSubscriptionFixture(t)
	now := time.Now().Unix()
	latestPayment, latestPeriod := waffoSubscriptionFixtureEvents(order, "new", "subscription.renewed", now-100, now+3600)
	for _, event := range []*WaffoPancakeSubscriptionEvent{&latestPayment, &latestPeriod} {
		_, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, event)
		require.NoError(t, err)
	}
	subscription := readWaffoSubscription(t, order)
	require.NoError(t, DB.Model(&subscription).Update("amount_used", 432).Error)
	oldPayment, oldPeriod := waffoSubscriptionFixtureEvents(order, "old", "subscription.activated", now-7200, now-3600)
	for _, event := range []*WaffoPancakeSubscriptionEvent{&oldPeriod, &oldPayment} {
		_, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, event)
		require.NoError(t, err)
	}
	stored := readWaffoSubscription(t, order)
	require.Equal(t, latestPeriod.PeriodEnd, stored.EndTime)
	require.EqualValues(t, 432, stored.AmountUsed)
	require.Equal(t, latestPeriod.PeriodEnd, GetSubscriptionOrderByTradeNo(order.TradeNo).CurrentPeriodEnd)
	var count int64
	require.NoError(t, DB.Model(&SubscriptionPaymentEvent{}).Where("subscription_order_id = ?", order.Id).Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestWaffoSubscriptionExpiredInitialPairHasNoActiveGrant(t *testing.T) {
	order := newWaffoSubscriptionFixture(t)
	now := time.Now().Unix()
	payment, period := waffoSubscriptionFixtureEvents(order, "expired", "subscription.activated", now-7200, now-3600)
	for _, event := range []*WaffoPancakeSubscriptionEvent{&payment, &period} {
		_, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, event)
		require.NoError(t, err)
	}
	subscription := readWaffoSubscription(t, order)
	require.Equal(t, "expired", subscription.Status)
	require.Equal(t, period.PeriodEnd, subscription.EndTime)
	var user User
	require.NoError(t, DB.First(&user, order.UserId).Error)
	require.Equal(t, "default", user.Group)
}

func TestWaffoSubscriptionSettlementFailureRollsBackCompletion(t *testing.T) {
	order := newWaffoSubscriptionFixture(t)
	now := time.Now().Unix()
	payment, period := waffoSubscriptionFixtureEvents(order, "atomic", "subscription.activated", now-100, now+3600)
	_, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, &period)
	require.NoError(t, err)
	require.NoError(t, DB.Exec(`CREATE TRIGGER reject_waffo_settlement BEFORE INSERT ON subscription_payment_events BEGIN SELECT RAISE(ABORT, 'simulated ledger failure'); END`).Error)
	t.Cleanup(func() { DB.Exec("DROP TRIGGER IF EXISTS reject_waffo_settlement") })
	settled, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, &payment)
	require.Error(t, err)
	require.False(t, settled)
	stored := GetSubscriptionOrderByTradeNo(order.TradeNo)
	require.Equal(t, common.TopUpStatusPending, stored.Status)
	require.Zero(t, stored.UserSubscriptionId)
	for _, table := range []interface{}{&UserSubscription{}, &TopUp{}} {
		var count int64
		require.NoError(t, DB.Model(table).Where("user_id = ?", order.UserId).Count(&count).Error)
		require.Zero(t, count)
	}
	require.NoError(t, DB.Exec("DROP TRIGGER reject_waffo_settlement").Error)
	settled, err = RecordWaffoPancakeSubscriptionEvent(order.TradeNo, &payment)
	require.NoError(t, err)
	require.True(t, settled)
}

func TestWaffoSubscriptionRejectsAmbiguousPeriodsAndConflictingIDs(t *testing.T) {
	order := newWaffoSubscriptionFixture(t)
	now := time.Now().Unix()
	payment, period := waffoSubscriptionFixtureEvents(order, "conflict", "subscription.activated", now-100, now+3600)
	_, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, &payment)
	require.NoError(t, err)
	changedPayment := payment
	changedPayment.PaymentDate++
	_, err = RecordWaffoPancakeSubscriptionEvent(order.TradeNo, &changedPayment)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)
	// Overlapping lifecycle receipts may be retained before a payment exists,
	// but must never choose a cycle arbitrarily once both are available.
	require.NoError(t, DB.Where("subscription_order_id = ?", order.Id).Delete(&WaffoPancakeSubscriptionPayment{}).Error)
	_, err = RecordWaffoPancakeSubscriptionEvent(order.TradeNo, &period)
	require.NoError(t, err)
	overlap := period
	overlap.EventID += "-overlap"
	overlap.PeriodStart--
	_, err = RecordWaffoPancakeSubscriptionEvent(order.TradeNo, &overlap)
	require.NoError(t, err)
	_, err = RecordWaffoPancakeSubscriptionEvent(order.TradeNo, &payment)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)
	require.Equal(t, common.TopUpStatusPending, GetSubscriptionOrderByTradeNo(order.TradeNo).Status)
}

func TestWaffoSubscriptionConcurrentDuplicateDeliverySettlesOnce(t *testing.T) {
	order := newWaffoSubscriptionFixture(t)
	now := time.Now().Unix()
	payment, period := waffoSubscriptionFixtureEvents(order, "concurrent", "subscription.activated", now-100, now+3600)
	errors := make(chan error, 12)
	var deliveries sync.WaitGroup
	for i := 0; i < 12; i++ {
		event := payment
		if i%2 == 0 {
			event = period
		}
		deliveries.Add(1)
		go func() {
			defer deliveries.Done()
			_, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, &event)
			errors <- err
		}()
	}
	deliveries.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, DB.Model(&SubscriptionPaymentEvent{}).Where("subscription_order_id = ?", order.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestWaffoSubscriptionLatePairDoesNotReviveCancellation(t *testing.T) {
	order := newWaffoSubscriptionFixture(t)
	require.NoError(t, CompleteSubscriptionOrder(order.TradeNo, "{}", PaymentProviderWaffoPancake, ""))
	now := time.Now().Unix()
	payment, period := waffoSubscriptionFixtureEvents(order, "cancelled", "subscription.activated", now-100, now+3600)
	require.NoError(t, UpdateSubscriptionProviderState(order.TradeNo, PaymentProviderWaffoPancake, payment.ProviderOrderID, "canceled", period.PeriodStart, period.PeriodEnd, now-50))
	for _, event := range []*WaffoPancakeSubscriptionEvent{&payment, &period} {
		_, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, event)
		require.NoError(t, err)
	}
	subscription := readWaffoSubscription(t, order)
	require.Equal(t, "cancelled", subscription.Status)
	require.Equal(t, now-50, subscription.EndTime)
	var user User
	require.NoError(t, DB.First(&user, order.UserId).Error)
	require.Equal(t, "default", user.Group)
}

func TestWaffoSubscriptionLegacyLedgerReplayUsesExactInlinePeriod(t *testing.T) {
	order := newWaffoSubscriptionFixture(t)
	require.NoError(t, CompleteSubscriptionOrder(order.TradeNo, "{}", PaymentProviderWaffoPancake, ""))
	day := time.Now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)
	start := day.Add(4*time.Hour + 20*time.Minute + 43*time.Second).Unix()
	payment, period := waffoSubscriptionFixtureEvents(order, "legacy", "subscription.activated", start, start+3*24*60*60)
	payment.PaymentDate = day.Unix() // Old date-only paymentDate predates the exact inline start.
	payment.PeriodStart, payment.PeriodEnd = period.PeriodStart, period.PeriodEnd
	require.NoError(t, ApplySubscriptionPaymentEvent(order.TradeNo, &SubscriptionPaymentEvent{
		PaymentProvider: PaymentProviderWaffoPancake, ProviderEventId: payment.EventID, ProviderTransactionId: payment.PaymentID,
		SettlementCurrency: payment.Currency, SettlementAmountMicros: payment.AmountMicros,
		PeriodStart: period.PeriodStart, PeriodEnd: period.PeriodEnd,
	}, payment.ProviderOrderID, "active"))
	settled, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, &payment)
	require.NoError(t, err)
	require.False(t, settled)
	payment.PeriodStart++
	_, err = RecordWaffoPancakeSubscriptionEvent(order.TradeNo, &payment)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)
}

func TestWaffoSubscriptionRenewalWithoutHistoricalLedgerResetsGrant(t *testing.T) {
	order := newWaffoSubscriptionFixture(t)
	require.NoError(t, CompleteSubscriptionOrder(order.TradeNo, "{}", PaymentProviderWaffoPancake, ""))
	subscription := readWaffoSubscription(t, order)
	require.NoError(t, DB.Model(&subscription).Update("amount_used", 555).Error)
	now := time.Now().Unix()
	payment, period := waffoSubscriptionFixtureEvents(order, "migration-renewal", "subscription.renewed", now-100, now+3600)
	for _, event := range []*WaffoPancakeSubscriptionEvent{&payment, &period} {
		_, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, event)
		require.NoError(t, err)
	}
	require.Zero(t, readWaffoSubscription(t, order).AmountUsed)
}
