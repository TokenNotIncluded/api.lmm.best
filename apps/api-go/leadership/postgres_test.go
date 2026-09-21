package leadership

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

func openPostgresLeadershipTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run PostgreSQL leadership integration tests")
	}
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(8)
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, db.PingContext(pingCtx))
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func TestLockKeyIsStableAndNamespaced(t *testing.T) {
	channelBalance, err := LockKey(AutomaticChannelBalanceNamespace)
	require.NoError(t, err)
	codex, err := LockKey(CodexCredentialRefreshNamespace)
	require.NoError(t, err)
	subscriptions, err := LockKey(SubscriptionMaintenanceNamespace)
	require.NoError(t, err)

	require.Equal(t, int64(1890918773716669845), channelBalance)
	require.Equal(t, int64(-3539149248670161494), codex)
	require.Equal(t, int64(-222328612053769287), subscriptions)
	require.NotEqual(t, channelBalance, codex)
	require.NotEqual(t, codex, subscriptions)
	_, err = LockKey("  ")
	require.Error(t, err)
}

func TestPostgresLeaseContentionAndSameConnectionRelease(t *testing.T) {
	db := openPostgresLeadershipTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	options := LeaseOptions{HeartbeatInterval: time.Second, HeartbeatTimeout: time.Second}

	leader, acquired, err := TryAcquire(ctx, db, "test/contention", options)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, leader)
	leaderConn := leader.conn

	follower, acquired, err := TryAcquire(ctx, db, "test/contention", options)
	require.NoError(t, err)
	require.False(t, acquired)
	require.Nil(t, follower)

	require.NoError(t, leader.Release())
	var one int
	require.ErrorIs(t, leaderConn.QueryRowContext(ctx, "SELECT 1").Scan(&one), sql.ErrConnDone)

	nextLeader, acquired, err := TryAcquire(ctx, db, "test/contention", options)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, nextLeader.Release())
}

func TestPostgresLeaseCancelsOnParentCancellation(t *testing.T) {
	db := openPostgresLeadershipTestDB(t)
	parent, cancel := context.WithCancel(context.Background())
	lease, acquired, err := TryAcquire(parent, db, "test/parent-cancellation", LeaseOptions{})
	require.NoError(t, err)
	require.True(t, acquired)

	cancel()
	select {
	case <-lease.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("lease context did not cancel with its parent")
	}
	require.ErrorIs(t, context.Cause(lease.Context()), context.Canceled)
	require.NoError(t, lease.Release())
}

func TestPostgresLeaseCancelsOnConnectionLoss(t *testing.T) {
	db := openPostgresLeadershipTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lease, acquired, err := TryAcquire(ctx, db, "test/connection-loss", LeaseOptions{
		HeartbeatInterval: 20 * time.Millisecond,
		HeartbeatTimeout:  100 * time.Millisecond,
		ReleaseTimeout:    100 * time.Millisecond,
	})
	require.NoError(t, err)
	require.True(t, acquired)

	var backendPID int
	require.NoError(t, lease.conn.QueryRowContext(ctx, "SELECT pg_catalog.pg_backend_pid()").Scan(&backendPID))
	var terminated bool
	require.NoError(t, db.QueryRowContext(ctx, "SELECT pg_catalog.pg_terminate_backend($1)", backendPID).Scan(&terminated))
	require.True(t, terminated)

	select {
	case <-lease.Context().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("lease context was not canceled after its dedicated connection was lost")
	}
	require.ErrorContains(t, context.Cause(lease.Context()), "leadership heartbeat failed")
	require.Error(t, lease.Release())

	replacement, acquired, err := TryAcquire(ctx, db, "test/connection-loss", LeaseOptions{})
	require.NoError(t, err)
	require.True(t, acquired, "a fresh connection must reacquire rather than inherit leadership")
	require.NoError(t, replacement.Release())
}

func TestFollowerRetriesWithoutRunningSideEffects(t *testing.T) {
	db := openPostgresLeadershipTestDB(t)
	leader, acquired, err := TryAcquire(context.Background(), db, "test/follower", LeaseOptions{})
	require.NoError(t, err)
	require.True(t, acquired)
	defer func() { require.NoError(t, leader.Release()) }()

	followerCtx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	var sideEffects atomic.Int64
	err = Run(followerCtx, db, "test/follower", RunOptions{
		RetryMin: 10 * time.Millisecond,
		RetryMax: 20 * time.Millisecond,
	}, func(context.Context) {
		sideEffects.Add(1)
	})
	require.True(t, errors.Is(err, context.DeadlineExceeded))
	require.Zero(t, sideEffects.Load(), "a follower must never invoke the scanner callback")
}
