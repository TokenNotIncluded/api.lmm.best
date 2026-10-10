package channel_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/relay/channel"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoRequestPingerWaitsForValidatedStreamCommit(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	previousEnabled, previousSeconds := settings.PingIntervalEnabled, settings.PingIntervalSeconds
	settings.PingIntervalEnabled, settings.PingIntervalSeconds = true, 1
	t.Cleanup(func() { settings.PingIntervalEnabled, settings.PingIntervalSeconds = previousEnabled, previousSeconds })
	service.InitHttpClient()
	for _, timeout := range []time.Duration{0, 100 * time.Millisecond} {
		t.Run(timeout.String(), func(t *testing.T) {
			gate := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(gate) }) }
			t.Cleanup(release)
			accepted := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(accepted)
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				io.WriteString(w, `{"error":"provider unavailable"}`)
			}))
			t.Cleanup(upstream.Close)
			router := gin.New()
			router.POST("/v1/chat/completions", func(c *gin.Context) {
				request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstream.URL, nil)
				if err != nil {
					c.Status(500)
					return
				}
				info := &relaycommon.RelayInfo{IsStream: true, FirstResponseTimeout: timeout, ChannelMeta: &relaycommon.ChannelMeta{}}
				response, err := channel.DoRequest(c, request, info)
				if err != nil {
					c.Status(502)
					return
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil {
					c.Status(502)
					return
				}
				c.Header("Content-Type", "application/json")
				c.Data(response.StatusCode, "application/json", body)
			})
			gateway := httptest.NewServer(router)
			t.Cleanup(gateway.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/chat/completions", nil)
			require.NoError(t, err)
			type result struct {
				response *http.Response
				err      error
			}
			completed := make(chan result, 1)
			go func() {
				response, err := (&http.Client{Timeout: 4 * time.Second}).Do(request)
				completed <- result{response, err}
			}()
			select {
			case <-accepted:
			case <-ctx.Done():
				release()
				t.Fatal("upstream request was not accepted")
			}
			var actual result
			select {
			case actual = <-completed:
				release()
				if actual.response != nil {
					actual.response.Body.Close()
				}
				t.Fatal("pre-validation pinger committed downstream headers before the provider responded")
			case <-time.After(1250 * time.Millisecond):
			}
			release()
			select {
			case actual = <-completed:
			case <-ctx.Done():
				t.Fatal("provider rejection did not reach client")
			}
			require.NoError(t, actual.err)
			defer actual.response.Body.Close()
			body, err := io.ReadAll(actual.response.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusBadGateway, actual.response.StatusCode)
			require.Contains(t, actual.response.Header.Get("Content-Type"), "application/json")
			require.NotContains(t, string(body), ": PING")
		})
	}
}
