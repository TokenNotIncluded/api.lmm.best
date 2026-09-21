package leadership

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var fakeLeadershipDriverSequence atomic.Uint64

type fakeLeadershipState struct {
	mu             sync.Mutex
	nextConnection int64
	lockOwner      int64
	lockKey        int64
	acquireConn    int64
	unlockConn     int64
	closedConn     int64
	failAcquire    bool
	failHeartbeat  bool
	failUnlock     bool
}

type fakeLeadershipDriver struct{ state *fakeLeadershipState }

type fakeLeadershipConn struct {
	state  *fakeLeadershipState
	id     int64
	closed bool
}

type fakeLeadershipRows struct {
	column string
	value  driver.Value
	read   bool
}

func openFakeLeadershipDB(t *testing.T) (*sql.DB, *fakeLeadershipState) {
	t.Helper()
	state := &fakeLeadershipState{}
	name := fmt.Sprintf("leadership-fake-%d", fakeLeadershipDriverSequence.Add(1))
	sql.Register(name, &fakeLeadershipDriver{state: state})
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, state
}

func (driverInstance *fakeLeadershipDriver) Open(string) (driver.Conn, error) {
	driverInstance.state.mu.Lock()
	defer driverInstance.state.mu.Unlock()
	driverInstance.state.nextConnection++
	return &fakeLeadershipConn{state: driverInstance.state, id: driverInstance.state.nextConnection}, nil
}

func (connection *fakeLeadershipConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported by the leadership test driver")
}

func (connection *fakeLeadershipConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported by the leadership test driver")
}

func (connection *fakeLeadershipConn) Close() error {
	connection.state.mu.Lock()
	defer connection.state.mu.Unlock()
	connection.closed = true
	connection.state.closedConn = connection.id
	if connection.state.lockOwner == connection.id {
		connection.state.lockOwner = 0
	}
	return nil
}

func (connection *fakeLeadershipConn) QueryContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	connection.state.mu.Lock()
	defer connection.state.mu.Unlock()
	if connection.closed {
		return nil, driver.ErrBadConn
	}
	normalized := strings.Join(strings.Fields(query), " ")
	switch {
	case strings.Contains(normalized, "pg_try_advisory_lock"):
		if connection.state.failAcquire {
			return nil, errors.New("injected non-PostgreSQL advisory-lock query failure")
		}
		key, ok := arguments[0].Value.(int64)
		if !ok {
			return nil, fmt.Errorf("unexpected lock key type %T", arguments[0].Value)
		}
		acquired := connection.state.lockOwner == 0
		if acquired {
			connection.state.lockOwner = connection.id
			connection.state.lockKey = key
			connection.state.acquireConn = connection.id
		}
		return &fakeLeadershipRows{column: "pg_try_advisory_lock", value: acquired}, nil
	case strings.Contains(normalized, "pg_advisory_unlock"):
		if connection.state.failUnlock {
			return nil, errors.New("injected unlock failure")
		}
		unlocked := connection.state.lockOwner == connection.id
		if unlocked {
			connection.state.lockOwner = 0
			connection.state.unlockConn = connection.id
		}
		return &fakeLeadershipRows{column: "pg_advisory_unlock", value: unlocked}, nil
	case normalized == "SELECT 1":
		if connection.state.failHeartbeat {
			return nil, errors.New("injected heartbeat failure")
		}
		return &fakeLeadershipRows{column: "alive", value: int64(1)}, nil
	default:
		return nil, fmt.Errorf("unexpected query %q", normalized)
	}
}

func (rows *fakeLeadershipRows) Columns() []string { return []string{rows.column} }
func (rows *fakeLeadershipRows) Close() error      { return nil }
func (rows *fakeLeadershipRows) Next(values []driver.Value) error {
	if rows.read {
		return io.EOF
	}
	rows.read = true
	values[0] = rows.value
	return nil
}

func TestLeaseUsesDedicatedConnectionAndUnlocksSameSession(t *testing.T) {
	db, state := openFakeLeadershipDB(t)
	lease, acquired, err := TryAcquire(context.Background(), db, "unit/same-session", LeaseOptions{
		HeartbeatInterval: time.Hour,
	})
	require.NoError(t, err)
	require.True(t, acquired)

	follower, acquired, err := TryAcquire(context.Background(), db, "unit/same-session", LeaseOptions{})
	require.NoError(t, err)
	require.False(t, acquired)
	require.Nil(t, follower)

	require.NoError(t, lease.Release())
	state.mu.Lock()
	require.NotZero(t, state.acquireConn)
	require.Equal(t, state.acquireConn, state.unlockConn)
	require.Zero(t, state.lockOwner)
	state.mu.Unlock()
}

func TestUnlockFailureDiscardsPhysicalSession(t *testing.T) {
	db, state := openFakeLeadershipDB(t)
	lease, acquired, err := TryAcquire(context.Background(), db, "unit/discard", LeaseOptions{})
	require.NoError(t, err)
	require.True(t, acquired)
	state.mu.Lock()
	owner := state.lockOwner
	state.failUnlock = true
	state.mu.Unlock()

	require.ErrorContains(t, lease.Release(), "injected unlock failure")
	state.mu.Lock()
	require.Equal(t, owner, state.closedConn)
	require.Zero(t, state.lockOwner, "discarding the physical session must release its advisory lock")
	state.failUnlock = false
	state.mu.Unlock()

	replacement, acquired, err := TryAcquire(context.Background(), db, "unit/discard", LeaseOptions{})
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, replacement.Release())
}

func TestLeaseContextCancelsOnParentAndHeartbeatFailure(t *testing.T) {
	t.Run("parent", func(t *testing.T) {
		db, _ := openFakeLeadershipDB(t)
		parent, cancel := context.WithCancel(context.Background())
		lease, acquired, err := TryAcquire(parent, db, "unit/parent", LeaseOptions{})
		require.NoError(t, err)
		require.True(t, acquired)
		cancel()
		require.Eventually(t, func() bool { return lease.Context().Err() != nil }, time.Second, time.Millisecond)
		require.ErrorIs(t, context.Cause(lease.Context()), context.Canceled)
		require.NoError(t, lease.Release())
	})

	t.Run("heartbeat", func(t *testing.T) {
		db, state := openFakeLeadershipDB(t)
		lease, acquired, err := TryAcquire(context.Background(), db, "unit/heartbeat", LeaseOptions{
			HeartbeatInterval: 2 * time.Millisecond,
			HeartbeatTimeout:  20 * time.Millisecond,
		})
		require.NoError(t, err)
		require.True(t, acquired)
		state.mu.Lock()
		state.failHeartbeat = true
		state.mu.Unlock()
		require.Eventually(t, func() bool { return lease.Context().Err() != nil }, time.Second, time.Millisecond)
		require.ErrorContains(t, context.Cause(lease.Context()), "leadership heartbeat failed")
		require.NoError(t, lease.Release())
	})
}

func TestFollowerRetriesWithoutCallbackSideEffects(t *testing.T) {
	db, _ := openFakeLeadershipDB(t)
	leader, acquired, err := TryAcquire(context.Background(), db, "unit/follower", LeaseOptions{})
	require.NoError(t, err)
	require.True(t, acquired)
	defer func() { require.NoError(t, leader.Release()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	var sideEffects atomic.Int64
	err = Run(ctx, db, "unit/follower", RunOptions{
		RetryMin: 2 * time.Millisecond,
		RetryMax: 5 * time.Millisecond,
		RetryDelay: func(minimum, _ time.Duration) time.Duration {
			return minimum
		},
	}, func(context.Context) {
		sideEffects.Add(1)
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, sideEffects.Load())
}

func TestAcquisitionErrorsFailClosedWithoutCallbackSideEffects(t *testing.T) {
	db, state := openFakeLeadershipDB(t)
	state.mu.Lock()
	state.failAcquire = true
	state.mu.Unlock()

	lease, acquired, err := TryAcquire(context.Background(), db, "unit/fail-closed", LeaseOptions{})
	require.ErrorContains(t, err, "try PostgreSQL advisory leadership lock")
	require.False(t, acquired)
	require.Nil(t, lease)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Millisecond)
	defer cancel()
	var sideEffects atomic.Int64
	err = Run(ctx, db, "unit/fail-closed", RunOptions{
		RetryMin:   time.Millisecond,
		RetryMax:   time.Millisecond,
		RetryDelay: func(time.Duration, time.Duration) time.Duration { return time.Millisecond },
	}, func(context.Context) {
		sideEffects.Add(1)
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, sideEffects.Load())
}

func TestRetryDelayIsBoundedAndRunnerRejectsNilContext(t *testing.T) {
	options := (RunOptions{
		RetryMin: 10 * time.Millisecond,
		RetryMax: 20 * time.Millisecond,
		RetryDelay: func(time.Duration, time.Duration) time.Duration {
			return time.Hour
		},
	}).withDefaults()
	require.Equal(t, 20*time.Millisecond, options.nextRetryDelay())

	err := Run(nil, nil, "unit/nil", RunOptions{}, func(context.Context) {})
	require.ErrorContains(t, err, "context is nil")
}
