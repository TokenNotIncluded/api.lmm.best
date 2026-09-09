package model

import (
	"context"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTryReserveUserQuotaWithMinimumStartingBalance(t *testing.T) {
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })

	for _, tc := range []struct {
		name     string
		balance  int
		amount   int
		minimum  int
		reserved bool
	}{
		{"below_floor", 99, 10, 100, false},
		{"exact_floor", 100, 10, 100, true},
		{"price_exceeds_balance", 100, 101, 100, false},
		{"no_minimum", 99, 99, 0, true},
		{"zero_amount_at_floor", 100, 0, 100, true},
		{"zero_amount_below_floor", 99, 0, 100, false},
		{"zero_amount_without_floor_in_debt", -1, 0, 0, true},
		{"bounded_maximum", common.MaxWalletQuota, common.MaxWalletQuota, common.MaxWalletQuota, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			user := User{Username: "quota-minimum-" + tc.name, Password: "password", Quota: tc.balance}
			require.NoError(t, DB.Create(&user).Error)

			reserved, err := TryReserveUserQuotaWithMinimum(user.Id, tc.amount, tc.minimum)
			require.NoError(t, err)
			require.Equal(t, tc.reserved, reserved)
			want := tc.balance
			if tc.reserved {
				want -= tc.amount
			}
			balance, err := currentWalletQuota(DB, user.Id)
			require.NoError(t, err)
			require.Equal(t, want, balance)
			if tc.reserved && tc.minimum > 0 && want < tc.minimum {
				reserved, err = TryReserveUserQuotaWithMinimum(user.Id, 1, tc.minimum)
				require.NoError(t, err)
				require.False(t, reserved, "a new request must meet the starting floor again")
				balance, err = currentWalletQuota(DB, user.Id)
				require.NoError(t, err)
				require.Equal(t, want, balance)
			}
		})
	}
}

func TestTryReserveUserQuotaWithMinimumConcurrentReservations(t *testing.T) {
	truncateTables(t)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	user := User{Username: "quota-minimum-concurrent", Password: "password", Quota: 100}
	require.NoError(t, DB.Create(&user).Error)

	const requests = 12
	type outcome struct {
		reserved bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan outcome, requests)
	for i := 0; i < requests; i++ {
		go func() {
			<-start
			reserved, err := TryReserveUserQuotaWithMinimum(user.Id, 1, 100)
			results <- outcome{reserved: reserved, err: err}
		}()
	}
	close(start)
	successes := 0
	for i := 0; i < requests; i++ {
		result := <-results
		require.NoError(t, result.err)
		if result.reserved {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	balance, err := currentWalletQuota(DB, user.Id)
	require.NoError(t, err)
	require.Equal(t, 99, balance)
}

func TestTryReserveUserQuotaWithMinimumRejectsStaleHighCache(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	user := User{Username: "quota-minimum-stale-cache", Password: "password", Quota: 99}
	require.NoError(t, DB.Create(&user).Error)
	stale := user.ToBaseUser()
	stale.Quota = 200
	require.NoError(t, writeUserCache(stale, true))

	reserved, err := TryReserveUserQuotaWithMinimum(user.Id, 10, 100)
	require.NoError(t, err)
	require.False(t, reserved)
	balance, err := currentWalletQuota(DB, user.Id)
	require.NoError(t, err)
	require.Equal(t, 99, balance)
	exists, err := common.RDB.Exists(context.Background(), getUserCacheKey(user.Id)).Result()
	require.NoError(t, err)
	require.Zero(t, exists)
}

func TestTryReserveUserQuotaWithMinimumValidatesInputs(t *testing.T) {
	truncateTables(t)
	user := User{Username: "quota-minimum-validation", Password: "password", Quota: 100}
	require.NoError(t, DB.Create(&user).Error)
	for _, tc := range []struct {
		name    string
		amount  int
		minimum int
	}{
		{"negative_amount", -1, 0},
		{"negative_minimum", 0, -1},
		{"unbounded_amount", common.MaxWalletQuota + 1, 0},
		{"unbounded_minimum", 0, common.MaxWalletQuota + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reserved, err := TryReserveUserQuotaWithMinimum(user.Id, tc.amount, tc.minimum)
			require.Error(t, err)
			require.False(t, reserved)
		})
	}
	balance, err := currentWalletQuota(DB, user.Id)
	require.NoError(t, err)
	require.Equal(t, 100, balance)

	for _, amount := range []int{0, 1} {
		reserved, err := TryReserveUserQuotaWithMinimum(987654, amount, 100)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		require.False(t, reserved)
	}
}
