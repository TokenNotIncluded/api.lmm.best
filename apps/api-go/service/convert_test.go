package service

import (
	"encoding/json"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseConverterFacades(t *testing.T) {
	cache5m, cache1h := NormalizeCacheCreationSplit(10, 3, 2)
	assert.Equal(t, 8, cache5m)
	assert.Equal(t, 2, cache1h)

	chatResp := &dto.OpenAITextResponse{
		Id:    "chatcmpl_1",
		Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Message: dto.Message{
					Role:    "assistant",
					Content: "hello",
				},
				FinishReason: "stop",
			},
		},
	}

	claudeResp := ResponseOpenAI2Claude(chatResp, &relaycommon.RelayInfo{})
	require.NotNil(t, claudeResp)
	assert.Equal(t, "message", claudeResp.Type)

	geminiResp := ResponseOpenAI2Gemini(chatResp, &relaycommon.RelayInfo{})
	require.NotNil(t, geminiResp)
	require.Len(t, geminiResp.Candidates, 1)
}

func TestStreamResponseConverterFacades(t *testing.T) {
	info := &relaycommon.RelayInfo{
		SendResponseCount: 1,
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeNone,
		},
	}
	streamResp := &dto.ChatCompletionsStreamResponse{
		Id:    "chatcmpl_1",
		Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					Content: ptrValue("hello"),
				},
			},
		},
	}

	claudeResponses := StreamResponseOpenAI2Claude(streamResp, info)
	require.NotEmpty(t, claudeResponses)

	geminiResp := StreamResponseOpenAI2Gemini(streamResp, &relaycommon.RelayInfo{})
	require.NotNil(t, geminiResp)
	require.Len(t, geminiResp.Candidates, 1)
}

func TestRequestConverterFacadeAcceptsTypedNilRelayInfo(t *testing.T) {
	for _, target := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatGemini} {
		t.Run(string(target), func(t *testing.T) {
			var info *relaycommon.RelayInfo
			request := &dto.GeneralOpenAIRequest{
				Model: "test-model",
				Messages: []dto.Message{
					{Role: "user", Content: "hello"},
				},
			}

			result, err := ConvertRequest(nil, info, target, request)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, target, result.To)
		})
	}
}

func TestRequestConverterFacadeWithoutChannelMeta(t *testing.T) {
	result, err := ConvertRequest(nil, &relaycommon.RelayInfo{IsStream: true}, types.RelayFormatOpenAI, &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}}},
	})
	require.NoError(t, err)
	chatRequest, ok := result.Value.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	assert.Nil(t, chatRequest.StreamOptions)
}

func TestCrossProtocolStreamingRequestsIncludeUsage(t *testing.T) {
	newInfo := func(isStream, supportsStreamOptions bool) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{
			IsStream: isStream,
			ChannelMeta: &relaycommon.ChannelMeta{
				SupportStreamOptions: supportsStreamOptions,
			},
		}
	}

	t.Run("direct Gemini conversion", func(t *testing.T) {
		result, err := ConvertRequest(nil, newInfo(true, true), types.RelayFormatOpenAI, &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}}},
		})
		require.NoError(t, err)
		chatRequest, ok := result.Value.(*dto.GeneralOpenAIRequest)
		require.True(t, ok)
		require.NotNil(t, chatRequest.StreamOptions)
		assert.True(t, chatRequest.StreamOptions.IncludeUsage)
	})

	byIDCases := []struct {
		name      string
		converter string
		request   any
	}{
		{
			name:      "Claude",
			converter: relayconvert.ConverterClaudeMessagesToOpenAIChat,
			request: &dto.ClaudeRequest{
				Model:    "gpt-test",
				Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
			},
		},
		{
			name:      "Gemini",
			converter: relayconvert.ConverterGeminiContentToOpenAIChat,
			request: &dto.GeminiChatRequest{
				Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}}},
			},
		},
		{
			name:      "Responses",
			converter: relayconvert.ConverterOpenAIResponsesToOpenAIChat,
			request: &dto.OpenAIResponsesRequest{
				Model: "gpt-test",
				Input: json.RawMessage(`"hello"`),
			},
		},
	}

	for _, tc := range byIDCases {
		t.Run("advanced custom "+tc.name, func(t *testing.T) {
			result, err := ConvertRequestByID(nil, newInfo(true, true), tc.converter, tc.request)
			require.NoError(t, err)
			chatRequest, ok := result.Value.(*dto.GeneralOpenAIRequest)
			require.True(t, ok)
			require.NotNil(t, chatRequest.StreamOptions)
			assert.True(t, chatRequest.StreamOptions.IncludeUsage)
		})
	}
}

func TestCrossProtocolStreamUsageRespectsStreamCapability(t *testing.T) {
	request := func() *dto.GeminiChatRequest {
		return &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}}},
		}
	}

	tests := []struct {
		name                  string
		isStream              bool
		supportsStreamOptions bool
	}{
		{name: "non-stream", isStream: false, supportsStreamOptions: true},
		{name: "unsupported upstream", isStream: true, supportsStreamOptions: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				IsStream: tc.isStream,
				ChannelMeta: &relaycommon.ChannelMeta{
					SupportStreamOptions: tc.supportsStreamOptions,
				},
			}
			result, err := ConvertRequest(nil, info, types.RelayFormatOpenAI, request())
			require.NoError(t, err)
			chatRequest, ok := result.Value.(*dto.GeneralOpenAIRequest)
			require.True(t, ok)
			assert.Nil(t, chatRequest.StreamOptions)
		})
	}
}

func TestStreamResponseConverterFacadesAcceptTypedNilRelayInfo(t *testing.T) {
	var info *relaycommon.RelayInfo
	streamResp := &dto.ChatCompletionsStreamResponse{
		Id:    "chatcmpl_typed_nil",
		Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					Content: ptrValue("hello"),
				},
			},
		},
	}

	claudeResponses := StreamResponseOpenAI2Claude(streamResp, info)
	require.NotEmpty(t, claudeResponses)
	assert.Equal(t, "content_block_start", claudeResponses[0].Type)

	geminiResp := StreamResponseOpenAI2Gemini(streamResp, info)
	require.NotNil(t, geminiResp)
	require.Len(t, geminiResp.Candidates, 1)
	assert.Zero(t, geminiResp.UsageMetadata.PromptTokenCount)
}

func ptrValue[T any](value T) *T {
	return &value
}
