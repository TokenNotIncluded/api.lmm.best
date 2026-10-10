package relayconvert

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestCacheClassificationAndDurationSurviveChatResponsesConversion(t *testing.T) {
	seconds := 12.5
	chat := textRegistryChatResponse()
	chat.Usage = dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 10,
		AudioSeconds:     &seconds,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 40, TextTokens: 40, ImageTokens: 60,
			CachedTokensDetails: &dto.CachedTokenDetails{TextTokens: 10, ImageTokens: 30},
		},
	}
	responsesResult, err := ConvertResponse(nil, nil, types.RelayFormatOpenAIResponses, chat)
	require.NoError(t, err)
	require.NotNil(t, responsesResult.Usage.InputTokensDetails)
	require.Equal(t, 10, responsesResult.Usage.InputTokensDetails.CachedTokensDetails.TextTokens)
	require.Equal(t, 12.5, *responsesResult.Usage.AudioSeconds)
	seconds = 999
	chat.Usage.PromptTokensDetails.CachedTokensDetails.TextTokens = 999
	require.Equal(t, 10, responsesResult.Usage.InputTokensDetails.CachedTokensDetails.TextTokens)
	require.Equal(t, 12.5, *responsesResult.Usage.AudioSeconds)

	responses := &dto.OpenAIResponsesResponse{
		ID: "resp_cache", Model: "gpt-test", Status: []byte(`"completed"`),
		Usage: responsesResult.Usage,
	}
	chatResult, err := ConvertResponse(nil, nil, types.RelayFormatOpenAI, responses)
	require.NoError(t, err)
	cached, status := chatResult.Usage.PromptTokensDetails.ValidatedCachedTokenDetails(100)
	require.Equal(t, dto.CacheReadDetailsReported, status)
	require.Equal(t, dto.CachedTokenDetails{TextTokens: 10, ImageTokens: 30}, cached)
	require.Equal(t, 12.5, *chatResult.Usage.AudioSeconds)
	responses.Usage.InputTokensDetails.CachedTokensDetails.ImageTokens = 888
	*responses.Usage.AudioSeconds = 888
	require.Equal(t, 30, chatResult.Usage.PromptTokensDetails.CachedTokensDetails.ImageTokens)
	require.Equal(t, 12.5, *chatResult.Usage.AudioSeconds)
	require.Equal(t, 30, chatResult.Usage.BillingUsage.OpenAIUsage.PromptTokensDetails.CachedTokensDetails.ImageTokens)
}

func TestObservedZeroCacheBreakdownSurvivesChatResponsesConversion(t *testing.T) {
	chat := textRegistryChatResponse()
	chat.Usage = dto.Usage{PromptTokensDetails: dto.InputTokenDetails{CachedTokensDetails: &dto.CachedTokenDetails{}}}
	result, err := ConvertResponse(nil, nil, types.RelayFormatOpenAIResponses, chat)
	require.NoError(t, err)
	require.NotNil(t, result.Usage.InputTokensDetails)
	require.NotNil(t, result.Usage.InputTokensDetails.CachedTokensDetails)
	_, status := result.Usage.InputTokensDetails.ValidatedCachedTokenDetails(0)
	require.Equal(t, dto.CacheReadDetailsReported, status)

	responses := &dto.OpenAIResponsesResponse{
		ID: "resp_zero", Model: "gpt-test", Status: []byte(`"completed"`), Usage: result.Usage,
	}
	converted, err := ConvertResponse(nil, nil, types.RelayFormatOpenAI, responses)
	require.NoError(t, err)
	require.NotNil(t, converted.Usage.PromptTokensDetails.CachedTokensDetails)
	_, status = converted.Usage.PromptTokensDetails.ValidatedCachedTokenDetails(0)
	require.Equal(t, dto.CacheReadDetailsReported, status)
}
