package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func runRatioQueueRequest(ctx context.Context, base string, count, timeout int) *httptest.ResponseRecorder {
	var body strings.Builder
	fmt.Fprintf(&body, `{"timeout":%d,"upstreams":[`, timeout)
	for i := 0; i < count; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"name":"queue-%d","base_url":%q,"endpoint":"/api/ratio_config"}`, i, base)
	}
	body.WriteString(`]}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/ratio_sync/fetch", strings.NewReader(body.String())).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	FetchUpstreamRatios(c)
	return w
}

func TestRatioSyncCancelsAnOccupiedQueue(t *testing.T) {
	var calls atomic.Int32
	occupied := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == maxConcurrentFetches {
			close(occupied)
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { runRatioQueueRequest(ctx, server.URL, maxConcurrentFetches*2, 10); close(done) }()
	select {
	case <-occupied:
	case <-time.After(5 * time.Second):
		t.Fatal("queue never filled")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("occupied queue ignored cancellation")
	}
	require.EqualValues(t, maxConcurrentFetches, calls.Load(), "queued requests must never reach upstream")
}

func TestRatioSyncQueueDoesNotConsumeRequestBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-time.After(650 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"model_ratio":{"queue-test":1}}}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	w := runRatioQueueRequest(ctx, server.URL, maxConcurrentFetches*2, 1)
	var response struct {
		Data struct {
			Results []struct{ Status string } `json:"test_results"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.Data.Results, maxConcurrentFetches*2)
	for _, result := range response.Data.Results {
		require.Equal(t, "success", result.Status)
	}
	require.EqualValues(t, maxConcurrentFetches*2, calls.Load())
}

func TestRatioSyncLastFailureHasNoBackoff(t *testing.T) {
	var calls atomic.Int32
	lastFailure := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		if calls.Add(1) == 3 {
			close(lastFailure)
		}
		_ = conn.Close()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { runRatioQueueRequest(ctx, server.URL, 1, 5); close(done) }()
	select {
	case <-lastFailure:
	case <-time.After(5 * time.Second):
		t.Fatal("three attempts not observed")
	}
	select {
	case <-done:
	case <-time.After(600 * time.Millisecond):
		t.Fatal("waited after final failed attempt")
	}
	require.EqualValues(t, 3, calls.Load())
}
