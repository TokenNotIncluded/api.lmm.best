package ollama

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const ollamaCompletedStream = `{"message":{"content":"answer"},"done":false}` + "\n" +
	`{"done":true,"prompt_eval_count":5,"eval_count":7}` + "\n"

func TestOllamaStreamPublishesTerminalStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       io.Reader
		wantReason relaycommon.StreamEndReason
		wantError  error
		wantAPIErr bool
		wantTokens int
	}{
		{
			name:       "completed",
			body:       strings.NewReader(ollamaCompletedStream),
			wantReason: relaycommon.StreamEndReasonDone,
			wantTokens: 12,
		},
		{
			name:       "missing done frame",
			body:       strings.NewReader(`{"message":{"content":"partial"},"done":false}` + "\n"),
			wantReason: relaycommon.StreamEndReasonEOF,
		},
		{
			name:       "scanner failure",
			body:       io.MultiReader(strings.NewReader(`{"message":{"content":"partial"}}`+"\n"), ollamaFailedReader{}),
			wantReason: relaycommon.StreamEndReasonScannerErr,
			wantError:  io.ErrUnexpectedEOF,
		},
		{
			name:       "decode failure",
			body:       strings.NewReader("{invalid}\n"),
			wantReason: relaycommon.StreamEndReasonHandlerStop,
			wantAPIErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "test"}}
			usage, apiErr := ollamaStreamHandler(c, info, &http.Response{Body: io.NopCloser(tc.body)})
			require.Equal(t, tc.wantAPIErr, apiErr != nil)
			require.Nil(t, info.StreamStatus)
			require.NotNil(t, info.RateLimitStreamStatus)
			require.Equal(t, tc.wantReason, info.RateLimitStreamStatus.EndReason)
			require.Equal(t, tc.wantReason != relaycommon.StreamEndReasonDone, info.RateLimitStreamStatus.HasErrors())
			if tc.wantError != nil {
				require.ErrorIs(t, info.RateLimitStreamStatus.EndError, tc.wantError)
			}
			require.Equal(t, tc.wantTokens, usage.TotalTokens)
			require.Equal(t, tc.wantReason == relaycommon.StreamEndReasonDone, strings.Contains(w.Body.String(), "data: [DONE]"))
		})
	}
}

type ollamaFailedReader struct{}

func (ollamaFailedReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type ollamaFailedWriter struct {
	*httptest.ResponseRecorder
	failAt int
	writes int
	short  bool
}

func (w *ollamaFailedWriter) Write(data []byte) (int, error) {
	if strings.HasPrefix(string(data), "data: ") {
		w.writes++
		if w.writes == w.failAt {
			if w.short {
				return len(data) - 1, nil
			}
			return 0, io.ErrClosedPipe
		}
	}
	return w.ResponseRecorder.Write(data)
}

func TestOllamaStreamWriteFailureRetainsUsage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAt int
		short  bool
	}{
		{name: "start", failAt: 1},
		{name: "delta", failAt: 2},
		{name: "stop", failAt: 3},
		{name: "usage", failAt: 4},
		{name: "done", failAt: 5},
		{name: "short write", failAt: 5, short: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &ollamaFailedWriter{ResponseRecorder: httptest.NewRecorder(), failAt: tc.failAt, short: tc.short}
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "test"}}
			usage, apiErr := ollamaStreamHandler(c, info, &http.Response{Body: io.NopCloser(strings.NewReader(ollamaCompletedStream))})
			require.Nil(t, apiErr)
			require.Nil(t, info.StreamStatus)
			require.Equal(t, 12, usage.TotalTokens)
			require.Equal(t, tc.failAt, w.writes)
			require.Equal(t, relaycommon.StreamEndReasonClientGone, info.RateLimitStreamStatus.EndReason)
			require.True(t, info.RateLimitStreamStatus.HasErrors())
			if tc.short {
				require.ErrorIs(t, info.RateLimitStreamStatus.EndError, io.ErrShortWrite)
			} else {
				require.ErrorIs(t, info.RateLimitStreamStatus.EndError, io.ErrClosedPipe)
			}
			require.NotContains(t, w.Body.String(), "data: [DONE]")
		})
	}
}

type ollamaCancelWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *ollamaCancelWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(data)
	if strings.Contains(string(data), "data: [DONE]") {
		w.cancel()
	}
	return n, err
}

func TestOllamaStreamCancellationIsNotSuccess(t *testing.T) {
	for _, cancelBefore := range []bool{true, false} {
		t.Run(map[bool]string{true: "before stream", false: "during final write"}[cancelBefore], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := &ollamaCancelWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
			if cancelBefore {
				cancel()
			}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "test"}}
			usage, apiErr := ollamaStreamHandler(c, info, &http.Response{Body: io.NopCloser(strings.NewReader(ollamaCompletedStream))})
			require.Nil(t, apiErr)
			require.Nil(t, info.StreamStatus)
			require.Equal(t, 12, usage.TotalTokens)
			require.Equal(t, relaycommon.StreamEndReasonClientGone, info.RateLimitStreamStatus.EndReason)
			require.ErrorIs(t, info.RateLimitStreamStatus.EndError, context.Canceled)
			require.True(t, info.RateLimitStreamStatus.HasErrors())
		})
	}
}

type ollamaBlockedBody struct {
	readStarted chan struct{}
	closed      chan struct{}
	readOnce    sync.Once
	closeOnce   sync.Once
}

func (b *ollamaBlockedBody) Read([]byte) (int, error) {
	b.readOnce.Do(func() { close(b.readStarted) })
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *ollamaBlockedBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestOllamaStreamCancellationClosesBlockedUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &ollamaBlockedBody{readStarted: make(chan struct{}), closed: make(chan struct{})}
	defer body.Close()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "test"}}
	returned := make(chan bool, 1)
	go func() {
		usage, apiErr := ollamaStreamHandler(c, info, &http.Response{Body: body})
		returned <- usage != nil && apiErr == nil
	}()
	select {
	case <-body.readStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("scanner did not begin reading")
	}
	cancel()
	select {
	case valid := <-returned:
		require.True(t, valid)
		require.Nil(t, info.StreamStatus)
		require.Equal(t, relaycommon.StreamEndReasonClientGone, info.RateLimitStreamStatus.EndReason)
		require.ErrorIs(t, info.RateLimitStreamStatus.EndError, context.Canceled)
		require.True(t, info.RateLimitStreamStatus.HasErrors())
		require.NotContains(t, w.Body.String(), "data: [DONE]")
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled stream did not return")
	}
}
