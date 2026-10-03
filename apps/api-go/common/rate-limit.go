package common

import (
	"math"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/pkg/cachex"
)

const (
	rateLimitMaxKeys  = 65_536
	rateLimitMaxBytes = 8 << 20
)

type rateWindow struct {
	Count     int
	StartedAt time.Time
}

func (w *rateWindow) refresh(now time.Time, duration time.Duration) {
	if w.StartedAt.IsZero() || now.Sub(w.StartedAt) >= duration {
		*w = rateWindow{StartedAt: now}
	}
}

type rateReservationState struct {
	Window   rateWindow
	InFlight int
}

// InMemoryRateLimiter mirrors the fixed-window Redis limiter while keeping a
// hard budget for key cardinality and bytes. Each key costs O(1) memory even
// when an administrator configures a very large request limit.
type InMemoryRateLimiter struct {
	initOnce sync.Once
	store    *cachex.ByteCache[rateWindow]
	mu       sync.Mutex
	// Active keys are pinned independently of the evictable committed cache.
	// Keep only one aggregate per key, bounded by the same key and byte limits;
	// reject new keys when full rather than evicting a live reservation.
	reservations     map[string]*rateReservationState
	reservationBytes int64
}

func (l *InMemoryRateLimiter) Init(_ time.Duration) {
	l.initOnce.Do(func() {
		l.store = cachex.NewByteCache[rateWindow](rateLimitMaxKeys, rateLimitMaxBytes, func(key string, _ rateWindow) int64 {
			return int64(len(key) + 40)
		})
		l.reservations = make(map[string]*rateReservationState)
	})
}

// Request records one request and reports whether it is within the limit.
// The duration parameter is in seconds.
func (l *InMemoryRateLimiter) Request(key string, maxRequestNum int, duration int64) bool {
	if maxRequestNum == 0 {
		return true
	}
	if maxRequestNum < 0 || duration <= 0 {
		return false
	}
	l.Init(0)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	window := rateLimitDuration(duration)
	if active := l.reservations[key]; active != nil {
		active.Window.refresh(now, window)
		if active.Window.Count >= maxRequestNum-active.InFlight {
			return false
		}
		active.Window.Count++
		l.store.SetWithTTL(key, active.Window, window)
		return true
	}
	allowed := false
	state, stored := l.store.Compute(key, window, func(current rateWindow, found bool) (rateWindow, bool) {
		if !found {
			current = rateWindow{}
		}
		current.refresh(now, window)
		if current.Count < maxRequestNum {
			current.Count++
			allowed = true
		}
		return current, true
	})
	return stored && allowed && state.Count > 0
}

// Check reports whether a request would be allowed without recording it.
// The duration parameter is in seconds.
func (l *InMemoryRateLimiter) Check(key string, maxRequestNum int, duration int64) bool {
	if maxRequestNum == 0 {
		return true
	}
	if maxRequestNum < 0 || duration <= 0 {
		return false
	}
	l.Init(0)
	l.mu.Lock()
	defer l.mu.Unlock()
	window := rateLimitDuration(duration)
	if active := l.reservations[key]; active != nil {
		active.Window.refresh(time.Now(), window)
		return active.Window.Count < maxRequestNum-active.InFlight
	}
	state, found := l.store.Load(key)
	return !found || time.Since(state.StartedAt) >= window || state.Count < maxRequestNum
}

// Reserve atomically admits one potential successful request. Its completion
// callback must be called on every exit path: false releases capacity, true
// converts it to a committed success. Repeated or concurrent completion is a
// no-op. Live requests continue occupying capacity across window expiry and
// committed-cache eviction; a successful completion is charged in the current
// window, matching Request's completion-time accounting.
func (l *InMemoryRateLimiter) Reserve(key string, maxRequestNum int, duration int64) (func(bool), bool) {
	if maxRequestNum == 0 {
		return func(bool) {}, true
	}
	if maxRequestNum < 0 || duration <= 0 {
		return nil, false
	}
	l.Init(0)
	l.mu.Lock()
	window := rateLimitDuration(duration)
	now := time.Now()
	active := l.reservations[key]
	if active == nil {
		current, _ := l.store.Load(key)
		current.refresh(now, window)
		weight := rateReservationWeight(key)
		if current.Count >= maxRequestNum || len(l.reservations) >= rateLimitMaxKeys || weight > rateLimitMaxBytes-l.reservationBytes {
			l.mu.Unlock()
			return nil, false
		}
		active = &rateReservationState{Window: current}
		l.reservations[key] = active
		l.reservationBytes += weight
	}
	active.Window.refresh(now, window)
	if active.Window.Count >= maxRequestNum-active.InFlight {
		l.mu.Unlock()
		return nil, false
	}
	active.InFlight++
	l.mu.Unlock()

	var once sync.Once
	return func(success bool) {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			active.InFlight--
			if success {
				now := time.Now()
				active.Window.refresh(now, window)
				if active.Window.Count == 0 {
					// The success window begins at the first completion, not
					// admission; slow requests must retain the full quota window.
					active.Window.StartedAt = now
				}
				if active.Window.Count < math.MaxInt {
					active.Window.Count++
				}
			}
			l.store.SetWithTTL(key, active.Window, window)
			if active.InFlight == 0 {
				delete(l.reservations, key)
				l.reservationBytes -= rateReservationWeight(key)
			}
		})
	}, true
}

func rateReservationWeight(key string) int64 {
	return int64(len(key)) + 80
}

func rateLimitDuration(seconds int64) time.Duration {
	if seconds > math.MaxInt64/int64(time.Second) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(seconds) * time.Second
}
