package model

import (
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedDeletableSubscriptionPlan(t *testing.T, id int) {
	t.Helper()
	require.NoError(t, DB.Create(&SubscriptionPlan{
		Id:            id,
		Title:         "Disposable plan",
		PriceAmount:   1,
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   100,
		Enabled:       true,
	}).Error)
}

func TestAdminDeleteSubscriptionPlanDeletesUnusedPlan(t *testing.T) {
	truncateTables(t)
	seedDeletableSubscriptionPlan(t, 9601)

	result, err := AdminDeleteSubscriptionPlan(9601)
	require.NoError(t, err)
	require.Equal(t, "deleted", result.Action)

	var plan SubscriptionPlan
	err = DB.Where("id = ?", 9601).First(&plan).Error
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestAdminDeleteSubscriptionPlanArchivesSubscribedPlan(t *testing.T) {
	truncateTables(t)
	seedDeletableSubscriptionPlan(t, 9604)
	require.NoError(t, DB.Create(&UserSubscription{
		Id: 9602, UserId: 101, PlanId: 9604, AmountTotal: 100,
		EndTime: GetDBTimestamp() + 3600, Status: "active",
	}).Error)

	result, err := AdminDeleteSubscriptionPlan(9604)
	require.NoError(t, err)
	require.Equal(t, "archived", result.Action)
	require.Positive(t, result.ArchivedAt)

	var plan SubscriptionPlan
	require.NoError(t, DB.Where("id = ?", 9604).First(&plan).Error)
	require.Equal(t, result.ArchivedAt, plan.ArchivedAt)
	require.False(t, plan.Enabled)
}

func TestAdminDeleteSubscriptionPlanArchivesPendingExternalCheckout(t *testing.T) {
	truncateTables(t)
	seedDeletableSubscriptionPlan(t, 9605)
	require.NoError(t, DB.Create(&User{
		Id: 101, Username: "pending-plan-checkout", Password: "password",
		Status: common.UserStatusEnabled, Group: "default",
	}).Error)
	require.NoError(t, DB.Create(&SubscriptionOrder{
		Id: 9603, UserId: 101, PlanId: 9605, Money: 1,
		TradeNo: "delete-plan-order", Status: common.TopUpStatusPending,
	}).Error)

	result, err := AdminDeleteSubscriptionPlan(9605)
	require.NoError(t, err)
	require.Equal(t, "archived", result.Action)
	require.Positive(t, result.ArchivedAt)
	require.Zero(t, result.CancelledOrders)

	var plan SubscriptionPlan
	require.NoError(t, DB.First(&plan, 9605).Error)
	require.False(t, plan.Enabled)
	require.Equal(t, result.ArchivedAt, plan.ArchivedAt)

	var order SubscriptionOrder
	require.NoError(t, DB.First(&order, 9603).Error)
	require.Equal(t, common.TopUpStatusPending, order.Status)
	require.NoError(t, CompleteSubscriptionOrder(order.TradeNo, `{}`, "", ""))
	require.NoError(t, DB.First(&order, 9603).Error)
	require.Equal(t, common.TopUpStatusSuccess, order.Status)

	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("plan_id = ?", 9605).Count(&subscriptionCount).Error)
	require.EqualValues(t, 1, subscriptionCount)
}

func TestAdminDeleteSubscriptionPlanDeletesPlanWithFailedOrderOnly(t *testing.T) {
	truncateTables(t)
	seedDeletableSubscriptionPlan(t, 9607)
	require.NoError(t, DB.Create(&SubscriptionOrder{
		Id: 9604, UserId: 102, PlanId: 9607, Money: 1,
		TradeNo: "delete-plan-failed-order", Status: common.TopUpStatusFailed,
	}).Error)

	result, err := AdminDeleteSubscriptionPlan(9607)
	require.NoError(t, err)
	require.Equal(t, "deleted", result.Action)
	require.Zero(t, result.CancelledOrders)

	var planCount int64
	require.NoError(t, DB.Model(&SubscriptionPlan{}).Where("id = ?", 9607).Count(&planCount).Error)
	require.Zero(t, planCount)
	var failedOrder SubscriptionOrder
	require.NoError(t, DB.First(&failedOrder, 9604).Error)
	require.Equal(t, common.TopUpStatusFailed, failedOrder.Status)
}

func TestAdminDeleteSubscriptionPlanArchivesCompletedOrderHistory(t *testing.T) {
	truncateTables(t)
	seedDeletableSubscriptionPlan(t, 9606)
	require.NoError(t, DB.Create(&SubscriptionOrder{
		Id: 9604, UserId: 101, PlanId: 9606, Money: 1,
		TradeNo: "delete-plan-completed-order", Status: common.TopUpStatusSuccess,
	}).Error)

	result, err := AdminDeleteSubscriptionPlan(9606)
	require.NoError(t, err)
	require.Equal(t, "archived", result.Action)
	require.Positive(t, result.ArchivedAt)

	var plan SubscriptionPlan
	require.NoError(t, DB.First(&plan, 9606).Error)
	require.False(t, plan.Enabled)
	require.Equal(t, result.ArchivedAt, plan.ArchivedAt)
}

func TestAdminDeleteSubscriptionPlanArchivesResetHistory(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, planId int)
	}{
		{
			name: "banked voucher",
			seed: func(t *testing.T, planId int) {
				require.NoError(t, DB.Create(&SubscriptionResetVoucher{
					UserId: 101, PlanId: planId, OperationId: "delete-plan-voucher-history",
					Status: SubscriptionResetVoucherAvailable, ExpiresAt: GetDBTimestamp() + 3600,
				}).Error)
			},
		},
		{
			name: "reset event",
			seed: func(t *testing.T, planId int) {
				require.NoError(t, DB.Create(&SubscriptionResetEvent{
					OperationId: "delete-plan-event-history", UserId: 101, PlanId: planId,
					Mode: SubscriptionResetModeHard, ActorUserId: 1, CreatedAt: GetDBTimestamp(),
				}).Error)
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncateTables(t)
			planId := 9610 + index
			seedDeletableSubscriptionPlan(t, planId)
			test.seed(t, planId)

			result, err := AdminDeleteSubscriptionPlan(planId)
			require.NoError(t, err)
			require.Equal(t, "archived", result.Action)
			var plan SubscriptionPlan
			require.NoError(t, DB.First(&plan, planId).Error)
			require.False(t, plan.Enabled)
			require.Positive(t, plan.ArchivedAt)
		})
	}
}

func TestAdminDeleteSubscriptionPlanRejectsInvalidOrMissingPlan(t *testing.T) {
	truncateTables(t)
	_, err := AdminDeleteSubscriptionPlan(0)
	require.Error(t, err)
	_, err = AdminDeleteSubscriptionPlan(999999)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestAdminBindSubscriptionRejectsArchivedPlanWithoutMutation(t *testing.T) {
	truncateTables(t)
	const userId = 9630
	const planId = 9631
	require.NoError(t, DB.Create(&User{
		Id: userId, Username: "archived-plan-bind", Password: "password", Status: common.UserStatusEnabled,
		Group: "default",
	}).Error)
	require.NoError(t, DB.Create(&SubscriptionPlan{
		Id: planId, Title: "Archived plan", DurationUnit: SubscriptionDurationMonth,
		DurationValue: 1, TotalAmount: 100, UpgradeGroup: "pro", Enabled: true, ArchivedAt: 1,
	}).Error)

	_, err := AdminBindSubscription(userId, planId, "test")
	require.ErrorIs(t, err, ErrSubscriptionPlanArchived)

	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", userId).Count(&subscriptionCount).Error)
	require.Zero(t, subscriptionCount)
	var user User
	require.NoError(t, DB.First(&user, userId).Error)
	require.Equal(t, "default", user.Group)
}

func runDeletePersistenceRace(delete func() error, persist func() error) (error, error) {
	start := make(chan struct{})
	var wait sync.WaitGroup
	var deleteErr error
	var persistErr error
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		deleteErr = delete()
	}()
	go func() {
		defer wait.Done()
		<-start
		persistErr = persist()
	}()
	close(start)
	wait.Wait()
	return deleteErr, persistErr
}

func TestSubscriptionPlanDeleteRacesOrderPersistenceAndPreservesOrderSnapshot(t *testing.T) {
	truncateTables(t)
	const planId = 9610
	seedDeletableSubscriptionPlan(t, planId)

	order := &SubscriptionOrder{
		UserId: 101, PlanId: planId, Money: 1,
		TradeNo: "delete-order-persistence-race", Status: "pending",
	}
	deleteErr, persistErr := runDeletePersistenceRace(
		func() error {
			_, err := AdminDeleteSubscriptionPlan(planId)
			return err
		},
		order.Insert,
	)
	require.NoError(t, deleteErr)
	if persistErr == nil {
		var persisted SubscriptionOrder
		require.NoError(t, DB.Where("trade_no = ?", order.TradeNo).First(&persisted).Error)
		require.Equal(t, planId, persisted.PlanId)
		require.Equal(t, order.Money, persisted.Money)
	} else {
		require.ErrorIs(t, persistErr, gorm.ErrRecordNotFound)
	}
}

func TestSubscriptionPlanDeleteRacesBalancePurchaseWithoutOrphan(t *testing.T) {
	truncateTables(t)
	const planId = 9620
	const userId = 9621
	seedDeletableSubscriptionPlan(t, planId)
	require.NoError(t, DB.Create(&User{
		Id: userId, Username: "delete-balance-race", Password: "password", Status: 1, Quota: 10_000_000,
	}).Error)

	deleteErr, persistErr := runDeletePersistenceRace(
		func() error {
			_, err := AdminDeleteSubscriptionPlan(planId)
			return err
		},
		func() error { return PurchaseSubscriptionWithBalance(userId, planId) },
	)
	require.NoError(t, deleteErr)
	if persistErr == nil {
		var plan SubscriptionPlan
		require.NoError(t, DB.Where("id = ?", planId).First(&plan).Error)
		require.Positive(t, plan.ArchivedAt)
		require.False(t, plan.Enabled)
	} else {
		require.ErrorIs(t, persistErr, gorm.ErrRecordNotFound)
	}

	var orphanSubscriptions int64
	require.NoError(t, DB.Table("user_subscriptions AS subscriptions").
		Joins("LEFT JOIN subscription_plans AS plans ON plans.id = subscriptions.plan_id").
		Where("subscriptions.plan_id = ? AND plans.id IS NULL", planId).
		Count(&orphanSubscriptions).Error)
	require.Zero(t, orphanSubscriptions)
}
