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
	require.NoError(t, db.AutoMigrate(&model.User{}))

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
