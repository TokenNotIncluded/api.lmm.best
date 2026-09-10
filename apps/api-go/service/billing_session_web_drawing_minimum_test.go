package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWalletFundingWebDrawingMinimum(t *testing.T) {
	for _, tc := range []struct {
		name    string
		balance int
		amount  int
		minimum int
		wantErr error
	}{
		{"below_floor", 99, 10, 100, ErrWebDrawingMinimumBalance},
		{"exact_floor", 100, 10, 100, nil},
		{"expensive_price", 100, 101, 100, ErrInsufficientWalletQuota},
		{"no_minimum", 99, 99, 0, nil},
		{"zero_amount_below_floor", 99, 0, 100, ErrWebDrawingMinimumBalance},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupBillingSessionWalletCacheTest(t)
			user := model.User{Username: "drawing-minimum-" + tc.name, Password: "password", Quota: tc.balance}
			require.NoError(t, db.Create(&user).Error)
			funding := &WalletFunding{userId: user.Id, minimumQuota: tc.minimum}
			err := funding.PreConsume(tc.amount)
			want := tc.balance
			if tc.wantErr == nil {
				require.NoError(t, err)
				require.Equal(t, tc.amount, funding.consumed)
				want -= tc.amount
			} else {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, funding.consumed)
			}
			var stored model.User
			require.NoError(t, db.First(&stored, user.Id).Error)
			require.Equal(t, want, stored.Quota)
		})
	}
}

func TestNewBillingSessionWebDrawingMinimumBeforeFundingPreference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, preference := range []string{"wallet_only", "wallet_first", "subscription_only", "subscription_first"} {
		t.Run(preference, func(t *testing.T) {
			db := setupBillingSessionWalletCacheTest(t)
			user := model.User{Username: "drawing-minimum-preference", Password: "password", Quota: 99}
			require.NoError(t, db.Create(&user).Error)
			cacheWalletQuotaForBillingTest(t, user.Id, 200)
			info := walletBillingRelayInfo(user.Id)
			info.UserSetting.BillingPreference = preference
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set(string(constant.ContextKeyWebDrawingMinimumQuota), 100)

			session, apiErr := NewBillingSession(ctx, info, 10)
			require.Nil(t, session)
			require.NotNil(t, apiErr)
			require.Equal(t, http.StatusForbidden, apiErr.StatusCode)
			require.Equal(t, types.ErrorCode("WEB_DRAWING_MINIMUM_BALANCE"), apiErr.GetErrorCode())
			require.ErrorIs(t, apiErr, ErrWebDrawingMinimumBalance)
			var stored model.User
			require.NoError(t, db.First(&stored, user.Id).Error)
			require.Equal(t, 99, stored.Quota)
		})
	}
}

func TestNewBillingSessionWebDrawingMinimumFundingClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		balance int
		amount  int
		minimum int
		wantErr types.ErrorCode
	}{
		{"exact_floor", 100, 10, 100, ""},
		{"expensive_price", 100, 101, 100, types.ErrorCodeInsufficientUserQuota},
		{"api_mcp_without_floor", 99, 10, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupBillingSessionWalletCacheTest(t)
			user := model.User{Username: "drawing-minimum-classification", Password: "password", Quota: tc.balance}
			require.NoError(t, db.Create(&user).Error)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			if tc.minimum > 0 {
				ctx.Set(string(constant.ContextKeyWebDrawingMinimumQuota), tc.minimum)
			}
			session, apiErr := NewBillingSession(ctx, walletBillingRelayInfo(user.Id), tc.amount)
			want := tc.balance
			if tc.wantErr == "" {
				require.Nil(t, apiErr)
				require.NotNil(t, session)
				require.Equal(t, tc.amount, session.GetPreConsumedQuota())
				want -= tc.amount
			} else {
				require.Nil(t, session)
				require.NotNil(t, apiErr)
				require.Equal(t, tc.wantErr, apiErr.GetErrorCode())
			}
			var stored model.User
			require.NoError(t, db.First(&stored, user.Id).Error)
			require.Equal(t, want, stored.Quota)
		})
	}
}

func TestNewBillingSessionWebDrawingMinimumLateDropRefundsTokenWithoutFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupBillingSessionWalletCacheTest(t)
	previousBatch := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.BatchUpdateEnabled = previousBatch })
	user := model.User{Username: "drawing-minimum-late-drop", Password: "password", Quota: 100}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "drawing-minimum-late-drop-key", Name: "drawing", RemainQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	cacheBillingTokenForTest(t, token)
	cacheWalletQuotaForBillingTest(t, user.Id, 100)
	info := walletBillingRelayInfo(user.Id)
	info.UserSetting.BillingPreference = "wallet_first"
	info.IsPlayground = false
	info.TokenUnlimited = false
	info.TokenId = token.Id
	info.TokenKey = token.Key
	preflightBalance, err := model.GetUserQuota(user.Id, true)
	require.NoError(t, err)
	require.Equal(t, 100, preflightBalance)

	// Simulate another request spending after all browser/session preflights
	// and token reservation, immediately before the guarded wallet UPDATE.
	dropped := false
	observedTokenQuota := 0
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:web_drawing_minimum_drop", func(tx *gorm.DB) {
		if dropped || tx.Statement.Table != "users" {
			return
		}
		dropped = true
		if err := tx.Session(&gorm.Session{NewDB: true}).Model(&model.Token{}).
			Where("id = ?", token.Id).Select("remain_quota").Take(&observedTokenQuota).Error; err != nil {
			tx.AddError(err)
			return
		}
		tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Exec("UPDATE users SET quota = ? WHERE id = ?", 99, user.Id).Error)
	}))
	subscriptionQueries := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:web_drawing_minimum_no_fallback", func(tx *gorm.DB) {
		if tx.Statement.Table == "user_subscriptions" {
			subscriptionQueries++
		}
	}))
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(string(constant.ContextKeyWebDrawingMinimumQuota), 100)
	session, apiErr := NewBillingSession(ctx, info, 10)

	require.True(t, dropped)
	require.Equal(t, 90, observedTokenQuota, "the token must have been reserved before the wallet rejection")
	require.Nil(t, session)
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusForbidden, apiErr.StatusCode)
	require.Equal(t, types.ErrorCode("WEB_DRAWING_MINIMUM_BALANCE"), apiErr.GetErrorCode())
	require.ErrorIs(t, apiErr, ErrWebDrawingMinimumBalance)
	require.Contains(t, apiErr.Error(), "starting wallet balance of at least USD 10")
	require.Zero(t, subscriptionQueries, "minimum denial must not try subscription funding")
	var storedUser model.User
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.Equal(t, 99, storedUser.Quota, "only the simulated prior request may debit the wallet")
	var storedToken model.Token
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	require.Equal(t, 100, storedToken.RemainQuota)
	require.Zero(t, storedToken.UsedQuota)
	cacheKey := fmt.Sprintf("token:%s", common.GenerateHMAC(token.Key))
	require.Eventually(t, func() bool {
		remaining, err := common.RDB.HGet(context.Background(), cacheKey, "RemainQuota").Int()
		return err == nil && remaining == 100
	}, time.Second, 10*time.Millisecond)
}
