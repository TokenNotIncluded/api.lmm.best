package ollama

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ollamaOpenAIChatInfo(mode int, format types.RelayFormat, passThrough bool) *relaycommon.RelayInfo {
	info := ollamaTestRelayInfo(mode, format, passThrough)
	info.ChannelType = constant.ChannelTypeOllama
	info.UpstreamModelName = "llama3.2"
	info.ChannelOtherSettings.OllamaOpenAIChat = true
	return info
}

func ollamaTestStreamTimeout(t *testing.T) {
	t.Helper()
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = previous })
}

func TestOllamaOpenAIChatTransportScope(t *testing.T) {
	for _, test := range []struct {
		name        string
		mode        int
		format      types.RelayFormat
		passThrough bool
		path        string
		wantPath    string
	}{
		{"openai chat", relayconstant.RelayModeChatCompletions, types.RelayFormatOpenAI, false, "/v1/chat/completions", "/v1/chat/completions"},
		{"openai chat body passthrough", relayconstant.RelayModeChatCompletions, types.RelayFormatOpenAI, true, "/v1/chat/completions", "/v1/chat/completions"},
		{"legacy claude conversion", relayconstant.RelayModeUnknown, types.RelayFormatClaude, false, "/v1/messages", "/api/chat"},
		{"native claude passthrough", relayconstant.RelayModeUnknown, types.RelayFormatClaude, true, "/v1/messages", "/v1/messages"},
		{"completions", relayconstant.RelayModeCompletions, types.RelayFormatOpenAI, false, "/v1/completions", "/api/generate"},
		{"legacy completions path", relayconstant.RelayModeChatCompletions, types.RelayFormatOpenAI, false, "/v1/completions", "/api/generate"},
		{"embeddings", relayconstant.RelayModeEmbeddings, types.RelayFormatOpenAI, false, "/v1/embeddings", "/api/embed"},
		{"responses", relayconstant.RelayModeResponses, types.RelayFormatOpenAIResponses, false, "/v1/responses", "/v1/responses"},
		{"other modes retain native fallback", relayconstant.RelayModeUnknown, types.RelayFormatOpenAI, false, "/other", "/api/chat"},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := ollamaOpenAIChatInfo(test.mode, test.format, test.passThrough)
			info.RequestURLPath = test.path
			url, err := (&Adaptor{}).GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, "http://ollama.test"+test.wantPath, url)
		})
	}
}

func TestOllamaOpenAIChatConversionAndUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		info := ollamaOpenAIChatInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatOpenAI, false)
		info.IsStream = stream
		info.UpstreamModelName = "gpt-5"
		maxTokens := uint(25)
		request := &dto.GeneralOpenAIRequest{
			Model: "gpt-5", Messages: []dto.Message{{Role: "system", Content: "instructions"}, {Role: "user", Content: "hello"}},
			Stream: &stream, MaxTokens: &maxTokens,
			Tools:          []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "weather", Parameters: map[string]any{"type": "object"}}}},
			ResponseFormat: &dto.ResponseFormat{Type: "json_object"},
		}
		converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
		require.NoError(t, err)
		require.Same(t, request, converted)
		assert.Equal(t, "developer", request.Messages[0].Role, "OpenAI capability conversion must be reused")
		assert.Nil(t, request.MaxTokens)
		require.NotNil(t, request.MaxCompletionTokens)
		assert.Equal(t, maxTokens, *request.MaxCompletionTokens)
		require.Len(t, request.Tools, 1)
		assert.Equal(t, "weather", request.Tools[0].Function.Name)
		assert.Equal(t, "json_object", request.ResponseFormat.Type)
		if stream {
			require.NotNil(t, request.StreamOptions)
			assert.True(t, request.StreamOptions.IncludeUsage)
		} else {
			assert.Nil(t, request.StreamOptions)
		}
	}
	_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, ollamaOpenAIChatInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatOpenAI, false), nil)
	require.EqualError(t, err, "request is nil")
}

func TestOllamaOpenAIChatKeepsNativeConversions(t *testing.T) {
	info := ollamaOpenAIChatInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatOpenAI, false)
	info.ChannelOtherSettings.OllamaOpenAIChat = false
	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &dto.GeneralOpenAIRequest{Model: "llama3.2", Messages: []dto.Message{{Role: "user", Content: "hello"}}})
	require.NoError(t, err)
	require.IsType(t, &OllamaChatRequest{}, converted)
	for _, mode := range []int{relayconstant.RelayModeCompletions, relayconstant.RelayModeChatCompletions} {
		info := ollamaOpenAIChatInfo(mode, types.RelayFormatOpenAI, false)
		info.RequestURLPath = "/v1/completions"
		converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &dto.GeneralOpenAIRequest{Model: "llama3.2", Prompt: "complete this"})
		require.NoError(t, err)
		require.IsType(t, &OllamaGenerateRequest{}, converted)
		assert.Equal(t, "complete this", converted.(*OllamaGenerateRequest).Prompt)
	}
	embeddingInfo := ollamaOpenAIChatInfo(relayconstant.RelayModeEmbeddings, types.RelayFormatOpenAI, false)
	converted, err = (&Adaptor{}).ConvertEmbeddingRequest(nil, embeddingInfo, dto.EmbeddingRequest{Model: "llama3.2", Input: "embed this"})
	require.NoError(t, err)
	require.IsType(t, &OllamaEmbeddingRequest{}, converted)
	assert.Equal(t, "embed this", converted.(*OllamaEmbeddingRequest).Input)
}

func TestOllamaOpenAIChatDoesNotChangeClaudeConversion(t *testing.T) {
	c, _ := ollamaTestContext("/v1/messages")
	for _, passThrough := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			info := ollamaOpenAIChatInfo(relayconstant.RelayModeUnknown, types.RelayFormatClaude, passThrough)
			info.RequestURLPath = "/v1/messages"
			info.ChannelOtherSettings.OllamaOpenAIChat = enabled
			request := &dto.ClaudeRequest{Model: "llama3.2", Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}}}
			converted, err := (&Adaptor{}).ConvertClaudeRequest(c, info, request)
			require.NoError(t, err)
			if passThrough {
				assert.Same(t, request, converted)
			} else {
				require.IsType(t, &OllamaChatRequest{}, converted)
				assert.Equal(t, "hello", converted.(*OllamaChatRequest).Messages[0].Content)
			}
		}
	}
}

func TestOllamaOpenAIChatThinkingState(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		info := ollamaOpenAIChatInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatOpenAI, false)
		info.ChannelOtherSettings.OllamaOpenAIChat = enabled
		info.ChannelSetting.ThinkingToContent = true
		adaptor := &Adaptor{}
		adaptor.Init(info)
		assert.Equal(t, enabled, info.ThinkingContentInfo.IsFirstThinkingContent)
		assert.False(t, info.ThinkingContentInfo.HasSentThinkingContent)
	}
}

func TestOllamaOpenAIChatNonStreamAndErrors(t *testing.T) {
	info := ollamaOpenAIChatInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatOpenAI, false)
	c, recorder := ollamaTestContext("/v1/chat/completions")
	body := `{"id":"chatcmpl_ollama","object":"chat.completion","model":"llama3.2","choices":[{"index":0,"message":{"role":"assistant","content":"hello","reasoning_content":"plan"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5},"ollama_extra":"preserved"}`
	usageAny, apiErr := (&Adaptor{}).DoResponse(c, ollamaTestHTTPResponse(body), info)
	require.Nil(t, apiErr)
	usage := usageAny.(*dto.Usage)
	assert.Equal(t, 2, usage.PromptTokens)
	assert.Equal(t, 3, usage.CompletionTokens)
	assert.Equal(t, 5, usage.TotalTokens)
	assert.JSONEq(t, body, recorder.Body.String())

	c, recorder = ollamaTestContext("/v1/chat/completions")
	response := ollamaTestHTTPResponse(`{"error":{"message":"slow down","type":"rate_limit_error","code":"rate_limit"}}`)
	response.StatusCode = http.StatusTooManyRequests
	_, apiErr = (&Adaptor{}).DoResponse(c, response, info)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
	assert.Equal(t, types.ErrorTypeOpenAIError, apiErr.GetErrorType())
	assert.Contains(t, apiErr.Error(), "slow down")
	assert.Empty(t, recorder.Body.String())
}

func TestOllamaOpenAIChatStreamUsageAndThinking(t *testing.T) {
	ollamaTestStreamTimeout(t)
	for _, includeUsage := range []bool{false, true} {
		info := ollamaOpenAIChatInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatOpenAI, false)
		info.IsStream, info.DisablePing, info.ShouldIncludeUsage = true, true, includeUsage
		info.ChannelSetting.ThinkingToContent = true
		adaptor := &Adaptor{}
		adaptor.Init(info)
		c, recorder := ollamaTestContext("/v1/chat/completions")
		body := "data: " + `{"id":"chatcmpl_ollama","object":"chat.completion.chunk","model":"llama3.2","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"plan"},"finish_reason":null}]}` + "\n\n" +
			"data: " + `{"id":"chatcmpl_ollama","object":"chat.completion.chunk","model":"llama3.2","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}` + "\n\n" +
			"data: " + `{"id":"chatcmpl_ollama","object":"chat.completion.chunk","model":"llama3.2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
			"data: " + `{"id":"chatcmpl_ollama","object":"chat.completion.chunk","model":"llama3.2","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}` + "\n\ndata: [DONE]\n\n"
		response := ollamaTestHTTPResponse(body)
		response.Header.Set("Content-Type", "text/event-stream")
		usageAny, apiErr := adaptor.DoResponse(c, response, info)
		require.Nil(t, apiErr)
		usage := usageAny.(*dto.Usage)
		assert.Equal(t, 2, usage.PromptTokens)
		assert.Equal(t, 3, usage.CompletionTokens)
		assert.Equal(t, 5, usage.TotalTokens)
		assert.Equal(t, 1, strings.Count(recorder.Body.String(), "data: [DONE]"))
		var content string
		var usageEvents int
		for _, line := range strings.Split(recorder.Body.String(), "\n") {
			if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
				continue
			}
			var chunk dto.ChatCompletionsStreamResponse
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk))
			for _, choice := range chunk.Choices {
				content += choice.Delta.GetContentString()
			}
			if chunk.Usage != nil && chunk.Usage.TotalTokens > 0 {
				usageEvents++
			}
		}
		assert.Equal(t, "<think>\nplan\n</think>\nhello", content)
		if includeUsage {
			assert.Equal(t, 1, usageEvents)
		} else {
			assert.Zero(t, usageEvents)
		}
	}
}

func TestOllamaOpenAIChatProtectedResponseDispatch(t *testing.T) {
	for _, test := range []struct {
		name        string
		mode        int
		format      types.RelayFormat
		passThrough bool
		body        string
		contains    string
	}{
		{"native claude", relayconstant.RelayModeUnknown, types.RelayFormatClaude, true, `{"id":"msg_1","type":"message","role":"assistant","model":"llama3.2","content":[{"type":"text","text":"native"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}`, `"id":"msg_1"`},
		{"legacy claude conversion", relayconstant.RelayModeUnknown, types.RelayFormatClaude, false, `{"model":"llama3.2","message":{"role":"assistant","content":"legacy"},"done":true,"prompt_eval_count":2,"eval_count":3}`, `"object":"chat.completion"`},
		{"responses", relayconstant.RelayModeResponses, types.RelayFormatOpenAIResponses, false, `{"id":"resp_1","object":"response","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`, `"id":"resp_1"`},
		{"generate", relayconstant.RelayModeCompletions, types.RelayFormatOpenAI, false, `{"model":"llama3.2","response":"generated","done":true,"prompt_eval_count":2,"eval_count":3}`, "generated"},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := ollamaOpenAIChatInfo(test.mode, test.format, test.passThrough)
			c, recorder := ollamaTestContext("/")
			usageAny, apiErr := (&Adaptor{}).DoResponse(c, ollamaTestHTTPResponse(test.body), info)
			require.Nil(t, apiErr)
			assert.Equal(t, 5, usageAny.(*dto.Usage).TotalTokens)
			assert.Contains(t, recorder.Body.String(), test.contains)
		})
	}
	for _, mode := range []int{relayconstant.RelayModeChatCompletions, relayconstant.RelayModeCompletions} {
		info := ollamaOpenAIChatInfo(mode, types.RelayFormatOpenAI, false)
		if mode == relayconstant.RelayModeChatCompletions {
			info.ChannelOtherSettings.OllamaOpenAIChat = false
		}
		info.IsStream = true
		c, recorder := ollamaTestContext("/")
		usageAny, apiErr := (&Adaptor{}).DoResponse(c, ollamaTestHTTPResponse(`{"model":"llama3.2","message":{"role":"assistant","content":"native"},"response":"native","done":true,"prompt_eval_count":2,"eval_count":3}`+"\n"), info)
		require.Nil(t, apiErr)
		assert.Equal(t, 5, usageAny.(*dto.Usage).TotalTokens)
		assert.Contains(t, recorder.Body.String(), "native")
		assert.Equal(t, 1, strings.Count(recorder.Body.String(), "data: [DONE]"))
	}
	info := ollamaOpenAIChatInfo(relayconstant.RelayModeEmbeddings, types.RelayFormatOpenAI, false)
	c, recorder := ollamaTestContext("/")
	usageAny, apiErr := (&Adaptor{}).DoResponse(c, ollamaTestHTTPResponse(`{"model":"llama3.2","embeddings":[[0.5,0.2]],"prompt_eval_count":2}`), info)
	require.Nil(t, apiErr)
	assert.Equal(t, 2, usageAny.(*dto.Usage).TotalTokens)
	assert.Contains(t, recorder.Body.String(), `"object":"embedding"`)
	var output dto.OpenAIEmbeddingResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &output))
	assert.Equal(t, []float64{0.5, 0.2}, output.Data[0].Embedding)
}

func TestOllamaOpenAIChatProtectedStreamingEndpoints(t *testing.T) {
	ollamaTestStreamTimeout(t)
	for _, test := range []struct {
		name        string
		mode        int
		format      types.RelayFormat
		passThrough bool
		body        string
		terminal    string
	}{
		{"native claude", relayconstant.RelayModeUnknown, types.RelayFormatClaude, true,
			"event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"llama3.2","content":[],"usage":{"input_tokens":2,"output_tokens":0}}}` + "\n\n" +
				"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}` + "\n\n" +
				"event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}` + "\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "event: message_stop"},
		{"responses", relayconstant.RelayModeResponses, types.RelayFormatOpenAIResponses, false,
			"event: response.created\ndata: " + `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}` + "\n\n" +
				"event: response.output_text.delta\ndata: " + `{"type":"response.output_text.delta","delta":"hello"}` + "\n\n" +
				"event: response.completed\ndata: " + `{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}` + "\n\n", "event: response.completed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := ollamaOpenAIChatInfo(test.mode, test.format, test.passThrough)
			info.IsStream, info.DisablePing = true, true
			c, recorder := ollamaTestContext("/")
			response := ollamaTestHTTPResponse(test.body)
			response.Header.Set("Content-Type", "text/event-stream")
			usageAny, apiErr := (&Adaptor{}).DoResponse(c, response, info)
			require.Nil(t, apiErr)
			assert.Equal(t, 5, usageAny.(*dto.Usage).TotalTokens)
			assert.Contains(t, recorder.Body.String(), "hello")
			assert.Equal(t, 1, strings.Count(recorder.Body.String(), test.terminal))
		})
	}
}

func TestOllamaOpenAIChatHTTPTransport(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(stringBool(enabled), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "Bearer ollama-key", r.Header.Get("Authorization"))
				if enabled {
					assert.Equal(t, "/v1/chat/completions", r.URL.Path)
					var body dto.GeneralOpenAIRequest
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					require.Len(t, body.Messages, 1)
					assert.Equal(t, "hello", body.Messages[0].Content)
					_, _ = w.Write([]byte(`{"id":"chatcmpl_http","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`))
				} else {
					assert.Equal(t, "/api/chat", r.URL.Path)
					var body OllamaChatRequest
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					require.Len(t, body.Messages, 1)
					assert.Equal(t, "hello", body.Messages[0].Content)
					_, _ = w.Write([]byte(`{"model":"llama3.2","message":{"role":"assistant","content":"reply"},"done":true,"prompt_eval_count":2,"eval_count":3}`))
				}
			}))
			defer server.Close()
			info := ollamaOpenAIChatInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatOpenAI, false)
			info.ChannelBaseUrl = server.URL
			info.ChannelOtherSettings.OllamaOpenAIChat = enabled
			c, recorder := ollamaTestContext("/v1/chat/completions")
			adaptor := &Adaptor{}
			adaptor.Init(info)
			converted, err := adaptor.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{Model: "llama3.2", Messages: []dto.Message{{Role: "user", Content: "hello"}}})
			require.NoError(t, err)
			body, err := common.Marshal(converted)
			require.NoError(t, err)
			responseAny, err := adaptor.DoRequest(c, info, bytes.NewReader(body))
			require.NoError(t, err)
			usageAny, apiErr := adaptor.DoResponse(c, responseAny.(*http.Response), info)
			require.Nil(t, apiErr)
			assert.Equal(t, 5, usageAny.(*dto.Usage).TotalTokens)
			assert.Contains(t, recorder.Body.String(), "reply")
		})
	}
}

func stringBool(enabled bool) string {
	if enabled {
		return "openai"
	}
	return "native"
}
