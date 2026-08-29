package service

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBillingSessionWalletCacheTest(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousRedis := common.RDB
	previousRedisEnabled := common.RedisEnabled

	server := miniredis.RunT(t)
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled = true

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}))

	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RDB = previousRedis
		common.RedisEnabled = previousRedisEnabled
		model.DB = previousDB
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func cacheWalletQuotaForBillingTest(t *testing.T, userID int, quota int) {
	t.Helper()
	_, err := model.GetUserCache(userID)
	require.NoError(t, err)
	err = common.RDB.HSet(context.Background(), fmt.Sprintf("user:%d", userID), "Quota", quota).Err()
	require.NoError(t, err)
}

func cacheBillingTokenForTest(t *testing.T, token model.Token) {
	t.Helper()
	key := fmt.Sprintf("token:%s", common.GenerateHMAC(token.Key))
	err := common.RDB.HSet(context.Background(), key,
		"Id", token.Id,
		"UserId", token.UserId,
		"Status", token.Status,
		"RemainQuota", token.RemainQuota,
		"UsedQuota", token.UsedQuota,
		"UnlimitedQuota", token.UnlimitedQuota,
	).Err()
	require.NoError(t, err)
}

func walletBillingRelayInfo(userID int) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:         userID,
		TokenUnlimited: true,
		IsPlayground:   true,
		UserSetting: dto.UserSetting{
			BillingPreference: "wallet_only",
		},
	}
}

func TestPreConsumeBillingPromotesUnverifiedZeroEstimate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupBillingSessionWalletCacheTest(t)
	user := model.User{
		Username: "billing-zero-estimate",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    1,
	}
	require.NoError(t, db.Create(&user).Error)
	info := walletBillingRelayInfo(user.Id)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	apiErr := PreConsumeBilling(ctx, 0, info)

	require.Nil(t, apiErr)
	require.NotNil(t, info.Billing)
	require.Equal(t, 1, info.Billing.GetPreConsumedQuota())
	require.Equal(t, 1, info.PriceData.QuotaToPreConsume)

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Zero(t, stored.Quota)
}

func TestPreConsumeBillingAllowsVerifiedFreeModelZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupBillingSessionWalletCacheTest(t)
	user := model.User{
		Username: "billing-verified-free",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    1,
	}
	require.NoError(t, db.Create(&user).Error)
	info := walletBillingRelayInfo(user.Id)
	info.PriceData.FreeModel = true

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	apiErr := PreConsumeBilling(ctx, 0, info)

	require.Nil(t, apiErr)
	require.NotNil(t, info.Billing)
	require.Zero(t, info.Billing.GetPreConsumedQuota())

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 1, stored.Quota)
}

func TestNewBillingSessionRejectsStaleHighWalletCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupBillingSessionWalletCacheTest(t)
	user := model.User{
		Username: "billing-stale-high",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    0,
	}
	require.NoError(t, db.Create(&user).Error)
	cacheWalletQuotaForBillingTest(t, user.Id, common.GetTrustQuota()+100)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	session, apiErr := NewBillingSession(ctx, walletBillingRelayInfo(user.Id), 1)

	require.Nil(t, session)
	require.NotNil(t, apiErr)
	require.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Zero(t, stored.Quota)
}

func TestNewBillingSessionReservesFiniteTokenAndWallet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupBillingSessionWalletCacheTest(t)
	user := model.User{Username: "billing-finite-token", Password: "password", Status: common.UserStatusEnabled, Quota: 100}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "billing-finite-token-key", Name: "billing finite", RemainQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	cacheBillingTokenForTest(t, token)
	info := walletBillingRelayInfo(user.Id)
	info.IsPlayground = false
	info.TokenUnlimited = false
	info.TokenId = token.Id
	info.TokenKey = token.Key

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	session, apiErr := NewBillingSession(ctx, info, 10)

	require.Nil(t, apiErr)
	require.NotNil(t, session)
	var storedUser model.User
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.Equal(t, 90, storedUser.Quota)
	var storedToken model.Token
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	require.Equal(t, 90, storedToken.RemainQuota)
}

func TestBillingSessionRollsBackFiniteTokenWhenWalletReserveFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupBillingSessionWalletCacheTest(t)
	user := model.User{Username: "billing-token-rollback", Password: "password", Status: common.UserStatusEnabled, Quota: 5}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "billing-token-rollback-key", Name: "billing rollback", RemainQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	cacheBillingTokenForTest(t, token)
	info := walletBillingRelayInfo(user.Id)
	info.IsPlayground = false
	info.TokenUnlimited = false
	info.TokenId = token.Id
	info.TokenKey = token.Key
	info.UserQuota = 100
	session := &BillingSession{relayInfo: info, funding: &WalletFunding{userId: user.Id}}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	apiErr := session.preConsume(ctx, 10)

	require.NotNil(t, apiErr)
	require.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
	var storedUser model.User
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.Equal(t, 5, storedUser.Quota)
	var storedToken model.Token
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	require.Equal(t, 100, storedToken.RemainQuota)
}

func TestNewBillingSessionAlwaysReservesTrustedWalletBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupBillingSessionWalletCacheTest(t)
	startingQuota := common.GetTrustQuota() + 100
	user := model.User{
		Username: "billing-trust-reserve",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    startingQuota,
	}
	require.NoError(t, db.Create(&user).Error)
	cacheWalletQuotaForBillingTest(t, user.Id, startingQuota)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	session, apiErr := NewBillingSession(ctx, walletBillingRelayInfo(user.Id), 1)

	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.False(t, session.trusted)
	require.Equal(t, 1, session.GetPreConsumedQuota())

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, startingQuota-1, stored.Quota)
}

func TestNewBillingSessionIgnoresStaleLowWalletCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupBillingSessionWalletCacheTest(t)
	user := model.User{
		Username: "billing-stale-low",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    100,
	}
	require.NoError(t, db.Create(&user).Error)
	cacheWalletQuotaForBillingTest(t, user.Id, 0)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	session, apiErr := NewBillingSession(ctx, walletBillingRelayInfo(user.Id), 10)

	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.Equal(t, 10, session.GetPreConsumedQuota())

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 90, stored.Quota)
}
