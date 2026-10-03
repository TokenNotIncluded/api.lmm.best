package helper

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type commitFailureWriter struct {
	*httptest.ResponseRecorder
	flushError error
	writeError error
}

func (w *commitFailureWriter) FlushError() error { return w.flushError }
func (w *commitFailureWriter) Write(data []byte) (int, error) {
	if w.writeError != nil {
		return 0, w.writeError
	}
	return w.ResponseRecorder.Write(data)
}
func (w *commitFailureWriter) WriteString(data string) (int, error) { return w.Write([]byte(data)) }

type commitTrackedBody struct {
	io.Reader
	reads  int
	closed bool
}

func (b *commitTrackedBody) Read(data []byte) (int, error) { b.reads++; return b.Reader.Read(data) }
func (b *commitTrackedBody) Close() error                  { b.closed = true; return nil }

func TestStreamCommitFlushFailureClosesBodyBeforeAnyRead(t *testing.T) {
	w := &commitFailureWriter{ResponseRecorder: httptest.NewRecorder(), flushError: errors.New("flush unavailable")}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	body := &commitTrackedBody{Reader: strings.NewReader("data: later\n\n")}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	callbacks := 0
	StreamScannerHandler(c, &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body}, info, func(string, *StreamResult) { callbacks++ })
	require.Zero(t, body.reads)
	require.Zero(t, callbacks)
	require.True(t, body.closed)
	require.Equal(t, relaycommon.StreamEndReasonWriterError, info.StreamStatus.EndReason)
	require.True(t, HTTPStreamDownstreamFailed(c))
	require.True(t, common.GetContextKeyBool(c, constant.ContextKeyHTTPStreamCommitted))
}

func TestStreamCommitWriteFailureStopsFurtherEvents(t *testing.T) {
	w := &commitFailureWriter{ResponseRecorder: httptest.NewRecorder(), writeError: errors.New("write unavailable")}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	body := &commitTrackedBody{Reader: strings.NewReader("data: first\n\ndata: later\n\ndata: [DONE]\n\n")}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	callbacks := 0
	StreamScannerHandler(c, &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body}, info, func(data string, _ *StreamResult) {
		callbacks++
		_ = StringData(c, data) // Legacy callers may ignore the error.
	})
	require.Equal(t, 1, callbacks)
	require.True(t, body.closed)
	require.True(t, HTTPStreamDownstreamFailed(c))
	require.Equal(t, relaycommon.StreamEndReasonWriterError, info.StreamStatus.EndReason)
	require.Empty(t, w.Body.String())
	require.Error(t, StringData(c, "must not write after failure"))
}

type resettableCommitWriter struct{ gin.ResponseWriter }

func (w *resettableCommitWriter) ResetForRelayRetry() error { return nil }

func TestStreamCommitDoesNotRetireInternalRecorderRetry(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Writer = &resettableCommitWriter{ResponseWriter: c.Writer}
	require.NoError(t, CommitEventStreamHeaders(c))
	require.False(t, c.Writer.Written(), "internal status must remain mutable before its first business frame")
	require.NoError(t, StringData(c, "internal frame"))
	require.True(t, c.Writer.Written())
	require.False(t, common.GetContextKeyBool(c, constant.ContextKeyHTTPStreamCommitted))
	require.False(t, HTTPStreamDownstreamFailed(c))
}

func TestStreamCommitRequiresExactSSEMediaType(t *testing.T) {
	for _, contentType := range []string{"text/event-stream", "TEXT/EVENT-STREAM", " Text/Event-Stream ; charset=UTF-8"} {
		require.True(t, IsEventStreamResponse(&http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}}))
	}
	for _, contentType := range []string{"", "application/json", "text/event-stream-malicious", "text/event-streaming; charset=utf-8"} {
		require.False(t, IsEventStreamResponse(&http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}}))
	}
	require.False(t, IsEventStreamResponse(&http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{"Content-Type": []string{"text/event-stream"}}}))
}
