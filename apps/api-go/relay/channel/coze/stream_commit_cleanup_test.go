package coze

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

type continuationFailedWriter struct {
	*httptest.ResponseRecorder
	failFlush       bool
	writes          int
	flushes         int
	writesAtFailure int
}

func (w *continuationFailedWriter) Write(data []byte) (int, error) {
	w.writes++
	if !w.failFlush {
		w.writesAtFailure = w.writes
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(data)
}

func (w *continuationFailedWriter) FlushError() error {
	w.flushes++
	if w.failFlush && w.flushes == 2 {
		w.writesAtFailure = w.writes
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	return nil
}

type continuationBody struct {
	io.Reader
	closed bool
}

func (b *continuationBody) Close() error { b.closed = true; return nil }

func TestStreamWriteFailureRetainsTerminalUsageWithoutMoreClientWrites(t *testing.T) {
	for _, failFlush := range []bool{false, true} {
		t.Run(map[bool]string{false: "write", true: "flush"}[failFlush], func(t *testing.T) {
			writer := &continuationFailedWriter{ResponseRecorder: httptest.NewRecorder(), failFlush: failFlush}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			body := &continuationBody{Reader: strings.NewReader(
				"event: conversation.message.delta\ndata: {\"content\":\"hello\"}\n\n" +
					"event: conversation.message.delta\ndata: {\"content\":\"continued\"}\n\n" +
					"event: conversation.chat.completed\ndata: {\"usage\":{\"input_count\":10,\"output_count\":2,\"token_count\":12}}\n\n")}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "coze-test"}}
			usage, apiErr := cozeChatStreamHandler(c, info, &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body})

			require.Nil(t, apiErr, "delivery failure must not discard the provider's terminal usage")
			require.Equal(t, 10, usage.PromptTokens)
			require.Equal(t, 2, usage.CompletionTokens)
			require.Equal(t, 12, usage.TotalTokens)
			require.Nil(t, info.StreamStatus, "legacy billing metadata must remain separate")
			require.True(t, info.RateLimitStreamStatus.HasErrors())
			require.Equal(t, relaycommon.StreamEndReasonClientGone, info.RateLimitStreamStatus.EndReason)
			require.True(t, helper.HTTPStreamDownstreamFailed(c))
			require.Positive(t, writer.writesAtFailure, "the failure must follow the first business write")
			require.Equal(t, writer.writesAtFailure, writer.writes, "later deltas, terminal frames and DONE must not write")
			require.Equal(t, map[bool]int{false: 1, true: 2}[failFlush], writer.flushes)
			require.NotContains(t, writer.Body.String(), "continued")
			require.NotContains(t, writer.Body.String(), "[DONE]")
			require.True(t, body.closed)
		})
	}
}

func TestStreamWriteFailureDoesNotMaskLaterDecodeFailure(t *testing.T) {
	for _, event := range []string{"conversation.message.delta", "conversation.chat.completed"} {
		for _, terminated := range []bool{false, true} {
			t.Run(event+map[bool]string{false: "/EOF", true: "/blank line"}[terminated], func(t *testing.T) {
				writer := &continuationFailedWriter{ResponseRecorder: httptest.NewRecorder()}
				c, _ := gin.CreateTestContext(writer)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				stream := "event: conversation.message.delta\ndata: {\"content\":\"hello\"}\n\n" +
					"event: " + event + "\ndata: {"
				if terminated {
					stream += "\n\n"
				}
				body := &continuationBody{Reader: strings.NewReader(stream)}
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "coze-test"}}
				usage, apiErr := cozeChatStreamHandler(c, info, &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body})

				require.NotNil(t, apiErr, "the delivery latch must not swallow a subsequent upstream decode error")
				require.Nil(t, usage, "preserve the existing decode-failure settlement outcome")
				require.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
				require.Equal(t, 1, writer.writes)
				require.Equal(t, 1, writer.flushes)
				require.True(t, helper.HTTPStreamDownstreamFailed(c))
				require.True(t, body.closed)
			})
		}
	}
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
			_, apiErr := cozeChatStreamHandler(c, info, response)

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

			usage, apiErr := cozeChatStreamHandler(c, info, response)

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
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "coze-test"},
	}
	body := &headerlessObservedBody{
		Reader:  strings.NewReader("event: conversation.message.delta\ndata: {\"content\":\"partial\"}\n\nevent: conversation.chat.completed\ndata: {\"usage\":{\"input_count\":2,\"output_count\":3,\"token_count\":5}}\n\n"),
		context: c,
	}
	response := &http.Response{StatusCode: http.StatusOK, Body: body}

	usage, apiErr := cozeChatStreamHandler(c, info, response)

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
