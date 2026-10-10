package openai

import (
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
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
	require.Empty(t, usage.ImageOutputUsageSource)
}

func TestNormalizeOpenAIImageUsagePreservesMeasuredCacheBreakdown(t *testing.T) {
	usage := &dto.Usage{
		InputTokens: 100,
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens: 40, TextTokens: 40, ImageTokens: 60,
			CachedTokensDetails: &dto.CachedTokenDetails{TextTokens: 10, ImageTokens: 30},
		},
	}
	normalizeOpenAIUsage(usage)
	require.Equal(t, 40, usage.PromptTokensDetails.CachedTokens)
	cached, status := usage.PromptTokensDetails.ValidatedCachedTokenDetails(usage.PromptTokens)
	require.Equal(t, dto.CacheReadDetailsReported, status)
	require.Equal(t, dto.CachedTokenDetails{TextTokens: 10, ImageTokens: 30}, cached)
	usage.PromptTokensDetails.CachedTokensDetails.ImageTokens = 999
	require.Equal(t, 30, usage.InputTokensDetails.CachedTokensDetails.ImageTokens)
}

func TestNormalizeOpenAIImageUsageDoesNotInferCacheClassification(t *testing.T) {
	usage := &dto.Usage{
		InputTokens:        100,
		InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 40, TextTokens: 40, ImageTokens: 60},
	}
	normalizeOpenAIUsage(usage)
	require.Nil(t, usage.PromptTokensDetails.CachedTokensDetails)
	_, status := usage.PromptTokensDetails.ValidatedCachedTokenDetails(usage.PromptTokens)
	require.Equal(t, dto.CacheReadDetailsUnknown, status)
	require.Equal(t, 40, usage.PromptTokensDetails.CachedTokens)
}

func TestOpenAIImageHandlersPreserveCacheClassificationAndRawResponse(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	usageJSON := `{"input_tokens":100,"output_tokens":10,"total_tokens":110,"input_tokens_details":{"cached_tokens":40,"text_tokens":40,"image_tokens":60,"cached_tokens_details":{"text_tokens":10,"image_tokens":30,"audio_tokens":0}}}`
	jsonBody := `{"data":[{"b64_json":"image"}],"usage":` + usageJSON + `}`
	eventBody := `{"type":"image_generation.completed","b64_json":"image","usage":` + usageJSON + `}`
	for _, test := range []struct {
		name        string
		body        string
		contentType string
		stream      bool
	}{
		{"JSON", jsonBody, "application/json", false},
		{"JSON_as_SSE", jsonBody, "application/json", true},
		{"SSE", "data: " + eventBody + "\n\ndata: [DONE]\n\n", "text/event-stream", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, recorder, resp, info := newImageTestContext(t, test.body, test.contentType, test.stream)
			var usage *dto.Usage
			if test.stream {
				parsed, err := OpenaiImageStreamHandler(c, info, resp)
				require.Nil(t, err)
				usage = parsed
			} else {
				parsed, err := OpenaiImageHandler(c, info, resp)
				require.Nil(t, err)
				usage = parsed
			}
			require.Equal(t, 40, usage.PromptTokensDetails.CachedTokens)
			require.Equal(t, 10, usage.CompletionTokenDetails.ImageTokens)
			require.Nil(t, usage.OutputTokensDetails, "do not manufacture provider-reported details")
			require.Equal(t, dto.ImageOutputUsageSourceImagesTokens, usage.ImageOutputUsageSource)
			params := service.BuildTieredTokenParams(usage, false, map[string]bool{"p": true, "img": true, "img_o": true})
			cost, _, pricingErr := billingexpr.RunExpr("p*2 + img*2.5 + img_o*8", params)
			require.NoError(t, pricingErr)
			require.Equal(t, float64(310), cost, "missing output details must not make the measured image output free")
			cached, status := usage.PromptTokensDetails.ValidatedCachedTokenDetails(usage.PromptTokens)
			require.Equal(t, dto.CacheReadDetailsReported, status)
			require.Equal(t, dto.CachedTokenDetails{TextTokens: 10, ImageTokens: 30}, cached)
			require.Contains(t, recorder.Body.String(), `"cached_tokens_details":{"text_tokens":10,"image_tokens":30,"audio_tokens":0}`)
			if !test.stream {
				require.Equal(t, jsonBody, recorder.Body.String())
			} else if strings.HasPrefix(test.body, "data:") {
				require.Contains(t, recorder.Body.String(), "data: "+eventBody)
			}
		})
	}
}

func TestNormalizeOpenAIImageUsagePreservesExplicitOutputDetails(t *testing.T) {
	for _, output := range []dto.OutputTokenDetails{
		{TextTokens: 10},
		{TextTokens: 4, ImageTokens: 6},
		{},
	} {
		usage := &dto.Usage{OutputTokens: 10, OutputTokensDetails: &output}
		normalizeOpenAIUsage(usage)
		require.Equal(t, output, usage.CompletionTokenDetails)
		require.Empty(t, usage.ImageOutputUsageSource)
	}
}

func TestOpenAIImageClassifiedCachePricingRejectsUnmeasuredUsageBeforeDelivery(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, test := range []struct {
		name       string
		details    string
		legacy     bool
		wantReject bool
	}{
		{"positive_unclassified", `"cached_tokens":40`, false, true},
		{"negative_classification", `"cached_tokens":40,"cached_tokens_details":{"text_tokens":-1,"image_tokens":41}`, false, true},
		{"partial_classification", `"cached_tokens":40,"cached_tokens_details":{"text_tokens":10,"image_tokens":29}`, false, true},
		{"invalid_classification_type", `"cached_tokens":40,"cached_tokens_details":{"text_tokens":"10","image_tokens":30}`, false, true},
		{"zero_unclassified", `"cached_tokens":0`, false, false},
		{"zero_reported", `"cached_tokens":0,"cached_tokens_details":{"text_tokens":0,"image_tokens":0,"audio_tokens":0}`, false, false},
		{"reported", `"cached_tokens":40,"cached_tokens_details":{"text_tokens":10,"image_tokens":30,"audio_tokens":0}`, false, false},
		{"legacy_positive_unclassified", `"cached_tokens":40`, true, false},
	} {
		for _, path := range []string{"JSON", "JSON_as_SSE", "SSE"} {
			t.Run(test.name+"/"+path, func(t *testing.T) {
				usageJSON := `{"input_tokens":100,"output_tokens":10,"total_tokens":110,"input_tokens_details":{"text_tokens":40,"image_tokens":60,` + test.details + `}}`
				body := `{"data":[{"b64_json":"complete_should_not_deliver"}],"usage":` + usageJSON + `}`
				contentType := "application/json"
				stream := path != "JSON"
				if path == "SSE" {
					contentType = "text/event-stream"
					body = "data: " + `{"type":"image_generation.partial_image","b64_json":"partial"}` + "\n\n" +
						"data: " + `{"type":"image_generation.completed","b64_json":"complete_should_not_deliver","usage":` + usageJSON + `}` + "\n\ndata: [DONE]\n\n"
				}
				c, recorder, resp, info := newImageTestContext(t, body, contentType, stream)
				expression := "p*2 + img*2.5 + img_o*8 + cr_text*0.2 + cr_img*0.25"
				if test.legacy {
					expression = "p*2 + img*2.5 + img_o*8 + cr*0.2"
				}
				info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expression}
				var usage *dto.Usage
				var relayErr *types.NewAPIError
				if stream {
					usage, relayErr = OpenaiImageStreamHandler(c, info, resp)
				} else {
					usage, relayErr = OpenaiImageHandler(c, info, resp)
				}
				if test.wantReject {
					require.NotNil(t, relayErr)
					require.Equal(t, 502, relayErr.StatusCode)
					require.True(t, types.IsSkipRetryError(relayErr))
					require.Nil(t, usage, "failed measurement must never reach normal settlement")
					require.NotContains(t, recorder.Body.String(), "complete_should_not_deliver")
					if path == "SSE" {
						require.Contains(t, recorder.Body.String(), `"b64_json":"partial"`)
						require.Contains(t, recorder.Body.String(), "event: error")
						require.NotContains(t, recorder.Body.String(), "[DONE]")
					} else {
						require.Empty(t, recorder.Body.String())
					}
				} else {
					require.Nil(t, relayErr)
					require.NotNil(t, usage)
					require.Contains(t, recorder.Body.String(), "complete_should_not_deliver")
				}
			})
		}
	}
}
