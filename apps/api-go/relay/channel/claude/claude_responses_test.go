package claude

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type claudeResponsesReadError struct{}

func (claudeResponsesReadError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type claudeResponsesFlushFailure struct{ *httptest.ResponseRecorder }

func (claudeResponsesFlushFailure) Flush() { panic("test downstream disconnected") }

func TestClaudeResponsesInterruptedStreamDoesNotCompleteOrExempt(t *testing.T) {
	setClaudeStreamingTimeoutForTest(t)
	setClaudeRefusalNoChargeForTest(t, true)
	start := `{"type":"message_start","message":{"id":"msg_interrupted","model":"claude-sonnet-4-5","content":[],"usage":{"input_tokens":7,"output_tokens":0}}}`
	for _, tc := range []struct {
		name, tail string
		readError  bool
	}{
		{name: "eof_before_final"},
		{name: "invalid_json", tail: `{`},
		{name: "provider_error", tail: `{"type":"error","error":{"type":"overloaded_error","message":"provider overloaded"}}`},
		{name: "transport_error", readError: true},
		{name: "no_final_stop_reason", tail: `{"type":"message_delta","usage":{"output_tokens":0}}`},
		{name: "missing_message_stop", tail: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":0}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder, info := newClaudeRefusalTestContext(types.RelayFormatOpenAIResponses, true)
			info.DisablePing = true
			events := []string{start}
			if tc.tail != "" {
				events = append(events, tc.tail)
			}
			response := claudeRefusalTestResponse(claudeRefusalSSE(events...), true)
			if tc.readError {
				response.Body = io.NopCloser(io.MultiReader(response.Body, claudeResponsesReadError{}))
			}
			gotUsage, apiErr := (&Adaptor{}).DoResponse(c, response, info)
			require.Nil(t, apiErr, "a started stream must settle rather than be retried")
			usage, ok := gotUsage.(*dto.Usage)
			require.True(t, ok)
			require.Equal(t, 7, usage.PromptTokens)
			require.Empty(t, common.GetContextKeyString(c, constant.ContextKeyBillingExemptReason))
			eventsByType := claudeResponsesSSEEvents(t, recorder.Body.String())
			require.Empty(t, eventsByType["response.completed"])
			require.Empty(t, eventsByType["response.incomplete"])
			require.Len(t, eventsByType["response.failed"], 1)
			require.Equal(t, "failed", eventsByType["response.failed"][0].Get("response.status").String())
			require.True(t, info.StreamStatus.HasErrors())
		})
	}
}

func TestClaudeResponsesDisconnectedClientDoesNotWriteTerminalEvent(t *testing.T) {
	setClaudeStreamingTimeoutForTest(t)
	for _, cancelled := range []bool{false, true} {
		name := "flush_failure"
		if cancelled {
			name = "context_cancelled"
		}
		t.Run(name, func(t *testing.T) {
			c, recorder, info := newClaudeRefusalTestContext(types.RelayFormatOpenAIResponses, true)
			info.DisablePing = true
			if cancelled {
				requestCtx, cancel := context.WithCancel(context.Background())
				c.Request = c.Request.WithContext(requestCtx)
				cancel()
			} else {
				newContext, _ := gin.CreateTestContext(&claudeResponsesFlushFailure{recorder})
				newContext.Request = c.Request
				c = newContext
			}
			response := claudeRefusalTestResponse(claudeRefusalSSE(`{"type":"message_start","message":{"id":"msg_disconnected","model":"claude-sonnet-4-5","usage":{"input_tokens":7,"output_tokens":0}}}`), true)
			_, apiErr := (&Adaptor{}).DoResponse(c, response, info)
			require.Nil(t, apiErr)
			require.NotContains(t, recorder.Body.String(), "response.completed")
			require.NotContains(t, recorder.Body.String(), "response.failed")
		})
	}
}

func TestClaudeResponsesRequestConvertsMessagesInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _, info := newClaudeRefusalTestContext(types.RelayFormatOpenAIResponses, false)
	maxTokens := uint(256)
	request := dto.OpenAIResponsesRequest{
		Model:           "claude-sonnet-4-5",
		Instructions:    []byte(`"answer briefly"`),
		Input:           []byte(`[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]`),
		MaxOutputTokens: &maxTokens,
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(c, info, request)
	require.NoError(t, err)
	claudeRequest, ok := converted.(*dto.ClaudeRequest)
	require.True(t, ok, "expected Claude request, got %T", converted)
	assert.Equal(t, request.Model, claudeRequest.Model)
	require.NotNil(t, claudeRequest.MaxTokens)
	assert.Equal(t, maxTokens, *claudeRequest.MaxTokens)
	require.Len(t, claudeRequest.Messages, 1)
	assert.Equal(t, "user", claudeRequest.Messages[0].Role)
	encoded, err := common.Marshal(claudeRequest)
	require.NoError(t, err)
	assert.Equal(t, "hello", gjson.GetBytes(encoded, "messages.0.content.0.text").String())
	assert.Contains(t, string(encoded), "answer briefly")
}

func TestClaudeResponsesJSONPreservesTextToolAndUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, recorder, info := newClaudeRefusalTestContext(types.RelayFormatOpenAIResponses, false)
	body := `{"id":"msg_responses","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"I will check."},{"type":"tool_use","id":"tool_1","name":"lookup","input":{"q":"weather"}}],"stop_reason":"tool_use","usage":{"input_tokens":7,"output_tokens":5}}`

	gotUsage, apiErr := (&Adaptor{}).DoResponse(c, claudeRefusalTestResponse(body, false), info)
	require.Nil(t, apiErr)
	usage, ok := gotUsage.(*dto.Usage)
	require.True(t, ok)
	assert.Equal(t, 7, usage.PromptTokens)
	assert.Equal(t, 5, usage.CompletionTokens)
	assert.Equal(t, 12, usage.TotalTokens)

	response := gjson.Parse(recorder.Body.String())
	assert.Equal(t, "response", response.Get("object").String())
	assert.Equal(t, "completed", response.Get("status").String())
	require.Len(t, response.Get("output").Array(), 2)
	assert.Equal(t, "message", response.Get("output.0.type").String())
	assert.Equal(t, "output_text", response.Get("output.0.content.0.type").String())
	assert.Equal(t, "I will check.", response.Get("output.0.content.0.text").String())
	assert.Equal(t, "function_call", response.Get("output.1.type").String())
	assert.Equal(t, "tool_1", response.Get("output.1.call_id").String())
	assert.Equal(t, "lookup", response.Get("output.1.name").String())
	assert.JSONEq(t, `{"q":"weather"}`, response.Get("output.1.arguments").String())
	assert.EqualValues(t, 7, response.Get("usage.input_tokens").Int())
	assert.EqualValues(t, 5, response.Get("usage.output_tokens").Int())
	assert.False(t, response.Get("choices").Exists())
}

func TestClaudeResponsesSSEPreservesTextToolAndUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setClaudeStreamingTimeoutForTest(t)
	c, recorder, info := newClaudeRefusalTestContext(types.RelayFormatOpenAIResponses, true)
	info.DisablePing = true
	body := claudeRefusalSSE(
		`{"type":"message_start","message":{"id":"msg_responses","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"usage":{"input_tokens":7,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"I will check."}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tool_1","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"weather\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	)

	gotUsage, apiErr := (&Adaptor{}).DoResponse(c, claudeRefusalTestResponse(body, true), info)
	require.Nil(t, apiErr)
	usage, ok := gotUsage.(*dto.Usage)
	require.True(t, ok)
	assert.Equal(t, 7, usage.PromptTokens)
	assert.Equal(t, 5, usage.CompletionTokens)
	assert.Equal(t, 12, usage.TotalTokens)
	assert.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))

	events := claudeResponsesSSEEvents(t, recorder.Body.String())
	require.NotEmpty(t, events["response.created"])
	textDeltas := events["response.output_text.delta"]
	require.Len(t, textDeltas, 1)
	assert.Equal(t, "I will check.", textDeltas[0].Get("delta").String())
	var arguments strings.Builder
	for _, event := range events["response.function_call_arguments.delta"] {
		arguments.WriteString(event.Get("delta").String())
	}
	assert.JSONEq(t, `{"q":"weather"}`, arguments.String())
	completed := events["response.completed"]
	require.Len(t, completed, 1)
	response := completed[0].Get("response")
	assert.Equal(t, "completed", response.Get("status").String())
	require.Len(t, response.Get("output").Array(), 2)
	assert.Equal(t, "I will check.", response.Get("output.0.content.0.text").String())
	assert.Equal(t, "function_call", response.Get("output.1.type").String())
	assert.Equal(t, "tool_1", response.Get("output.1.call_id").String())
	assert.Equal(t, "lookup", response.Get("output.1.name").String())
	assert.JSONEq(t, `{"q":"weather"}`, response.Get("output.1.arguments").String())
	assert.EqualValues(t, 7, response.Get("usage.input_tokens").Int())
	assert.EqualValues(t, 5, response.Get("usage.output_tokens").Int())
}

func claudeResponsesSSEEvents(t *testing.T, body string) map[string][]gjson.Result {
	t.Helper()
	events := make(map[string][]gjson.Result)
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		require.True(t, gjson.Valid(data), "invalid Responses event: %s", data)
		event := gjson.Parse(data)
		events[event.Get("type").String()] = append(events[event.Get("type").String()], event)
	}
	return events
}
