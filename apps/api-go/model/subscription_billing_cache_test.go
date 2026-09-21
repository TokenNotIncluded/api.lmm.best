package model

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionBillingMixedWalletBatchCannotLoseTokenReservation(t *testing.T) {
	db := subscriptionBillingModelFixture(t, false)
	useUserCacheMiniRedis(t)
	resetBatchUpdateTestState(t)
	initCol()
	require.NoError(t, db.Model(&Token{}).Where("id = ?", 9002).Update("remain_quota", 100).Error)
	var token Token
	require.NoError(t, db.First(&token, 9002).Error)
	require.NoError(t, cacheSetToken(token))
	reserved, err := TryReserveTokenQuota(9002, token.Key, 60, false)
	require.NoError(t, err)
	require.True(t, reserved)
	_, err = PreConsumeSubscriptionBilling("mixed-batch", 9001, 9002, "model", 40, true)
	require.NoError(t, err)
	reserved, err = TryReserveTokenQuota(9002, token.Key, 1, false)
	require.NoError(t, err)
	require.False(t, reserved, "wallet and subscription have already reserved all 100 token quota")
	FlushBatchUpdates()
	require.NoError(t, db.First(&token, 9002).Error)
	require.Zero(t, token.RemainQuota)
	require.Equal(t, 100, token.UsedQuota)
}

func TestSubscriptionBillingMixedWalletRedisBatchConcurrentPostgres(t *testing.T) {
	db := subscriptionBillingModelFixture(t, true)
	useUserCacheMiniRedis(t)
	resetBatchUpdateTestState(t)
	require.NoError(t, db.Model(&Token{}).Where("id = ?", 9002).Update("remain_quota", 100).Error)
	var token Token
	require.NoError(t, db.First(&token, 9002).Error)
	require.NoError(t, cacheSetToken(token))
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				_, err := PreConsumeSubscriptionBilling(fmt.Sprint("redis-mixed-", i), 9001, 9002, "model", 10, true)
				if errors.Is(err, ErrSubscriptionBillingTokenQuota) {
					err = nil
				}
				errs <- err
				return
			}
			ok, err := TryReserveTokenQuota(9002, token.Key, 10, false)
			if err == nil && ok {
				ok, err = TryReserveUserQuota(9001, 10)
				if err == nil && !ok {
					err = errors.New("unexpected wallet denial")
				}
			}
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	FlushBatchUpdates()
	var stored Token
	require.NoError(t, db.First(&stored, 9002).Error)
	require.Zero(t, stored.RemainQuota)
	require.Equal(t, 100, stored.UsedQuota)
	var user User
	var sub UserSubscription
	require.NoError(t, db.First(&user, 9001).Error)
	require.NoError(t, db.First(&sub, 9101).Error)
	require.EqualValues(t, 100, 1000000-user.Quota+int(sub.AmountUsed))
	// A stale rehydration cannot authorize any additional spend.
	require.NoError(t, cacheSetToken(token))
	ok, err := TryReserveTokenQuota(9002, token.Key, 1, false)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestSubscriptionBillingMixedBatchCreditsAreDurableAndCacheReplaySafe(t *testing.T) {
	db := subscriptionBillingModelFixture(t, false)
	useUserCacheMiniRedis(t)
	resetBatchUpdateTestState(t)
	initCol()
	_, err := PreConsumeSubscriptionBilling("mixed-credit", 9001, 9002, "model", 60000, true)
	require.NoError(t, err)
	_, err = SettleSubscriptionBilling("mixed-credit", 9001, 160000)
	require.NoError(t, err)
	require.True(t, common.BatchUpdateEnabled)
	require.NoError(t, IncreaseUserQuota(9001, 10000, false))
	require.NoError(t, IncreaseTokenQuota(9002, "billing-model-token", 10000))
	var user User
	var token Token
	require.NoError(t, db.First(&user, 9001).Error)
	require.NoError(t, db.First(&token, 9002).Error)
	require.Equal(t, 950000, user.Quota)
	require.Equal(t, 850000, token.RemainQuota)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, cacheSetToken(token))
	// Delayed post-commit notification after another request hydrated DB state.
	require.NoError(t, cacheIncrUserQuota(9001, 10000))
	require.NoError(t, invalidateTokenCacheForMutation(token.Key))
	FlushBatchUpdates()
	cachedUser, err := GetUserCache(9001)
	require.NoError(t, err)
	cachedToken, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	require.Equal(t, 950000, cachedUser.Quota)
	require.Equal(t, 850000, cachedToken.RemainQuota)
}
