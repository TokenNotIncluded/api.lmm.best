package ollama

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaStreamRetainsDonePayload(t *testing.T) {
	for _, payload := range []string{
		`"message":{"content":"final text","thinking":"final thought","tool_calls":[{"function":{"name":"weather","arguments":{"city":"Paris"}}},{"function":{"name":"clock","arguments":{}}}]}`,
		`"response":"final text"`,
		`"message":{"content":""}`,
	} {
		t.Run(payload, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			body := `{"model":"test",` + payload + `,"done":true,"prompt_eval_count":5,"eval_count":7}` + "\n"
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
			usage, apiErr := ollamaStreamHandler(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "test"}}, resp)
			require.Nil(t, apiErr)
			require.Equal(t, 12, usage.TotalTokens)
			var calls []dto.ToolCallResponse
			var content, reasoning, finish string
			var chunks int
			for _, line := range strings.Split(w.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
					continue
				}
				var event dto.ChatCompletionsStreamResponse
				require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event))
				chunks++
				for _, choice := range event.Choices {
					content += choice.Delta.GetContentString()
					calls = append(calls, choice.Delta.ToolCalls...)
					if choice.Delta.ReasoningContent != nil {
						reasoning += *choice.Delta.ReasoningContent
					}
					if choice.FinishReason != nil {
						finish = *choice.FinishReason
					}
				}
			}
			require.Equal(t, 1, strings.Count(w.Body.String(), "data: [DONE]"))
			if strings.Contains(payload, "tool_calls") {
				require.Len(t, calls, 2)
				require.Equal(t, 0, *calls[0].Index)
				require.Equal(t, 1, *calls[1].Index)
				require.JSONEq(t, `{"city":"Paris"}`, calls[0].Function.Arguments)
				require.Equal(t, "final thought", reasoning)
				require.Equal(t, "tool_calls", finish)
			} else {
				require.Equal(t, "stop", finish)
			}
			if strings.Contains(payload, "final text") {
				require.Equal(t, "final text", content)
			} else {
				require.Empty(t, content)
				require.Equal(t, 3, chunks)
			}
		})
	}
}

func TestOllamaChatHandlerNonStreamToolCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		raw    string
		wantID string
	}{
		{
			name:   "compact json per-line parse path",
			raw:    `{"model":"llama3.1","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_upstream","function":{"name":"get_weather","arguments":{"city":"Paris","days":0}}}]},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`,
			wantID: "call_upstream",
		},
		{
			name: "pretty json fallback parse path",
			raw: `{
  "model": "llama3.1",
  "created_at": "2026-05-27T12:00:00Z",
  "message": {
    "role": "assistant",
    "content": "",
    "tool_calls": [
      {
        "function": {
          "name": "get_weather",
          "arguments": {
            "city": "Paris",
            "days": 0
          }
        }
      }
    ]
  },
  "done": true,
  "done_reason": "stop",
  "prompt_eval_count": 5,
  "eval_count": 7
}`,
			wantID: "call_0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.raw)),
			}

			usage, apiErr := ollamaChatHandler(c, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
			}, resp)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, 12, usage.TotalTokens)

			var out dto.OpenAITextResponse
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &out))
			require.Len(t, out.Choices, 1)
			assert.Equal(t, constant.FinishReasonToolCalls, out.Choices[0].FinishReason)

			var toolCalls []dto.ToolCallResponse
			require.NoError(t, common.Unmarshal(out.Choices[0].Message.ToolCalls, &toolCalls))
			require.Len(t, toolCalls, 1)
			assert.Equal(t, tt.wantID, toolCalls[0].ID)
			assert.Equal(t, "function", toolCalls[0].Type)
			assert.Equal(t, "get_weather", toolCalls[0].Function.Name)
			assert.Nil(t, toolCalls[0].Index)

			var args map[string]any
			require.NoError(t, common.Unmarshal([]byte(toolCalls[0].Function.Arguments), &args))
			assert.Equal(t, "Paris", args["city"])
			assert.Equal(t, float64(0), args["days"])
		})
	}
}

func TestOpenAIChatToOllamaPreservesReasoningAndToolContext(t *testing.T) {
	reasoning := "planning"
	toolMessage := dto.Message{Role: "tool", Content: "Paris", ToolCallId: "call_weather"}
	assistantMessage := dto.Message{
		Role:             "assistant",
		Content:          "",
		ReasoningContent: &reasoning,
	}
	assistantMessage.SetToolCalls([]dto.ToolCallRequest{{
		ID:   "call_weather",
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      "get_weather",
			Arguments: `{"city":"Paris"}`,
		},
	}})

	request, err := openAIChatToOllamaChat(nil, &dto.GeneralOpenAIRequest{
		Model: "llama3.1",
		Messages: []dto.Message{
			assistantMessage,
			toolMessage,
		},
		ReasoningEffort: "high",
	})
	require.NoError(t, err)
	require.Len(t, request.Messages, 2)
	assert.Equal(t, "call_weather", request.Messages[0].ToolCalls[0].ID)
	assert.Equal(t, `"planning"`, string(request.Messages[0].Thinking))
	assert.Equal(t, "call_weather", request.Messages[1].ToolCallID)
	assert.Equal(t, "get_weather", request.Messages[1].ToolName)
	assert.Equal(t, `"high"`, string(request.Think))
}
