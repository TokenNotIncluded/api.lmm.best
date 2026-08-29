package ollama

import (
	"io"
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

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ollamaTestRelayInfo(relayMode int, relayFormat types.RelayFormat, passThrough bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayMode:      relayMode,
		RelayFormat:    relayFormat,
		RequestURLPath: "/v1/chat/completions",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "http://ollama.test",
			ApiKey:         "ollama-key",
			ChannelSetting: dto.ChannelSettings{PassThroughBodyEnabled: passThrough},
		},
	}
}

func ollamaTestContext(path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c, recorder
}

func ollamaTestHTTPResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestAdaptorRequestURLs(t *testing.T) {
	adaptor := &Adaptor{}
	tests := []struct {
		name        string
		info        *relaycommon.RelayInfo
		requestPath string
		want        string
	}{
		{
			name: "responses without pass-through",
			info: ollamaTestRelayInfo(relayconstant.RelayModeResponses, types.RelayFormatOpenAIResponses, false),
			want: "http://ollama.test/v1/responses",
		},
		{
			name: "responses compact with pass-through",
			info: ollamaTestRelayInfo(relayconstant.RelayModeResponsesCompact, types.RelayFormatOpenAIResponses, true),
			want: "http://ollama.test/v1/responses/compact",
		},
		{
			name: "claude legacy by default",
			info: ollamaTestRelayInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatClaude, false),
			want: "http://ollama.test/api/chat",
		},
		{
			name: "claude native when opted in",
			info: ollamaTestRelayInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatClaude, true),
			want: "http://ollama.test/v1/messages",
		},
		{
			name: "chat completions unchanged",
			info: ollamaTestRelayInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatOpenAI, true),
			want: "http://ollama.test/api/chat",
		},
		{
			name: "legacy completions unchanged",
			info: ollamaTestRelayInfo(relayconstant.RelayModeCompletions, types.RelayFormatOpenAI, true),
			want: "http://ollama.test/api/generate",
		},
		{
			name: "embeddings unchanged",
			info: ollamaTestRelayInfo(relayconstant.RelayModeEmbeddings, types.RelayFormatOpenAI, true),
			want: "http://ollama.test/api/embed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := adaptor.GetRequestURL(test.info)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestAdaptorClaudeConversionRequiresChannelOptIn(t *testing.T) {
	adaptor := &Adaptor{}
	c, _ := ollamaTestContext("/v1/messages")
	request := &dto.ClaudeRequest{
		Model: "llama3.2",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}

	legacyInfo := ollamaTestRelayInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatClaude, false)
	legacy, err := adaptor.ConvertClaudeRequest(c, legacyInfo, request)
	require.NoError(t, err)
	legacyRequest, ok := legacy.(*OllamaChatRequest)
	require.True(t, ok, "default Claude path must remain Claude -> OpenAI -> Ollama chat")
	assert.Equal(t, "llama3.2", legacyRequest.Model)
	require.Len(t, legacyRequest.Messages, 1)
	assert.Equal(t, "hello", legacyRequest.Messages[0].Content)

	nativeInfo := ollamaTestRelayInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatClaude, true)
	native, err := adaptor.ConvertClaudeRequest(c, nativeInfo, request)
	require.NoError(t, err)
	assert.Same(t, request, native, "native Claude path must use the Claude adaptor without conversion")
}

func TestAdaptorNativeClaudeHeadersRequireChannelOptIn(t *testing.T) {
	adaptor := &Adaptor{}
	c, _ := ollamaTestContext("/v1/messages")
	c.Request.Header.Set("anthropic-version", "2024-01-01")
	c.Request.Header.Set("anthropic-beta", "tools-2024-04-04")

	legacyHeaders := make(http.Header)
	require.NoError(t, adaptor.SetupRequestHeader(c, &legacyHeaders,
		ollamaTestRelayInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatClaude, false)))
	assert.Equal(t, "Bearer ollama-key", legacyHeaders.Get("Authorization"))
	assert.Empty(t, legacyHeaders.Get("anthropic-version"))
	assert.Empty(t, legacyHeaders.Get("anthropic-beta"))

	nativeHeaders := make(http.Header)
	require.NoError(t, adaptor.SetupRequestHeader(c, &nativeHeaders,
		ollamaTestRelayInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatClaude, true)))
	assert.Equal(t, "Bearer ollama-key", nativeHeaders.Get("Authorization"))
	assert.Equal(t, "2024-01-01", nativeHeaders.Get("anthropic-version"))
	assert.Equal(t, "tools-2024-04-04", nativeHeaders.Get("anthropic-beta"))
}

func TestAdaptorResponsesConversionUsesOpenAIAdaptor(t *testing.T) {
	adaptor := &Adaptor{}
	c, _ := ollamaTestContext("/v1/responses")
	info := ollamaTestRelayInfo(relayconstant.RelayModeResponses, types.RelayFormatOpenAIResponses, false)

	converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{Model: "gpt-5-high"})
	require.NoError(t, err)
	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Equal(t, "gpt-5", request.Model)
	require.NotNil(t, request.Reasoning)
	assert.Equal(t, "high", request.Reasoning.Effort)
	assert.Equal(t, "high", info.GetReasoningEffort())
}

func TestAdaptorResponseDispatch(t *testing.T) {
	adaptor := &Adaptor{}
	tests := []struct {
		name     string
		info     *relaycommon.RelayInfo
		body     string
		contains string
	}{
		{
			name:     "legacy Claude uses Ollama response handling",
			info:     ollamaTestRelayInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatClaude, false),
			body:     `{"model":"llama3.2","message":{"role":"assistant","content":"legacy"},"done":true,"prompt_eval_count":2,"eval_count":3}`,
			contains: `"object":"chat.completion"`,
		},
		{
			name:     "native Claude uses Claude response handling",
			info:     ollamaTestRelayInfo(relayconstant.RelayModeChatCompletions, types.RelayFormatClaude, true),
			body:     `{"id":"msg_1","type":"message","role":"assistant","model":"llama3.2","content":[{"type":"text","text":"native"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}}`,
			contains: `"id":"msg_1"`,
		},
		{
			name:     "responses uses OpenAI response handling",
			info:     ollamaTestRelayInfo(relayconstant.RelayModeResponses, types.RelayFormatOpenAIResponses, false),
			body:     `{"id":"resp_1","object":"response","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`,
			contains: `"id":"resp_1"`,
		},
		{
			name:     "responses compact uses OpenAI response handling",
			info:     ollamaTestRelayInfo(relayconstant.RelayModeResponsesCompact, types.RelayFormatOpenAIResponses, false),
			body:     `{"id":"resp_compact_1","object":"response.compaction","output":[],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`,
			contains: `"id":"resp_compact_1"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, recorder := ollamaTestContext("/")
			usageAny, apiErr := adaptor.DoResponse(c, ollamaTestHTTPResponse(test.body), test.info)
			require.Nil(t, apiErr)
			usage, ok := usageAny.(*dto.Usage)
			require.True(t, ok)
			assert.Equal(t, 2, usage.PromptTokens)
			assert.Equal(t, 3, usage.CompletionTokens)
			assert.Equal(t, 5, usage.TotalTokens)
			assert.Contains(t, recorder.Body.String(), test.contains)
		})
	}
}

func TestOllamaSupportsResponsesCompact(t *testing.T) {
	assert.True(t, common.SupportsResponsesCompact(constant.ChannelTypeOllama, constant.APITypeOllama))
}
