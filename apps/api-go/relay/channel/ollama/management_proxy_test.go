package ollama

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestManagementRequestsHonorProxyAndCancellation(t *testing.T) {
	cases := []struct {
		name, method, path, response string
		call                         func(context.Context, string, dto.ChannelSettings) error
	}{
		{"tags", "GET", "/api/tags", `{"models":[]}`, func(ctx context.Context, base string, s dto.ChannelSettings) error {
			_, err := FetchOllamaModels(ctx, base, "test-key", s)
			return err
		}},
		{"version", "GET", "/api/version", `{"version":"test"}`, func(ctx context.Context, base string, s dto.ChannelSettings) error {
			_, err := FetchOllamaVersion(ctx, base, "test-key", s)
			return err
		}},
		{"pull", "POST", "/api/pull", `{"status":"success"}`, func(ctx context.Context, base string, s dto.ChannelSettings) error {
			return PullOllamaModel(ctx, base, "test-key", "test-model", s)
		}},
		{"pull-stream", "POST", "/api/pull", "{\"status\":\"success\"}\n", func(ctx context.Context, base string, s dto.ChannelSettings) error {
			return PullOllamaModelStream(ctx, base, "test-key", "test-model", s, nil)
		}},
		{"delete", "DELETE", "/api/delete", "", func(ctx context.Context, base string, s dto.ChannelSettings) error {
			return DeleteOllamaModel(ctx, base, "test-key", "test-model", s)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != tc.method || r.URL.Path != tc.path || r.Header.Get("Authorization") != "Bearer test-key" {
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				fmt.Fprint(w, tc.response)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// An unresolvable base ensures proxy requests cannot silently fall back to direct traffic.
			require.NoError(t, tc.call(ctx, "http://ollama.invalid", dto.ChannelSettings{Proxy: server.URL}))
			require.NoError(t, tc.call(ctx, server.URL, dto.ChannelSettings{}))
			require.EqualValues(t, 2, calls.Load())
			cancel()
			require.Error(t, tc.call(ctx, "http://ollama.invalid", dto.ChannelSettings{Proxy: server.URL}))
			require.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestProxiedDiscoveryCancelsWhileWaitingForHeaders(t *testing.T) {
	started, released := make(chan struct{}), make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(released)
	}))
	defer proxy.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := FetchOllamaModels(ctx, "http://ollama.invalid", "test-key", dto.ChannelSettings{Proxy: proxy.URL})
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("proxy request never started")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not cancel")
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("proxy request was not released")
	}
}

func TestProxiedDiscoveryCancelsStalledResponseBody(t *testing.T) {
	started, released := make(chan struct{}), make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[`)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(released)
	}))
	defer proxy.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := FetchOllamaModels(ctx, "http://ollama.invalid", "test-key", dto.ChannelSettings{Proxy: proxy.URL})
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("proxy did not send response headers")
	}
	// A partial JSON body must not be mistaken for a successful empty model list.
	select {
	case err := <-done:
		t.Fatalf("discovery returned before body completion or cancellation: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("body read did not cancel")
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("stalled proxy response was not released")
	}
}
