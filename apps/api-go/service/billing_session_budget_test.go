package service

import (
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func assertWalletBudgetBalances(t *testing.T, db *gorm.DB, info *relaycommon.RelayInfo, wallet, token, used int) {
	t.Helper()
	var user model.User
	require.NoError(t, db.First(&user, info.UserId).Error)
	require.Equal(t, wallet, user.Quota)
	var key model.Token
	require.NoError(t, db.First(&key, info.TokenId).Error)
	require.Equal(t, token, key.RemainQuota)
	require.Equal(t, used, key.UsedQuota)
}

func TestBillingBudgetWalletGrowthIsGuardedAndAtomic(t *testing.T) {
	for _, deniedBy := range []string{"wallet", "token", "stale_unlimited_flag"} {
		t.Run(deniedBy, func(t *testing.T) {
			db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", info.UserId).Update("quota", 100).Error)
			tokenQuota := 100
			if deniedBy != "wallet" {
				tokenQuota = 70
			}
			require.NoError(t, db.Model(&model.Token{}).Where("id = ?", info.TokenId).Update("remain_quota", tokenQuota).Error)
			if deniedBy == "stale_unlimited_flag" {
				info.TokenUnlimited = true
			}
			session, apiErr := NewBudgetBillingSession(c, info, 30)
			require.Nil(t, apiErr)
			require.NoError(t, session.Reserve(60))
			assertWalletBudgetBalances(t, db, info, 40, tokenQuota-60, 60)
			target := 120
			if deniedBy != "wallet" {
				target = 80 // Wallet covers it; the actual DB token limit does not.
			}
			require.Error(t, session.Reserve(target))
			assertWalletBudgetBalances(t, db, info, 40, tokenQuota-60, 60)
			require.Equal(t, 60, session.GetReservedBudget())
			require.Equal(t, 60, session.GetPreConsumedQuota())
			session.Refund(c)
			session.Refund(c)
			assertWalletBudgetBalances(t, db, info, 100, tokenQuota, 0)
			require.Error(t, session.Reserve(1))
		})
	}
}

func TestBillingBudgetInitialFailureLeavesBothBalancesUntouched(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", info.TokenId).Update("remain_quota", 10).Error)
	session, apiErr := NewBudgetBillingSession(c, info, 30)
	require.Nil(t, session)
	require.NotNil(t, apiErr)
	require.ErrorIs(t, apiErr, model.ErrSubscriptionBillingTokenQuota)
	assertWalletBudgetBalances(t, db, info, 1000000, 10, 0)
}

func TestBillingBudgetInternalWalletAdmissionRemainsRefundable(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	info.IsPlayground = true
	session, apiErr := NewBudgetBillingSession(c, info, 30)
	require.Nil(t, apiErr)
	require.True(t, session.NeedsRefund())
	require.NoError(t, session.Reserve(50))
	assertWalletBudgetBalances(t, db, info, 1000000-50, 1000000, 0)
	session.Refund(c)
	require.False(t, session.NeedsRefund())
	assertWalletBudgetBalances(t, db, info, 1000000, 1000000, 0)
}

func TestBillingBudgetFinalSettlementAccountsForServedTail(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", info.UserId).Update("quota", 100).Error)
	session, apiErr := NewBudgetBillingSession(c, info, 30)
	require.Nil(t, apiErr)
	require.NoError(t, session.Reserve(80))
	require.Error(t, session.Reserve(120))
	// The provider has already served a trailing report. Budget rejection
	// stops future work; final settlement must not discard this observed cost.
	require.NoError(t, session.Settle(105))
	require.NoError(t, session.Settle(105))
	session.Refund(c)
	assertWalletBudgetBalances(t, db, info, -5, 1000000-105, 105)
	require.Error(t, session.Reserve(110))
}

func TestBillingBudgetInvalidTargetsAndUsageDoNotMutateOrClose(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	session, apiErr := NewBudgetBillingSession(c, info, 30)
	require.Nil(t, apiErr)
	for _, invalid := range []int{-1, common.MaxWalletQuota + 1} {
		require.Error(t, session.Reserve(invalid))
		require.Error(t, session.Settle(invalid))
		assertWalletBudgetBalances(t, db, info, 1000000-30, 1000000-30, 30)
		require.Equal(t, 30, session.GetReservedBudget())
	}
	require.NoError(t, session.Reserve(40))
	require.NoError(t, session.Settle(35))
	assertWalletBudgetBalances(t, db, info, 1000000-35, 1000000-35, 35)
	require.Error(t, session.Reserve(41))
	for _, invalid := range []int{-1, common.MaxWalletQuota + 1} {
		bad, apiErr := NewBudgetBillingSession(c, info, invalid)
		require.Nil(t, bad)
		require.NotNil(t, apiErr)
	}
}

func TestBillingBudgetFailedSettlementClosesGrowthAndRefundButCanRetry(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	session, apiErr := NewBudgetBillingSession(c, info, 30)
	require.Nil(t, apiErr)
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_budget_settlement BEFORE UPDATE OF quota ON users BEGIN SELECT RAISE(ABORT, 'wallet unavailable'); END`).Error)
	require.Error(t, session.Settle(35))
	require.Error(t, session.Reserve(40))
	session.Refund(c)
	assertWalletBudgetBalances(t, db, info, 1000000-30, 1000000-30, 30)
	require.NoError(t, db.Exec(`DROP TRIGGER reject_budget_settlement`).Error)
	require.NoError(t, session.Settle(35))
	assertWalletBudgetBalances(t, db, info, 1000000-35, 1000000-35, 35)
}

func TestBillingBudgetSubscriptionIncludesPendingWalletBudget(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100, true, "subscription_first")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", info.UserId).Update("quota", 50).Error)
	session, apiErr := NewBudgetBillingSession(c, info, 120)
	require.Nil(t, apiErr)
	require.Equal(t, 100, session.GetPreConsumedQuota())
	require.Equal(t, 120, session.GetReservedBudget())
	require.NoError(t, session.Reserve(150))
	require.Equal(t, 150, session.GetReservedBudget())
	require.Error(t, session.Reserve(151))
	require.Equal(t, 150, session.GetReservedBudget())
	var token model.Token
	require.NoError(t, db.First(&token, info.TokenId).Error)
	require.Equal(t, 150, token.UsedQuota)
	require.NoError(t, session.Settle(140))
	var user model.User
	require.NoError(t, db.First(&user, info.UserId).Error)
	require.Equal(t, 10, user.Quota)
	require.NoError(t, db.First(&token, info.TokenId).Error)
	require.Equal(t, 140, token.UsedQuota)
}

func TestBillingBudgetSubscriptionFailureRollsBackGrantAndToken(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100, true, "subscription_first")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", info.UserId).Update("quota", 20).Error)
	session, apiErr := NewBudgetBillingSession(c, info, 50)
	require.Nil(t, apiErr)
	require.Error(t, session.Reserve(121))
	require.Equal(t, 50, session.GetReservedBudget())
	var sub model.UserSubscription
	require.NoError(t, db.First(&sub, info.SubscriptionId).Error)
	require.EqualValues(t, 50, sub.AmountUsed)
	assertWalletBudgetBalances(t, db, info, 20, 1000000-50, 50)
	session.Refund(c)
	session.Refund(c)
	require.NoError(t, db.First(&sub, info.SubscriptionId).Error)
	require.Zero(t, sub.AmountUsed)
	assertWalletBudgetBalances(t, db, info, 20, 1000000, 0)
}

func TestBillingBudgetSubscriptionReplayCannotClaimUnreservedCapacity(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100, true, "subscription_first")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", info.UserId).Update("quota", 20).Error)
	original, apiErr := NewBudgetBillingSession(c, info, 50)
	require.Nil(t, apiErr)
	replayed, apiErr := NewBudgetBillingSession(c, info, 150)
	require.Nil(t, replayed)
	require.NotNil(t, apiErr)
	require.Equal(t, 50, original.GetReservedBudget())
	assertWalletBudgetBalances(t, db, info, 20, 1000000-50, 50)
	require.Error(t, original.Reserve(150))
	require.Equal(t, 50, original.GetReservedBudget())
	require.NoError(t, original.Reserve(120))
	require.Equal(t, 120, original.GetReservedBudget())
	original.Refund(c)
	assertWalletBudgetBalances(t, db, info, 20, 1000000, 0)
}

func TestBillingBudgetFreeZeroDoesNotCreateSubscriptionHold(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "subscription_first")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", info.UserId).Update("quota", 0).Error)
	info.PriceData.FreeModel = true
	session, apiErr := NewBudgetBillingSession(c, info, 0)
	require.Nil(t, apiErr)
	require.Zero(t, session.GetReservedBudget())
	require.NoError(t, session.Reserve(0))
	require.NoError(t, session.Settle(0))
	assertWalletBudgetBalances(t, db, info, 0, 1000000, 0)
	var count int64
	require.NoError(t, db.Model(&model.SubscriptionPreConsumeRecord{}).Count(&count).Error)
	require.Zero(t, count)
	info.PriceData.FreeModel = false
	info.UserSetting.BillingPreference = "wallet_only"
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", info.UserId).Update("quota", 1).Error)
	require.NoError(t, model.InvalidateUserCache(info.UserId))
	paid, apiErr := NewBudgetBillingSession(c, info, 0)
	require.Nil(t, apiErr)
	require.Equal(t, 1, paid.GetReservedBudget())
	paid.Refund(c)
	assertWalletBudgetBalances(t, db, info, 1, 1000000, 0)
}

func TestBillingBudgetConcurrentGrowthDoesNotDoubleReserve(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", info.UserId).Update("quota", 100).Error)
	session, apiErr := NewBudgetBillingSession(c, info, 30)
	require.Nil(t, apiErr)
	var workers sync.WaitGroup
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results <- session.Reserve(90)
		}()
	}
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.Equal(t, 90, session.GetReservedBudget())
	assertWalletBudgetBalances(t, db, info, 10, 1000000-90, 90)
	session.Refund(c)
	assertWalletBudgetBalances(t, db, info, 100, 1000000, 0)
}

func TestBillingBudgetDoesNotChangeOrdinaryReserve(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", info.UserId).Update("quota", 100).Error)
	info.UserSetting = dto.UserSetting{BillingPreference: "wallet_only"}
	ordinary, apiErr := NewBillingSession(c, info, 30)
	require.Nil(t, apiErr)
	require.NoError(t, ordinary.Reserve(120))
	assertWalletBudgetBalances(t, db, info, -20, 1000000-120, 120)
	ordinary.Refund(c)
}
