package dto

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCachedTokenDetailsPreservesUnknownAndObservedZero(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		status string
	}{
		{"omitted", `{}`, CacheReadDetailsUnknown},
		{"null", `{"cached_tokens_details":null}`, CacheReadDetailsUnknown},
		{"zero", `{"cached_tokens_details":{"text_tokens":0,"image_tokens":0,"audio_tokens":0}}`, CacheReadDetailsReported},
	} {
		t.Run(test.name, func(t *testing.T) {
			var details InputTokenDetails
			require.NoError(t, json.Unmarshal([]byte(test.body), &details))
			cached, status := details.ValidatedCachedTokenDetails(0)
			require.Equal(t, test.status, status)
			require.Equal(t, CachedTokenDetails{}, cached)
			raw, err := json.Marshal(details)
			require.NoError(t, err)
			if test.status == CacheReadDetailsUnknown {
				require.NotContains(t, string(raw), "cached_tokens_details")
			} else {
				require.Contains(t, string(raw), `"cached_tokens_details":{"text_tokens":0,"image_tokens":0,"audio_tokens":0}`)
			}
		})
	}
}

func TestValidatedCachedTokenDetailsRejectsInconsistentCountersWithoutChangingRawUsage(t *testing.T) {
	valid := InputTokenDetails{
		CachedTokens: 60,
		TextTokens:   40,
		ImageTokens:  35,
		AudioTokens:  25,
		CachedTokensDetails: &CachedTokenDetails{
			TextTokens: 20, ImageTokens: 25, AudioTokens: 15,
		},
	}
	for _, test := range []struct {
		name   string
		change func(*InputTokenDetails)
		prompt int
		status string
	}{
		{"valid", func(*InputTokenDetails) {}, 100, CacheReadDetailsReported},
		{"text_parent_omitted", func(d *InputTokenDetails) { d.TextTokens = 0 }, 100, CacheReadDetailsReported},
		{"negative_text_cache", func(d *InputTokenDetails) { d.CachedTokensDetails.TextTokens = -1 }, 100, CacheReadDetailsInvalid},
		{"incomplete_split", func(d *InputTokenDetails) { d.CachedTokensDetails.TextTokens = 19 }, 100, CacheReadDetailsInvalid},
		{"split_exceeds_total", func(d *InputTokenDetails) { d.CachedTokensDetails.TextTokens = 21 }, 100, CacheReadDetailsInvalid},
		{"split_overflow", func(d *InputTokenDetails) { d.CachedTokensDetails.TextTokens = math.MaxInt }, 100, CacheReadDetailsInvalid},
		{"negative_total", func(d *InputTokenDetails) { d.CachedTokens = -1 }, 100, CacheReadDetailsInvalid},
		{"negative_parent", func(d *InputTokenDetails) { d.ImageTokens = -1 }, 100, CacheReadDetailsInvalid},
		{"image_cache_exceeds_parent", func(d *InputTokenDetails) { d.ImageTokens = 24 }, 100, CacheReadDetailsInvalid},
		{"audio_cache_exceeds_parent", func(d *InputTokenDetails) { d.AudioTokens = 14 }, 100, CacheReadDetailsInvalid},
		{"text_cache_exceeds_parent", func(d *InputTokenDetails) { d.TextTokens = 19 }, 100, CacheReadDetailsInvalid},
		{"media_leaves_no_text_budget", func(d *InputTokenDetails) { d.TextTokens = 0; d.ImageTokens = 75 }, 100, CacheReadDetailsInvalid},
		{"parent_sum_exceeds_input", func(d *InputTokenDetails) { d.TextTokens = 50 }, 100, CacheReadDetailsInvalid},
		{"cached_exceeds_input", func(*InputTokenDetails) {}, 59, CacheReadDetailsInvalid},
		{"negative_input", func(*InputTokenDetails) {}, -1, CacheReadDetailsInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			details := CloneInputTokenDetails(valid)
			test.change(&details)
			before := CloneInputTokenDetails(details)
			cached, status := details.ValidatedCachedTokenDetails(test.prompt)
			require.Equal(t, test.status, status)
			require.Equal(t, before, details, "raw usage must not be rewritten")
			if status == CacheReadDetailsReported {
				require.Equal(t, *details.CachedTokensDetails, cached)
			} else {
				require.Equal(t, CachedTokenDetails{}, cached)
			}
		})
	}
}

func TestCloneBillingUsageDetachesCacheBreakdownsAndDuration(t *testing.T) {
	seconds := 12.5
	usage := &Usage{
		PromptTokens: 100,
		AudioSeconds: &seconds,
		PromptTokensDetails: InputTokenDetails{
			CachedTokens:        60,
			CachedTokensDetails: &CachedTokenDetails{TextTokens: 20, ImageTokens: 25, AudioTokens: 15},
		},
		InputTokensDetails: &InputTokenDetails{
			CachedTokens:        60,
			CachedTokensDetails: &CachedTokenDetails{TextTokens: 20, ImageTokens: 25, AudioTokens: 15},
		},
		OutputTokensDetails: &OutputTokenDetails{ImageTokens: 10},
	}
	wrapped := NewOpenAIResponsesBillingUsage(usage)
	clone := CloneBillingUsage(wrapped)
	require.NotNil(t, clone)
	usage.PromptTokensDetails.CachedTokensDetails.TextTokens = 999
	usage.InputTokensDetails.CachedTokensDetails.ImageTokens = 999
	usage.OutputTokensDetails.ImageTokens = 999
	seconds = 999
	require.Equal(t, 20, clone.OpenAIUsage.PromptTokensDetails.CachedTokensDetails.TextTokens)
	require.Equal(t, 25, clone.OpenAIUsage.InputTokensDetails.CachedTokensDetails.ImageTokens)
	require.Equal(t, 10, clone.OpenAIUsage.OutputTokensDetails.ImageTokens)
	require.Equal(t, 12.5, *clone.OpenAIUsage.AudioSeconds)
	wrapped.OpenAIUsage.InputTokensDetails.CachedTokensDetails.ImageTokens = 888
	*wrapped.OpenAIUsage.AudioSeconds = 888
	require.Equal(t, 25, clone.OpenAIUsage.InputTokensDetails.CachedTokensDetails.ImageTokens)
	require.Equal(t, 12.5, *clone.OpenAIUsage.AudioSeconds)
}

func TestAudioSecondsPreservesUnknownAndObservedZero(t *testing.T) {
	var unknown Usage
	require.NoError(t, json.Unmarshal([]byte(`{}`), &unknown))
	require.Nil(t, unknown.AudioSeconds)
	var zero Usage
	require.NoError(t, json.Unmarshal([]byte(`{"audio_seconds":0}`), &zero))
	require.NotNil(t, zero.AudioSeconds)
	require.Zero(t, *zero.AudioSeconds)
	wrapper := NewOpenAIResponsesBillingUsage(&zero)
	require.NotNil(t, wrapper, "a reported duration of zero must survive normalization")
	require.NotNil(t, wrapper.OpenAIUsage.AudioSeconds)
}

func TestImageOutputProtocolSourceCannotBeSuppliedByUpstreamJSON(t *testing.T) {
	var usage Usage
	require.NoError(t, json.Unmarshal([]byte(`{"image_output_usage_source":"openai_images_output_tokens","output_tokens":10}`), &usage))
	require.Empty(t, usage.ImageOutputUsageSource)
	usage.ImageOutputUsageSource = ImageOutputUsageSourceImagesTokens
	encoded, err := json.Marshal(usage)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "image_output_usage_source")
}
