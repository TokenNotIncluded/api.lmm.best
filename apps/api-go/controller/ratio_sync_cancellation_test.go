package controller

import (
	"bytes"
	"context"
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

func TestFetchUpstreamRatiosCancellationInterruptsRetryBackoff(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var requests atomic.Int32
	requested := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(requested)
		}
		time.Sleep(100 * time.Millisecond)
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("test server does not support connection hijacking")
			return
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requestBody := fmt.Sprintf("{\"timeout\":5,\"upstreams\":[{\"name\":\"slow-failure\",\"base_url\":%q,\"endpoint\":\"/api/pricing\"}]}", server.URL)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/ratio-sync/fetch", bytes.NewBufferString(requestBody)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")

	done := make(chan struct{})
	go func() {
		FetchUpstreamRatios(c)
		close(done)
	}()
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("first upstream request did not start")
	}
	time.Sleep(150 * time.Millisecond)
	cancel()

	startedCancel := time.Now()
	select {
	case <-done:
		elapsed := time.Since(startedCancel)
		require.Less(t, elapsed, 120*time.Millisecond, "cancellation should interrupt retry backoff")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ratio sync did not return promptly after cancellation")
	}
}

func TestFetchUpstreamRatiosPreCancelledRequestDoesNotStartUpstreams(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var upstreams strings.Builder
	upstreams.WriteString("{\"timeout\":5,\"upstreams\":[")
	for i := 0; i < maxConcurrentFetches*2; i++ {
		if i > 0 {
			upstreams.WriteByte(',')
		}
		fmt.Fprintf(&upstreams, "{\"name\":\"cancelled-%d\",\"base_url\":%q,\"endpoint\":\"/api/pricing\"}", i, server.URL)
	}
	upstreams.WriteString("]}")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/ratio-sync/fetch", strings.NewReader(upstreams.String())).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")

	FetchUpstreamRatios(c)
	require.Zero(t, requests.Load())
}
