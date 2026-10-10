package openai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponsesMissingUsageGeneratedOutput(t *testing.T) {
	for _, kind := range []string{"output_text", "refusal", "function_call_arguments", "reasoning_text", "reasoning_summary_text"} {
		t.Run(kind, func(t *testing.T) {
			body := fmt.Sprintf("data: {\"type\":\"response.%s.delta\",\"delta\":\"hello world\"}\n\n", kind)
			usage, apiErr, writer, _ := runResponsesTerminalTest(t, strings.NewReader(body), false)
			require.Nil(t, apiErr)
			require.Equal(t, service.CountTextToken("hello world", "gpt-4o"), usage.CompletionTokens)
			require.Equal(t, 12, usage.PromptTokens)
			require.Equal(t, 1, strings.Count(writer.Body.String(), "event: response.failed"))
		})
	}
}

func TestResponsesSuccessfulTerminalOnlyOutput(t *testing.T) {
	for _, tc := range []struct {
		name, output, text string
	}{
		{"text", `[{"type":"message","content":[{"type":"output_text","text":"hello world"}]}]`, "hello world"},
		{"refusal", `[{"type":"message","content":[{"type":"refusal","refusal":"hello world"}]}]`, "hello world"},
		{"function", `[{"type":"function_call","arguments":"hello world"}]`, "hello world"},
		{"reasoning", `[{"type":"reasoning","content":[{"type":"reasoning_text","text":"hello "}],"summary":[{"type":"summary_text","text":"world"}]}]`, "hello world"},
		{"opaque", `[{"type":"reasoning","encrypted_content":"private opaque bytes"},{"type":"image_generation_call","result":"base64 image"},{"type":"web_search_call","action":{"query":"metadata"}}]`, ""},
	} {
		for _, terminal := range []string{"completed", "done"} {
			t.Run(tc.name+"/"+terminal, func(t *testing.T) {
				body := fmt.Sprintf("data: {\"type\":\"response.%s\",\"response\":{\"status\":\"completed\",\"output\":%s}}\n\n", terminal, tc.output)
				usage, apiErr, _, info := runResponsesTerminalTest(t, strings.NewReader(body), false)
				require.Nil(t, apiErr)
				require.False(t, info.StreamStatus.HasErrors())
				require.Equal(t, service.CountTextToken(tc.text, "gpt-4o"), usage.CompletionTokens)
				if tc.text != "" {
					require.Equal(t, 12, usage.PromptTokens)
				} else {
					require.Zero(t, usage.PromptTokens)
				}
			})
		}
	}
}

func TestResponsesTerminalOutputDoesNotReplaceDeltasOrUsage(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, reported string
		prompt, completion     int
	}{
		{"deltas", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello world\"}\n\n", "", 12, service.CountTextToken("hello world", "gpt-4o")},
		{"measured", "", `,"usage":{"input_tokens":100,"output_tokens":7,"total_tokens":107}`, 100, 7},
		{"terminal_zero", "", `,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.prefix + fmt.Sprintf("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello world plus terminal snapshot\"}]}]%s}}\n\n", tc.reported)
			usage, apiErr, _, _ := runResponsesTerminalTest(t, strings.NewReader(body), false)
			require.Nil(t, apiErr)
			require.Equal(t, tc.prompt, usage.PromptTokens)
			require.Equal(t, tc.completion, usage.CompletionTokens)
		})
	}
}

func TestResponsesUnsuccessfulTerminalOnlyOutputIsNotEstimated(t *testing.T) {
	for _, kind := range []string{"failed", "incomplete", "cancelled", "canceled", "completed"} {
		body := fmt.Sprintf("data: {\"type\":\"response.%s\",\"response\":{\"status\":\"failed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello world\"}]}]}}\n\n", kind)
		usage, apiErr, _, info := runResponsesTerminalTest(t, strings.NewReader(body), false)
		require.Nil(t, apiErr)
		require.Zero(t, usage.TotalTokens)
		require.True(t, info.StreamStatus.HasErrors())
	}
}

func TestResponsesFlatErrorStopsBeforeTrailingCompletion(t *testing.T) {
	body := "data: {\"type\":\"error\",\"code\":\"upstream_failed\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":7}}}\n\n"
	usage, apiErr, writer, info := runResponsesTerminalTest(t, strings.NewReader(body), false)
	require.Nil(t, apiErr)
	require.True(t, info.StreamStatus.HasErrors())
	require.Zero(t, usage.TotalTokens)
	require.Equal(t, 1, strings.Count(writer.Body.String(), "event: error"))
	require.NotContains(t, writer.Body.String(), "event: response.completed")
	require.NotContains(t, writer.Body.String(), "event: response.failed")
}

func TestResponsesNonstreamExplicitZeroIsReported(t *testing.T) {
	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}
	body := `{"status":"completed","usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0},"output":[{"type":"message","content":[{"type":"output_text","text":"hello world"}]}]}`
	usage, apiErr := OaiResponsesHandler(ctx, info, &http.Response{Body: io.NopCloser(strings.NewReader(body))})
	require.Nil(t, apiErr)
	require.Zero(t, usage.TotalTokens)
	require.True(t, info.ResponsesUsageReported)
	require.Equal(t, body, writer.Body.String())
}
