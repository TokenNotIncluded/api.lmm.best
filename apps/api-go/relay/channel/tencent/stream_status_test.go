package tencent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type tencentStreamErrorReader struct {
	io.Reader
	err error
}

func (r tencentStreamErrorReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		return n, r.err
	}
	return n, err
}

type tencentFailedStreamWriter struct {
	*httptest.ResponseRecorder
}

func (w tencentFailedStreamWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

type tencentCompletionFlushFailedWriter struct {
	*httptest.ResponseRecorder
	writes  int
	flushes int
}

func (w *tencentCompletionFlushFailedWriter) Write(data []byte) (int, error) {
	w.writes++
	return w.ResponseRecorder.Write(data)
}

func (w *tencentCompletionFlushFailedWriter) FlushError() error {
	w.flushes++
	if w.flushes == 3 {
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	return nil
}

func TestTencentTerminalFlushFailurePublishesClientGone(t *testing.T) {
	writer := &tencentCompletionFlushFailedWriter{ResponseRecorder: httptest.NewRecorder()}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "tencent-hunyuan"},
	}
	const event = `data: {"Choices":[{"Delta":{"Content":"partial"},"FinishReason":"stop"}]}` + "\n"
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(event)),
	}

	usage, apiErr := tencentStreamHandler(c, info, response)

	require.Nil(t, apiErr)
	require.Positive(t, usage.CompletionTokens)
	require.True(t, helper.HTTPStreamDownstreamFailed(c))
	require.Equal(t, relaycommon.StreamEndReasonClientGone, info.RateLimitStreamStatus.EndReason)
	require.ErrorIs(t, info.RateLimitStreamStatus.EndError, io.ErrClosedPipe)
	require.Equal(t, 3, writer.flushes, "initial commit, content, then failed completion flush")
	require.Equal(t, 4, writer.writes, "content and completion each render payload and delimiter")
}

func TestTencentStreamHandlerPublishesStreamOutcome(t *testing.T) {
	const event = `data: {"Choices":[{"Delta":{"Content":"partial"},"FinishReason":"stop"}]}` + "\n"
	readErr := errors.New("synthetic Tencent read failure")
	for _, test := range []struct {
		name   string
		data   string
		err    error
		cancel bool
		failed bool
		want   relaycommon.StreamEndReason
	}{
		{name: "normal EOF", data: event, want: relaycommon.StreamEndReasonEOF},
		{name: "invalid JSON", data: "data: {\n" + event, want: relaycommon.StreamEndReasonEOF},
		{name: "upstream error", data: `data: {"Error":{"Code":1,"Message":"synthetic upstream error"}}` + "\n" + event, want: relaycommon.StreamEndReasonEOF},
		{name: "read failed", data: event, err: readErr, want: relaycommon.StreamEndReasonScannerErr},
		{name: "client canceled", data: event, cancel: true, want: relaycommon.StreamEndReasonClientGone},
		{name: "write failed", data: event, failed: true, want: relaycommon.StreamEndReasonClientGone},
	} {
		t.Run(test.name, func(t *testing.T) {
			requestContext, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			if test.cancel {
				cancel()
			}
			var writer http.ResponseWriter = httptest.NewRecorder()
			if test.failed {
				writer = &tencentFailedStreamWriter{ResponseRecorder: httptest.NewRecorder()}
			}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestContext)
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "tencent-hunyuan"},
			}
			var reader io.Reader = strings.NewReader(test.data)
			if test.err != nil {
				reader = tencentStreamErrorReader{Reader: reader, err: test.err}
			}
			response := &http.Response{Body: io.NopCloser(reader)}

			usage, apiErr := tencentStreamHandler(c, info, response)

			require.Nil(t, info.StreamStatus)
			require.Nil(t, apiErr)
			require.NotNil(t, usage, "headerless input retains the legacy usage return")
			require.Positive(t, usage.CompletionTokens)
			require.Equal(t, test.want, info.RateLimitStreamStatus.EndReason)
			require.Equal(t, test.name != "normal EOF", info.RateLimitStreamStatus.HasErrors())
			if test.err != nil {
				require.ErrorIs(t, info.RateLimitStreamStatus.EndError, test.err)
			}
		})
	}
}
