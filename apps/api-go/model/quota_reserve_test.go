package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestBatchQuotaAccumulatorNeverWraps(t *testing.T) {
	resetBatchUpdateTestState(t)

	addNewRecord(BatchUpdateTypeUserQuota, 1, common.MaxQuota)
	addNewRecord(BatchUpdateTypeUserQuota, 1, common.MaxQuota)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	require.Equal(t, common.MaxQuota*2, batchUpdateStores[BatchUpdateTypeUserQuota][1])
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()

	resetBatchUpdateTestState(t)
	addNewRecord(BatchUpdateTypeUserQuota, 1, common.MaxWalletQuota)
	addNewRecord(BatchUpdateTypeUserQuota, 1, 1)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	require.Equal(t, common.MaxWalletQuota, batchUpdateStores[BatchUpdateTypeUserQuota][1])
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()

	resetBatchUpdateTestState(t)
	addNewRecord(BatchUpdateTypeUserQuota, 1, common.MinWalletQuota)
	addNewRecord(BatchUpdateTypeUserQuota, 1, -1)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	require.Equal(t, common.MinWalletQuota, batchUpdateStores[BatchUpdateTypeUserQuota][1])
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
}

func TestQuotaReserveFallsBackToConditionalDatabaseBalance(t *testing.T) {
	truncateTables(t)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })

	user := User{
		Username: "quota-reserve-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Quota:    100,
	}
	require.NoError(t, DB.Create(&user).Error)

	reserved, err := TryReserveUserQuota(user.Id, 60)
	require.NoError(t, err)
	require.True(t, reserved)

	reserved, err = TryReserveUserQuota(user.Id, 50)
	require.NoError(t, err)
	require.False(t, reserved)

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	require.Equal(t, 40, stored.Quota)
}

func TestPersistUserQuotaDeltaBypassesBatchBufferForReservations(t *testing.T) {
	truncateTables(t)
	previousBatchUpdateEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = previousBatchUpdateEnabled })

	user := User{
		Username: "quota-reservation-durable",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Quota:    100,
	}
	require.NoError(t, DB.Create(&user).Error)

	// Redis-backed TryReserveUserQuota reaches this helper after the cache
	// reservation succeeds. It must not leave the matching database debit in
	// the process-local batch buffer.
	require.NoError(t, persistUserQuotaDelta(user.Id, -60))

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	require.Equal(t, 40, stored.Quota)
}
