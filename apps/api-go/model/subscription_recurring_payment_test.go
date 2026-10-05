package model

import (
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestApplySubscriptionPaymentEventRenewsExactlyOncePerProviderPeriod(t *testing.T) {
	runSubscriptionPaymentEventRenewal(t, false)
}

func TestMigratedSubscriptionPaymentEventRenewsCorrectedGrant(t *testing.T) {
	runSubscriptionPaymentEventRenewal(t, true)
}

func runSubscriptionPaymentEventRenewal(t *testing.T, migrated bool) {
	t.Helper()
	var (
		userID  = 980101
		planID  = 980102
		tradeNo = "WAFFO_PANCAKE_SUB-recurring-contract"
	)
	if migrated {
		userID, planID = 980111, 980112
		tradeNo += "-migrated"
	}
	now := time.Now().Unix()
	user := &User{
		Id:       userID,
		Username: t.Name(),
		AffCode:  t.Name(),
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	t.Cleanup(func() {
		DB.Where("subscription_order_id IN (?)", DB.Model(&SubscriptionOrder{}).Select("id").Where("trade_no = ?", tradeNo)).Delete(&SubscriptionPaymentEvent{})
		DB.Where("trade_no = ?", tradeNo).Delete(&TopUp{})
		DB.Where("trade_no = ?", tradeNo).Delete(&SubscriptionOrder{})
		DB.Where("user_id = ?", userID).Delete(&UserSubscription{})
		DB.Where("id = ?", planID).Delete(&SubscriptionPlan{})
		DB.Where("id = ?", userID).Delete(&User{})
	})
	require.NoError(t, DB.Create(user).Error)
	plan := &SubscriptionPlan{
		Id:               planID,
		Title:            "Recurring contract",
		PriceAmount:      6.8,
		Currency:         "CNY",
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		Enabled:          true,
		TotalAmount:      1_000,
		QuotaResetPeriod: SubscriptionResetMonthly,
	}
	require.NoError(t, DB.Create(plan).Error)
	order := &SubscriptionOrder{
		UserId:               userID,
		PlanId:               planID,
		Money:                plan.PriceAmount,
		TradeNo:              tradeNo,
		PaymentMethod:        PaymentMethodWaffoPancake,
		PaymentProvider:      PaymentProviderWaffoPancake,
		Status:               common.TopUpStatusPending,
		ExpectedAmountMicros: 1_000_000,
		SettlementCurrency:   "USD",
		ProviderProductId:    "PROD_recurring",
		ProviderStoreId:      "STO_recurring",
	}
	require.NoError(t, order.Insert())
	require.NoError(t, CompleteSubscriptionOrder(
		tradeNo,
		`{"event":"initial"}`,
		PaymentProviderWaffoPancake,
		PaymentMethodWaffoPancake,
	))

	firstStart := now - 10
	firstEnd := now + 30*24*60*60
	initial := &SubscriptionPaymentEvent{
		PaymentProvider:        PaymentProviderWaffoPancake,
		ProviderEventId:        "EVT_recurring_initial",
		ProviderTransactionId:  "PAY_recurring_initial",
		SettlementCurrency:     "USD",
		SettlementAmountMicros: 1_000_000,
		PeriodStart:            firstStart,
		PeriodEnd:              firstEnd,
	}
	require.NoError(t, ApplySubscriptionPaymentEvent(tradeNo, initial, "ORD_recurring", "active"))

	storedOrder := GetSubscriptionOrderByTradeNo(tradeNo)
	require.NotNil(t, storedOrder)
	require.Equal(t, firstEnd, storedOrder.CurrentPeriodEnd)
	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, storedOrder.UserSubscriptionId).Error)
	require.Equal(t, firstEnd, subscription.EndTime)
	require.NoError(t, DB.Model(&subscription).Update("amount_used", 700).Error)
	if migrated {
		require.NoError(t, DB.Model(&subscription).Updates(map[string]any{"amount_total": 730, "reset_amount": 100, "renewal_amount": 149}).Error)
	}
	require.NoError(t, DB.Model(storedOrder).Updates(map[string]any{
		"refunded_amount_micros": 500_000,
		"refunded_quota":         100,
	}).Error)

	secondStart := firstEnd
	secondEnd := firstEnd + 31*24*60*60
	renewal := &SubscriptionPaymentEvent{
		PaymentProvider:        PaymentProviderWaffoPancake,
		ProviderEventId:        "EVT_recurring_renewal",
		ProviderTransactionId:  "PAY_recurring_renewal",
		SettlementCurrency:     "USD",
		SettlementAmountMicros: 1_000_000,
		PeriodStart:            secondStart,
		PeriodEnd:              secondEnd,
	}
	require.NoError(t, ApplySubscriptionPaymentEvent(tradeNo, renewal, "ORD_recurring", "active"))
	require.NoError(t, DB.First(&subscription, storedOrder.UserSubscriptionId).Error)
	require.Zero(t, subscription.AmountUsed)
	if migrated {
		require.EqualValues(t, 149, subscription.AmountTotal, "paid renewal cannot reload the old 1000-credit snapshot")
	} else {
		require.EqualValues(t, 1000, subscription.AmountTotal)
	}
	require.Equal(t, secondEnd, subscription.EndTime)
	storedOrder = GetSubscriptionOrderByTradeNo(tradeNo)
	require.NotNil(t, storedOrder)
	require.Zero(t, storedOrder.RefundedAmountMicros)
	require.Zero(t, storedOrder.RefundedQuota)

	// A provider retry cannot reset already-consumed quota a second time.
	require.NoError(t, DB.Model(&subscription).Update("amount_used", 123).Error)
	require.NoError(t, ApplySubscriptionPaymentEvent(tradeNo, renewal, "ORD_recurring", "active"))
	require.NoError(t, DB.First(&subscription, storedOrder.UserSubscriptionId).Error)
	require.EqualValues(t, 123, subscription.AmountUsed)

	var eventCount int64
	require.NoError(t, DB.Model(&SubscriptionPaymentEvent{}).
		Where("subscription_order_id = ?", storedOrder.Id).
		Count(&eventCount).Error)
	require.EqualValues(t, 2, eventCount)

	wrongAmount := *renewal
	wrongAmount.ProviderEventId = "EVT_recurring_wrong_amount"
	wrongAmount.ProviderTransactionId = "PAY_recurring_wrong_amount"
	wrongAmount.PeriodStart = secondEnd
	wrongAmount.PeriodEnd = secondEnd + 31*24*60*60
	wrongAmount.SettlementAmountMicros = 6_800_000
	require.Error(t, ApplySubscriptionPaymentEvent(tradeNo, &wrongAmount, "ORD_recurring", "active"))

	// Cancellation is effective when the terminal event is applied, not at the
	// earlier request time or the beginning of this test. Bound the actual call.
	cancellationStarted := common.GetTimestamp()
	require.NoError(t, UpdateSubscriptionProviderState(
		tradeNo,
		PaymentProviderWaffoPancake,
		"ORD_recurring",
		"canceled",
		secondStart,
		secondEnd,
		now-60,
	))
	require.NoError(t, DB.First(&subscription, storedOrder.UserSubscriptionId).Error)
	require.Equal(t, "cancelled", subscription.Status)
	require.GreaterOrEqual(t, subscription.EndTime, cancellationStarted)
	require.LessOrEqual(t, subscription.EndTime, common.GetTimestamp())
	require.Equal(t, subscription.UpdatedAt, subscription.EndTime)
	require.Zero(t, subscription.NextResetTime)
}
