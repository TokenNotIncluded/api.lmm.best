package model

import (
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func openWalletQuotaTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}))
	t.Cleanup(func() {
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

func TestApplyWalletQuotaDeltaEnforcesSymmetricSafeBounds(t *testing.T) {
	db := openWalletQuotaTestDB(t)
	user := User{Id: 91001, Username: "wallet-boundary", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	require.NoError(t, ApplyWalletQuotaDelta(db, user.Id, common.MaxWalletQuota))
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, common.MaxWalletQuota, user.Quota)

	err := ApplyWalletQuotaDelta(db, user.Id, 1)
	require.ErrorIs(t, err, ErrWalletQuotaOutOfRange)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, common.MaxWalletQuota, user.Quota)

	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 0).Error)
	require.NoError(t, ApplyWalletQuotaDelta(db, user.Id, common.MinWalletQuota))
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, common.MinWalletQuota, user.Quota)

	err = ApplyWalletQuotaDelta(db, user.Id, -1)
	require.ErrorIs(t, err, ErrWalletQuotaOutOfRange)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, common.MinWalletQuota, user.Quota)
}

func TestUpdateWalletQuotaByDeltaKeepsCallerPredicates(t *testing.T) {
	db := openWalletQuotaTestDB(t)
	user := User{Id: 91002, Username: "wallet-predicate", Status: common.UserStatusEnabled, Quota: 10}
	require.NoError(t, db.Create(&user).Error)

	result := UpdateWalletQuotaByDelta(
		db.Model(&User{}).Where("id = ? AND quota >= ?", user.Id, 11),
		-11,
	)
	require.NoError(t, result.Error)
	assert.Zero(t, result.RowsAffected)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 10, user.Quota)
}

func TestApplyWalletQuotaDeltaDistinguishesMissingUserAndValidatesZeroDelta(t *testing.T) {
	db := openWalletQuotaTestDB(t)

	err := ApplyWalletQuotaDelta(db, 91999, 1)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	err = ApplyWalletQuotaDelta(db, 91999, 0)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	user := User{Id: 91003, Username: "wallet-zero-delta", Status: common.UserStatusEnabled, Quota: 7}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, ApplyWalletQuotaDelta(db, user.Id, 0))

	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", common.MaxWalletQuota+1).Error)
	require.ErrorIs(t, ApplyWalletQuotaDelta(db, user.Id, 0), ErrWalletQuotaOutOfRange)
}

func TestRequestCountExpressionUsesLegacyInt32Bounds(t *testing.T) {
	db := openWalletQuotaTestDB(t)
	user := User{
		Id:           91004,
		Username:     "request-count-boundary",
		Status:       common.UserStatusEnabled,
		RequestCount: math.MaxInt32,
	}
	require.NoError(t, db.Create(&user).Error)

	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).
		UpdateColumn("request_count", boundedInt32CounterExpr("request_count", 1)).Error)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, math.MaxInt32, user.RequestCount)

	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).
		UpdateColumn("request_count", boundedInt32CounterExpr("request_count", -math.MaxInt32-1)).Error)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, -1, user.RequestCount)
}

func TestSaturatingAddUsesWalletBoundsWithoutNativeIntWrap(t *testing.T) {
	assert.Equal(t, common.MaxWalletQuota, saturatingAdd(common.MaxWalletQuota-1, 2))
	assert.Equal(t, common.MinWalletQuota, saturatingAdd(common.MinWalletQuota+1, -2))
	assert.Equal(t, common.MaxWalletQuota, saturatingAdd(0, int(^uint(0)>>1)))
	assert.Equal(t, 7, saturatingAdd(3, 4))
}
