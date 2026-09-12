package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type responsesReadError struct{}

func TestResponsesEventNameInjection(t *testing.T) {
	for _, name := range []string{"response.created\nevent: injected", "response.created\rdata: injected", "response.created\r\n\r\nevent: injected"} {
		encoded, err := json.Marshal(map[string]string{"type": name})
		require.NoError(t, err)
		body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\"}}\n\ndata: " + string(encoded) + "\n\n"
		_, e, w, info := runResponsesTerminalTest(t, strings.NewReader(body), false)
		require.Nil(t, e)
		require.NotContains(t, w.Body.String(), "injected")
		require.Equal(t, 1, strings.Count(w.Body.String(), "event: response.failed"))
		require.True(t, info.StreamStatus.HasErrors())
		direct := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(direct)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		require.Error(t, writeResponsesEvent(c, name, `{}`))
		require.False(t, c.Writer.Written())
	}
}

func TestResponsesCreatedOnlyUsagePolicy(t *testing.T) {
	for _, withUsage := range []bool{false, true} {
		u := ""
		if withUsage {
			u = `,"usage":{"input_tokens":100,"output_tokens":0,"total_tokens":100}`
		}
		body := `data: {"type":"response.created","response":{"id":"resp_test"` + u + "}}\n\n"
		usage, e, _, _ := runResponsesTerminalTest(t, strings.NewReader(body), false)
		require.Nil(t, e)
		require.Zero(t, usage.CompletionTokens)
		if withUsage {
			require.Equal(t, 100, usage.PromptTokens)
		} else {
			require.Zero(t, usage.PromptTokens)
		}
	}
}

type responsesFailedWriter struct {
	*httptest.ResponseRecorder
	writes int
}

func (w *responsesFailedWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, io.ErrClosedPipe
}

func TestResponsesDownstreamWriteFailureDoesNotAppendTerminal(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	w := &responsesFailedWriter{ResponseRecorder: httptest.NewRecorder()}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, DisablePing: true}
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\"}}\n\n"
	_, e := OaiResponsesStreamHandler(c, info, &http.Response{Body: io.NopCloser(strings.NewReader(body))})
	require.Nil(t, e)
	require.Equal(t, 1, w.writes)
	require.True(t, info.StreamStatus.HasErrors())
}

func (responsesReadError) Read([]byte) (int, error) {
	return 0, errors.New("private upstream address and key")
}

func runResponsesTerminalTest(t *testing.T, body io.Reader, cancelled bool) (*dto.Usage, *types.NewAPIError, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	t.Helper()
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if cancelled {
		ctx, cancel := context.WithCancel(c.Request.Context())
		cancel()
		c.Request = c.Request.WithContext(ctx)
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, DisablePing: true}
	info.SetEstimatePromptTokens(12)
	u, e := OaiResponsesStreamHandler(c, info, &http.Response{Body: io.NopCloser(body)})
	return u, e, w, info
}

func TestResponsesTerminalUsageAndSingleEnd(t *testing.T) {
	for _, terminal := range []string{"completed", "done", "incomplete", "failed", "cancelled", "canceled"} {
		for _, withUsage := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/usage=%v", terminal, withUsage), func(t *testing.T) {
				usage := ""
				if withUsage {
					usage = `,"usage":{"input_tokens":100,"output_tokens":64,"total_tokens":164,"input_tokens_details":{"cached_tokens":20},"output_tokens_details":{"reasoning_tokens":64}}`
				}
				frame := fmt.Sprintf("data: {\"type\":\"response.%s\",\"response\":{\"id\":\"resp_test\",\"status\":\"%s\"%s}}\n\n", terminal, terminal, usage)
				u, e, w, _ := runResponsesTerminalTest(t, strings.NewReader(frame+frame), false)
				require.Nil(t, e) // the settlement caller receives facts, not a refund/retry error
				require.Equal(t, 1, strings.Count(w.Body.String(), "event: response."))
				if withUsage {
					require.Equal(t, 100, u.PromptTokens)
					require.Equal(t, 64, u.CompletionTokens)
					require.Equal(t, 164, u.TotalTokens)
					require.Equal(t, 20, u.PromptTokensDetails.CachedTokens)
					require.Equal(t, 64, u.CompletionTokenDetails.ReasoningTokens)
				} else {
					require.Zero(t, u.TotalTokens)
				}
			})
		}
	}
}

func TestResponsesInterruptedStream(t *testing.T) {
	created := "data: {\"type\":\"response.created\",\"sequence_number\":7,\"response\":{\"id\":\"resp_test\",\"usage\":{\"input_tokens\":100,\"output_tokens\":64,\"total_tokens\":164}}}\n\n"
	tool := "data: {\"type\":\"response.function_call_arguments.delta\",\"sequence_number\":8,\"delta\":\"arg\"}\n\n"
	for _, prefix := range []string{created, created + tool} {
		for _, readError := range []bool{false, true} {
			t.Run(fmt.Sprintf("tool=%v/error=%v", strings.Contains(prefix, "arguments"), readError), func(t *testing.T) {
				var body io.Reader = strings.NewReader(prefix)
				if readError {
					body = io.MultiReader(body, responsesReadError{})
				}
				u, e, w, info := runResponsesTerminalTest(t, body, false)
				require.Nil(t, e)
				require.Equal(t, 164, u.TotalTokens)
				require.True(t, info.StreamStatus.HasErrors())
				if readError {
					require.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.EndReason)
				} else {
					require.Equal(t, relaycommon.StreamEndReasonEOF, info.StreamStatus.EndReason)
				}
				require.Equal(t, 1, strings.Count(w.Body.String(), "event: response.failed"))
				require.NotContains(t, w.Body.String(), "private upstream")
				lines := strings.Split(w.Body.String(), "\n")
				for _, line := range lines {
					if !strings.HasPrefix(line, "data: ") || !strings.Contains(line, "upstream_stream_interrupted") {
						continue
					}
					var event struct {
						SequenceNumber int `json:"sequence_number"`
						Response       struct {
							ID     string `json:"id"`
							Status string `json:"status"`
						} `json:"response"`
					}
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
					require.Equal(t, "resp_test", event.Response.ID)
					require.Equal(t, "failed", event.Response.Status)
					want := 8
					if strings.Contains(prefix, "arguments") {
						want = 9
					}
					require.Equal(t, want, event.SequenceNumber)
				}
			})
		}
	}
}

func TestResponsesEmptyFailureAndCancellation(t *testing.T) {
	_, e, w, _ := runResponsesTerminalTest(t, responsesReadError{}, false)
	require.NotNil(t, e)
	require.Equal(t, http.StatusBadGateway, e.StatusCode)
	require.Empty(t, w.Body.String())
	_, e, w, _ = runResponsesTerminalTest(t, strings.NewReader(""), true)
	require.Nil(t, e)
	require.Empty(t, w.Body.String())
}

func TestResponsesReportedZeroUsageIsNotEstimated(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.incomplete\",\"response\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"total_tokens\":0}}}\n\n"
	u, e, _, _ := runResponsesTerminalTest(t, strings.NewReader(body), false)
	require.Nil(t, e)
	require.Zero(t, u.TotalTokens)
}

func TestResponsesPartialOutputUsageFallback(t *testing.T) {
	for _, kind := range []string{"output_text", "function_call_arguments"} {
		body := fmt.Sprintf("data: {\"type\":\"response.%s.delta\",\"delta\":\"hello world\"}\n\n", kind)
		u, e, w, _ := runResponsesTerminalTest(t, strings.NewReader(body), false)
		require.Nil(t, e)
		require.Positive(t, u.CompletionTokens)
		require.Equal(t, 12, u.PromptTokens)
		require.Equal(t, u.PromptTokens+u.CompletionTokens, u.TotalTokens)
		require.Equal(t, 1, strings.Count(w.Body.String(), "event: response.failed"))
	}
}

func TestResponsesTerminalBeforeReadFailure(t *testing.T) {
	for _, kind := range []string{"completed", "incomplete"} {
		body := fmt.Sprintf("data: {\"type\":\"response.%s\",\"response\":{\"id\":\"resp_test\"}}\n\n", kind)
		_, e, w, _ := runResponsesTerminalTest(t, io.MultiReader(strings.NewReader(body), responsesReadError{}), false)
		require.Nil(t, e)
		require.Equal(t, 1, strings.Count(w.Body.String(), "event: response."))
		require.NotContains(t, w.Body.String(), "upstream_stream_interrupted")
	}
}
