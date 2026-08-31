package openai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	relaytypes "github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type firstFlushWriter struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
	once    sync.Once
}

func (w *firstFlushWriter) Flush() {
	w.ResponseRecorder.Flush()
	w.once.Do(func() {
		close(w.flushed)
	})
}

func runOaiStreamWithPausedFirstChunk(t *testing.T, shouldIncludeUsage, forceFormat bool) (bool, string) {
	t.Helper()

	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	reader, writer := io.Pipe()
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	downstream := &firstFlushWriter{
		ResponseRecorder: httptest.NewRecorder(),
		flushed:          make(chan struct{}),
	}
	c, _ := gin.CreateTestContext(downstream)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       reader,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-test",
		},
		IsStream:           true,
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        relaytypes.RelayFormatOpenAI,
		ShouldIncludeUsage: shouldIncludeUsage,
		DisablePing:        true,
	}
	info.ChannelSetting.ForceFormat = forceFormat

	type streamResult struct {
		usageErr error
		apiErr   *relaytypes.NewAPIError
	}
	done := make(chan streamResult, 1)
	go func() {
		usage, apiErr := OaiStreamHandler(c, info, resp)
		var usageErr error
		if usage == nil {
			usageErr = fmt.Errorf("usage is nil")
		}
		done <- streamResult{usageErr: usageErr, apiErr: apiErr}
	}()

	firstChunk := `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}`
	_, err := fmt.Fprintf(writer, "data: %s\n\n", firstChunk)
	require.NoError(t, err)

	flushedBeforeNextEvent := false
	select {
	case <-downstream.flushed:
		flushedBeforeNextEvent = true
	case <-time.After(500 * time.Millisecond):
	}

	usageChunk := `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	_, err = fmt.Fprintf(writer, "data: %s\n\ndata: [DONE]\n\n", usageChunk)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	select {
	case result := <-done:
		require.NoError(t, result.usageErr)
		require.Nil(t, result.apiErr)
	case <-time.After(2 * time.Second):
		t.Fatal("stream handler did not finish")
	}

	return flushedBeforeNextEvent, downstream.Body.String()
}

func TestOaiStreamHandlerFlushesFirstChunkBeforeNextEvent(t *testing.T) {
	flushedBeforeNextEvent, body := runOaiStreamWithPausedFirstChunk(t, true, false)

	require.True(t, flushedBeforeNextEvent, "first chat chunk must flush before the next upstream event")
	require.Contains(t, body, `"content":"hello"`)
	require.Contains(t, body, `"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2`)
}

func TestOaiStreamHandlerFlushesFirstChunkWhenUsageExcluded(t *testing.T) {
	flushedBeforeNextEvent, body := runOaiStreamWithPausedFirstChunk(t, false, false)

	require.True(t, flushedBeforeNextEvent, "excluding usage must not delay the first chat chunk")
	require.Contains(t, body, `"content":"hello"`)
	require.NotContains(t, body, `"usage":`)
}

func TestOaiStreamHandlerFlushesFirstChunkWithForcedFormat(t *testing.T) {
	flushedBeforeNextEvent, body := runOaiStreamWithPausedFirstChunk(t, true, true)

	require.True(t, flushedBeforeNextEvent, "forced-format chat chunks must flush immediately")
	require.Contains(t, body, `"content":"hello"`)
}

func runOaiAudioStream(t *testing.T, shouldIncludeUsage bool) (*dto.Usage, string) {
	t.Helper()

	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl-audio","object":"chat.completion.chunk","created":1710000000,"model":"gpt-audio","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-audio","object":"chat.completion.chunk","created":1710000000,"model":"gpt-audio","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`,
		`data: {"id":"chatcmpl-audio","object":"chat.completion.chunk","created":1710000000,"model":"gpt-audio","choices":[],"finish_reason":"stop"}`,
		`data: [DONE]`,
		``,
	}, "\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "gpt-audio"},
		IsStream:           true,
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        relaytypes.RelayFormatOpenAI,
		ShouldIncludeUsage: shouldIncludeUsage,
		DisablePing:        true,
	}

	usage, apiErr := OaiStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	return usage, recorder.Body.String()
}

func TestOaiStreamHandlerAudioUsageStillUsesSecondLastChunk(t *testing.T) {
	usage, _ := runOaiAudioStream(t, true)

	require.Equal(t, 4, usage.PromptTokens)
	require.Equal(t, 5, usage.CompletionTokens)
	require.Equal(t, 9, usage.TotalTokens)
}

func TestOaiStreamHandlerDoesNotLeakHeldUsageBeforeLaterEvent(t *testing.T) {
	usage, body := runOaiAudioStream(t, false)

	require.Equal(t, 9, usage.TotalTokens)
	require.Contains(t, body, `"finish_reason":"stop"`)
	require.NotContains(t, body, `"usage":`)
}
