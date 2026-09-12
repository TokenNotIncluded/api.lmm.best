package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func subscriptionBillingFixture(t *testing.T, total int64, allow bool, preference string) (*gorm.DB, *relaycommon.RelayInfo, *gin.Context) {
	t.Helper()
	db := setupBillingSessionWalletCacheTest(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))
	previousBatch := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.BatchUpdateEnabled = previousBatch })
	user := model.User{Username: "subscription-billing", Quota: 1000000}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "subscription-billing-token", RemainQuota: 1000000}
	require.NoError(t, db.Create(&token).Error)
	plan := model.SubscriptionPlan{Title: t.Name(), DurationUnit: model.SubscriptionDurationDay, DurationValue: 1, QuotaResetPeriod: model.SubscriptionResetNever}
	require.NoError(t, db.Create(&plan).Error)
	require.NoError(t, db.Create(&model.UserSubscription{UserId: user.Id, PlanId: plan.Id, AmountTotal: total, Status: "active", StartTime: time.Now().Unix() - 10, EndTime: time.Now().Unix() + 3600, AllowWalletOverflow: allow}).Error)
	cacheWalletQuotaForBillingTest(t, user.Id, user.Quota)
	cacheBillingTokenForTest(t, token)
	info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: "subscription-request", ForcePreConsume: true, UserSetting: dto.UserSetting{BillingPreference: preference}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return db, info, c
}

func assertSubscriptionBillingBalances(t *testing.T, db *gorm.DB, info *relaycommon.RelayInfo, subQuota int64, walletQuota, tokenQuota int) {
	t.Helper()
	var sub model.UserSubscription
	require.NoError(t, db.First(&sub, info.SubscriptionId).Error)
	require.Equal(t, subQuota, sub.AmountUsed)
	var user model.User
	require.NoError(t, db.First(&user, info.UserId).Error)
	require.Equal(t, 1000000-walletQuota, user.Quota)
	var token model.Token
	require.NoError(t, db.First(&token, info.TokenId).Error)
	require.Equal(t, 1000000-tokenQuota, token.RemainQuota)
	require.Equal(t, tokenQuota, token.UsedQuota)
}

func TestSubscriptionBillingSettlement(t *testing.T) {
	for _, tc := range []struct {
		name, pref            string
		total                 int64
		allow, strict, denied bool
		reserve, actual       int
		sub                   int64
		wallet                int
	}{
		{"overflow", "subscription_first", 100000, true, false, false, 0, 160000, 100000, 60000},
		{"only", "subscription_only", 100000, true, false, true, 0, 160000, 60000, 0},
		{"denied", "subscription_first", 100000, false, false, true, 0, 160000, 60000, 0},
		{"other_strict", "subscription_first", 100000, true, true, true, 0, 160000, 60000, 0},
		{"no_overflow", "subscription_first", 100000, true, false, false, 0, 80000, 80000, 0},
		{"refund_delta", "subscription_first", 100000, true, false, false, 0, 20000, 20000, 0},
		{"zero", "subscription_first", 100000, true, false, false, 0, 0, 0, 0},
		{"equal", "subscription_first", 100000, true, false, false, 0, 60000, 60000, 0},
		{"unlimited", "subscription_first", 0, false, false, false, 0, 160000, 160000, 0},
		{"reserved_overflow", "subscription_first", 100000, true, false, false, 90000, 160000, 100000, 60000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, info, c := subscriptionBillingFixture(t, tc.total, tc.allow, tc.pref)
			// Subscription transactions must remain durable with batching enabled.
			common.BatchUpdateEnabled = true
			session, apiErr := NewBillingSession(c, info, 60000)
			require.Nil(t, apiErr)
			if tc.strict {
				require.NoError(t, db.Create(&model.UserSubscription{UserId: info.UserId, Status: "active", EndTime: time.Now().Unix() + 7200, AllowWalletOverflow: false}).Error)
			}
			if tc.reserve > 0 {
				require.NoError(t, session.Reserve(tc.reserve))
			}
			err := session.Settle(tc.actual)
			if tc.denied {
				require.ErrorIs(t, err, model.ErrSubscriptionQuotaInsufficient)
				require.False(t, session.NeedsRefund())
				session.Refund(c)
				require.ErrorIs(t, session.Settle(tc.actual), model.ErrSubscriptionQuotaInsufficient)
				assertSubscriptionBillingBalances(t, db, info, tc.sub, tc.wallet, 60000)
				r := session.SubscriptionSettlement()
				require.Equal(t, "settling", r.Status)
				require.EqualValues(t, tc.actual, r.ActualQuota)
				require.EqualValues(t, 60000, r.SubscriptionQuota)
				return
			}
			require.NoError(t, err)
			require.NoError(t, session.Settle(tc.actual))
			// Another session/process can repeat settlement without another debit.
			_, err = model.SettleSubscriptionBilling(info.RequestId, info.UserId, int64(tc.actual))
			require.NoError(t, err)
			require.Error(t, session.Settle(tc.actual+1))
			session.Refund(c)
			assertSubscriptionBillingBalances(t, db, info, tc.sub, tc.wallet, tc.actual)
			require.EqualValues(t, tc.sub-int64(session.GetPreConsumedQuota()), info.SubscriptionPostDelta)
			r := session.SubscriptionSettlement()
			require.Equal(t, "settled", r.Status)
			require.EqualValues(t, tc.wallet, r.WalletQuota)
			cacheExists, err := common.RDB.Exists(context.Background(), "token:"+common.GenerateHMAC(info.TokenKey)).Result()
			require.NoError(t, err)
			require.Zero(t, cacheExists, "stale token snapshot must be evicted after commit")
			cachedUser, err := model.GetUserCache(info.UserId)
			require.NoError(t, err)
			require.Equal(t, 1000000-tc.wallet, cachedUser.Quota)
		})
	}
}

func TestSubscriptionBillingAtomicFailureAndRetry(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "subscription_first")
	session, apiErr := NewBillingSession(c, info, 60000)
	require.Nil(t, apiErr)
	// Fail the final token update after subscription and wallet SQL executed.
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("fail_billing_token", func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("injected token write failure"))
		}
	}))
	require.ErrorContains(t, session.Settle(160000), "injected token write failure")
	assertSubscriptionBillingBalances(t, db, info, 60000, 0, 60000)
	require.False(t, session.settled)
	require.Error(t, model.RefundSubscriptionPreConsume(info.RequestId))
	result, err := model.GetSubscriptionBillingResult(info.RequestId, info.UserId)
	require.NoError(t, err)
	require.Equal(t, "settling", result.Status)
	require.EqualValues(t, 160000, result.ActualQuota)
	require.NoError(t, db.Callback().Update().Remove("fail_billing_token"))
	require.NoError(t, session.Settle(160000))
	assertSubscriptionBillingBalances(t, db, info, 100000, 60000, 160000)
}

func TestSubscriptionBillingReserveRefundAndReplay(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "subscription_first")
	session, apiErr := NewBillingSession(c, info, 60000)
	require.Nil(t, apiErr)
	require.NoError(t, session.Reserve(90000))
	require.NoError(t, session.Reserve(90000))
	require.Error(t, session.Reserve(110000))
	assertSubscriptionBillingBalances(t, db, info, 90000, 0, 90000)
	copyInfo := *info
	replayed, apiErr := NewBillingSession(c, &copyInfo, 60000)
	require.Nil(t, apiErr)
	require.Equal(t, 90000, replayed.GetPreConsumedQuota())
	assertSubscriptionBillingBalances(t, db, info, 90000, 0, 90000)
	session.Refund(c)
	replayed.Refund(c)
	require.NoError(t, model.RefundSubscriptionPreConsume(info.RequestId))
	assertSubscriptionBillingBalances(t, db, info, 0, 0, 0)
	require.Error(t, session.Settle(100000))
	_, apiErr = NewBillingSession(c, &copyInfo, 60000)
	require.NotNil(t, apiErr)
}

func TestSubscriptionBillingPreconsumeFallback(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100, true, "subscription_first")
	session, apiErr := NewBillingSession(c, info, 60000)
	require.Nil(t, apiErr)
	require.Equal(t, BillingSourceWallet, session.funding.Source())
	var token model.Token
	require.NoError(t, db.First(&token, info.TokenId).Error)
	require.Equal(t, 60000, token.UsedQuota)
	var count int64
	require.NoError(t, db.Model(&model.SubscriptionPreConsumeRecord{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestSubscriptionBillingReserveTokenFailureRollsBack(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "subscription_first")
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", info.TokenId).Update("remain_quota", 60000).Error)
	session, apiErr := NewBillingSession(c, info, 60000)
	require.Nil(t, apiErr)
	require.Error(t, session.Reserve(90000))
	var sub model.UserSubscription
	require.NoError(t, db.First(&sub, info.SubscriptionId).Error)
	require.EqualValues(t, 60000, sub.AmountUsed)
	result, err := model.GetSubscriptionBillingResult(info.RequestId, info.UserId)
	require.NoError(t, err)
	require.EqualValues(t, 60000, result.ReservedQuota)
	session.Refund(c)
	var token model.Token
	require.NoError(t, db.First(&token, info.TokenId).Error)
	require.Equal(t, 60000, token.RemainQuota)
	require.Zero(t, token.UsedQuota)
}

func TestSubscriptionBillingTaskDoesNotCreateUnrefundableWalletSplit(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "subscription_first")
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{}
	session, apiErr := NewBillingSession(c, info, 60000)
	require.Nil(t, apiErr)
	require.ErrorIs(t, session.Settle(160000), model.ErrSubscriptionQuotaInsufficient)
	assertSubscriptionBillingBalances(t, db, info, 60000, 0, 60000)
}
