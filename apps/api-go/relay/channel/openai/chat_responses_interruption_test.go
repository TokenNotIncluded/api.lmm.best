package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func chatResponseFrame(choice string) string {
	return "data: {\"id\":\"chat_test\",\"model\":\"gpt-test\",\"choices\":[" + choice + "]}\n\n"
}

func TestChatResponsesInterruptedHTTPStream(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	reasoning := chatResponseFrame(`{"index":0,"delta":{"reasoning_content":"partial"}}`)
	text := chatResponseFrame(`{"index":0,"delta":{"content":"partial"}}`)
	tool := chatResponseFrame(`{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_test","function":{"name":"lookup","arguments":"{"}}]}}`)
	finish := func(reason string) string {
		return chatResponseFrame(fmt.Sprintf(`{"index":0,"delta":{},"finish_reason":%q}`, reason))
	}
	usage := "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2,\"total_tokens\":102}}\n\n"
	for _, tc := range []struct {
		name, body, terminal string
		noOutput             bool
	}{
		{"empty EOF", "", "", true},
		{"partial field EOF", "data: {}\n", "", true},
		{"DONE only", "data: [DONE]\n\n", "", true},
		{"reasoning EOF", reasoning, "response.failed", false},
		{"text EOF", text, "response.failed", false},
		{"tool EOF", tool, "response.failed", false},
		{"resumed EOF", reasoning + finish("tool_calls") + reasoning, "response.failed", false},
		{"malformed", text + "data: not-json\n\n", "response.failed", false},
		{"unknown finish DONE", text + finish("unknown") + "data: [DONE]\n\n", "response.failed", false},
		{"second choice open", text + chatResponseFrame(`{"index":1,"delta":{"content":"other"}}`) + finish("stop"), "response.failed", false},
		{"usage EOF", text + usage, "response.failed", false},
		{"stop usage EOF", text + finish("stop") + usage, "response.completed", false},
		{"tool_calls EOF", tool + finish("tool_calls"), "response.completed", false},
		{"function_call EOF", text + finish("function_call"), "response.completed", false},
		{"length EOF", text + finish("length"), "response.incomplete", false},
		{"content_filter EOF", text + finish("content_filter"), "response.incomplete", false},
		{"DONE", text + "data: [DONE]\n\n", "response.completed", false},
		{"bare DONE", text + "[DONE]\n", "response.completed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			response, err := client.Get(server.URL)
			require.NoError(t, err)
			c, recorder, _, info := newResponsesChatTestContext(t, "", true)
			gotUsage, apiErr := OaiChatToResponsesStreamHandler(c, info, response)
			body := recorder.Body.String()
			if tc.noOutput {
				require.NotNil(t, apiErr)
				require.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
				require.Empty(t, body)
				return
			}
			require.Nil(t, apiErr)
			require.Equal(t, 1, strings.Count(body, "event: "+tc.terminal+"\n"))
			if strings.Contains(tc.body, `"usage"`) {
				require.Equal(t, 102, gotUsage.TotalTokens)
			}
			if tc.terminal != "response.failed" {
				return
			}
			require.True(t, info.StreamStatus.HasErrors())
			require.NotContains(t, body, "event: response.completed")
			var failed dto.ResponsesStreamResponse
			for _, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"type":"response.failed"`) {
					require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &failed))
				}
			}
			require.NotNil(t, failed.Response)
			require.NotEmpty(t, failed.Response.Output)
			require.Contains(t, body, "upstream_stream_interrupted")
			if !strings.Contains(tc.name, "resumed") && !strings.Contains(tc.name, "second choice") && !strings.Contains(tc.name, "unknown finish") {
				require.NotContains(t, body, "event: response.output_item.done")
				require.Equal(t, "in_progress", failed.Response.Output[0].Status)
			}
		})
	}
}

func TestChatResponsesWriteFailureDoesNotAppendTerminal(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	c, _, response, info := newResponsesChatTestContext(t, chatResponseFrame(`{"delta":{"content":"partial"}}`), true)
	writer := &responsesFailedWriter{ResponseRecorder: httptest.NewRecorder()}
	c, _ = gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	_, apiErr := OaiChatToResponsesStreamHandler(c, info, response)
	require.Nil(t, apiErr)
	require.Equal(t, 1, writer.writes)
	require.True(t, info.StreamStatus.HasErrors())
}

func TestChatResponsesReadFailureKeepsSafePartialOutput(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	c, recorder, response, info := newResponsesChatTestContext(t, "", true)
	response.Body = io.NopCloser(io.MultiReader(strings.NewReader(chatResponseFrame(`{"delta":{"content":"partial"}}`)), responsesReadError{}))
	_, apiErr := OaiChatToResponsesStreamHandler(c, info, response)
	require.Nil(t, apiErr)
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed"))
	require.NotContains(t, recorder.Body.String(), "private upstream")
	require.Contains(t, recorder.Body.String(), "partial")
}

func TestChatResponsesTimeoutReleasesUpstream(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 1
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	for _, started := range []bool{false, true} {
		t.Run(fmt.Sprint(started), func(t *testing.T) {
			released := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(released)
				w.Header().Set("Content-Type", "text/event-stream")
				if started {
					fmt.Fprint(w, chatResponseFrame(`{"delta":{"content":"partial"}}`))
				}
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			response, err := client.Get(server.URL)
			require.NoError(t, err)
			c, recorder, _, info := newResponsesChatTestContext(t, "", true)
			_, apiErr := OaiChatToResponsesStreamHandler(c, info, response)
			if started {
				require.Nil(t, apiErr)
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed"))
			} else {
				require.NotNil(t, apiErr)
				require.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
				require.Empty(t, recorder.Body.String())
			}
			require.NotContains(t, recorder.Body.String(), "event: response.completed")
			select {
			case <-released:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream was not released")
			}
		})
	}
}
