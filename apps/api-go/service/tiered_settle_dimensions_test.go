package service

import (
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func classifiedTieredUsage() *dto.Usage {
	return &dto.Usage{
		PromptTokens: 1000, CompletionTokens: 30, TotalTokens: 1030,
		PromptTokensDetails: dto.InputTokenDetails{
			TextTokens: 500, ImageTokens: 300, AudioTokens: 200, CachedTokens: 400,
			CachedTokensDetails: &dto.CachedTokenDetails{TextTokens: 100, ImageTokens: 200, AudioTokens: 100},
		},
		CompletionTokenDetails: dto.OutputTokenDetails{ImageTokens: 10, AudioTokens: 5},
	}
}

func TestBuildTieredTokenParams_ClassifiedCacheExcludesMediaOverlapOnce(t *testing.T) {
	for _, tc := range []struct {
		name, expression            string
		prompt, image, audio, quota float64
	}{
		{"all modalities", `p * 2 + cr_text * 0.2 + cr_img * 0.7 + cr_audio * 1.3 + img * 5 + ai * 11`, 400, 100, 100, 1345},
		{"image cache with image", `p * 2 + cr_img * 0.7 + img * 5`, 700, 100, 200, 1020},
		{"text cache with inclusive image", `p * 2 + cr_text * 0.2 + img * 5`, 600, 300, 200, 1360},
		{"classified cache without media rates", `p * 2 + cr_text * 0.2 + cr_img * 0.7 + cr_audio * 1.3`, 600, 100, 100, 745},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := classifiedTieredUsage()
			params := BuildTieredTokenParams(usage, false, billingexpr.UsedVars(tc.expression))
			assert.Equal(t, tc.prompt, params.P)
			assert.Equal(t, tc.image, params.Img)
			assert.Equal(t, tc.audio, params.AI)
			assert.Equal(t, float64(1000), params.Len, "tier context length remains inclusive")
			assert.Equal(t, float64(400), params.CR, "legacy aggregate cache is retained")
			assert.Equal(t, dto.CacheReadDetailsReported, params.CacheClassificationStatus)
			assert.Empty(t, params.MeasurementError)
			info := makeRelayInfo(tc.expression, 1, 1000, 0)
			ok, quota, result, err := TryTieredSettleWithError(info, params)
			require.NoError(t, err)
			require.True(t, ok)
			require.NotNil(t, result)
			assert.EqualValues(t, tc.quota, quota)
			assert.Equal(t, 300, usage.PromptTokensDetails.ImageTokens, "normalization never mutates upstream usage")
			assert.Equal(t, 200, usage.PromptTokensDetails.CachedTokensDetails.ImageTokens)
		})
	}
}

func TestBuildTieredTokenParams_LegacyCacheAndMediaKeepExistingMeaning(t *testing.T) {
	const expression = `p + c + cr + img + img_o + ai + ao`
	for _, classified := range []bool{false, true} {
		usage := classifiedTieredUsage()
		if !classified {
			usage.PromptTokensDetails.CachedTokensDetails = nil
		}
		params := BuildTieredTokenParams(usage, false, billingexpr.UsedVars(expression))
		assert.Equal(t, float64(100), params.P)
		assert.Equal(t, float64(15), params.C)
		assert.Equal(t, float64(400), params.CR)
		assert.Equal(t, float64(300), params.Img)
		assert.Equal(t, float64(200), params.AI)
		assert.Equal(t, float64(10), params.ImgO)
		assert.Equal(t, float64(5), params.AO)
		assert.Empty(t, params.MeasurementError)
		cost, _, err := billingexpr.RunExpr(expression, params)
		require.NoError(t, err)
		assert.Equal(t, float64(1030), cost)
	}
}

func TestBuildTieredTokenParams_UnknownCacheClassificationIsNotMeasuredZero(t *testing.T) {
	const expression = `p * 2 + cr_text * 0.2 + cr_img * 0.7 + cr_audio * 1.3`
	for _, tc := range []struct {
		name    string
		cached  int
		details *dto.CachedTokenDetails
		status  string
		valid   bool
	}{
		{"missing nonzero", 400, nil, dto.CacheReadDetailsUnknown, false},
		{"invalid total", 400, &dto.CachedTokenDetails{TextTokens: 100}, dto.CacheReadDetailsInvalid, false},
		{"negative", 400, &dto.CachedTokenDetails{TextTokens: -1, ImageTokens: 200, AudioTokens: 201}, dto.CacheReadDetailsInvalid, false},
		{"invalid parent modality", 400, &dto.CachedTokenDetails{ImageTokens: 400}, dto.CacheReadDetailsInvalid, false},
		{"sum overflow", 400, &dto.CachedTokenDetails{TextTokens: math.MaxInt, ImageTokens: math.MaxInt}, dto.CacheReadDetailsInvalid, false},
		{"unknown aggregate zero", 0, nil, dto.CacheReadDetailsUnknown, true},
		{"reported zero", 0, &dto.CachedTokenDetails{}, dto.CacheReadDetailsReported, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := classifiedTieredUsage()
			usage.PromptTokensDetails.CachedTokens = tc.cached
			usage.PromptTokensDetails.CachedTokensDetails = tc.details
			params := BuildTieredTokenParams(usage, false, billingexpr.UsedVars(expression))
			assert.Equal(t, tc.status, params.CacheClassificationStatus)
			info := makeRelayInfo(expression, 1, 1000, 0)
			info.FinalPreConsumedQuota = 777
			ok, quota, result, err := TryTieredSettleWithError(info, params)
			require.True(t, ok)
			if !tc.valid {
				require.Error(t, err, "unavailable classifications require an explicit caller policy")
				assert.Zero(t, quota, "error-returning settlement does not silently keep a guessed reservation")
				assert.Nil(t, result)
				assert.Nil(t, params.CRText)
				legacyOK, legacyQuota, legacyResult := TryTieredSettle(info, params)
				assert.True(t, legacyOK)
				assert.Equal(t, 777, legacyQuota, "legacy caller compatibility remains explicit")
				assert.Nil(t, legacyResult)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, 1000, quota)
			require.NotNil(t, params.CRText)
			assert.Zero(t, *params.CRText)
		})
	}
}

func TestBuildTieredTokenParams_DurationOnlyUsageHasRealCost(t *testing.T) {
	const expression = `tier("duration", audio_s * 0.06 * 1000000 / 60)`
	seconds := 90.5
	usage := &dto.Usage{AudioSeconds: &seconds}
	params := BuildTieredTokenParams(usage, false, billingexpr.UsedVars(expression))
	seconds = 1 // The built parameters retain their captured measurement.
	require.NotNil(t, params.AudioSeconds)
	assert.Equal(t, 90.5, *params.AudioSeconds)
	assert.Zero(t, params.P)
	assert.Zero(t, params.C)
	info := makeRelayInfo(expression, 1.5, 0, 0)
	info.FinalPreConsumedQuota = 123
	ok, quota, result, err := TryTieredSettleWithError(info, params)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, result)
	assert.Equal(t, 67875, quota, "zero token totals cannot erase measured seconds")
	assert.InDelta(t, 45250, result.ActualQuotaBeforeGroup, 1e-9)
	assert.Equal(t, "duration", result.MatchedTier)

	for _, measured := range []*float64{nil, durationTieredPointer(0), durationTieredPointer(-1), durationTieredPointer(math.NaN()), durationTieredPointer(math.Inf(1))} {
		params := BuildTieredTokenParams(&dto.Usage{AudioSeconds: measured}, false, billingexpr.UsedVars(expression))
		_, quota, result, err := TryTieredSettleWithError(info, params)
		if measured != nil && *measured == 0 {
			require.NoError(t, err)
			assert.Zero(t, quota)
			assert.NotNil(t, result)
		} else {
			require.Error(t, err)
			assert.Zero(t, quota)
			assert.Nil(t, result)
		}
	}
}

func durationTieredPointer(value float64) *float64 { return &value }
