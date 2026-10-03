package common

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/pkg/cachex"
)

func TestInMemoryRateLimiterUsesConstantSpacePerKey(t *testing.T) {
	limiter := &InMemoryRateLimiter{}
	limiter.Init(time.Minute)

	for range 1_000 {
		if !limiter.Request("user", math.MaxInt, 60) {
			t.Fatal("request was unexpectedly rejected")
		}
	}
	state, found := limiter.store.Load("user")
	if !found || state.Count != 1_000 {
		t.Fatalf("stored state = %+v, found=%v", state, found)
	}
	if limiter.store.Len() != 1 {
		t.Fatalf("entry count = %d, want 1", limiter.store.Len())
	}
}

func TestInMemoryRateLimiterReservationConcurrentAdmission(t *testing.T) {
	limiter := &InMemoryRateLimiter{}
	var wg sync.WaitGroup
	completions := make(chan func(bool), 64)
	for range cap(completions) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if complete, allowed := limiter.Reserve("user", 1, 60); allowed {
				completions <- complete
			}
		}()
	}
	wg.Wait()
	close(completions)
	if len(completions) != 1 {
		t.Fatalf("concurrent admissions = %d, want 1", len(completions))
	}
	if limiter.Check("user", 1, 60) || limiter.Request("user", 1, 60) {
		t.Fatal("a live reservation must occupy capacity for every admission API")
	}
	for complete := range completions {
		complete(false)
	}
	complete, allowed := limiter.Reserve("user", 1, 60)
	if !allowed {
		t.Fatal("failed request did not release capacity")
	}
	complete(false)
	if len(limiter.reservations) != 0 || limiter.reservationBytes != 0 {
		t.Fatal("completed reservations retained active-key state")
	}
}

func TestInMemoryRateLimiterReservationCompletionIsIdempotent(t *testing.T) {
	for _, successful := range []bool{false, true} {
		t.Run(fmt.Sprintf("success=%t", successful), func(t *testing.T) {
			limiter := &InMemoryRateLimiter{}
			complete, allowed := limiter.Reserve("user", 1, 60)
			if !allowed {
				t.Fatal("first reservation was rejected")
			}
			complete(successful)
			var wg sync.WaitGroup
			for range 32 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					complete(!successful)
					complete(successful)
				}()
			}
			wg.Wait()
			state, found := limiter.store.Load("user")
			want := 0
			if successful {
				want = 1
			}
			if !found || state.Count != want || len(limiter.reservations) != 0 || limiter.reservationBytes != 0 {
				t.Fatalf("state=%+v, found=%t, active=%d, bytes=%d", state, found, len(limiter.reservations), limiter.reservationBytes)
			}
			if limiter.Check("user", 1, 60) == successful {
				t.Fatal("completion charged the wrong success quota")
			}
		})
	}
}

func TestInMemoryRateLimiterReservationSurvivesExpiry(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(fmt.Sprintf("success=%t", success), func(t *testing.T) {
			limiter := &InMemoryRateLimiter{}
			complete, allowed := limiter.Reserve("user", 1, 1)
			if !allowed {
				t.Fatal("first reservation was rejected")
			}
			limiter.mu.Lock()
			limiter.reservations["user"].Window.StartedAt = time.Now().Add(-2 * time.Second)
			limiter.store.SetWithTTL("user", limiter.reservations["user"].Window, time.Nanosecond)
			limiter.mu.Unlock()
			time.Sleep(time.Millisecond)
			if _, found := limiter.store.Load("user"); found {
				t.Fatal("test cache entry did not expire")
			}
			if _, allowed := limiter.Reserve("user", 1, 1); allowed || limiter.Check("user", 1, 1) {
				t.Fatal("window/cache expiry discarded live capacity")
			}
			complete(success)
			if limiter.Check("user", 1, 1) == success {
				t.Fatal("completion after expiry charged the wrong current-window quota")
			}
		})
	}
}

func TestInMemoryRateLimiterReservationSurvivesEviction(t *testing.T) {
	limiter := &InMemoryRateLimiter{}
	limiter.Init(0)
	limiter.store = cachex.NewByteCache[rateWindow](1, 1_024, nil)
	first, allowed := limiter.Reserve("user", 2, 60)
	if !allowed {
		t.Fatal("first reservation was rejected")
	}
	second, allowed := limiter.Reserve("user", 2, 60)
	if !allowed {
		t.Fatal("second reservation was rejected")
	}
	first(true)
	limiter.Request("other", 1, 60)
	if _, found := limiter.store.Load("user"); found {
		t.Fatal("test cache entry did not evict")
	}
	if _, allowed := limiter.Reserve("user", 2, 60); allowed {
		t.Fatal("eviction lost committed success while another reservation was live")
	}
	second(false)
	state, found := limiter.store.Load("user")
	if !found || state.Count != 1 || len(limiter.reservations) != 0 {
		t.Fatalf("final state=%+v, found=%t, active=%d", state, found, len(limiter.reservations))
	}
	third, allowed := limiter.Reserve("user", 2, 60)
	if !allowed {
		t.Fatal("remaining unconsumed capacity was not released")
	}
	third(false)
}

func TestInMemoryRateLimiterReservationBoundsDistinctKeys(t *testing.T) {
	limiter := &InMemoryRateLimiter{}
	completions := make([]func(bool), 0, rateLimitMaxKeys)
	for i := range rateLimitMaxKeys {
		complete, allowed := limiter.Reserve(fmt.Sprintf("key-%d", i), 1, 60)
		if !allowed {
			t.Fatalf("reservation %d was unexpectedly rejected", i)
		}
		completions = append(completions, complete)
	}
	if _, allowed := limiter.Reserve("overflow", 1, 60); allowed {
		t.Fatal("distinct active keys exceeded the cardinality budget")
	}
	if len(limiter.reservations) != rateLimitMaxKeys || limiter.reservationBytes > rateLimitMaxBytes {
		t.Fatal("active state exceeded its hard budgets")
	}
	completions[0](false)
	complete, allowed := limiter.Reserve("overflow", 1, 60)
	if !allowed {
		t.Fatal("completion did not free key capacity")
	}
	complete(false)
	for _, complete := range completions {
		complete(false)
	}
	if len(limiter.reservations) != 0 || limiter.reservationBytes != 0 || limiter.store.Len() > rateLimitMaxKeys || limiter.store.Bytes() > rateLimitMaxBytes {
		t.Fatal("limiter retained unbounded state after completion")
	}
}

func TestInMemoryRateLimiterReservationBoundsBytes(t *testing.T) {
	limiter := &InMemoryRateLimiter{}
	key := strings.Repeat("x", rateLimitMaxBytes/4)
	var completions []func(bool)
	for i := range 3 {
		complete, allowed := limiter.Reserve(fmt.Sprintf("%d%s", i, key), 1, 60)
		if !allowed {
			t.Fatal("reservation within byte budget was rejected")
		}
		completions = append(completions, complete)
	}
	if _, allowed := limiter.Reserve("overflow"+key, 1, 60); allowed {
		t.Fatal("active state exceeded the byte budget")
	}
	for _, complete := range completions {
		complete(false)
	}
	if _, allowed := limiter.Reserve(strings.Repeat("x", rateLimitMaxBytes), 1, 60); allowed {
		t.Fatal("an individually oversized key was admitted")
	}
	if limiter.reservationBytes != 0 || len(limiter.reservations) != 0 {
		t.Fatal("byte-bounded reservations leaked after completion")
	}
}

func TestInMemoryRateLimiterReservationConstantSpacePerKey(t *testing.T) {
	limiter := &InMemoryRateLimiter{}
	completions := make([]func(bool), 0, 1_000)
	for range cap(completions) {
		complete, allowed := limiter.Reserve("user", math.MaxInt, math.MaxInt64)
		if !allowed {
			t.Fatal("valid large-window reservation was rejected")
		}
		completions = append(completions, complete)
	}
	if len(limiter.reservations) != 1 || limiter.reservationBytes != rateReservationWeight("user") {
		t.Fatal("active state grew with same-key request count")
	}
	for _, complete := range completions {
		complete(true)
	}
	state, found := limiter.store.Load("user")
	if !found || state.Count != 1_000 || len(limiter.reservations) != 0 {
		t.Fatalf("final state=%+v, found=%t, active=%d", state, found, len(limiter.reservations))
	}
}

func TestInMemoryRateLimiterSuccessWindowStartsAtCompletion(t *testing.T) {
	limiter := &InMemoryRateLimiter{}
	complete, allowed := limiter.Reserve("user", 1, 60)
	if !allowed {
		t.Fatal("first reservation was rejected")
	}
	limiter.mu.Lock()
	limiter.reservations["user"].Window.StartedAt = time.Now().Add(-30 * time.Second)
	limiter.mu.Unlock()
	beforeCompletion := time.Now()
	complete(true)
	state, found := limiter.store.Load("user")
	if !found || state.StartedAt.Before(beforeCompletion) {
		t.Fatalf("successful window started before completion: %+v", state)
	}
}

func TestInMemoryRateLimiterRejectsAtLimit(t *testing.T) {
	limiter := &InMemoryRateLimiter{}
	firstOK := limiter.Request("user", 2, 60)
	secondOK := limiter.Request("user", 2, 60)
	if !firstOK || !secondOK {
		t.Fatal("requests within the limit were rejected")
	}
	if limiter.Request("user", 2, 60) {
		t.Fatal("request beyond the limit was allowed")
	}
	if limiter.Check("user", 2, 60) {
		t.Fatal("check beyond the limit was allowed")
	}
}

func TestInMemoryRateLimiterBoundsDistinctKeys(t *testing.T) {
	limiter := &InMemoryRateLimiter{}
	for i := 0; i < rateLimitMaxKeys+1_000; i++ {
		limiter.Request(fmt.Sprintf("key-%d", i), 1, 60)
	}
	if limiter.store.Len() > rateLimitMaxKeys {
		t.Fatalf("entry count = %d, max = %d", limiter.store.Len(), rateLimitMaxKeys)
	}
	if limiter.store.Bytes() > rateLimitMaxBytes {
		t.Fatalf("cache bytes = %d, max = %d", limiter.store.Bytes(), rateLimitMaxBytes)
	}
}
