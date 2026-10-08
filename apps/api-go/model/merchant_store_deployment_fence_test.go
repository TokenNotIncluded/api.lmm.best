package model

import (
	"context"
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMerchantStoreDeploymentFenceGenericOptionsCannotWriteAnyOwnerAlias(t *testing.T) {
	db := marketTestDB(t)
	for _, key := range []string{
		deploymentfence.OptionPrefix + "reviewed:host-a",
		"merchantstoredeploymentfence:unknown",
		" \tMERCHANTSTOREDEPLOYMENTFENCE:case-alias\n",
		"\u0085\u00a0\u2003MerchantStoreDeploymentFence:unicode\u3000",
	} {
		t.Run(key, func(t *testing.T) {
			require.ErrorIs(t, validateOptionValue(key, "{}"), ErrMerchantStoreWriterGateReserved)
			require.ErrorIs(t, UpdateOption(key, "{}"), ErrMerchantStoreWriterGateReserved)
			_, err := UpdateOptionsBulkWithWarnings(map[string]string{key: "{}", "ordinary_fence_fixture": "must-not-commit"})
			require.ErrorIs(t, err, ErrMerchantStoreWriterGateReserved)
		})
	}
	var rows []Option
	require.NoError(t, db.Where("key <> ?", MerchantStoreWriterCapabilityOption).Find(&rows).Error)
	require.Empty(t, rows, "a rejected reserved key must not partially commit the ordinary bulk item")
}

func TestMerchantStoreDeploymentFenceUnknownRecordsBlockEveryActivation(t *testing.T) {
	for _, record := range []Option{
		{Key: deploymentfence.OptionPrefix + "deployment:host-a", Value: `{"state":"ACTIVE"}`},
		{Key: deploymentfence.OptionPrefix + "deployment:host-b", Value: `{"state":"RELEASED","expires_at":1}`},
		{Key: "\u2003MERCHANTSTOREDEPLOYMENTFENCE:malformed\u3000", Value: "not-json"},
	} {
		t.Run(record.Key, func(t *testing.T) {
			db := marketTestDB(t)
			// Only trusted fixture SQL may insert a sealed native-owner record.
			require.NoError(t, db.Create(&record).Error)
			for _, activation := range []func(*gorm.DB) error{
				BootstrapMerchantStoreWriterGate,
				func(db *gorm.DB) error { return ActivateMerchantStoreVariants(db, 1) },
				func(db *gorm.DB) error { return ActivateMerchantStoreProductLifecycle(db, 2) },
			} {
				require.ErrorIs(t, activation(db), ErrMerchantStoreWriterFrozen)
			}
			var persisted Option
			require.NoError(t, db.Where("key = ?", record.Key).First(&persisted).Error)
			require.Equal(t, record, persisted, "activation never parses, expires or removes an unknown native owner")
			status, err := GetMerchantStoreWriterGateStatus(db)
			require.NoError(t, err)
			require.Equal(t, 1, status.RequiredCapability)
			// Removing a different value cannot release this owner's record.
			result := db.Where("key = ? AND value = ?", record.Key, record.Value+"mismatch").Delete(&Option{})
			require.NoError(t, result.Error)
			require.Zero(t, result.RowsAffected)
			require.ErrorIs(t, BootstrapMerchantStoreWriterGate(db), ErrMerchantStoreWriterFrozen)
			result = db.Where("key = ? AND value = ?", record.Key, record.Value).Delete(&Option{})
			require.NoError(t, result.Error)
			require.EqualValues(t, 1, result.RowsAffected)
			require.NoError(t, BootstrapMerchantStoreWriterGate(db))
		})
	}
}

func TestMerchantStoreDeploymentFencePinnedSessionLockOrderAndFailureCleanup(t *testing.T) {
	for _, test := range []struct {
		name       string
		fenceOwner int
		record     bool
		fail       bool
		cancel     bool
		queries    []string
	}{
		{"success", 0, false, false, false, []string{"identity", "control", "fence-acquire", "fence-presence", "acquire", "release", "fence-release"}},
		{"live-shared-owner", 777, false, false, false, []string{"identity", "control", "fence-acquire"}},
		{"durable-orphan-owner", 0, true, false, false, []string{"identity", "control", "fence-acquire", "fence-presence", "fence-release"}},
		{"business-failure", 0, false, true, false, []string{"identity", "control", "fence-acquire", "fence-presence", "acquire", "release", "fence-release"}},
		{"caller-cancelled", 0, false, false, true, []string{"identity", "control", "fence-acquire", "fence-presence", "acquire", "release", "fence-release"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool, _, state := openMigrationUnixTestDB(t, migrationUnixEndpointFor("7654321"))
			pool.SetMaxOpenConns(1)
			state.fenceOwner, state.fenceRecord = test.fenceOwner, test.record
			db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Discard})
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ran := false
			businessErr := errors.New("fixture-business-failure")
			err = storeWithPostgresActivationSession(db.WithContext(ctx), func(bound *gorm.DB) error {
				ran = true
				require.NotEqual(t, pool, bound.Statement.ConnPool)
				if test.cancel {
					cancel()
				}
				if test.fail {
					return businessErr
				}
				return nil
			})
			if test.fenceOwner != 0 || test.record {
				require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
				require.False(t, ran)
			} else if test.fail {
				require.ErrorIs(t, err, businessErr)
			} else {
				require.NoError(t, err)
				require.True(t, ran)
			}
			state.mu.Lock()
			queries := make([]string, 0, len(state.queries))
			for _, query := range state.queries {
				require.Equal(t, 1, query.connection, "all identity, fence, presence and migration operations use one physical connection")
				queries = append(queries, query.kind)
			}
			require.Equal(t, test.queries, queries)
			require.Zero(t, state.lockOwner)
			require.Equal(t, test.fenceOwner, state.fenceOwner, "cleanup never unlocks another live holder")
			state.mu.Unlock()
			require.Zero(t, pool.Stats().InUse)
			conn, err := pool.Conn(context.Background())
			require.NoError(t, err, "the original one-connection pool remains usable")
			require.NoError(t, conn.Close())
		})
	}
}

func TestMerchantStoreDeploymentFenceFailedUnlockDiscardsPhysicalSession(t *testing.T) {
	pool, _, state := openMigrationUnixTestDB(t, migrationUnixEndpointFor("7654321"))
	pool.SetMaxOpenConns(1)
	state.failRelease = true
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Discard})
	require.NoError(t, err)
	require.ErrorIs(t, storeWithPostgresActivationSession(db, func(*gorm.DB) error { return nil }), ErrMerchantStoreWriterFrozen)
	state.mu.Lock()
	require.Zero(t, state.lockOwner, "closing the unusable physical connection releases the migration lock")
	require.Zero(t, state.fenceOwner, "closing the unusable physical connection also releases the fence lock")
	require.Equal(t, "release", state.queries[len(state.queries)-1].kind)
	state.mu.Unlock()
	require.Zero(t, pool.Stats().InUse)
	conn, err := pool.Conn(context.Background())
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	state.mu.Lock()
	require.Equal(t, 2, state.connections, "the poisoned session was discarded instead of being returned to the pool")
	state.mu.Unlock()
}
