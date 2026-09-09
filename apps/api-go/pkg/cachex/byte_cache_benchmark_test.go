package cachex

import (
	"strconv"
	"testing"
	"time"
)

func BenchmarkByteCacheBoundedSet(b *testing.B) {
	cache := NewByteCache[string](256, 1<<20, func(key, value string) int64 {
		return int64(len(key) + len(value))
	})
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		cache.SetWithTTL(strconv.Itoa(i), "value", time.Minute)
	}
	if cache.Len() > 256 || cache.Bytes() > 1<<20 {
		b.Fatal("cache exceeded its budget")
	}
}

var benchmarkByteCache *ByteCache[int]

func BenchmarkByteCacheIdle(b *testing.B) {
	for _, capacity := range []int{256, 16_384, 65_536} {
		b.Run(strconv.Itoa(capacity), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkByteCache = NewByteCache[int](capacity, 8<<20, nil)
			}
		})
	}
}

func BenchmarkByteCacheUpdate(b *testing.B) {
	for _, compute := range []bool{false, true} {
		name := "SetWithTTL"
		if compute {
			name = "Compute"
		}
		b.Run(name, func(b *testing.B) {
			cache := NewByteCache[int](65_536, 8<<20, func(key string, _ int) int64 {
				return int64(len(key) + 8)
			})
			cache.Store("hot-key", 0)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if compute {
					cache.Compute("hot-key", time.Minute, func(current int, _ bool) (int, bool) {
						return current + 1, true
					})
				} else {
					cache.SetWithTTL("hot-key", 1, time.Minute)
				}
			}
		})
	}
}

func BenchmarkByteCachePurge(b *testing.B) {
	cache := NewByteCache[int](65_536, 8<<20, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		cache.Store("key", 1)
		cache.Purge()
	}
}
