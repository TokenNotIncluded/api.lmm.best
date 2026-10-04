package service

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestEffectiveOpenAIBillingUsagePreservesMeasuredCacheBreakdown(t *testing.T) {
	seconds := 12.5
	raw := &dto.Usage{
		InputTokens:  100,
		OutputTokens: 10,
		AudioSeconds: &seconds,
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens:        60,
			TextTokens:          40,
			ImageTokens:         35,
			AudioTokens:         25,
			CachedTokensDetails: &dto.CachedTokenDetails{TextTokens: 20, ImageTokens: 25, AudioTokens: 15},
		},
	}
	wrapped := &dto.Usage{BillingUsage: dto.NewOpenAIResponsesBillingUsage(raw)}
	effective := effectiveBillingUsage(wrapped)
	require.Equal(t, 100, effective.PromptTokens)
	require.Equal(t, 60, effective.PromptTokensDetails.CachedTokens)
	cached, status := effective.PromptTokensDetails.ValidatedCachedTokenDetails(effective.PromptTokens)
	require.Equal(t, dto.CacheReadDetailsReported, status)
	require.Equal(t, dto.CachedTokenDetails{TextTokens: 20, ImageTokens: 25, AudioTokens: 15}, cached)
	require.Equal(t, 12.5, *effective.AudioSeconds)
	*effective.AudioSeconds = 999
	effective.PromptTokensDetails.CachedTokensDetails.TextTokens = 999
	effective.InputTokensDetails.CachedTokensDetails.ImageTokens = 999
	require.Equal(t, 20, wrapped.BillingUsage.OpenAIUsage.InputTokensDetails.CachedTokensDetails.TextTokens)
	require.Equal(t, 25, wrapped.BillingUsage.OpenAIUsage.InputTokensDetails.CachedTokensDetails.ImageTokens)
	require.Equal(t, 12.5, *wrapped.BillingUsage.OpenAIUsage.AudioSeconds)
	second := effectiveBillingUsage(wrapped)
	require.Equal(t, 20, second.PromptTokensDetails.CachedTokensDetails.TextTokens)
}

func TestEffectiveOpenAIBillingUsageKeepsUnknownAndInvalidCacheClassification(t *testing.T) {
	for _, test := range []struct {
		name   string
		cached *dto.CachedTokenDetails
		status string
	}{
		{"unknown", nil, dto.CacheReadDetailsUnknown},
		{"invalid", &dto.CachedTokenDetails{TextTokens: -5, ImageTokens: 45}, dto.CacheReadDetailsInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			wrapped := &dto.Usage{BillingUsage: dto.NewOpenAIResponsesBillingUsage(&dto.Usage{
				InputTokens: 100,
				InputTokensDetails: &dto.InputTokenDetails{
					CachedTokens: 40, ImageTokens: 60, CachedTokensDetails: test.cached,
				},
			})}
			effective := effectiveBillingUsage(wrapped)
			require.Equal(t, 40, effective.PromptTokensDetails.CachedTokens, "raw aggregate must stay intact")
			_, status := effective.PromptTokensDetails.ValidatedCachedTokenDetails(effective.PromptTokens)
			require.Equal(t, test.status, status)
			require.Equal(t, test.cached, effective.PromptTokensDetails.CachedTokensDetails)
		})
	}
}

func TestApplyResponsesUsageDetachesMeasuredCacheBreakdownAndDuration(t *testing.T) {
	seconds := 12.5
	src := &dto.Usage{
		InputTokens:  100,
		AudioSeconds: &seconds,
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens: 40, ImageTokens: 60,
			CachedTokensDetails: &dto.CachedTokenDetails{TextTokens: 10, ImageTokens: 30},
		},
	}
	dst := &dto.Usage{}
	ApplyResponsesUsage(dst, src)
	require.NotNil(t, dst.InputTokensDetails)
	require.NotNil(t, dst.PromptTokensDetails.CachedTokensDetails)
	seconds = 999
	src.InputTokensDetails.CachedTokensDetails.TextTokens = 999
	require.Equal(t, 10, dst.InputTokensDetails.CachedTokensDetails.TextTokens)
	require.Equal(t, 10, dst.PromptTokensDetails.CachedTokensDetails.TextTokens)
	require.Equal(t, 12.5, *dst.AudioSeconds)
	dst.PromptTokensDetails.CachedTokensDetails.TextTokens = 888
	require.Equal(t, 10, dst.InputTokensDetails.CachedTokensDetails.TextTokens)
}
