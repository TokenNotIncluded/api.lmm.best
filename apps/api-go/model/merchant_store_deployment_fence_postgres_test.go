package model

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/stretchr/testify/require"
)

// This is a disposable, explicitly selected loopback database test. A synthetic
// session or an absent DSN cannot establish actual PostgreSQL fence behavior.
func TestMerchantStoreDeploymentFencePostgresSharedSessionAndDurableCrashOwner(t *testing.T) {
	db, _, _, _ := merchantStorePGDB(t)
	pool, err := db.DB()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	holder, err := pool.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Close() })
	_, err = holder.ExecContext(ctx, "SELECT pg_catalog.pg_advisory_lock_shared($1)", deploymentfence.AdvisoryKey)
	require.NoError(t, err)
	var holderPID int
	require.NoError(t, holder.QueryRowContext(ctx, "SELECT pg_catalog.pg_backend_pid()").Scan(&holderPID))
	owner := Option{Key: deploymentfence.OptionPrefix + "sealed-test:host-a", Value: `{"format":1,"state":"ACTIVE","nonce":"fixture-only"}`}
	_, err = holder.ExecContext(ctx, "INSERT INTO options (key,value) VALUES ($1,$2)", owner.Key, owner.Value)
	require.NoError(t, err)
	var actualShared int
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM pg_catalog.pg_locks WHERE pid=? AND locktype='advisory' AND mode='ShareLock' AND granted", holderPID).Scan(&actualShared).Error)
	require.Equal(t, 1, actualShared, "the exact live backend owns a real shared advisory lock")
	require.ErrorIs(t, ActivateMerchantStoreVariants(db, 1), ErrMerchantStoreWriterFrozen)
	status, err := GetMerchantStoreWriterGateStatus(db)
	require.NoError(t, err)
	require.Equal(t, 1, status.RequiredCapability)

	// Discard the physical connection, rather than returning an advisory-locked
	// session to the pool. The durable owner intentionally survives this crash.
	require.ErrorIs(t, holder.Raw(func(any) error { return driver.ErrBadConn }), driver.ErrBadConn)
	if err = holder.Close(); err != nil {
		require.True(t, errors.Is(err, sql.ErrConnDone))
	}
	probe, err := pool.Conn(ctx)
	require.NoError(t, err)
	var acquired, released bool
	require.NoError(t, probe.QueryRowContext(ctx, "SELECT pg_catalog.pg_try_advisory_lock($1)", deploymentfence.AdvisoryKey).Scan(&acquired))
	require.True(t, acquired, "loss of the physical session releases its live advisory lock")
	require.NoError(t, probe.QueryRowContext(ctx, "SELECT pg_catalog.pg_advisory_unlock($1)", deploymentfence.AdvisoryKey).Scan(&released))
	require.True(t, released)
	require.NoError(t, probe.Close())
	require.ErrorIs(t, ActivateMerchantStoreVariants(db, 1), ErrMerchantStoreWriterFrozen, "a lost session does not release its durable owner")
	var persisted Option
	require.NoError(t, db.Where("key = ?", owner.Key).First(&persisted).Error)
	require.Equal(t, owner, persisted)
	result := db.Where("key = ? AND value = ?", owner.Key, owner.Value+"changed").Delete(&Option{})
	require.NoError(t, result.Error)
	require.Zero(t, result.RowsAffected)
	require.ErrorIs(t, ActivateMerchantStoreVariants(db, 1), ErrMerchantStoreWriterFrozen)
	// Test-only trusted recovery uses the exact sealed value; there is no public
	// mutation endpoint or automatic expiration for these reserved records.
	result = db.Where("key = ? AND value = ?", owner.Key, owner.Value).Delete(&Option{})
	require.NoError(t, result.Error)
	require.EqualValues(t, 1, result.RowsAffected)
	require.NoError(t, ActivateMerchantStoreVariants(db, 1))
	status, err = GetMerchantStoreWriterGateStatus(db)
	require.NoError(t, err)
	require.Equal(t, 2, status.RequiredCapability)

	for _, alias := range []Option{
		{Key: "\u0085\u00a0\u2003MERCHANTSTOREDEPLOYMENTFENCE:unknown\u3000", Value: "invalid-json"},
		{Key: deploymentfence.OptionPrefix + "expired", Value: `{"state":"RELEASED","expires_at":1}`},
	} {
		require.NoError(t, db.Create(&alias).Error)
		var present bool
		require.NoError(t, db.Raw("SELECT EXISTS (SELECT 1 FROM options WHERE "+deploymentfence.PostgreSQLPresencePredicate+")").Scan(&present).Error)
		require.True(t, present, "PostgreSQL and Go both reject abnormal Unicode/case owner aliases")
		require.ErrorIs(t, BootstrapMerchantStoreWriterGate(db), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, ActivateMerchantStoreProductLifecycle(db, 2), ErrMerchantStoreWriterFrozen)
		result = db.Where("key = ? AND value = ?", alias.Key, alias.Value).Delete(&Option{})
		require.NoError(t, result.Error)
		require.EqualValues(t, 1, result.RowsAffected)
	}
	require.NoError(t, ActivateMerchantStoreProductLifecycle(db, 2))
	status, err = GetMerchantStoreWriterGateStatus(db)
	require.NoError(t, err)
	require.Equal(t, 3, status.RequiredCapability)
	t.Logf("real shared holder PID=%d blocked activation; after physical-session loss its durable owner still blocked until exact CAS recovery", holderPID)
}
