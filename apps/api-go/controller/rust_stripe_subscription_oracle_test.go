package controller

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81/webhook"
)

func TestRustStripeSubscriptionCurrentGoOracle(t *testing.T) {
	output := os.Getenv("LMM_STRIPE_SUBSCRIPTION_GO_ORACLE_OUTPUT")
	if output == "" {
		t.Skip("set LMM_STRIPE_SUBSCRIPTION_GO_ORACLE_OUTPUT to export current Go lifecycle evidence")
	}
	preservePaymentGatewaySettings(t)
	confirmPaymentComplianceForTest(t)
	setupTopupInfoUser(t, 7, "default")
	previousLog := model.LOG_DB
	t.Cleanup(func() { model.LOG_DB = previousLog })
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB(new(model.StartupMigrationSession)))
	require.NoError(t, model.DB.AutoMigrate(&model.Log{}, &model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.UserSubscription{}, &model.SubscriptionPaymentEvent{}))
	setting.StripeApiSecret = "sk_subscription_fixture"
	setting.StripeWebhookSecret = "whsec_fixture"
	setting.StripePriceId = ""
	plan := model.SubscriptionPlan{Id: 3, Title: "Purchased plan", PriceAmount: 1, Currency: "USD", Enabled: true, DurationUnit: "day", DurationValue: 1, TotalAmount: 1000, UpgradeGroup: "pro", DowngradeGroup: "default", QuotaResetPeriod: "custom", QuotaResetCustomSeconds: 1800}
	plan.NormalizeDefaults()
	require.NoError(t, model.DB.Create(&plan).Error)
	order := model.SubscriptionOrder{UserId: 7, PlanId: 3, Money: 1, TradeNo: "sub_order", PaymentMethod: "stripe", PaymentProvider: "stripe", Status: "pending", PlanSnapshot: common.GetJsonString(plan), ExpectedAmountMicros: 1000000, SettlementCurrency: "USD", ProviderProductId: "price_plan"}
	require.NoError(t, order.Insert())
	require.NoError(t, model.DB.Model(&model.SubscriptionPlan{}).Where("id=3").Updates(map[string]any{"total_amount": 9000, "title": "Changed live plan"}).Error)
	deliver := func(event any) int {
		raw, err := json.Marshal(event)
		require.NoError(t, err)
		signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: raw, Secret: "whsec_fixture", Timestamp: time.Now()})
		writer := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(writer)
		ctx.Request = httptest.NewRequest("POST", "/api/stripe/webhook", strings.NewReader(string(raw)))
		ctx.Request.Header.Set("Stripe-Signature", signed.Header)
		StripeWebhook(ctx)
		return writer.Code
	}
	checkout := map[string]any{"id": "evt_checkout", "type": "checkout.session.completed", "data": map[string]any{"object": map[string]any{"id": "cs_subscription", "client_reference_id": "sub_order", "mode": "subscription", "status": "complete", "payment_status": "paid", "amount_total": 100, "currency": "usd", "subscription": "sub_fixture", "metadata": map[string]string{"subscription_price_id": "price_plan"}}}}
	invoice := func(id string, start, end int64) map[string]any {
		return map[string]any{"id": "evt_" + id, "type": "invoice.paid", "data": map[string]any{"object": map[string]any{"id": id, "status": "paid", "total": 100, "currency": "usd", "subscription": "sub_fixture", "subscription_details": map[string]any{"metadata": map[string]string{"subscription_trade_no": "sub_order"}}, "lines": map[string]any{"data": []any{map[string]any{"price": map[string]string{"id": "price_plan"}, "period": map[string]int64{"start": start, "end": end}}}}}}}
	}
	statuses := []int{deliver(checkout), deliver(checkout)}
	var sub model.UserSubscription
	require.NoError(t, model.DB.First(&sub).Error)
	purchasedTotal := sub.AmountTotal
	first := invoice("in_initial", 4102444800, 4102448400)
	statuses = append(statuses, deliver(first), deliver(first))
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id=?", sub.Id).Updates(map[string]any{"amount_total": 300, "amount_used": 200}).Error)
	require.NoError(t, model.DB.Model(&model.SubscriptionOrder{}).Where("id=?", order.Id).Updates(map[string]any{"refunded_amount_micros": 500000, "refunded_quota": 700}).Error)
	renewal := invoice("in_renewal", 4102448400, 4102452000)
	statuses = append(statuses, deliver(renewal), deliver(renewal))
	require.NoError(t, model.DB.First(&sub, sub.Id).Error)
	renewed := map[string]any{"amount_total": sub.AmountTotal, "amount_used": sub.AmountUsed, "quota_version": sub.QuotaVersion, "start_time": sub.StartTime, "end_time": sub.EndTime, "last_reset_time": sub.LastResetTime, "next_reset_time": sub.NextResetTime}
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id=?", sub.Id).Update("amount_used", 17).Error)
	statuses = append(statuses, deliver(invoice("in_late", 4102441200, 4102444800)))
	var count int64
	require.NoError(t, model.DB.Model(&model.SubscriptionPaymentEvent{}).Count(&count).Error)
	lifecycle := map[string]any{"id": "evt_canceling", "type": "customer.subscription.updated", "data": map[string]any{"object": map[string]any{"id": "sub_fixture", "status": "active", "cancel_at_period_end": true, "canceled_at": 1, "current_period_start": 4102448400, "current_period_end": 4102452000, "metadata": map[string]string{"subscription_trade_no": "sub_order"}}}}
	statuses = append(statuses, deliver(lifecycle))
	require.NoError(t, model.DB.First(&sub, sub.Id).Error)
	cancelingStatus := sub.Status
	cancelingEnd := sub.EndTime
	lifecycle["type"] = "customer.subscription.deleted"
	statuses = append(statuses, deliver(lifecycle))
	lifecycle["type"] = "customer.subscription.updated"
	lifecycle["data"].(map[string]any)["object"].(map[string]any)["cancel_at_period_end"] = false
	statuses = append(statuses, deliver(lifecycle))
	require.NoError(t, model.DB.First(&sub, sub.Id).Error)
	require.NoError(t, model.DB.First(&order, order.Id).Error)
	var user model.User
	require.NoError(t, model.DB.First(&user, 7).Error)
	var topups, purchaseLogs int64
	require.NoError(t, model.DB.Model(&model.TopUp{}).Count(&topups).Error)
	require.NoError(t, model.DB.Model(&model.Log{}).Where("type=1").Count(&purchaseLogs).Error)
	result := map[string]any{"plan_snapshot": plan, "statuses": statuses, "purchased_total": purchasedTotal, "renewed": renewed, "receipt_count": count, "canceling_status": cancelingStatus, "canceling_end": cancelingEnd, "final_status": sub.Status, "final_used": sub.AmountUsed, "final_quota_version": sub.QuotaVersion, "final_next_reset": sub.NextResetTime, "final_group": user.Group, "wallet_quota": user.Quota, "provider_state": order.ProviderSubscriptionState, "provider_subscription_id": order.ProviderSubscriptionId, "current_period_start": order.CurrentPeriodStart, "current_period_end": order.CurrentPeriodEnd, "refunded_amount_micros": order.RefundedAmountMicros, "refunded_quota": order.RefundedQuota, "shadow_topup_count": topups, "purchase_and_renewal_logs": purchaseLogs, "cancellation_not_backdated": sub.EndTime > 1700000000 && sub.EndTime < 4102444800}
	// Go commits the initial purchased entitlement before its separate recurring
	// receipt transaction. Preserve and export the real failure/retry boundary.
	failedOrder := model.SubscriptionOrder{UserId: 7, PlanId: 3, Money: 1, TradeNo: "sub_failure", PaymentMethod: "stripe", PaymentProvider: "stripe", Status: "pending", PlanSnapshot: common.GetJsonString(plan), ExpectedAmountMicros: 1000000, SettlementCurrency: "USD", ProviderProductId: "price_plan"}
	require.NoError(t, failedOrder.Insert())
	require.NoError(t, model.DB.Exec("CREATE TRIGGER fail_stripe_receipt BEFORE INSERT ON subscription_payment_events BEGIN SELECT RAISE(ABORT, 'fixture receipt failed'); END").Error)
	fault := invoice("in_failure", 4102444800, 4102448400)
	faultObject := fault["data"].(map[string]any)["object"].(map[string]any)
	faultObject["subscription"] = "sub_failure"
	faultObject["subscription_details"].(map[string]any)["metadata"].(map[string]string)["subscription_trade_no"] = "sub_failure"
	failedStatus := deliver(fault)
	require.NoError(t, model.DB.First(&failedOrder, failedOrder.Id).Error)
	var failedCount, failedReceipts int64
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Count(&failedCount).Error)
	require.NoError(t, model.DB.Model(&model.SubscriptionPaymentEvent{}).Where("subscription_order_id=?", failedOrder.Id).Count(&failedReceipts).Error)
	failure := map[string]any{"status_on_failure": failedStatus, "order_status_on_failure": failedOrder.Status, "new_entitlements_on_failure": failedCount - 1, "receipt_count_on_failure": failedReceipts}
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id=?", failedOrder.UserSubscriptionId).Update("amount_used", 31).Error)
	require.NoError(t, model.DB.Exec("DROP TRIGGER fail_stripe_receipt").Error)
	failure["status_on_retry"] = deliver(fault)
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Count(&failedCount).Error)
	require.NoError(t, model.DB.Model(&model.SubscriptionPaymentEvent{}).Where("subscription_order_id=?", failedOrder.Id).Count(&failedReceipts).Error)
	failure["new_entitlements_after_retry"] = failedCount - 1
	failure["receipt_count_after_retry"] = failedReceipts
	var failedSubscription model.UserSubscription
	require.NoError(t, model.DB.First(&failedSubscription, failedOrder.UserSubscriptionId).Error)
	failure["used_quota_after_retry"] = failedSubscription.AmountUsed
	result["receipt_failure"] = failure
	encoded, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, append(encoded, '\n'), 0600))
}
