package model

import (
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func requireWalletBillingBudgetBalances(t *testing.T, db *gorm.DB, wallet, remain, used int) {
	t.Helper()
	var user User
	var token Token
	require.NoError(t, db.First(&user, 9001).Error)
	require.NoError(t, db.Unscoped().First(&token, 9002).Error)
	require.Equal(t, wallet, user.Quota)
	require.Equal(t, remain, token.RemainQuota)
	require.Equal(t, used, token.UsedQuota)
}

func TestWalletBillingBudgetReservesBothImmediately(t *testing.T) {
	db := subscriptionBillingModelFixture(t, false)
	resetBatchUpdateTestState(t)
	require.True(t, common.BatchUpdateEnabled)
	require.NoError(t, ReserveWalletBillingBudget(9001, 9002, 600000, 900000))
	requireWalletBillingBudgetBalances(t, db, 400000, 400000, 600000)
	FlushBatchUpdates()
	requireWalletBillingBudgetBalances(t, db, 400000, 400000, 600000)
	for _, store := range batchUpdateStores {
		require.Empty(t, store, "a durable paired reservation must never enqueue another debit")
	}
}

func TestWalletBillingBudgetWalletFailureLeavesTokenUntouched(t *testing.T) {
	for _, tc := range []struct {
		name            string
		amount, minimum int
	}{
		{name: "amount", amount: 1000001},
		{name: "minimum", amount: 1, minimum: 1000001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := subscriptionBillingModelFixture(t, false)
			require.ErrorIs(t, ReserveWalletBillingBudget(9001, 9002, tc.amount, tc.minimum), ErrWalletBillingBudgetQuota)
			requireWalletBillingBudgetBalances(t, db, 1000000, 1000000, 0)
		})
	}
}

func TestWalletBillingBudgetTokenFailureRollsBackWallet(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *gorm.DB)
		want  error
	}{
		{name: "insufficient", setup: func(t *testing.T, db *gorm.DB) {
			require.NoError(t, db.Model(&Token{}).Where("id = ?", 9002).Update("remain_quota", 9).Error)
		}, want: ErrSubscriptionBillingTokenQuota},
		{name: "ownership", setup: func(t *testing.T, db *gorm.DB) {
			require.NoError(t, db.Model(&Token{}).Where("id = ?", 9002).Update("user_id", 9003).Error)
		}, want: gorm.ErrRecordNotFound},
		{name: "deleted", setup: func(t *testing.T, db *gorm.DB) {
			require.NoError(t, db.Delete(&Token{}, 9002).Error)
		}, want: gorm.ErrRecordNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := subscriptionBillingModelFixture(t, false)
			tc.setup(t, db)
			var before Token
			require.NoError(t, db.Unscoped().First(&before, 9002).Error)
			require.ErrorIs(t, ReserveWalletBillingBudget(9001, 9002, 10, 0), tc.want)
			requireWalletBillingBudgetBalances(t, db, 1000000, before.RemainQuota, before.UsedQuota)
		})
	}
}

func TestWalletBillingBudgetUnlimitedTruthAndOverflowComeFromDatabase(t *testing.T) {
	for _, tc := range []struct {
		name         string
		remain, used int
		fail         bool
	}{
		{name: "unlimited", remain: 0},
		{name: "used-overflow", used: math.MaxInt64, fail: true},
		{name: "remain-underflow", remain: math.MinInt64, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := subscriptionBillingModelFixture(t, false)
			require.NoError(t, db.Model(&Token{}).Where("id = ?", 9002).Updates(map[string]interface{}{
				"unlimited_quota": true, "remain_quota": tc.remain, "used_quota": tc.used,
			}).Error)
			err := ReserveWalletBillingBudget(9001, 9002, 10, 0)
			if tc.fail {
				require.ErrorContains(t, err, "token quota overflow")
				requireWalletBillingBudgetBalances(t, db, 1000000, tc.remain, tc.used)
				return
			}
			require.NoError(t, err)
			requireWalletBillingBudgetBalances(t, db, 999990, -10, 10)
		})
	}
}

func TestWalletBillingBudgetCannotSpendSubscriptionWalletHolds(t *testing.T) {
	db := subscriptionBillingModelFixture(t, false)
	require.NoError(t, db.Model(&User{}).Where("id = ?", 9001).Update("quota", 100).Error)
	require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", 9101).Update("amount_used", 99970).Error)
	_, err := PreConsumeSubscriptionBilling("wallet-budget-pending", 9001, 9002, "model", 100, true)
	require.NoError(t, err)
	require.ErrorIs(t, ReserveWalletBillingBudget(9001, 9002, 31, 0), ErrWalletBillingBudgetQuota)
	requireWalletBillingBudgetBalances(t, db, 100, 999900, 100)
	require.NoError(t, ReserveWalletBillingBudget(9001, 9002, 30, 0))
	requireWalletBillingBudgetBalances(t, db, 70, 999870, 130)
	_, err = SettleSubscriptionBilling("wallet-budget-pending", 9001, 100)
	require.NoError(t, err)
	requireWalletBillingBudgetBalances(t, db, 0, 999870, 130)
}

func TestWalletBillingBudgetValidatesBoundsBeforeDatabaseAccess(t *testing.T) {
	db := subscriptionBillingModelFixture(t, false)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	for _, tc := range []struct {
		userID, tokenID, amount, minimum int
	}{
		{9001, 9002, -1, 0},
		{9001, 9002, 0, -1},
		{9001, 9002, common.MaxWalletQuota + 1, 0},
		{9001, 9002, 0, common.MaxWalletQuota + 1},
		{0, 9002, 1, 0},
		{9001, -1, 1, 0},
	} {
		err := ReserveWalletBillingBudget(tc.userID, tc.tokenID, tc.amount, tc.minimum)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "database is closed")
	}
	for _, tc := range []struct {
		userID, tokenID, amount int
	}{
		{9001, 9002, -1},
		{9001, 9002, common.MaxWalletQuota + 1},
		{0, 9002, 1},
		{9001, -1, 1},
	} {
		err := RefundWalletBillingBudget(tc.userID, tc.tokenID, tc.amount)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "database is closed")
	}
}

func TestWalletBillingBudgetRejectsOutOfRangeWalletAndPendingOverflow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		amount int
		setup  func(*testing.T, *gorm.DB)
	}{
		{name: "wallet", amount: 1, setup: func(t *testing.T, db *gorm.DB) {
			require.NoError(t, db.Model(&User{}).Where("id = ?", 9001).Update("quota", common.MaxWalletQuota+1).Error)
		}},
		{name: "zero-wallet", setup: func(t *testing.T, db *gorm.DB) {
			require.NoError(t, db.Model(&User{}).Where("id = ?", 9001).Update("quota", common.MaxWalletQuota+1).Error)
		}},
		{name: "pending", amount: 1, setup: func(t *testing.T, db *gorm.DB) {
			require.NoError(t, db.Create(&SubscriptionPreConsumeRecord{
				RequestId: "wallet-budget-overflow", UserId: 9001, UserSubscriptionId: 9101,
				BillingManaged: true, WalletOverflow: true, Status: "consumed", TokenConsumed: math.MaxInt64,
			}).Error)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := subscriptionBillingModelFixture(t, false)
			tc.setup(t, db)
			require.ErrorIs(t, ReserveWalletBillingBudget(9001, 9002, tc.amount, 0), ErrWalletQuotaOutOfRange)
			var token Token
			require.NoError(t, db.First(&token, 9002).Error)
			require.Equal(t, 1000000, token.RemainQuota)
			require.Zero(t, token.UsedQuota)
		})
	}
}

func TestWalletBillingBudgetZeroValidatesWithoutMutations(t *testing.T) {
	db := subscriptionBillingModelFixture(t, false)
	server := useUserCacheMiniRedis(t)
	initCol()
	var user User
	var token Token
	require.NoError(t, db.First(&user, 9001).Error)
	require.NoError(t, db.First(&token, 9002).Error)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, cacheSetToken(token))
	require.NoError(t, ReserveWalletBillingBudget(9001, 9002, 0, 1000000))
	require.NoError(t, RefundWalletBillingBudget(9001, 9002, 0))
	require.True(t, server.Exists(getUserCacheKey(9001)))
	require.True(t, server.Exists(getTokenCacheKey(token.Key)))
	requireWalletBillingBudgetBalances(t, db, 1000000, 1000000, 0)
	var after Token
	require.NoError(t, db.First(&after, 9002).Error)
	require.Equal(t, token.AccessedTime, after.AccessedTime)
	require.ErrorIs(t, ReserveWalletBillingBudget(9001, 9002, 0, 1000001), ErrWalletBillingBudgetQuota)
	require.ErrorIs(t, ReserveWalletBillingBudget(9001, 9999, 0, 0), gorm.ErrRecordNotFound)
	require.ErrorIs(t, RefundWalletBillingBudget(9001, 9999, 0), gorm.ErrRecordNotFound)
	require.ErrorIs(t, ReserveWalletBillingBudget(9999, 9002, 0, 0), gorm.ErrRecordNotFound)
	require.NoError(t, db.Model(&Token{}).Where("id = ?", 9002).Update("user_id", 9003).Error)
	require.ErrorIs(t, ReserveWalletBillingBudget(9001, 9002, 0, 0), gorm.ErrRecordNotFound)
	require.ErrorIs(t, RefundWalletBillingBudget(9001, 9002, 0), gorm.ErrRecordNotFound)
}

func TestWalletBillingBudgetInternalTokenIsCallerControlled(t *testing.T) {
	db := subscriptionBillingModelFixture(t, false)
	require.NoError(t, ReserveWalletBillingBudget(9001, 0, 10, 0))
	requireWalletBillingBudgetBalances(t, db, 999990, 1000000, 0)
	require.NoError(t, RefundWalletBillingBudget(9001, 0, 10))
	requireWalletBillingBudgetBalances(t, db, 1000000, 1000000, 0)
}

func TestWalletBillingBudgetDatabaseOverridesStaleRedis(t *testing.T) {
	db := subscriptionBillingModelFixture(t, false)
	server := useUserCacheMiniRedis(t)
	resetBatchUpdateTestState(t)
	initCol()
	var staleUser User
	var staleToken Token
	require.NoError(t, db.First(&staleUser, 9001).Error)
	require.NoError(t, db.First(&staleToken, 9002).Error)
	require.NoError(t, populateUserCache(staleUser))
	require.NoError(t, cacheSetToken(staleToken))
	require.NoError(t, db.Model(&User{}).Where("id = ?", 9001).Update("quota", 50).Error)
	require.NoError(t, db.Model(&Token{}).Where("id = ?", 9002).Updates(map[string]interface{}{
		"remain_quota": 40, "unlimited_quota": false,
	}).Error)
	staleToken.UnlimitedQuota = true
	require.NoError(t, cacheSetToken(staleToken))
	require.ErrorIs(t, ReserveWalletBillingBudget(9001, 9002, 51, 0), ErrWalletBillingBudgetQuota)
	require.ErrorIs(t, ReserveWalletBillingBudget(9001, 9002, 41, 0), ErrSubscriptionBillingTokenQuota)
	requireWalletBillingBudgetBalances(t, db, 50, 40, 0)
	require.True(t, server.Exists(getUserCacheKey(9001)), "failed transactions must not mutate caches")
	require.True(t, server.Exists(getTokenCacheKey(staleToken.Key)))
	require.NoError(t, ReserveWalletBillingBudget(9001, 9002, 40, 0))
	require.False(t, server.Exists(getUserCacheKey(9001)))
	require.False(t, server.Exists(getTokenCacheKey(staleToken.Key)))
	require.True(t, server.Exists(getTokenCacheFenceKey(staleToken.Key)))
	requireWalletBillingBudgetBalances(t, db, 10, 0, 40)
	// Even an explicit stale rehydration cannot authorize another durable debit.
	require.NoError(t, populateUserCache(staleUser))
	require.NoError(t, cacheSetToken(staleToken))
	require.ErrorIs(t, ReserveWalletBillingBudget(9001, 9002, 1, 0), ErrSubscriptionBillingTokenQuota)
	FlushBatchUpdates()
	requireWalletBillingBudgetBalances(t, db, 10, 0, 40)
}

func TestWalletBillingBudgetConcurrentReservations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		postgres bool
	}{
		{name: "sqlite"},
		{name: "postgres", postgres: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := subscriptionBillingModelFixture(t, tc.postgres)
			if !tc.postgres {
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
			}
			require.NoError(t, db.Model(&User{}).Where("id = ?", 9001).Update("quota", 100).Error)
			require.NoError(t, db.Model(&Token{}).Where("id = ?", 9002).Update("remain_quota", 100).Error)
			const workers = 12
			start := make(chan struct{})
			results := make(chan error, workers)
			var wg sync.WaitGroup
			for range workers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					results <- ReserveWalletBillingBudget(9001, 9002, 20, 0)
				}()
			}
			close(start)
			wg.Wait()
			close(results)
			succeeded := 0
			for err := range results {
				if err == nil {
					succeeded++
				} else {
					require.ErrorIs(t, err, ErrWalletBillingBudgetQuota)
				}
			}
			require.Equal(t, 5, succeeded)
			requireWalletBillingBudgetBalances(t, db, 0, 0, 100)
		})
	}
}

func TestWalletBillingBudgetRefundPairsIncludingDeletedToken(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		name := "active"
		if deleted {
			name = "deleted"
		}
		t.Run(name, func(t *testing.T) {
			db := subscriptionBillingModelFixture(t, false)
			useUserCacheMiniRedis(t)
			resetBatchUpdateTestState(t)
			initCol()
			require.NoError(t, ReserveWalletBillingBudget(9001, 9002, 60, 0))
			var user User
			var token Token
			require.NoError(t, db.First(&user, 9001).Error)
			require.NoError(t, db.First(&token, 9002).Error)
			require.NoError(t, populateUserCache(user))
			require.NoError(t, cacheSetToken(token))
			if deleted {
				require.NoError(t, db.Delete(&Token{}, 9002).Error)
			}
			require.NoError(t, RefundWalletBillingBudget(9001, 9002, 50))
			requireWalletBillingBudgetBalances(t, db, 999990, 999990, 10)
			FlushBatchUpdates()
			requireWalletBillingBudgetBalances(t, db, 999990, 999990, 10)
			var cachedUser UserBase
			require.Error(t, common.RedisHGetObj(getUserCacheKey(user.Id), &cachedUser))
			_, err := cacheGetTokenByKey(token.Key)
			require.Error(t, err)
		})
	}
}

func TestWalletBillingBudgetRefundTokenFailureRollsBackWallet(t *testing.T) {
	db := subscriptionBillingModelFixture(t, false)
	require.NoError(t, ReserveWalletBillingBudget(9001, 9002, 60, 0))
	require.NoError(t, db.Model(&Token{}).Where("id = ?", 9002).Update("user_id", 9003).Error)
	require.ErrorIs(t, RefundWalletBillingBudget(9001, 9002, 50), gorm.ErrRecordNotFound)
	requireWalletBillingBudgetBalances(t, db, 999940, 999940, 60)
}

func TestWalletBillingBudgetRefundOverflowRollsBackBoth(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*testing.T, *gorm.DB)
		wallet int
		remain int
		used   int
		want   error
	}{
		{name: "wallet", wallet: common.MaxWalletQuota, remain: 1000000, setup: func(t *testing.T, db *gorm.DB) {
			require.NoError(t, db.Model(&User{}).Where("id = ?", 9001).Update("quota", common.MaxWalletQuota).Error)
		}, want: ErrWalletQuotaOutOfRange},
		{name: "token", wallet: 1000000, remain: math.MaxInt64, setup: func(t *testing.T, db *gorm.DB) {
			require.NoError(t, db.Model(&Token{}).Where("id = ?", 9002).Update("remain_quota", math.MaxInt64).Error)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := subscriptionBillingModelFixture(t, false)
			tc.setup(t, db)
			err := RefundWalletBillingBudget(9001, 9002, 1)
			if tc.want != nil {
				require.True(t, errors.Is(err, tc.want), "%v", err)
			} else {
				require.ErrorContains(t, err, "token quota overflow")
			}
			requireWalletBillingBudgetBalances(t, db, tc.wallet, tc.remain, tc.used)
		})
	}
}
