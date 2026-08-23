package common

import (
	"fmt"
	"math"
	"testing"
	"time"
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
