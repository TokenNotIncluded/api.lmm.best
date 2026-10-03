package tencent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type commitFailureWriter struct {
	*httptest.ResponseRecorder
	flushes int
}

func (w *commitFailureWriter) FlushError() error {
	w.flushes++
	return io.ErrClosedPipe
}

type commitTrackedBody struct {
	reads  atomic.Int32
	closed atomic.Bool
}

func (b *commitTrackedBody) Read([]byte) (int, error) {
	b.reads.Add(1)
	return 0, io.EOF
}

func (b *commitTrackedBody) Close() error {
	b.closed.Store(true)
	return nil
}

func TestStreamHeaderCommitFailureClosesBodyBeforeReading(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "flush failure", true: "already canceled"}[canceled], func(t *testing.T) {
			writer := &commitFailureWriter{ResponseRecorder: httptest.NewRecorder()}
			c, _ := gin.CreateTestContext(writer)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
			}
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
			body := &commitTrackedBody{}
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}}, Body: body}
			info := &relaycommon.RelayInfo{}
			_, apiErr := tencentStreamHandler(c, info, response)

			require.NotNil(t, apiErr)
			require.True(t, types.IsSkipRetryError(apiErr))
			require.True(t, body.closed.Load(), "failed commit must close the upstream body")
			require.Zero(t, body.reads.Load(), "failed commit must precede every upstream read")
			require.True(t, helper.HTTPStreamDownstreamFailed(c))
			require.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamChannelFailure))
			require.False(t, service.ShouldRetryRelayError(c, apiErr, 3))
			require.False(t, service.ShouldExcludeChannelForRetry(c, apiErr))
			require.Equal(t, relaycommon.StreamEndReasonClientGone, info.RateLimitStreamStatus.EndReason)
			require.Empty(t, writer.Body.String())
			if canceled {
				require.Zero(t, writer.flushes)
				require.ErrorIs(t, info.RateLimitStreamStatus.EndError, context.Canceled)
			} else {
				require.Equal(t, 1, writer.flushes)
				require.ErrorIs(t, info.RateLimitStreamStatus.EndError, io.ErrClosedPipe)
			}
		})
	}
}

func TestStreamRejectsExplicitNonSSEBeforeReadingOrCommitting(t *testing.T) {
	for _, contentType := range []string{
		"application/json",
		"text/html",
		"application/x-ndjson",
		"text/event-stream-malicious",
		"application/text/event-stream",
		"text/event-stream; charset",
	} {
		t.Run(contentType, func(t *testing.T) {
			writer := &commitFailureWriter{ResponseRecorder: httptest.NewRecorder()}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			body := &commitTrackedBody{}
			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{contentType}},
				Body:       body,
			}
			info := &relaycommon.RelayInfo{}

			usage, apiErr := tencentStreamHandler(c, info, response)

			require.NotNil(t, apiErr)
			require.Nil(t, usage)
			require.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
			require.Equal(t, types.ErrorCodeBadResponse, apiErr.GetErrorCode())
			require.False(t, types.IsSkipRetryError(apiErr))
			require.True(t, service.ShouldRetryRelayError(c, apiErr, 3))
			require.Zero(t, body.reads.Load())
			require.True(t, body.closed.Load())
			require.False(t, c.Writer.Written())
			require.Empty(t, writer.Body.String())
			require.Zero(t, writer.flushes)
			require.False(t, common.GetContextKeyBool(c, constant.ContextKeyHTTPStreamCommitted))
			require.False(t, helper.HTTPStreamDownstreamFailed(c))
			require.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamChannelFailure))
		})
	}
}

type headerlessObservedWriter struct {
	*httptest.ResponseRecorder
	flushes    int
	clientGone chan bool
}

func (w *headerlessObservedWriter) FlushError() error {
	w.flushes++
	w.ResponseRecorder.Flush()
	return nil
}

func (w *headerlessObservedWriter) CloseNotify() <-chan bool {
	return w.clientGone
}

type headerlessObservedBody struct {
	io.Reader
	context             *gin.Context
	reads               atomic.Int32
	committedBeforeRead atomic.Bool
	closed              atomic.Bool
}

func (b *headerlessObservedBody) Read(data []byte) (int, error) {
	if b.reads.Add(1) == 1 {
		b.committedBeforeRead.Store(b.context.Writer.Written() ||
			common.GetContextKeyBool(b.context, constant.ContextKeyHTTPStreamCommitted))
	}
	return b.Reader.Read(data)
}

func (b *headerlessObservedBody) Close() error {
	b.closed.Store(true)
	return nil
}

func TestHeaderlessStreamWaitsForBusinessFrameBeforeCommitting(t *testing.T) {
	writer := &headerlessObservedWriter{
		ResponseRecorder: httptest.NewRecorder(),
		clientGone:       make(chan bool),
	}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "tencent-test"},
	}
	body := &headerlessObservedBody{
		Reader:  strings.NewReader(`data: {"Choices":[{"Delta":{"Content":"partial"},"FinishReason":"stop"}]}` + "\n"),
		context: c,
	}
	response := &http.Response{StatusCode: http.StatusOK, Body: body}

	usage, apiErr := tencentStreamHandler(c, info, response)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.Positive(t, body.reads.Load())
	require.False(t, body.committedBeforeRead.Load(), "headerless input must not commit before reading a business frame")
	require.True(t, body.closed.Load())
	require.True(t, c.Writer.Written())
	require.True(t, common.GetContextKeyBool(c, constant.ContextKeyHTTPStreamCommitted))
	require.False(t, helper.HTTPStreamDownstreamFailed(c))
	require.Positive(t, writer.flushes)
	require.Contains(t, writer.Body.String(), "partial")
}
