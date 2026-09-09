package cachex

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestByteCacheHonorsCountAndByteBudgets(t *testing.T) {
	cache := NewByteCache[string](3, 7, func(key, value string) int64 {
		return int64(len(key) + len(value))
	})
	cache.SetWithTTL("a", "123", time.Minute)
	cache.SetWithTTL("b", "456", time.Minute)
	assert.LessOrEqual(t, cache.Bytes(), int64(7))
	_, firstFound, err := cache.Get("a")
	require.NoError(t, err)
	_, secondFound, err := cache.Get("b")
	require.NoError(t, err)
	assert.NotEqual(t, firstFound, secondFound, "the byte budget must evict one entry")
	cache.SetWithTTL("oversized", "value", time.Minute)
	_, found, err := cache.Get("oversized")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestByteCacheComputeIsAtomicAndBounded(t *testing.T) {
	cache := NewByteCache[int](32, 1<<10, func(key string, _ int) int64 { return int64(len(key) + 8) })
	for i := 0; i < 10_000; i++ {
		key := fmt.Sprintf("key-%d", i)
		value, stored := cache.Compute(key, time.Minute, func(current int, _ bool) (int, bool) {
			return current + 1, true
		})
		require.True(t, stored)
		require.Equal(t, 1, value)
	}
	assert.LessOrEqual(t, cache.Len(), 32)
	assert.LessOrEqual(t, cache.Bytes(), int64(1<<10))
}

func TestByteCacheExpiresWithoutJanitor(t *testing.T) {
	cache := NewByteCache[string](2, 64, func(key, value string) int64 { return int64(len(key) + len(value)) })
	cache.SetWithTTL("key", "value", time.Nanosecond)
	time.Sleep(time.Millisecond)
	_, found, err := cache.Get("key")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Zero(t, cache.Bytes())
}

func TestByteCacheMapCompatibilityRemainsBounded(t *testing.T) {
	cache := NewByteCache[int](1, 64, func(key string, _ int) int64 { return int64(len(key) + 8) })
	actual, loaded := cache.LoadOrStore("first", 1)
	assert.False(t, loaded)
	assert.Equal(t, 1, actual)
	actual, loaded = cache.LoadOrStore("first", 2)
	assert.True(t, loaded)
	assert.Equal(t, 1, actual)
	cache.Store("second", 2)
	_, found := cache.Load("first")
	assert.False(t, found)
	cache.Delete("second")
	assert.Empty(t, cache.Keys())
}

func TestByteCacheReplacement(t *testing.T) {
	for _, method := range []string{"SetWithTTL", "Compute"} {
		t.Run(method, func(t *testing.T) {
			cache := NewByteCache[string](3, 9, func(key, value string) int64 {
				return int64(len(key) + len(value))
			})
			replace := func(value string, ttl time.Duration) {
				if method == "Compute" {
					_, stored := cache.Compute("a", ttl, func(current string, found bool) (string, bool) {
						require.True(t, found)
						require.NotEmpty(t, current)
						return value, true
					})
					require.True(t, stored)
				} else {
					cache.SetWithTTL("a", value, ttl)
				}
			}
			cache.SetWithTTL("a", "12", time.Hour)
			cache.Store("b", "12")
			cache.Store("c", "12")
			replace("12345", 0)
			assert.Equal(t, int64(9), cache.Bytes())
			assert.Equal(t, []string{"a", "c"}, cache.Keys(), "replacement becomes most recently used")
			_, found := cache.Load("b")
			assert.False(t, found, "growth evicts the oldest other entry")
			assert.True(t, cache.entries["a"].Value.(*byteCacheEntry[string]).expiresAt.IsZero(), "zero TTL clears old expiration")
			replace("1", time.Hour)
			assert.Equal(t, int64(5), cache.Bytes(), "shrink releases the previous weight")
			assert.True(t, cache.entries["a"].Value.(*byteCacheEntry[string]).expiresAt.After(time.Now()))
			value, found := cache.Load("a")
			assert.True(t, found)
			assert.Equal(t, "1", value)
		})
	}
}

func TestByteCacheRejectedReplacement(t *testing.T) {
	cache := NewByteCache[string](2, 4, func(_ string, value string) int64 { return int64(len(value)) })
	cache.Store("key", "old")
	cache.Store("key", "oversized")
	value, found := cache.Load("key")
	assert.True(t, found, "SetWithTTL preserves the old value when replacement is rejected")
	assert.Equal(t, "old", value)
	_, stored := cache.Compute("key", 0, func(_ string, _ bool) (string, bool) { return "oversized", true })
	assert.False(t, stored)
	assert.Zero(t, cache.Len(), "Compute removes the old value when replacement is rejected")
	assert.Zero(t, cache.Bytes())
	cache.Store("key", "old")
	_, stored = cache.Compute("key", 0, func(_ string, _ bool) (string, bool) { return "", false })
	assert.False(t, stored)
	assert.Zero(t, cache.Len())
	assert.Zero(t, cache.Bytes())
}

func TestByteCacheComputeExpiredAndPurge(t *testing.T) {
	cache := NewByteCache[int](65_536, 8<<20, nil)
	cache.Store("key", 1)
	cache.entries["key"].Value.(*byteCacheEntry[int]).expiresAt = time.Now().Add(-time.Second)
	value, stored := cache.Compute("key", 0, func(current int, found bool) (int, bool) {
		assert.False(t, found)
		assert.Zero(t, current)
		return 2, true
	})
	assert.True(t, stored)
	assert.Equal(t, 2, value)
	assert.Equal(t, int64(3), cache.Bytes())
	cache.Purge()
	assert.Zero(t, cache.Len())
	assert.Zero(t, cache.Bytes())
	assert.Empty(t, cache.Keys())
	cache.Store("after", 3)
	value, found := cache.Load("after")
	assert.True(t, found)
	assert.Equal(t, 3, value)
	assert.Equal(t, int64(5), cache.Bytes())
}

func TestByteCacheConcurrentCompute(t *testing.T) {
	cache := NewByteCache[int](1, 64, nil)
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for range 1_000 {
				cache.Compute("counter", 0, func(current int, _ bool) (int, bool) {
					return current + 1, true
				})
			}
		})
	}
	workers.Wait()
	value, found := cache.Load("counter")
	assert.True(t, found)
	assert.Equal(t, 16_000, value)
	assert.Equal(t, int64(len("counter")), cache.Bytes())
}
