package model

import (
	"errors"
	"sync"
	"testing"

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

	require.NoError(t, AdminDeleteSubscriptionPlan(9601))

	var plan SubscriptionPlan
	err := DB.Where("id = ?", 9601).First(&plan).Error
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestAdminDeleteSubscriptionPlanRejectsHistoricalReferences(t *testing.T) {
	for _, test := range []struct {
		name string
		seed func(t *testing.T, planId int)
	}{
		{
			name: "user subscription",
			seed: func(t *testing.T, planId int) {
				t.Helper()
				require.NoError(t, DB.Create(&UserSubscription{
					Id:          9602,
					UserId:      101,
					PlanId:      planId,
					AmountTotal: 100,
					EndTime:     GetDBTimestamp() + 3600,
					Status:      "active",
				}).Error)
			},
		},
		{
			name: "subscription order",
			seed: func(t *testing.T, planId int) {
				t.Helper()
				require.NoError(t, DB.Create(&SubscriptionOrder{
					Id:      9603,
					UserId:  101,
					PlanId:  planId,
					Money:   1,
					TradeNo: "delete-plan-order",
				}).Error)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			truncateTables(t)
			seedDeletableSubscriptionPlan(t, 9604)
			test.seed(t, 9604)

			err := AdminDeleteSubscriptionPlan(9604)
			require.True(t, errors.Is(err, ErrSubscriptionPlanInUse))

			var count int64
			require.NoError(t, DB.Model(&SubscriptionPlan{}).Where("id = ?", 9604).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestAdminDeleteSubscriptionPlanRejectsInvalidOrMissingPlan(t *testing.T) {
	truncateTables(t)
	require.Error(t, AdminDeleteSubscriptionPlan(0))
	require.ErrorIs(t, AdminDeleteSubscriptionPlan(999999), gorm.ErrRecordNotFound)
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

func assertDeletePersistenceRaceResult(t *testing.T, planId int, deleteErr, persistErr error) {
	t.Helper()
	if deleteErr == nil {
		require.ErrorIs(t, persistErr, gorm.ErrRecordNotFound)
	} else {
		require.ErrorIs(t, deleteErr, ErrSubscriptionPlanInUse)
		require.NoError(t, persistErr)
	}

	var orphanOrders int64
	require.NoError(t, DB.Table("subscription_orders AS orders").
		Joins("LEFT JOIN subscription_plans AS plans ON plans.id = orders.plan_id").
		Where("orders.plan_id = ? AND plans.id IS NULL", planId).
		Count(&orphanOrders).Error)
	require.Zero(t, orphanOrders)

	var orphanSubscriptions int64
	require.NoError(t, DB.Table("user_subscriptions AS subscriptions").
		Joins("LEFT JOIN subscription_plans AS plans ON plans.id = subscriptions.plan_id").
		Where("subscriptions.plan_id = ? AND plans.id IS NULL", planId).
		Count(&orphanSubscriptions).Error)
	require.Zero(t, orphanSubscriptions)
}

func TestSubscriptionPlanDeleteRacesOrderPersistenceWithoutOrphan(t *testing.T) {
	truncateTables(t)
	const planId = 9610
	seedDeletableSubscriptionPlan(t, planId)

	order := &SubscriptionOrder{
		UserId:  101,
		PlanId:  planId,
		Money:   1,
		TradeNo: "delete-order-persistence-race",
		Status:  "pending",
	}
	deleteErr, persistErr := runDeletePersistenceRace(
		func() error { return AdminDeleteSubscriptionPlan(planId) },
		order.Insert,
	)
	assertDeletePersistenceRaceResult(t, planId, deleteErr, persistErr)
}

func TestSubscriptionPlanDeleteRacesBalancePurchaseWithoutOrphan(t *testing.T) {
	truncateTables(t)
	const planId = 9620
	const userId = 9621
	seedDeletableSubscriptionPlan(t, planId)
	require.NoError(t, DB.Create(&User{
		Id:       userId,
		Username: "delete-balance-race",
		Password: "password",
		Status:   1,
		Quota:    10_000_000,
	}).Error)

	deleteErr, persistErr := runDeletePersistenceRace(
		func() error { return AdminDeleteSubscriptionPlan(planId) },
		func() error { return PurchaseSubscriptionWithBalance(userId, planId) },
	)
	assertDeletePersistenceRaceResult(t, planId, deleteErr, persistErr)
}
