// Package leadership provides PostgreSQL session-lock based leadership leases.
package leadership

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
)

const lockNamespacePrefix = "api.lmm.best/postgres-leadership/v1/"

const (
	AutomaticChannelBalanceNamespace = "scanner/automatic-channel-balance"
	CodexCredentialRefreshNamespace  = "scanner/codex-credential-refresh"
	SubscriptionMaintenanceNamespace = "scanner/subscription-maintenance"
)

var ErrLeaseReleased = errors.New("PostgreSQL leadership lease released")

// LockKey deterministically maps a namespace to PostgreSQL's signed bigint
// advisory-lock key space. The prefix and existing namespace strings are a
// persistent cross-version contract and must not be changed.
func LockKey(namespace string) (int64, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return 0, errors.New("PostgreSQL leadership namespace is empty")
	}
	digest := sha256.Sum256([]byte(lockNamespacePrefix + namespace))
	return int64(binary.BigEndian.Uint64(digest[:8])), nil
}

type LeaseOptions struct {
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
	ReleaseTimeout    time.Duration
}

func (options LeaseOptions) withDefaults() LeaseOptions {
	if options.HeartbeatInterval <= 0 {
		options.HeartbeatInterval = 5 * time.Second
	}
	if options.HeartbeatTimeout <= 0 {
		options.HeartbeatTimeout = 2 * time.Second
	}
	if options.ReleaseTimeout <= 0 {
		options.ReleaseTimeout = 2 * time.Second
	}
	return options
}

// Lease owns one dedicated database/sql connection for its entire lifetime.
// A canceled lease context means leadership must no longer be used. It does
// not fence work that a remote provider has already accepted.
type Lease struct {
	conn    *sql.Conn
	key     int64
	ctx     context.Context
	cancel  context.CancelCauseFunc
	options LeaseOptions

	heartbeatDone chan struct{}
	releaseDone   chan struct{}
	releaseOnce   sync.Once
	releaseErr    error
}

func (lease *Lease) Context() context.Context {
	if lease == nil || lease.ctx == nil {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	return lease.ctx
}

func (lease *Lease) Key() int64 {
	if lease == nil {
		return 0
	}
	return lease.key
}

// TryAcquire attempts a nonblocking PostgreSQL advisory lock on a dedicated
// connection. Contention is reported as (nil, false, nil). Any SQL error is
// returned and is never interpreted as leadership.
func TryAcquire(ctx context.Context, db *sql.DB, namespace string, options LeaseOptions) (*Lease, bool, error) {
	if ctx == nil {
		return nil, false, errors.New("PostgreSQL leadership parent context is nil")
	}
	if db == nil {
		return nil, false, errors.New("PostgreSQL leadership database is nil")
	}
	key, err := LockKey(namespace)
	if err != nil {
		return nil, false, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("open PostgreSQL leadership connection: %w", err)
	}
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_catalog.pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("try PostgreSQL advisory leadership lock: %w", err)
	}
	if !acquired {
		if err := conn.Close(); err != nil {
			return nil, false, fmt.Errorf("close contending PostgreSQL leadership connection: %w", err)
		}
		return nil, false, nil
	}

	leaseCtx, cancel := context.WithCancelCause(ctx)
	lease := &Lease{
		conn:          conn,
		key:           key,
		ctx:           leaseCtx,
		cancel:        cancel,
		options:       options.withDefaults(),
		heartbeatDone: make(chan struct{}),
		releaseDone:   make(chan struct{}),
	}
	go lease.heartbeat()
	return lease, true, nil
}

func (lease *Lease) heartbeat() {
	defer close(lease.heartbeatDone)
	ticker := time.NewTicker(lease.options.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-lease.ctx.Done():
			return
		case <-ticker.C:
			heartbeatCtx, cancel := context.WithTimeout(lease.ctx, lease.options.HeartbeatTimeout)
			var alive int
			err := lease.conn.QueryRowContext(heartbeatCtx, "SELECT 1").Scan(&alive)
			cancel()
			if err != nil || alive != 1 {
				if err == nil {
					err = fmt.Errorf("unexpected heartbeat result %d", alive)
				}
				lease.cancel(fmt.Errorf("PostgreSQL leadership heartbeat failed: %w", err))
				return
			}
		}
	}
}

// Release cancels the lease, waits for its heartbeat to stop, attempts unlock
// on the same dedicated connection, and then closes that connection.
func (lease *Lease) Release() error {
	if lease == nil {
		return nil
	}
	lease.releaseOnce.Do(func() {
		defer close(lease.releaseDone)
		lease.cancel(ErrLeaseReleased)
		<-lease.heartbeatDone

		releaseCtx, cancel := context.WithTimeout(context.Background(), lease.options.ReleaseTimeout)
		defer cancel()
		var unlocked bool
		unlockErr := lease.conn.QueryRowContext(releaseCtx,
			"SELECT pg_catalog.pg_advisory_unlock($1)", lease.key).Scan(&unlocked)
		if unlockErr == nil && !unlocked {
			unlockErr = errors.New("PostgreSQL advisory leadership lock was not owned by its lease connection")
		}
		if unlockErr != nil {
			// sql.Conn.Close normally returns a physical session to the pool. An
			// uncertain unlock must instead discard that session so a pooled
			// connection can never retain a possibly-held advisory lock.
			_ = lease.conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		lease.releaseErr = errors.Join(unlockErr, lease.conn.Close())
	})
	<-lease.releaseDone
	return lease.releaseErr
}

type RunOptions struct {
	Lease       LeaseOptions
	RetryMin    time.Duration
	RetryMax    time.Duration
	OnRetryable func(error)

	// RetryDelay overrides randomized bounded jitter for deterministic tests.
	// Returned values are clamped to [RetryMin, RetryMax].
	RetryDelay func(minimum, maximum time.Duration) time.Duration
}

func (options RunOptions) withDefaults() RunOptions {
	if options.RetryMin <= 0 {
		options.RetryMin = 500 * time.Millisecond
	}
	if options.RetryMax <= 0 {
		options.RetryMax = 2 * time.Second
	}
	if options.RetryMax < options.RetryMin {
		options.RetryMax = options.RetryMin
	}
	return options
}

func (options RunOptions) nextRetryDelay() time.Duration {
	if options.RetryDelay != nil {
		delay := options.RetryDelay(options.RetryMin, options.RetryMax)
		if delay < options.RetryMin {
			return options.RetryMin
		}
		if delay > options.RetryMax {
			return options.RetryMax
		}
		return delay
	}
	if span := options.RetryMax - options.RetryMin; span > 0 {
		return options.RetryMin + time.Duration(rand.Int64N(int64(span)))
	}
	return options.RetryMin
}

// Run repeatedly attempts leadership with bounded jitter. run is invoked only
// while this process owns a fresh lease. After a connection failure, any later
// leadership is acquired through a new connection and a new advisory lock.
func Run(ctx context.Context, db *sql.DB, namespace string, options RunOptions, run func(context.Context)) error {
	if ctx == nil {
		return errors.New("PostgreSQL leadership runner context is nil")
	}
	if run == nil {
		return errors.New("PostgreSQL leadership callback is nil")
	}
	options = options.withDefaults()
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		lease, acquired, err := TryAcquire(ctx, db, namespace, options.Lease)
		if err == nil && acquired {
			run(lease.Context())
			releaseErr := lease.Release()
			if releaseErr != nil && options.OnRetryable != nil && context.Cause(ctx) == nil {
				options.OnRetryable(releaseErr)
			}
		} else if err != nil && options.OnRetryable != nil {
			options.OnRetryable(err)
		}

		timer := time.NewTimer(options.nextRetryDelay())
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return context.Cause(ctx)
		case <-timer.C:
		}
	}
}
