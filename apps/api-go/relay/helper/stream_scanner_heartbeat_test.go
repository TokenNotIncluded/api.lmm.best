package helper

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStreamScannerHeartbeatFlushesOnLiveConnection(t *testing.T) {
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		pr, pw := io.Pipe()
		defer pw.Close()
		go func() { _, _ = io.WriteString(pw, "data: business\n\n: keep-alive\n\n") }()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, DisablePing: true}
		StreamScannerHandler(c, &http.Response{Body: pr}, info, func(data string, sr *StreamResult) {
			if err := StringData(c, data); err != nil {
				sr.Stop(err)
			}
		})
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if scanner.Text() == ": PING" {
			break
		}
	}
	require.NoError(t, scanner.Err())
	require.Equal(t, []string{"data: business", "", ": PING"}, lines)
	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("scanner did not exit after client cancellation")
	}
}

func TestStreamScannerUpstreamHeartbeats(t *testing.T) {
	for _, localPing := range []bool{false, true} {
		for _, disablePing := range []bool{false, true} {
			t.Run(fmt.Sprintf("local=%v/disabled=%v", localPing, disablePing), func(t *testing.T) {
				setting := operation_setting.GetGeneralSetting()
				previous := setting.PingIntervalEnabled
				setting.PingIntervalEnabled = localPing
				t.Cleanup(func() { setting.PingIntervalEnabled = previous })
				body := ": preamble\n\n:\n\ndata: business\n\n" + strings.Repeat(":\n\n: keep-alive\n\n", 1000) + "data: second\n\ndata: [DONE]\n\n"
				c, resp, info := setupStreamTest(t, strings.NewReader(body))
				info.DisablePing = disablePing
				var received []string
				StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
					if len(received) == 0 {
						require.False(t, c.Writer.Written(), "preamble must not commit HTTP status")
					}
					received = append(received, data)
					if err := StringData(c, data); err != nil {
						sr.Stop(err)
					}
				})
				w := c.Writer
				require.True(t, w.Written())
				require.Equal(t, []string{"business", "second"}, received)
				require.Equal(t, 2, info.ReceivedResponseCount)
			})
		}
	}
}

func TestStreamScannerHeartbeatFrames(t *testing.T) {
	c, resp, info := setupStreamTest(t, strings.NewReader(":\n\ndata: business\n\n"+strings.Repeat(": ping\n\n", 1000)))
	w := httptest.NewRecorder()
	// Create a fresh Gin writer through the normal test-context constructor.
	context, _ := gin.CreateTestContext(w)
	context.Request = c.Request
	info.DisablePing = true
	StreamScannerHandler(context, resp, info, func(data string, sr *StreamResult) {
		if err := StringData(context, data); err != nil {
			sr.Stop(err)
		}
	})
	require.True(t, w.Flushed)
	require.Equal(t, "data: business\n\n: PING\n\n", w.Body.String())
}
