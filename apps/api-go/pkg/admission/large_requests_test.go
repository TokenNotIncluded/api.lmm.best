package admission

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func requestWithLength(method string, size int64) *http.Request {
	r, _ := http.NewRequest(method, "http://example.test/v1/responses", strings.NewReader("body"))
	r.ContentLength = size
	return r
}

func TestLargeRequestLimiterCapacityAndBypasses(t *testing.T) {
	limiter := NewLargeRequestLimiter(2, 4)
	large := requestWithLength(http.MethodPost, 4)
	first, ok := limiter.TryAcquire(large)
	if !ok {
		t.Fatal("first request rejected")
	}
	defer first()
	second, ok := limiter.TryAcquire(large)
	if !ok {
		t.Fatal("second request rejected")
	}
	defer second()

	for _, size := range []int64{4, 5, -1, 0} {
		if release, ok := limiter.TryAcquire(requestWithLength(http.MethodPost, size)); ok {
			release()
			t.Fatalf("full pool admitted body length %d", size)
		}
	}
	for _, r := range []*http.Request{
		requestWithLength(http.MethodPost, 3),
		requestWithLength(http.MethodGet, 10),
		requestWithLength(http.MethodHead, 10),
		requestWithLength(http.MethodOptions, 10),
		{Method: http.MethodPost, Body: http.NoBody},
		{Method: http.MethodPost},
		nil,
	} {
		release, ok := limiter.TryAcquire(r)
		if !ok {
			t.Fatal("small/empty/read-only request rejected")
		}
		release()
	}
	if limiter.active.Load() != 2 {
		t.Fatal("bypasses changed the active count")
	}
	first()
	first() // an accidental duplicate release must not create extra slots
	third, ok := limiter.TryAcquire(large)
	if !ok {
		t.Fatal("released slot was not reusable")
	}
	defer third()
	if release, ok := limiter.TryAcquire(large); ok {
		release()
		t.Fatal("duplicate release created an extra slot")
	}
}

func TestLargeRequestLimiterConcurrentAcquisition(t *testing.T) {
	limiter := NewLargeRequestLimiter(2, 4)
	start := make(chan struct{})
	finish := make(chan struct{})
	stop := sync.OnceFunc(func() { close(finish) })
	defer stop()
	results := make(chan bool, 64)
	var wg sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, ok := limiter.TryAcquire(requestWithLength(http.MethodPost, 28<<20))
			results <- ok
			if ok {
				defer release()
				<-finish
			}
		}()
	}
	close(start)
	accepted := 0
	for i := 0; i < cap(results); i++ {
		if <-results {
			accepted++
		}
	}
	if accepted != 2 {
		t.Fatalf("accepted %d concurrent requests, want 2", accepted)
	}
	stop()
	wg.Wait()
	if limiter.active.Load() != 0 {
		t.Fatal("slots leaked")
	}
}

type unreadBody struct{ reads, closes int }

func (b *unreadBody) Read([]byte) (int, error) { b.reads++; return 0, io.EOF }
func (b *unreadBody) Close() error             { b.closes++; return nil }

func TestLargeRequestLimiterDoesNotTouchPayload(t *testing.T) {
	body := &unreadBody{}
	r := &http.Request{Method: http.MethodPost, ContentLength: -1, Body: body}
	limiter := NewLargeRequestLimiter(1, 4)
	release, ok := limiter.TryAcquire(r)
	if !ok {
		t.Fatal("first request rejected")
	}
	defer release()
	if extra, ok := limiter.TryAcquire(r); ok {
		extra()
		t.Fatal("full pool admitted request")
	}
	if body.reads != 0 || body.closes != 0 {
		t.Fatal("admission touched the body")
	}
}

func TestLargeRequestLimiterDisabledAndDefaultThreshold(t *testing.T) {
	for _, limit := range []int64{0, -1} {
		limiter := NewLargeRequestLimiter(limit, 4)
		for i := 0; i < 5; i++ {
			release, ok := limiter.TryAcquire(requestWithLength(http.MethodPost, 28<<20))
			if !ok {
				t.Fatal("disabled limiter rejected a request")
			}
			defer release()
		}
		if limiter.active.Load() != 0 {
			t.Fatal("disabled limiter tracked requests")
		}
	}
	limiter := NewLargeRequestLimiter(1, 0)
	release, ok := limiter.TryAcquire(requestWithLength(http.MethodPost, DefaultLargeRequestThreshold))
	if !ok {
		t.Fatal("first request rejected")
	}
	defer release()
	if next, ok := limiter.TryAcquire(requestWithLength(http.MethodPost, DefaultLargeRequestThreshold-1)); !ok {
		t.Fatal("small request rejected at default threshold")
	} else {
		next()
	}
}

func BenchmarkLargeRequestAdmission(b *testing.B) {
	for _, tc := range []struct {
		name        string
		limit, size int64
	}{
		{"disabled", 0, 28 << 20}, {"small", 2, 1024}, {"large", 2, 28 << 20},
	} {
		b.Run(tc.name, func(b *testing.B) {
			limiter := NewLargeRequestLimiter(tc.limit, DefaultLargeRequestThreshold)
			r := requestWithLength(http.MethodPost, tc.size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				release, ok := limiter.TryAcquire(r)
				if !ok {
					b.Fatal("unexpected rejection")
				}
				release()
			}
		})
	}
}
