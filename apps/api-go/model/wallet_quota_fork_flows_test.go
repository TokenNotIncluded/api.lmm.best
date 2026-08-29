package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupWalletQuotaHelperTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&User{}))
	t.Cleanup(func() {
		DB = previousDB
		common.RedisEnabled = previousRedisEnabled
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestApplyWalletQuotaDeltaRejectsBothSafeIntegerBoundaries(t *testing.T) {
	db := setupWalletQuotaHelperTestDB(t)
	cases := []struct {
		name  string
		quota int
		delta int
	}{
		{name: "positive overflow", quota: common.MaxWalletQuota, delta: 1},
		{name: "negative overflow", quota: common.MinWalletQuota, delta: -1},
	}
	for index, tc := range cases {
		username := fmt.Sprintf("wallet-boundary-%d", index)
		user := User{Username: username, Password: "password", AffCode: username, Quota: tc.quota}
		require.NoError(t, db.Create(&user).Error)

		err := db.Transaction(func(tx *gorm.DB) error {
			return ApplyWalletQuotaDelta(tx, user.Id, tc.delta)
		})
		require.ErrorIs(t, err, ErrWalletQuotaOutOfRange, tc.name)

		var stored User
		require.NoError(t, db.First(&stored, user.Id).Error)
		require.Equal(t, tc.quota, stored.Quota, tc.name)
	}
}

func TestUpdateWalletQuotaByDeltaPreservesSufficientBalancePredicate(t *testing.T) {
	db := setupWalletQuotaHelperTestDB(t)
	user := User{Username: "wallet-predicate", Password: "password", Quota: 10}
	require.NoError(t, db.Create(&user).Error)

	result := UpdateWalletQuotaByDelta(
		db.Model(&User{}).Where("id = ? AND quota >= ?", user.Id, 20),
		-20,
	)
	require.NoError(t, result.Error)
	require.Zero(t, result.RowsAffected)

	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 10, stored.Quota)

	result = UpdateWalletQuotaByDelta(
		db.Model(&User{}).Where("id = ? AND quota >= ?", user.Id, 10),
		-10,
	)
	require.NoError(t, result.Error)
	require.EqualValues(t, 1, result.RowsAffected)
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Zero(t, stored.Quota)
}

func TestCreditTopUpQuotaRetainsGuardedMultiColumnAtomicity(t *testing.T) {
	db := setupWalletQuotaHelperTestDB(t)
	user := User{Username: "topup-guarded-wallet", Password: "password", AffCode: "topup-guarded-wallet-code", Quota: common.MaxWalletQuota}
	require.NoError(t, db.Create(&user).Error)

	err := db.Transaction(func(tx *gorm.DB) error {
		return creditTopUpQuota(tx, user.Id, 1, map[string]interface{}{"stripe_customer": "must-roll-back"})
	})
	require.ErrorIs(t, err, ErrTopUpQuotaLimitExceeded)

	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, common.MaxWalletQuota, stored.Quota)
	require.Empty(t, stored.StripeCustomer)
}

func TestUpdateWalletQuotaByDeltaRejectsOutOfDomainDeltaBeforeWrite(t *testing.T) {
	db := setupWalletQuotaHelperTestDB(t)
	user := User{Username: "wallet-invalid-delta", Password: "password", Quota: 25}
	require.NoError(t, db.Create(&user).Error)

	result := UpdateWalletQuotaByDelta(db.Model(&User{}).Where("id = ?", user.Id), common.MaxWalletQuota+1)
	require.Error(t, result.Error)

	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 25, stored.Quota)
}
