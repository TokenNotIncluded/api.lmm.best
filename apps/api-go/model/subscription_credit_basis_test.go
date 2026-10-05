package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMigratedSubscriptionResetKeepsCorrectedGrantAndAudit(t *testing.T) {
	truncateTables(t)
	seedResetSubscription(t, 9771, 9772, 9773, 9000)
	// Migration retains 9,000 consumed credits and corrects the 1,000
	// remaining credits by 6.710363. A full subsequent grant is 1,490.
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", 9773).
		Updates(map[string]any{"amount_total": 9149, "reset_amount": 1490}).Error)
	var before UserSubscription
	require.NoError(t, DB.First(&before, 9773).Error)
	require.EqualValues(t, 9000, before.AmountUsed)
	require.EqualValues(t, 149, before.AmountTotal-before.AmountUsed)

	preview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{{UserId: 9771, PlanId: 9772}},
	})
	require.NoError(t, err)
	require.EqualValues(t, 1341, preview.QuotaToRestore)
	result, err := AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, OperationId: "migrated-reset", PreviewToken: preview.Token,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1341, result.RestoredQuota)
	var after UserSubscription
	require.NoError(t, DB.First(&after, 9773).Error)
	require.Zero(t, after.AmountUsed)
	require.EqualValues(t, 1490, after.AmountTotal)
	require.EqualValues(t, 1490, *after.ResetAmount)
	require.Equal(t, before.NextResetTime, after.NextResetTime)

	// Voucher reset uses the same path as a single-user administrator reset.
	plan := &SubscriptionPlan{Id: 9772}
	require.NoError(t, DB.First(plan).Error)
	require.NoError(t, DB.Model(&after).Update("amount_used", 100).Error)
	reset, err := adminResetUserSubscriptionsByPlanTx(DB, 9771, plan, GetDBTimestamp(), false)
	require.NoError(t, err)
	require.EqualValues(t, 100, reset.RestoredQuota)
	require.NoError(t, DB.First(&after, 9773).Error)
	require.EqualValues(t, 1490, after.AmountTotal)
}

func TestMigratedSubscriptionScheduledResetAndZeroGrant(t *testing.T) {
	for _, grant := range []int64{1490, 0} {
		t.Run(time.Unix(grant, 0).Format("150405"), func(t *testing.T) {
			db := subscriptionBillingModelFixture(t, false)
			now := time.Now().Unix()
			plan := &SubscriptionPlan{Id: 9003}
			require.NoError(t, db.First(plan).Error)
			plan.QuotaResetPeriod = SubscriptionResetDaily
			require.NoError(t, db.Save(plan).Error)
			require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", 9101).Updates(map[string]any{
				"amount_total": 9149, "amount_used": 9000, "reset_amount": grant,
				"last_reset_time": now - 25*3600, "next_reset_time": now - 3600,
			}).Error)
			var sub UserSubscription
			require.NoError(t, db.First(&sub, 9101).Error)
			require.NoError(t, maybeResetUserSubscriptionWithPlanTx(db, &sub, plan, now))
			require.Zero(t, sub.AmountUsed)
			require.Equal(t, grant, sub.AmountTotal)
			require.True(t, sub.hasFiniteQuota())
			if grant == 0 {
				_, err := PreConsumeUserSubscription("zero-grant", 9001, "test", 0, 1)
				require.ErrorIs(t, err, ErrSubscriptionQuotaInsufficient, "a rounded-to-zero finite grant is never unlimited")
			} else {
				_, err := PreConsumeUserSubscription("limited-grant", 9001, "test", 0, grant)
				require.NoError(t, err)
				_, err = PreConsumeUserSubscription("excess-grant", 9001, "test", 0, 1)
				require.ErrorIs(t, err, ErrSubscriptionQuotaInsufficient)
			}
		})
	}
}

func TestMigratedSubscriptionLifecycleRenewalCannotRestoreOldSnapshot(t *testing.T) {
	order := newWaffoSubscriptionFixture(t)
	now := time.Now().Unix()
	payment, period := waffoSubscriptionFixtureEvents(order, "first", "subscription.activated", now-100, now+3600)
	for _, event := range []*WaffoPancakeSubscriptionEvent{&payment, &period} {
		_, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, event)
		require.NoError(t, err)
	}
	sub := readWaffoSubscription(t, order)
	require.NoError(t, DB.Model(&sub).Updates(map[string]any{
		"amount_total": 910, "amount_used": 900, "reset_amount": 100, "renewal_amount": 149,
	}).Error)
	var resetPlan SubscriptionPlan
	require.NoError(t, DB.First(&resetPlan, order.PlanId).Error)
	require.NoError(t, DB.First(&sub, sub.Id).Error)
	require.NoError(t, resetUserSubscriptionTx(DB, &sub, &resetPlan, now, false))
	require.EqualValues(t, 100, sub.AmountTotal, "reset retains the partial-refund reduction for this paid period")
	require.EqualValues(t, 149, *sub.RenewalAmount, "full renewal contract is independent of current-period refunds")
	payment2, period2 := waffoSubscriptionFixtureEvents(order, "second", "subscription.renewed", now+3600, now+7200)
	for _, event := range []*WaffoPancakeSubscriptionEvent{&payment2, &period2} {
		_, err := RecordWaffoPancakeSubscriptionEvent(order.TradeNo, event)
		require.NoError(t, err)
	}
	after := readWaffoSubscription(t, order)
	require.EqualValues(t, 149, after.AmountTotal, "old purchased snapshot remains 1000 but its corrected grant must persist")
	require.Zero(t, after.AmountUsed)
	require.EqualValues(t, 149, *after.ResetAmount)
	require.EqualValues(t, 149, *after.RenewalAmount)
}

func TestMigratedSubscriptionResetPreviewRejectsGrantChange(t *testing.T) {
	truncateTables(t)
	seedResetSubscription(t, 9761, 9762, 9763, 9000)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", 9763).
		Updates(map[string]any{"amount_total": 9149, "reset_amount": 1490}).Error)
	preview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, Mode: SubscriptionResetModeHard,
		Targets: []SubscriptionResetTarget{{UserId: 9761, PlanId: 9762}},
	})
	require.NoError(t, err)
	// Simulate a grant adjustment without a timestamp/usage change. It still
	// invalidates the frozen preview rather than executing its stale grant.
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", 9763).UpdateColumn("reset_amount", 1400).Error)
	_, err = AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
		ActorUserId: 1, OperationId: "changed-migrated-grant", PreviewToken: preview.Token,
	})
	require.ErrorIs(t, err, ErrSubscriptionResetPreviewStale)
	var sub UserSubscription
	require.NoError(t, DB.First(&sub, 9763).Error)
	require.EqualValues(t, 9000, sub.AmountUsed)
}
