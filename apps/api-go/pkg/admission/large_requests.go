// Package admission provides process-local admission controls without reading
// request payloads or retaining rejected/waiting requests.
package admission

import (
	"net/http"
	"sync"
	"sync/atomic"
)

const DefaultLargeRequestThreshold = int64(4 << 20)

// LargeRequestLimiter bounds in-flight HTTP requests whose bodies are large or
// of unknown length. It is shared across routes within one server process, not
// across a cluster. It supplements, rather than replaces, byte and memory caps.
type LargeRequestLimiter struct {
	limit     int64
	threshold int64
	active    atomic.Int64
}

// NewLargeRequestLimiter disables admission when limit <= 0. A nonpositive
// threshold uses the default 4 MiB threshold.
func NewLargeRequestLimiter(limit, threshold int64) *LargeRequestLimiter {
	if threshold <= 0 {
		threshold = DefaultLargeRequestThreshold
	}
	return &LargeRequestLimiter{limit: limit, threshold: threshold}
}

func noRelease() {}

// TryAcquire never reads or closes Body and never queues a caller. On success,
// the caller must defer release until its handler (including streaming) exits.
// release is idempotent. Bodies with unknown length conservatively take a slot;
// compressed bodies must have their wire ContentLength invalidated beforehand.
func (l *LargeRequestLimiter) TryAcquire(r *http.Request) (release func(), ok bool) {
	if l.limit <= 0 || r == nil || r.Body == nil || r.Body == http.NoBody {
		return noRelease, true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return noRelease, true
	}
	if r.ContentLength > 0 && r.ContentLength < l.threshold {
		return noRelease, true
	}
	for {
		active := l.active.Load()
		if active >= l.limit {
			return nil, false
		}
		if l.active.CompareAndSwap(active, active+1) {
			return sync.OnceFunc(func() { l.active.Add(-1) }), true
		}
	}
}
