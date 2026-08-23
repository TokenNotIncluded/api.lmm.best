package model

import (
	"errors"
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
