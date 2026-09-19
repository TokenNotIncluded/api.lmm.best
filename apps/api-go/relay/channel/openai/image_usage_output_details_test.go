package openai

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAIUsageMapsOutputTokenDetails(t *testing.T) {
	usage := &dto.Usage{
		InputTokens:  15,
		OutputTokens: 1352,
		TotalTokens:  1367,
		OutputTokensDetails: &dto.OutputTokenDetails{
			ImageTokens: 1120,
			TextTokens:  232,
		},
	}

	normalizeOpenAIUsage(usage)

	require.Equal(t, 15, usage.PromptTokens)
	require.Equal(t, 1352, usage.CompletionTokens)
	require.Equal(t, 1367, usage.TotalTokens)
	require.Equal(t, 1120, usage.CompletionTokenDetails.ImageTokens)
	require.Equal(t, 232, usage.CompletionTokenDetails.TextTokens)
}
