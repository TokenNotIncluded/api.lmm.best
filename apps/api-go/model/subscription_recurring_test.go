package model

import (
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestApplySubscriptionPaymentEventIsIdempotentAndRenewsOnce(t *testing.T) {
	const userID = 501
	const planID = 601
	user := &User{Id: userID, Username: "recurring_idempotency_user", AffCode: "recurring_idempotency_user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}
	plan := &SubscriptionPlan{Id: planID, Title: "Recurring idempotency plan", PriceAmount: 9.99, Currency: "USD", DurationUnit: "month", DurationValue: 1, TotalAmount: 1000, Enabled: true}
	require.NoError(t, DB.Create(user).Error)
	require.NoError(t, DB.Create(plan).Error)
	t.Cleanup(func() {
		DB.Where("subscription_order_id IN (?)", DB.Model(&SubscriptionOrder{}).Select("id").Where("trade_no = ?", "recurring-contract-order")).Delete(&SubscriptionPaymentEvent{})
		DB.Where("trade_no = ?", "recurring-contract-order").Delete(&TopUp{})
		DB.Where("trade_no = ?", "recurring-contract-order").Delete(&SubscriptionOrder{})
		DB.Where("user_id = ?", userID).Delete(&UserSubscription{})
		DB.Where("id = ?", planID).Delete(&SubscriptionPlan{})
		DB.Where("id = ?", userID).Delete(&User{})
	})
	order := &SubscriptionOrder{
		UserId:               userID,
		PlanId:               plan.Id,
		Money:                plan.PriceAmount,
		TradeNo:              "recurring-contract-order",
		PaymentMethod:        PaymentMethodWaffoPancake,
		PaymentProvider:      PaymentProviderWaffoPancake,
		Status:               common.TopUpStatusPending,
		CreateTime:           time.Now().Unix(),
		PlanSnapshot:         common.GetJsonString(plan),
		ExpectedAmountMicros: 9_990_000,
		SettlementCurrency:   "USD",
		ProviderProductId:    "PROD_recurring",
	}
	require.NoError(t, order.Insert())
	require.NoError(t, CompleteSubscriptionOrder(order.TradeNo, `{}`, PaymentProviderWaffoPancake, ""))

	now := common.GetTimestamp()
	first := &SubscriptionPaymentEvent{
		PaymentProvider:        PaymentProviderWaffoPancake,
		ProviderEventId:        "event-cycle-1",
		ProviderTransactionId:  "payment-cycle-1",
		SettlementCurrency:     "USD",
		SettlementAmountMicros: 9_990_000,
		PeriodStart:            now - 60,
		PeriodEnd:              now + 3600,
	}
	require.NoError(t, ApplySubscriptionPaymentEvent(order.TradeNo, first, "provider-subscription", "active"))

	storedOrder := GetSubscriptionOrderByTradeNo(order.TradeNo)
	require.NotNil(t, storedOrder)
	require.Positive(t, storedOrder.UserSubscriptionId)
	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, storedOrder.UserSubscriptionId).Error)
	require.Equal(t, first.PeriodEnd, subscription.EndTime)

	require.NoError(t, DB.Model(&subscription).Update("amount_used", int64(123)).Error)
	second := &SubscriptionPaymentEvent{
		PaymentProvider:        PaymentProviderWaffoPancake,
		ProviderEventId:        "event-cycle-2",
		ProviderTransactionId:  "payment-cycle-2",
		SettlementCurrency:     "USD",
		SettlementAmountMicros: 9_990_000,
		PeriodStart:            first.PeriodEnd,
		PeriodEnd:              first.PeriodEnd + 3600,
	}
	require.NoError(t, ApplySubscriptionPaymentEvent(order.TradeNo, second, "provider-subscription", "active"))
	require.NoError(t, DB.First(&subscription, storedOrder.UserSubscriptionId).Error)
	require.Equal(t, second.PeriodEnd, subscription.EndTime)
	require.Zero(t, subscription.AmountUsed)

	// Exact replay is a no-op.
	require.NoError(t, ApplySubscriptionPaymentEvent(order.TradeNo, second, "provider-subscription", "active"))
	var count int64
	require.NoError(t, DB.Model(&SubscriptionPaymentEvent{}).Where("subscription_order_id = ?", storedOrder.Id).Count(&count).Error)
	require.EqualValues(t, 2, count)

	// Reusing an event id with different evidence is a conflict, not a silent replay.
	conflict := *second
	conflict.ProviderTransactionId = "different-payment"
	err := ApplySubscriptionPaymentEvent(order.TradeNo, &conflict, "provider-subscription", "active")
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)
}
