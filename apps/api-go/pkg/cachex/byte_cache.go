package cachex

import (
	"container/list"
	"sync"
	"time"
)

type byteCacheEntry[V any] struct {
	key       string
	value     V
	bytes     int64
	expiresAt time.Time
}

// ByteCache is a concurrency-safe LRU bounded by both entry count and bytes.
// It intentionally has no janitor goroutine: expiration is handled on access,
// which avoids one long-lived goroutine per cache instance. The index grows
// on demand; maxEntries is an eviction limit, not an initial allocation size.
type ByteCache[V any] struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	order      *list.List
	maxEntries int
	maxBytes   int64
	usedBytes  int64
	weigh      func(string, V) int64
}

func NewByteCache[V any](maxEntries int, maxBytes int64, weigh func(string, V) int64) *ByteCache[V] {
	if maxEntries < 1 {
		maxEntries = 1
	}
	if maxBytes < 1 {
		maxBytes = 1
	}
	if weigh == nil {
		weigh = func(key string, _ V) int64 { return int64(len(key)) }
	}
	return &ByteCache[V]{
		entries: make(map[string]*list.Element), order: list.New(),
		maxEntries: maxEntries, maxBytes: maxBytes, weigh: weigh,
	}
}

func (c *ByteCache[V]) Get(key string) (V, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		var zero V
		return zero, false, nil
	}
	entry := element.Value.(*byteCacheEntry[V])
	if !entry.expiresAt.IsZero() && !time.Now().Before(entry.expiresAt) {
		c.remove(element)
		var zero V
		return zero, false, nil
	}
	c.order.MoveToFront(element)
	return entry.value, true, nil
}

func (c *ByteCache[V]) Load(key string) (V, bool) {
	value, found, _ := c.Get(key)
	return value, found
}

func (c *ByteCache[V]) SetWithTTL(key string, value V, ttl time.Duration) {
	weight := c.weigh(key, value)
	if weight < 0 || weight > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store(key, value, weight, ttl)
}

func (c *ByteCache[V]) Store(key string, value V) {
	c.SetWithTTL(key, value, 0)
}

func (c *ByteCache[V]) LoadOrStore(key string, value V) (V, bool) {
	weight := c.weigh(key, value)
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		entry := element.Value.(*byteCacheEntry[V])
		if entry.expiresAt.IsZero() || time.Now().Before(entry.expiresAt) {
			c.order.MoveToFront(element)
			return entry.value, true
		}
		c.remove(element)
	}
	if weight < 0 || weight > c.maxBytes {
		return value, false
	}
	c.store(key, value, weight, 0)
	return value, false
}

// Compute atomically replaces one value. The callback runs while the cache is
// locked and must not call back into the same cache. keep=false removes the
// value. stored=false means the callback removed it or its weight exceeded the
// byte budget.
func (c *ByteCache[V]) Compute(
	key string,
	ttl time.Duration,
	update func(current V, found bool) (next V, keep bool),
) (next V, stored bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var current V
	found := false
	if element, ok := c.entries[key]; ok {
		entry := element.Value.(*byteCacheEntry[V])
		if !entry.expiresAt.IsZero() && !time.Now().Before(entry.expiresAt) {
			c.remove(element)
		} else {
			current = entry.value
			found = true
		}
	}

	next, keep := update(current, found)
	if !keep {
		if element, ok := c.entries[key]; ok {
			c.remove(element)
		}
		return next, false
	}
	weight := c.weigh(key, next)
	if weight < 0 || weight > c.maxBytes {
		if element, ok := c.entries[key]; ok {
			c.remove(element)
		}
		return next, false
	}
	c.store(key, next, weight, ttl)
	_, stored = c.entries[key]
	return next, stored
}

func (c *ByteCache[V]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.remove(element)
	}
}

func (c *ByteCache[V]) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	keys := make([]string, 0, len(c.entries))
	for element := c.order.Front(); element != nil; {
		next := element.Next()
		entry := element.Value.(*byteCacheEntry[V])
		if !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt) {
			c.remove(element)
		} else {
			keys = append(keys, entry.key)
		}
		element = next
	}
	return keys
}

func (c *ByteCache[V]) DeleteMany(keys []string) map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	deleted := make(map[string]bool, len(keys))
	for _, key := range keys {
		if element, ok := c.entries[key]; ok {
			c.remove(element)
			deleted[key] = true
		} else {
			deleted[key] = false
		}
	}
	return deleted
}

func (c *ByteCache[V]) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*list.Element)
	c.order.Init()
	c.usedBytes = 0
}

func (c *ByteCache[V]) Capacity() (int, int)        { return c.maxEntries, 0 }
func (c *ByteCache[V]) Algorithm() (string, string) { return "lru-bytes", "" }

func (c *ByteCache[V]) Bytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usedBytes
}

func (c *ByteCache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *ByteCache[V]) remove(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*byteCacheEntry[V])
	delete(c.entries, entry.key)
	c.order.Remove(element)
	c.usedBytes -= entry.bytes
}

// store requires c.mu and a validated weight. Reuse an existing entry and list
// node so frequently updated counters do not allocate on every request.
func (c *ByteCache[V]) store(key string, value V, weight int64, ttl time.Duration) {
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	if element, ok := c.entries[key]; ok {
		entry := element.Value.(*byteCacheEntry[V])
		c.usedBytes -= entry.bytes
		entry.value, entry.bytes, entry.expiresAt = value, weight, expiresAt
		c.order.MoveToFront(element)
	} else {
		entry := &byteCacheEntry[V]{key: key, value: value, bytes: weight, expiresAt: expiresAt}
		c.entries[key] = c.order.PushFront(entry)
	}
	c.usedBytes += weight
	for len(c.entries) > c.maxEntries || c.usedBytes > c.maxBytes {
		c.remove(c.order.Back())
	}
}
