package model

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

const (
	cacheWarmRetryInitialDelay = 250 * time.Millisecond
	cacheWarmRetryMaxDelay     = 5 * time.Second
)

var cacheWarmLock sync.Mutex
var cacheReadinessEnforced atomic.Bool
var cacheRuntimeUnavailable atomic.Bool
var cacheWarmStateLock sync.Mutex
var cacheWarmLastError error
var cacheWarmNextRetry time.Time
var cacheWarmRetryDelay time.Duration
var cacheWarmNow = time.Now
var cacheWarmAttemptHook func()

func CachesReady() bool {
	if cacheRuntimeUnavailable.Load() {
		return false
	}
	cacheWarmStateLock.Lock()
	warmErr := cacheWarmLastError
	cacheWarmStateLock.Unlock()
	if warmErr != nil {
		return false
	}
	if pricingCache.Load() == nil {
		return false
	}
	if !common.MemoryCacheEnabled {
		return true
	}
	channelSyncLock.RLock()
	ready := channelCacheReady
	channelSyncLock.RUnlock()
	return ready
}

func CacheReadinessError() error {
	if cacheRuntimeUnavailable.Load() {
		return errors.New("runtime is shutting down")
	}
	if !cacheReadinessEnforced.Load() {
		return nil
	}
	cacheWarmStateLock.Lock()
	warmErr := cacheWarmLastError
	cacheWarmStateLock.Unlock()
	if warmErr != nil {
		return fmt.Errorf("cache warm failed: %w", warmErr)
	}
	if common.MemoryCacheEnabled {
		channelSyncLock.RLock()
		ready := channelCacheReady
		lastErr := channelCacheLastError
		channelSyncLock.RUnlock()
		if !ready {
			if lastErr != nil {
				return fmt.Errorf("channel cache is not ready: %w", lastErr)
			}
			return errors.New("channel cache is not ready")
		}
	}
	if pricingCache.Load() == nil {
		return errors.New("pricing cache is not ready")
	}
	return nil
}

// MarkCacheReadinessUnavailable fails readiness before graceful drain starts.
func MarkCacheReadinessUnavailable() {
	cacheReadinessEnforced.Store(true)
	cacheRuntimeUnavailable.Store(true)
}

func WarmCaches() error {
	cacheReadinessEnforced.Store(true)
	cacheWarmLock.Lock()
	defer cacheWarmLock.Unlock()
	err := warmCachesSafely()
	recordCacheWarmResult(err)
	return err
}

func warmCachesLocked() error {
	if cacheWarmAttemptHook != nil {
		cacheWarmAttemptHook()
	}
	if common.MemoryCacheEnabled {
		if err := InitChannelCache(); err != nil {
			return err
		}
	}
	if err := RefreshPricing(); err != nil {
		return err
	}
	return nil
}

func warmCachesSafely() (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("cache warm panic: %v", recovered)
			common.SysLog(err.Error())
		}
	}()
	return warmCachesLocked()
}

func cacheWarmRetryAllowed() bool {
	cacheWarmStateLock.Lock()
	defer cacheWarmStateLock.Unlock()
	return cacheWarmNextRetry.IsZero() || !cacheWarmNow().Before(cacheWarmNextRetry)
}

func recordCacheWarmResult(err error) {
	cacheWarmStateLock.Lock()
	defer cacheWarmStateLock.Unlock()
	if err == nil {
		cacheWarmLastError = nil
		cacheWarmNextRetry = time.Time{}
		cacheWarmRetryDelay = 0
		return
	}
	cacheWarmLastError = err
	if cacheWarmRetryDelay == 0 {
		cacheWarmRetryDelay = cacheWarmRetryInitialDelay
	} else {
		cacheWarmRetryDelay *= 2
		if cacheWarmRetryDelay > cacheWarmRetryMaxDelay {
			cacheWarmRetryDelay = cacheWarmRetryMaxDelay
		}
	}
	cacheWarmNextRetry = cacheWarmNow().Add(cacheWarmRetryDelay)
}

// EnsureCachesWarmAsync is safe for readiness probes and cold request paths:
// one caller starts the bounded warm attempt and all others return immediately.
// Failures use capped exponential backoff so high-frequency probes cannot turn
// a database outage into a retry storm.
func EnsureCachesWarmAsync() {
	cacheReadinessEnforced.Store(true)
	if cacheRuntimeUnavailable.Load() {
		return
	}
	if !cacheWarmLock.TryLock() {
		return
	}
	if cacheRuntimeUnavailable.Load() || !cacheWarmRetryAllowed() {
		cacheWarmLock.Unlock()
		return
	}
	go func() {
		defer cacheWarmLock.Unlock()
		err := warmCachesSafely()
		recordCacheWarmResult(err)
		if err != nil {
			common.SysLog(fmt.Sprintf("cache warm retry failed: %v", err))
		}
	}()
}

// WaitForCacheWarm waits for any in-flight asynchronous warm attempt. Callers
// must first mark readiness unavailable so no new attempt can be admitted.
func WaitForCacheWarm(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if cacheWarmLock.TryLock() {
			cacheWarmLock.Unlock()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
