package helper

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureNativeDimensionPrice(t *testing.T, modelName, expression string) {
	t.Helper()
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error { saved[key] = value; return nil }))
	previousQuotaUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() {
		common.QuotaPerUnit = previousQuotaUnit
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	modes, err := json.Marshal(map[string]string{modelName: "tiered_expr"})
	require.NoError(t, err)
	expressions, err := json.Marshal(map[string]string{modelName: expression})
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": string(modes), "billing_setting.billing_expr": string(expressions),
		"group_ratio_setting.group_ratio": `{"default":1}`,
	}))
}

func nativeDimensionPriceContext(path, modelName string) (*gin.Context, *relaycommon.RelayInfo) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("group", "default")
	return c, &relaycommon.RelayInfo{
		OriginModelName: modelName, UserGroup: "default", UsingGroup: "default",
		RequestHeaders:      map[string]string{"Content-Type": "application/json"},
		BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{}`), Headers: map[string]string{"Content-Type": "application/json"}},
	}
}

func TestNativeDimensionPriceVoiceMinuteReservationsUseSeconds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		model, expression string
		quota             int
	}{
		{"gpt-live-1", `tier("base", audio_s * 0.05 * 1000000 / 60)`, 25000},
		{"gpt-live-transcribe", `tier("base", audio_s * 0.017 * 1000000 / 60)`, 8500},
		{"gpt-realtime-whisper", `tier("base", audio_s * 0.017 * 1000000 / 60)`, 8500},
		{"gpt-realtime-translate", `tier("base", audio_s * 0.034 * 1000000 / 60)`, 17000},
	} {
		t.Run(tc.model, func(t *testing.T) {
			configureNativeDimensionPrice(t, tc.model, tc.expression)
			c, info := nativeDimensionPriceContext("/v1/live/sessions", tc.model)
			info.NativeVoiceReserveSeconds = 60
			price, err := ModelPriceHelper(c, info, 0, &types.TokenCountMeta{})
			require.NoError(t, err)
			assert.Equal(t, tc.quota, price.QuotaToPreConsume)
			require.NotNil(t, info.TieredBillingSnapshot)
			assert.Zero(t, info.TieredBillingSnapshot.EstimatedPromptTokens)
			assert.Zero(t, info.TieredBillingSnapshot.EstimatedCompletionTokens, "native duration reserve never invents 8192 completion tokens")
			assert.Equal(t, tc.quota, info.TieredBillingSnapshot.EstimatedQuotaAfterGroup)
			assert.Equal(t, tc.model, info.TieredBillingSnapshot.ModelName)
			assert.Equal(t, tc.expression, info.TieredBillingSnapshot.ExprString)
			assert.Equal(t, billingexpr.ExprHashString(tc.expression), info.TieredBillingSnapshot.ExprHash)
			assert.False(t, price.FreeModel)

			// A reservation is an estimate. Actual settlement still requires a
			// fresh measured duration; the 60-second budget cannot supply it.
			_, err = billingexpr.ComputeTieredQuota(info.TieredBillingSnapshot, billingexpr.TokenParams{})
			require.ErrorContains(t, err, "audio_s")
			seconds := 60.0
			settled, err := billingexpr.ComputeTieredQuota(info.TieredBillingSnapshot, billingexpr.TokenParams{AudioSeconds: &seconds})
			require.NoError(t, err)
			assert.Equal(t, tc.quota, settled.ActualQuotaAfterGroup)
		})
	}
}

func TestNativeDimensionPriceVoiceFreezesOriginalRequestAndPrice(t *testing.T) {
	const modelName = "customer-live-alias"
	const expression = `param("model") == "customer-live-alias" && header("x-billing-plan") == "original" ? tier("original", audio_s * 0.05 * 1000000 / 60) : tier("mutated", audio_s * 1000000)`
	configureNativeDimensionPrice(t, modelName, expression)
	c, info := nativeDimensionPriceContext("/v1/live/sessions", modelName)
	originalBody := []byte(`{"model":"customer-live-alias"}`)
	originalHeaders := map[string]string{"X-Billing-Plan": "original"}
	info.BillingRequestInput = &billingexpr.RequestInput{Body: originalBody, Headers: originalHeaders}
	c.Request.Body = http.NoBody
	info.NativeVoiceReserveSeconds = 60
	price, err := ModelPriceHelper(c, info, 0, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.Equal(t, 25000, price.QuotaToPreConsume)
	assert.Equal(t, "original", info.TieredBillingSnapshot.EstimatedTier)

	originalBody[0] = '['
	originalHeaders["X-Billing-Plan"] = "mutated"
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/live/sessions", strings.NewReader(`{"model":"gpt-live-1"}`))
	c.Request.Header.Set("X-Billing-Plan", "mutated")
	replacement, err := json.Marshal(map[string]string{modelName: `tier("replacement", audio_s * 1000000)`})
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"billing_setting.billing_expr": string(replacement)}))
	assert.JSONEq(t, `{"model":"customer-live-alias"}`, string(info.BillingRequestInput.Body))
	assert.Equal(t, "original", info.BillingRequestInput.Headers["X-Billing-Plan"])
	seconds := 6.0
	settled, err := billingexpr.ComputeTieredQuotaWithRequest(info.TieredBillingSnapshot, billingexpr.TokenParams{AudioSeconds: &seconds}, *info.BillingRequestInput)
	require.NoError(t, err)
	assert.Equal(t, 2500, settled.ActualQuotaAfterGroup, "actual 6 seconds use the frozen public request and original price")
	assert.Equal(t, "original", settled.MatchedTier)
	assert.Equal(t, 25000, info.TieredBillingSnapshot.EstimatedQuotaAfterGroup, "budget and actual usage remain distinct")
}

func TestNativeDimensionPriceImagesSettleOfficialCacheRatesWithoutOverlap(t *testing.T) {
	for _, modelCase := range []struct {
		model, expression                                      string
		reserve, uncached, textCached, imageCached, bothCached int
	}{
		{"chatgpt-image-latest", `tier("base", p*5 + c*10 + img*8 + img_o*32 + cr_text*1.25 + cr_img*2)`, 2600, 4250, 4063, 3650, 3463},
		{"gpt-image-1-mini", `tier("base", p*2 + img*2.5 + img_o*8 + cr_text*0.2 + cr_img*0.25)`, 1000, 1350, 1260, 1125, 1035},
	} {
		t.Run(modelCase.model, func(t *testing.T) {
			configureNativeDimensionPrice(t, modelCase.model, modelCase.expression)
			c, info := nativeDimensionPriceContext("/v1/images/generations", modelCase.model)
			var format types.RelayFormat = types.RelayFormatOpenAIImage
			info.RelayFormat = format
			price, err := ModelPriceHelper(c, info, 1000, &types.TokenCountMeta{MaxTokens: 20})
			require.NoError(t, err, "synthetic zero cache permits reservation before upstream measurement")
			assert.Equal(t, modelCase.reserve, price.QuotaToPreConsume)
			for _, tc := range []struct {
				name               string
				text, image, quota int
			}{
				{"uncached", 0, 0, modelCase.uncached},
				{"text cache", 100, 0, modelCase.textCached},
				{"image cache", 0, 200, modelCase.imageCached},
				{"both cache modalities", 100, 200, modelCase.bothCached},
			} {
				t.Run(tc.name, func(t *testing.T) {
					usage := &dto.Usage{
						PromptTokens: 1000, CompletionTokens: 60, TotalTokens: 1060,
						PromptTokensDetails: dto.InputTokenDetails{
							TextTokens: 400, ImageTokens: 600, CachedTokens: tc.text + tc.image,
							CachedTokensDetails: &dto.CachedTokenDetails{TextTokens: tc.text, ImageTokens: tc.image},
						},
						CompletionTokenDetails: dto.OutputTokenDetails{TextTokens: 10, ImageTokens: 50},
					}
					params := service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(modelCase.expression))
					assert.EqualValues(t, 400-tc.text, params.P)
					assert.EqualValues(t, 600-tc.image, params.Img)
					assert.EqualValues(t, 10, params.C)
					assert.EqualValues(t, 50, params.ImgO)
					settled, err := billingexpr.ComputeTieredQuota(info.TieredBillingSnapshot, params)
					require.NoError(t, err)
					assert.Equal(t, tc.quota, settled.ActualQuotaAfterGroup)
				})
			}

			unknown := &dto.Usage{PromptTokens: 1000, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 300}}
			params := service.BuildTieredTokenParams(unknown, false, billingexpr.UsedVars(modelCase.expression))
			_, err = billingexpr.ComputeTieredQuota(info.TieredBillingSnapshot, params)
			require.Error(t, err, "a saved price and synthetic reserve do not turn missing actual cache classification into zero")
		})
	}
}
