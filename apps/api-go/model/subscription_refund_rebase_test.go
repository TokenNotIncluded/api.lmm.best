package model

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func migratedSubscriptionRefundFixture(t *testing.T, paymentBound bool, used int64) (subscriptionPaymentRefundFixture, SubscriptionOrderCreditRebase) {
	t.Helper()
	db := setupSubscriptionPaymentRefundTestDB(t)
	require.NoError(t, db.AutoMigrate(&SubscriptionOrderCreditRebase{}))
	f := createSubscriptionPaymentRefundFixture(t, db, "migrated")
	// R=10: the 100,000-credit sold grant becomes 10,000 credits while
	// consumed history remains unchanged in amount_total.
	remaining := (100_000 - used) / 10
	require.NoError(t, db.Model(&f.subscription).Updates(map[string]interface{}{
		"amount_total": used + remaining, "amount_used": used,
		"reset_amount": 10_000, "renewal_amount": 10_000,
	}).Error)
	if !paymentBound {
		require.NoError(t, db.Model(&f.order).Updates(map[string]interface{}{
			"plan_snapshot":        `{"total_amount":100000,"waffo_pancake_product_type":"one_time"}`,
			"current_period_start": 0, "current_period_end": 0, "provider_subscription_id": "",
		}).Error)
		f.order.CurrentPeriodStart, f.order.CurrentPeriodEnd = 0, 0
	}
	base := SubscriptionOrderCreditRebase{
		SubscriptionOrderID: f.order.Id, UserSubscriptionID: f.subscription.Id,
		UserID: f.user.Id, MigrationID: "sold-grant-v1", PeriodStart: f.order.CurrentPeriodStart,
		PeriodEnd: f.order.CurrentPeriodEnd, SubscriptionEndTime: f.subscription.EndTime,
		OriginalCreditQuota: 100_000, OriginalPaidAmountMicros: 1_000_000,
		RefundableQuota: remaining, ResetQuota: 10_000,
	}
	require.NoError(t, db.Create(&base).Error)
	return f, base
}

func migratedSubscriptionRefund(t *testing.T, f subscriptionPaymentRefundFixture, bound bool, event string, amount int64) (PaymentRefundResult, error) {
	t.Helper()
	if bound {
		return ApplySubscriptionPaymentRefund(f.request(f.currentPayment, event, amount))
	}
	return ApplyPaymentRefund(f.order.TradeNo, true, amount, FinanceCurrencyUSD, event,
		f.order.PaymentMethod, f.order.PaymentProvider, "rebased subscription", f.user.Id)
}

func TestMigratedSubscriptionRefundShrinksResetWithoutChangingUsageOrRenewal(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(fmt.Sprint(bound), func(t *testing.T) {
			f, base := migratedSubscriptionRefundFixture(t, bound, 20_000)
			result, err := migratedSubscriptionRefund(t, f, bound, "first", 250_000)
			require.NoError(t, err)
			require.EqualValues(t, 2_500, result.QuotaDebited)
			var sub UserSubscription
			require.NoError(t, f.db.First(&sub, f.subscription.Id).Error)
			require.EqualValues(t, 20_000, sub.AmountUsed)
			require.EqualValues(t, 25_500, sub.AmountTotal)
			require.EqualValues(t, 7_500, *sub.ResetAmount)
			require.EqualValues(t, 10_000, *sub.RenewalAmount)
			var plan SubscriptionPlan
			require.NoError(t, f.db.First(&plan, f.order.PlanId).Error)
			require.NoError(t, resetUserSubscriptionTx(f.db, &sub, &plan, common.GetTimestamp(), false))
			require.EqualValues(t, 7_500, sub.AmountTotal)
			require.Zero(t, sub.AmountUsed)
			result, err = migratedSubscriptionRefund(t, f, bound, "second", 250_000)
			require.NoError(t, err)
			require.EqualValues(t, 2_500, result.QuotaDebited)
			require.NoError(t, f.db.First(&sub, sub.Id).Error)
			require.EqualValues(t, 5_000, sub.AmountTotal)
			require.EqualValues(t, 5_000, *sub.ResetAmount)
			require.NoError(t, f.db.First(&f.order, f.order.Id).Error)
			require.EqualValues(t, 50_000, f.order.RefundedQuota, "order retains its old credit unit")
			require.EqualValues(t, 500_000, f.order.RefundedAmountMicros)
			require.NoError(t, f.db.First(&base, "subscription_order_id = ?", f.order.Id).Error)
			require.EqualValues(t, 5_000, base.ResetReducedQuota)
			require.EqualValues(t, 5_000, base.RebasedDebitedQuota)
			result, err = migratedSubscriptionRefund(t, f, bound, "second", 250_000)
			require.NoError(t, err)
			require.False(t, result.Created)
			require.Zero(t, result.QuotaDebited)
		})
	}
}

func TestMigratedSubscriptionRefundSeparatesNominalGrantAndActualDebit(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(fmt.Sprint(bound), func(t *testing.T) {
			f, base := migratedSubscriptionRefundFixture(t, bound, 90_000)
			result, err := migratedSubscriptionRefund(t, f, bound, "exhausted", 250_000)
			require.NoError(t, err)
			require.EqualValues(t, 1_000, result.QuotaDebited)
			result, err = migratedSubscriptionRefund(t, f, bound, "further", 250_000)
			require.NoError(t, err)
			require.Zero(t, result.QuotaDebited)
			var sub UserSubscription
			require.NoError(t, f.db.First(&sub, f.subscription.Id).Error)
			require.EqualValues(t, 90_000, sub.AmountUsed)
			require.EqualValues(t, 90_000, sub.AmountTotal)
			require.EqualValues(t, 5_000, *sub.ResetAmount)
			require.NoError(t, f.db.First(&base, "subscription_order_id = ?", f.order.Id).Error)
			require.EqualValues(t, 5_000, base.ResetReducedQuota)
			require.EqualValues(t, 1_000, base.RebasedDebitedQuota)
		})
	}
}

func TestMigratedSubscriptionRefundMissingOrCorruptAuditFailsClosed(t *testing.T) {
	for _, mode := range []string{"missing_row", "missing_table", "missing_both_audits", "counter", "owner", "reset", "missing_paid_amount"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := migratedSubscriptionRefundFixture(t, false, 20_000)
			if mode != "missing_both_audits" {
				require.NoError(t, f.db.Exec("CREATE TABLE wallet_credit_rebases (plan TEXT)").Error)
				plan := fmt.Sprintf(`{"subscriptions":[{"id":%d}],"subscription_refund_bases":[{"subscription_order_id":%d}]}`, f.subscription.Id, f.order.Id)
				require.NoError(t, f.db.Exec("INSERT INTO wallet_credit_rebases (plan) VALUES (?)", plan).Error)
			}
			switch mode {
			case "missing_row", "missing_both_audits":
				require.NoError(t, f.db.Delete(&SubscriptionOrderCreditRebase{}, "subscription_order_id = ?", f.order.Id).Error)
			case "missing_table":
				require.NoError(t, f.db.Migrator().DropTable(&SubscriptionOrderCreditRebase{}))
			case "counter":
				require.NoError(t, f.db.Model(&SubscriptionOrderCreditRebase{}).Where("subscription_order_id = ?", f.order.Id).Update("reset_reduced_quota", 1).Error)
			case "owner":
				require.NoError(t, f.db.Model(&SubscriptionOrderCreditRebase{}).Where("subscription_order_id = ?", f.order.Id).Update("user_id", f.user.Id+1).Error)
			case "reset":
				require.NoError(t, f.db.Model(&UserSubscription{}).Where("id = ?", f.subscription.Id).Update("reset_amount", 9_999).Error)
			case "missing_paid_amount":
				require.NoError(t, f.db.Model(&f.order).Updates(map[string]interface{}{"expected_amount_micros": 0, "money": 0}).Error)
			}
			before := readSubscriptionPaymentRefundState(t, f.db)
			_, err := migratedSubscriptionRefund(t, f, false, "bad-audit", 250_000)
			require.ErrorIs(t, err, ErrSubscriptionRefundReconciliationRequired)
			require.Equal(t, before, readSubscriptionPaymentRefundState(t, f.db))
		})
	}
}

func TestMigratedSubscriptionPaymentRefundPreservesPriorRefundFacts(t *testing.T) {
	db := setupSubscriptionPaymentRefundTestDB(t)
	require.NoError(t, db.AutoMigrate(&SubscriptionOrderCreditRebase{}))
	f := createSubscriptionPaymentRefundFixture(t, db, "prior-refund")
	require.NoError(t, db.Model(&f.order).Update("charged_quota", 340_000).Error)
	f.apply(t, f.currentPayment, "before-migration", 250_000, 25_000)
	prior := readSubscriptionPaymentRefundState(t, db)
	require.NoError(t, db.Model(&f.subscription).Updates(map[string]interface{}{
		"amount_used": 20_000, "amount_total": 25_500,
		"reset_amount": 7_500, "renewal_amount": 10_000,
	}).Error)
	base := SubscriptionOrderCreditRebase{
		SubscriptionOrderID: f.order.Id, UserSubscriptionID: f.subscription.Id,
		UserID: f.user.Id, MigrationID: "prior-refund", PeriodStart: f.order.CurrentPeriodStart,
		PeriodEnd: f.order.CurrentPeriodEnd, SubscriptionEndTime: f.subscription.EndTime,
		OriginalCreditQuota: 100_000, OriginalRefundedQuota: 25_000,
		OriginalRefundedAmountMicros: 250_000, OriginalPaidAmountMicros: 1_000_000,
		RefundableQuota: 5_500, ResetQuota: 7_500,
	}
	require.NoError(t, db.Create(&base).Error)
	f.apply(t, f.currentPayment, "after-migration", 250_000, 2_500)
	f.apply(t, f.currentPayment, "remaining-refund", 500_000, 3_000)
	state := readSubscriptionPaymentRefundState(t, db)
	require.Equal(t, prior.refunds[0], state.refunds[0], "old refund credit facts remain immutable")
	require.Equal(t, prior.ledger[0], state.ledger[0], "old fiat ledger remains immutable")
	require.EqualValues(t, 100_000, state.orders[0].RefundedQuota)
	require.EqualValues(t, 340_000, state.orders[0].ChargedQuota, "the original charged quota is a historical fact")
	require.EqualValues(t, 1_000_000, state.orders[0].RefundedAmountMicros)
	require.EqualValues(t, 20_000, state.subscriptions[0].AmountUsed)
	require.EqualValues(t, 20_000, state.subscriptions[0].AmountTotal)
	require.Zero(t, *state.subscriptions[0].ResetAmount)
	require.EqualValues(t, 10_000, *state.subscriptions[0].RenewalAmount)
	require.NoError(t, db.First(&base, "subscription_order_id = ?", f.order.Id).Error)
	require.EqualValues(t, 7_500, base.ResetReducedQuota)
	require.EqualValues(t, 5_500, base.RebasedDebitedQuota)
}

func TestMigratedSubscriptionPaymentRefundRejectsUnprovenRenewalAudit(t *testing.T) {
	for _, mode := range []string{"period_start", "period_end", "owner", "original_grant", "missing_both_audits"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := migratedSubscriptionRefundFixture(t, true, 20_000)
			switch mode {
			case "period_start":
				require.NoError(t, f.db.Model(&SubscriptionOrderCreditRebase{}).Where("subscription_order_id = ?", f.order.Id).Update("period_start", f.order.CurrentPeriodStart-1).Error)
			case "period_end":
				require.NoError(t, f.db.Model(&SubscriptionOrderCreditRebase{}).Where("subscription_order_id = ?", f.order.Id).Update("period_end", f.order.CurrentPeriodStart-1).Error)
			case "owner":
				require.NoError(t, f.db.Model(&SubscriptionOrderCreditRebase{}).Where("subscription_order_id = ?", f.order.Id).Update("user_id", f.user.Id+1).Error)
			case "original_grant":
				require.NoError(t, f.db.Model(&SubscriptionOrderCreditRebase{}).Where("subscription_order_id = ?", f.order.Id).Update("original_credit_quota", 200_000).Error)
			case "missing_both_audits":
				require.NoError(t, f.db.Delete(&SubscriptionOrderCreditRebase{}, "subscription_order_id = ?", f.order.Id).Error)
			}
			before := readSubscriptionPaymentRefundState(t, f.db)
			_, err := migratedSubscriptionRefund(t, f, true, "tampered-period", 250_000)
			require.ErrorIs(t, err, ErrSubscriptionRefundReconciliationRequired)
			require.Equal(t, before, readSubscriptionPaymentRefundState(t, f.db))
		})
	}
}

func TestMigratedSubscriptionRefundRollsBackAuditWithFinanceFailure(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(fmt.Sprint(bound), func(t *testing.T) {
			f, base := migratedSubscriptionRefundFixture(t, bound, 20_000)
			require.NoError(t, f.db.Exec(`CREATE TRIGGER migrated_refund_failure
				BEFORE INSERT ON finance_ledger_entries BEGIN SELECT RAISE(ABORT, 'forced_refund_failure'); END`).Error)
			before := readSubscriptionPaymentRefundState(t, f.db)
			result, err := migratedSubscriptionRefund(t, f, bound, "retryable", 250_000)
			require.ErrorContains(t, err, "forced_refund_failure")
			require.Zero(t, result)
			require.Equal(t, before, readSubscriptionPaymentRefundState(t, f.db))
			var after SubscriptionOrderCreditRebase
			require.NoError(t, f.db.First(&after, "subscription_order_id = ?", f.order.Id).Error)
			require.Equal(t, base, after)
			require.NoError(t, f.db.Exec("DROP TRIGGER migrated_refund_failure").Error)
			result, err = migratedSubscriptionRefund(t, f, bound, "retryable", 250_000)
			require.NoError(t, err)
			require.True(t, result.Created)
			require.EqualValues(t, 2_500, result.QuotaDebited)
		})
	}
}

func TestMigratedSubscriptionPaymentRefundHistoricalPeriodAndRenewal(t *testing.T) {
	f, base := migratedSubscriptionRefundFixture(t, true, 20_000)
	f.apply(t, f.currentPayment, "first", 250_000, 2_500)
	oldPayment := f.currentPayment
	next := SubscriptionPaymentEvent{
		PaymentProvider: f.order.PaymentProvider, ProviderEventId: "EVT_new_migrated_period",
		ProviderTransactionId: "PAY_new_migrated_period", SettlementCurrency: FinanceCurrencyUSD,
		SettlementAmountMicros: 1_000_000, PeriodStart: f.currentPayment.PeriodEnd,
		PeriodEnd: f.currentPayment.PeriodEnd + 3_600,
	}
	require.NoError(t, ApplySubscriptionPaymentEvent(f.order.TradeNo, &next, f.order.ProviderSubscriptionId, "active"))
	require.NoError(t, f.db.Create(&WaffoPancakeSubscriptionPayment{
		SubscriptionOrderID: f.order.Id, EventID: next.ProviderEventId, PaymentID: next.ProviderTransactionId,
		Currency: next.SettlementCurrency, AmountMicros: next.SettlementAmountMicros,
		PeriodStart: next.PeriodStart, PeriodEnd: next.PeriodEnd,
	}).Error)
	before := readSubscriptionPaymentRefundState(t, f.db)
	f.apply(t, oldPayment, "late-old-period", 250_000, 0)
	after := readSubscriptionPaymentRefundState(t, f.db)
	assertSubscriptionPaymentRefundEntitlementUnchanged(t, before, after)
	f.apply(t, next, "new-period", 250_000, 2_500)
	var sub UserSubscription
	require.NoError(t, f.db.First(&sub, f.subscription.Id).Error)
	require.EqualValues(t, 7_500, sub.AmountTotal)
	require.EqualValues(t, 7_500, *sub.ResetAmount)
	require.EqualValues(t, 10_000, *sub.RenewalAmount)
	var unchanged SubscriptionOrderCreditRebase
	require.NoError(t, f.db.First(&unchanged, "subscription_order_id = ?", base.SubscriptionOrderID).Error)
	require.EqualValues(t, 2_500, unchanged.RebasedDebitedQuota, "original migration period's audit is never reused for renewal")
}

func TestMigratedSubscriptionRenewalRefundUsesExactCreditRounding(t *testing.T) {
	f, base := migratedSubscriptionRefundFixture(t, true, 0)
	const paid = int64(9_000_000_000_000_000)
	require.NoError(t, f.db.Model(&f.order).Update("expected_amount_micros", paid).Error)
	require.NoError(t, f.db.Model(&f.subscription).Updates(map[string]interface{}{
		"amount_total": 1, "reset_amount": 1, "renewal_amount": 1,
	}).Error)
	require.NoError(t, f.db.Model(&base).Updates(map[string]interface{}{
		"original_paid_amount_micros": paid, "reset_quota": 1, "refundable_quota": 1,
	}).Error)
	next := SubscriptionPaymentEvent{
		PaymentProvider: f.order.PaymentProvider, ProviderEventId: "EVT_exact_refund_period",
		ProviderTransactionId: "PAY_exact_refund_period", SettlementCurrency: FinanceCurrencyUSD,
		SettlementAmountMicros: paid, PeriodStart: f.currentPayment.PeriodEnd,
		PeriodEnd: f.currentPayment.PeriodEnd + 3_600,
	}
	require.NoError(t, ApplySubscriptionPaymentEvent(f.order.TradeNo, &next, f.order.ProviderSubscriptionId, "active"))
	require.NoError(t, f.db.Create(&WaffoPancakeSubscriptionPayment{
		SubscriptionOrderID: f.order.Id, EventID: next.ProviderEventId, PaymentID: next.ProviderTransactionId,
		Currency: next.SettlementCurrency, AmountMicros: paid, PeriodStart: next.PeriodStart, PeriodEnd: next.PeriodEnd,
	}).Error)
	f.apply(t, next, "below-half-credit", 4_499_999_999_999_999, 0)
	var sub UserSubscription
	require.NoError(t, f.db.First(&sub, f.subscription.Id).Error)
	require.EqualValues(t, 1, sub.AmountTotal)
	require.EqualValues(t, 1, *sub.ResetAmount)
	require.Equal(t, "active", sub.Status)
	f.apply(t, next, "exact-half-credit", 1, 1)
	require.NoError(t, f.db.First(&sub, f.subscription.Id).Error)
	require.Zero(t, sub.AmountTotal)
	require.Zero(t, *sub.ResetAmount)
}
