package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestOllamaDiscoveryHonorsChannelProxy(t *testing.T) {
	var directCalls, proxyCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directCalls.Add(1)
		_, _ = w.Write([]byte(`{"models":[{"name":"wrong-direct-route"}]}`))
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.URL.String() != upstream.URL+"/api/tags" || r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unexpected proxy request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"proxied-model"}]}`))
	}))
	defer proxy.Close()
	channel := &model.Channel{Type: constant.ChannelTypeOllama, Key: "test-key", BaseURL: &upstream.URL}
	channel.SetSetting(dto.ChannelSettings{Proxy: proxy.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, err := fetchChannelUpstreamModelIDs(ctx, channel)
	require.NoError(t, err)
	require.Equal(t, []string{"proxied-model"}, models)
	require.EqualValues(t, 1, proxyCalls.Load())
	require.Zero(t, directCalls.Load())
}

func TestScheduledOllamaDiscoveryDeadlineReleasesStalledUpstream(t *testing.T) {
	t.Setenv("CHANNEL_UPSTREAM_MODEL_UPDATE_FETCH_TIMEOUT_SECONDS", "1")
	for _, partialBody := range []bool{false, true} {
		name := "waiting for headers"
		if partialBody {
			name = "waiting for body"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			released := make(chan struct{})
			stop := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if partialBody {
					_, _ = w.Write([]byte(`{"models":[`))
					w.(http.Flusher).Flush()
				}
				close(started)
				select {
				case <-r.Context().Done():
					close(released)
				case <-stop:
				}
			}))
			defer server.Close()
			defer close(stop)

			channel := &model.Channel{Type: constant.ChannelTypeOllama, BaseURL: &server.URL}
			parent, cancelParent := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelParent()
			fetchCtx, cancelFetch := newChannelUpstreamModelUpdateFetchContext(parent)
			defer cancelFetch()
			deadline, ok := fetchCtx.Deadline()
			require.True(t, ok)
			require.LessOrEqual(t, time.Until(deadline), time.Second)

			done := make(chan error, 1)
			go func() {
				_, err := fetchChannelUpstreamModelIDs(fetchCtx, channel)
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("Ollama request did not start")
			}
			select {
			case err := <-done:
				require.ErrorContains(t, err, "context deadline exceeded")
			case <-time.After(3 * time.Second):
				t.Fatal("stalled Ollama discovery outlived its per-channel deadline")
			}
			select {
			case <-released:
			case <-time.After(2 * time.Second):
				t.Fatal("deadline did not release the upstream request")
			}
		})
	}
}
