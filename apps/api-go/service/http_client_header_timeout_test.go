package service

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestRelayResponseHeaderTimeoutConfiguration(t *testing.T) {
	original, originalDefault := common.RelayResponseHeaderTimeout, http.DefaultTransport
	t.Cleanup(func() {
		common.RelayResponseHeaderTimeout = original
		http.DefaultTransport = originalDefault
	})
	// Disabling the setting must also override a cloned transport's timeout.
	http.DefaultTransport = &http.Transport{ResponseHeaderTimeout: time.Hour}
	for _, seconds := range []int{1800, 0, -1, math.MaxInt} {
		t.Run(strconv.Itoa(seconds), func(t *testing.T) {
			common.RelayResponseHeaderTimeout = seconds
			transport := newRelayHTTPTransport()
			defer transport.CloseIdleConnections()
			var want time.Duration
			if seconds > 0 {
				want = time.Duration(min(int64(seconds), maxTimeoutSeconds)) * time.Second
			}
			require.Equal(t, want, transport.ResponseHeaderTimeout)
		})
	}
}

func TestRelayHeaderTimeoutBoundsWaitButNotStreaming(t *testing.T) {
	for _, flushHeaders := range []bool{false, true} {
		t.Run(strconv.FormatBool(flushHeaders), func(t *testing.T) {
			release := make(chan struct{})
			releaseBody := sync.OnceFunc(func() { close(release) })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if flushHeaders {
					w.Header().Set("Content-Type", "text/event-stream")
					w.(http.Flusher).Flush()
				}
				select {
				case <-release:
					_, _ = io.WriteString(w, "data: done\n\n")
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			defer releaseBody()
			transport := newRelayHTTPTransport()
			transport.ResponseHeaderTimeout = 50 * time.Millisecond
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			require.NoError(t, err)
			response, err := client.Do(request)
			if !flushHeaders {
				require.ErrorContains(t, err, "timeout awaiting response headers")
				return
			}
			require.NoError(t, err)
			defer response.Body.Close()
			// Send a body chunk only after the header deadline has elapsed.
			timer := time.AfterFunc(3*transport.ResponseHeaderTimeout, releaseBody)
			defer timer.Stop()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, "data: done\n\n", string(body))
		})
	}
}
