package model

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// syncBuffer collects log output written from the detached invalidation
// goroutine without racing the test's own reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureSysError(t *testing.T) *syncBuffer {
	t.Helper()
	buffer := &syncBuffer{}
	previous := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = buffer
	t.Cleanup(func() { gin.DefaultErrorWriter = previous })
	return buffer
}

// A failed cache eviction must not roll back a committed reward, and it must
// leave evidence: these two paths previously discarded the error silently.
func TestGiftClaimCreditsAndLogsWhenCacheInvalidationFails(t *testing.T) {
	setupGiftTestDB(t)
	server := useUserCacheMiniRedis(t)
	server.Close()
	buffer := captureSysError(t)

	now := time.Now().Unix()
	user := createGiftTestUser(t, "gift_cache_failure", now-10*86400, 500)
	gift := createTestGift(t, 1000, now-3600, now+86400, 100, 7)

	claim, alreadyClaimed, err := ClaimGift(user.Id, gift.Id)
	require.NoError(t, err)
	require.False(t, alreadyClaimed)
	require.Equal(t, 1000, claim.Quota)

	updated, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	require.Equal(t, 1000, updated.Quota, "a failed eviction must not lose the reward")

	require.Eventually(t, func() bool {
		return strings.Contains(
			buffer.String(),
			"failed to invalidate quota cache after gift claim",
		)
	}, 5*time.Second, 10*time.Millisecond, "the invalidation failure must be logged")
}

func setupCheckinTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB, LOG_DB = db, db
		// Trust-level evaluation reads payment history, so top_ups must exist too.
	require.NoError(t, db.AutoMigrate(&User{}, &Checkin{}, &TopUp{}))

	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func enableCheckinForTest(t *testing.T) {
	t.Helper()
	setting := operation_setting.GetCheckinSetting()
	previousEnabled, previousMin, previousMax := setting.Enabled, setting.MinQuota, setting.MaxQuota
	previousMultipliers := append([]float64(nil), setting.LevelMultipliers...)
	setting.Enabled = true
	// Pin the range and neutralise the per-level multiplier so the awarded
	// quota is deterministic regardless of the user's trust level.
	setting.MinQuota, setting.MaxQuota = 1000, 1000
	setting.LevelMultipliers = []float64{1, 1, 1, 1, 1}
	t.Cleanup(func() {
		setting.Enabled = previousEnabled
		setting.MinQuota, setting.MaxQuota = previousMin, previousMax
		setting.LevelMultipliers = previousMultipliers
	})
}

func TestCheckinCreditsAndLogsWhenCacheInvalidationFails(t *testing.T) {
	setupCheckinTestDB(t)
	enableCheckinForTest(t)
	server := useUserCacheMiniRedis(t)
	server.Close()
	buffer := captureSysError(t)

	user := &User{Username: "checkin_cache_failure", Password: "password123"}
	require.NoError(t, DB.Create(user).Error)

	checkin, err := UserCheckin(user.Id)
	require.NoError(t, err)
	require.Equal(t, 1000, checkin.QuotaAwarded)

	updated, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	require.Equal(t, 1000, updated.Quota, "a failed eviction must not lose the reward")

	require.Eventually(t, func() bool {
		return strings.Contains(
			buffer.String(),
			"failed to invalidate quota cache after checkin",
		)
	}, 5*time.Second, 10*time.Millisecond, "the invalidation failure must be logged")
}
