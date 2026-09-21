package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupGiftTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB, LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&User{}, &Gift{}, &GiftClaim{}))

	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func createGiftTestUser(t *testing.T, username string, createdAt int64, usedQuota int) *User {
	t.Helper()
	user := &User{
		Username:  username,
		Password:  "password123",
		CreatedAt: createdAt,
		UsedQuota: usedQuota,
	}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func createTestGift(t *testing.T, quota int, startAt int64, endAt int64, minUsed int, minAgeDays int) *Gift {
	t.Helper()
	gift := &Gift{
		Title:             "compensation",
		Quota:             quota,
		StartAt:           startAt,
		EndAt:             endAt,
		MinUsedQuota:      minUsed,
		MinAccountAgeDays: minAgeDays,
		Enabled:           true,
	}
	require.NoError(t, CreateGift(gift))
	return gift
}

func TestClaimGiftSuccess(t *testing.T) {
	setupGiftTestDB(t)
	now := time.Now().Unix()
	user := createGiftTestUser(t, "gift_user_ok", now-10*86400, 500)
	gift := createTestGift(t, 1000, now-3600, now+86400, 100, 7)

	claim, alreadyClaimed, err := ClaimGift(user.Id, gift.Id)
	require.NoError(t, err)
	require.False(t, alreadyClaimed)
	require.Equal(t, 1000, claim.Quota)

	updated, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	require.Equal(t, 1000, updated.Quota)
}

func TestClaimGiftOverflowRollsBackClaimRecord(t *testing.T) {
	db := setupGiftTestDB(t)
	now := time.Now().Unix()
	user := createGiftTestUser(t, "gift_user_overflow", 0, 0)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", common.MaxWalletQuota).Error)
	gift := createTestGift(t, 1, now-3600, now+86400, 0, 0)

	claim, alreadyClaimed, err := ClaimGift(user.Id, gift.Id)
	require.Error(t, err)
	require.Nil(t, claim)
	require.False(t, alreadyClaimed)

	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, common.MaxWalletQuota, stored.Quota)
	var claimCount int64
	require.NoError(t, db.Model(&GiftClaim{}).Where("gift_id = ? AND user_id = ?", gift.Id, user.Id).Count(&claimCount).Error)
	require.Zero(t, claimCount)
}

func TestClaimGiftDuplicateRejected(t *testing.T) {
	setupGiftTestDB(t)
	now := time.Now().Unix()
	user := createGiftTestUser(t, "gift_user_dup", 0, 0)
	gift := createTestGift(t, 100, now-3600, now+86400, 0, 0)

	first, alreadyClaimed, err := ClaimGift(user.Id, gift.Id)
	require.NoError(t, err)
	require.False(t, alreadyClaimed)

	// 重复领取：幂等返回已有记录，不重复发放额度
	second, alreadyClaimed, err := ClaimGift(user.Id, gift.Id)
	require.NoError(t, err)
	require.True(t, alreadyClaimed)
	require.Equal(t, first.Quota, second.Quota)

	updated, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	require.Equal(t, 100, updated.Quota, "quota should only be credited once")
}

func TestClaimGiftWindowEnforced(t *testing.T) {
	setupGiftTestDB(t)
	now := time.Now().Unix()
	user := createGiftTestUser(t, "gift_user_window", 0, 0)

	notStarted := createTestGift(t, 100, now+3600, now+86400, 0, 0)
	_, _, err := ClaimGift(user.Id, notStarted.Id)
	require.ErrorIs(t, err, ErrGiftNotStarted)

	expired := createTestGift(t, 100, now-86400, now-3600, 0, 0)
	_, _, err = ClaimGift(user.Id, expired.Id)
	require.ErrorIs(t, err, ErrGiftExpired)
}

func TestClaimGiftEligibilityGates(t *testing.T) {
	setupGiftTestDB(t)
	now := time.Now().Unix()
	// 新注册 + 低消耗，不满足门槛
	user := createGiftTestUser(t, "gift_user_gate", now-86400, 10)
	gift := createTestGift(t, 100, now-3600, now+86400, 100, 7)

	_, _, err := ClaimGift(user.Id, gift.Id)
	require.ErrorIs(t, err, ErrGiftNotEligible)
}

func TestClaimGiftDisabledRejected(t *testing.T) {
	setupGiftTestDB(t)
	now := time.Now().Unix()
	user := createGiftTestUser(t, "gift_user_disabled", 0, 0)
	gift := createTestGift(t, 100, now-3600, now+86400, 0, 0)
	gift.Enabled = false
	require.NoError(t, UpdateGift(gift))

	_, _, err := ClaimGift(user.Id, gift.Id)
	require.ErrorIs(t, err, ErrGiftNotFound)
}

func TestGetAvailableGiftsForUser(t *testing.T) {
	setupGiftTestDB(t)
	now := time.Now().Unix()
	user := createGiftTestUser(t, "gift_user_list", 0, 0)

	// 快乐路径：有效且可领取
	active := createTestGift(t, 100, now-3600, now+86400, 0, 0)
	// 过期超过 7 天宽限期：不展示
	createTestGift(t, 100, now-86400*10, now-86400*9, 0, 0)
	// 刚过期（宽限期内）：展示但不可领取
	recentlyExpired := createTestGift(t, 100, now-86400*2, now-3600, 0, 0)
	// 不满足门槛：展示但不可领取
	ineligibleByGates := createTestGift(t, 100, now-3600, now+86400, 1, 1)
	// 禁用：不展示
	disabled := createTestGift(t, 100, now-3600, now+86400, 0, 0)
	disabled.Enabled = false
	require.NoError(t, UpdateGift(disabled))

	_, _, err := ClaimGift(user.Id, active.Id)
	require.NoError(t, err)

	gifts, err := GetAvailableGiftsForUser(user.Id)
	require.NoError(t, err)
	require.Len(t, gifts, 3)

	var activeGift, expiredGift, gateGift *GiftWithClaimStatus
	for i := range gifts {
		switch gifts[i].Id {
		case active.Id:
			activeGift = &gifts[i]
		case recentlyExpired.Id:
			expiredGift = &gifts[i]
		case ineligibleByGates.Id:
			gateGift = &gifts[i]
		case disabled.Id:
			t.Fatalf("disabled gift %d should not be listed", disabled.Id)
		}
	}
	require.NotNil(t, activeGift)
	require.NotNil(t, expiredGift)
	require.NotNil(t, gateGift)

	t.Run("active gift is claimed and eligible", func(t *testing.T) {
		require.True(t, activeGift.Claimed)
		require.True(t, activeGift.Eligible)
		require.Empty(t, activeGift.Reason)
	})

	t.Run("recently expired gift is listed but ineligible", func(t *testing.T) {
		require.False(t, expiredGift.Claimed)
		require.False(t, expiredGift.Eligible)
		require.Equal(t, ErrGiftExpired.Error(), expiredGift.Reason)
	})

	t.Run("gift failing gates is listed but ineligible", func(t *testing.T) {
		require.False(t, gateGift.Claimed)
		require.False(t, gateGift.Eligible)
		require.Equal(t, ErrGiftNotEligible.Error(), gateGift.Reason)
	})
}
