package model

import (
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createBalanceSubscriptionPlan(t *testing.T, title string, price float64) SubscriptionPlan {
	t.Helper()
	plan := SubscriptionPlan{
		Title:         title,
		PriceAmount:   price,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationDay,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   100,
	}
	require.NoError(t, DB.Create(&plan).Error)
	return plan
}

func TestPurchaseSubscriptionWithBalanceDebitsAtomically(t *testing.T) {
	truncateTables(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	oldRedisEnabled := common.RedisEnabled
	common.QuotaPerUnit = 1
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuotaPerUnit
		common.RedisEnabled = oldRedisEnabled
	})

	plan := createBalanceSubscriptionPlan(t, "wallet exact debit", 1)
	requiredQuota, err := calcSubscriptionBalanceQuota(plan.PriceAmount, plan.Currency)
	require.NoError(t, err)
	require.Positive(t, requiredQuota)
	user := User{Username: "subscription-wallet-exact", Status: common.UserStatusEnabled, Quota: requiredQuota}
	require.NoError(t, DB.Create(&user).Error)

	require.NoError(t, PurchaseSubscriptionWithBalance(user.Id, plan.Id))
	require.NoError(t, DB.First(&user, user.Id).Error)
	assert.Zero(t, user.Quota)
	var orderCount, subscriptionCount int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).Where("user_id = ?", user.Id).Count(&orderCount).Error)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", user.Id).Count(&subscriptionCount).Error)
	assert.EqualValues(t, 1, orderCount)
	assert.EqualValues(t, 1, subscriptionCount)
}

func TestPurchaseSubscriptionWithBalanceRollsBackOnInsufficientOrUnsafeQuota(t *testing.T) {
	truncateTables(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	oldRedisEnabled := common.RedisEnabled
	common.QuotaPerUnit = 1
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuotaPerUnit
		common.RedisEnabled = oldRedisEnabled
	})

	plan := createBalanceSubscriptionPlan(t, "wallet insufficient", 2)
	requiredQuota, err := calcSubscriptionBalanceQuota(plan.PriceAmount, plan.Currency)
	require.NoError(t, err)
	require.Greater(t, requiredQuota, 0)
	user := User{Username: "subscription-wallet-insufficient", Status: common.UserStatusEnabled, Quota: requiredQuota - 1}
	require.NoError(t, DB.Create(&user).Error)

	require.Error(t, PurchaseSubscriptionWithBalance(user.Id, plan.Id))
	require.NoError(t, DB.First(&user, user.Id).Error)
	assert.Equal(t, requiredQuota-1, user.Quota)
	var orderCount, subscriptionCount int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).Where("user_id = ?", user.Id).Count(&orderCount).Error)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", user.Id).Count(&subscriptionCount).Error)
	assert.Zero(t, orderCount)
	assert.Zero(t, subscriptionCount)

	unsafePlan := createBalanceSubscriptionPlan(t, "wallet unsafe", math.MaxFloat64)
	before := user.Quota
	require.Error(t, PurchaseSubscriptionWithBalance(user.Id, unsafePlan.Id))
	require.NoError(t, DB.First(&user, user.Id).Error)
	assert.Equal(t, before, user.Quota)
	require.NoError(t, DB.Model(&SubscriptionOrder{}).Where("user_id = ?", user.Id).Count(&orderCount).Error)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", user.Id).Count(&subscriptionCount).Error)
	assert.Zero(t, orderCount)
	assert.Zero(t, subscriptionCount)
}
