package billing_setting_test

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nativeDimensionPriceCase struct {
	model      string
	expression string
	dimensions []string
	zeroCost   float64
}

func nativeDimensionPriceCases() []nativeDimensionPriceCase {
	return []nativeDimensionPriceCase{
		{
			model:      "chatgpt-image-latest",
			expression: `tier("base", p*5 + c*10 + img*8 + img_o*32 + cr_text*1.25 + cr_img*2)`,
			dimensions: []string{"cr_text", "cr_img"},
			zeroCost:   1720,
		},
		{
			model:      "gpt-image-1-mini",
			expression: `tier("base", p*2 + img*2.5 + img_o*8 + cr_text*0.2 + cr_img*0.25)`,
			dimensions: []string{"cr_text", "cr_img"},
			zeroCost:   490,
		},
		{
			model:      "gpt-live-1",
			expression: `tier("base", audio_s*0.05*1000000/60)`,
			dimensions: []string{"audio_s"},
		},
		{
			model:      "gpt-live-transcribe",
			expression: `tier("base", audio_s*0.017*1000000/60)`,
			dimensions: []string{"audio_s"},
		},
		{
			model:      "gpt-realtime-whisper",
			expression: `tier("base", audio_s*0.017*1000000/60)`,
			dimensions: []string{"audio_s"},
		},
		{
			model:      "gpt-realtime-translate",
			expression: `tier("base", audio_s*0.034*1000000/60)`,
			dimensions: []string{"audio_s"},
		},
	}
}

func setNativeMeasuredDimension(params *billingexpr.TokenParams, name string, value *float64) {
	switch name {
	case "cr_text":
		params.CRText = value
	case "cr_img":
		params.CRImg = value
	case "audio_s":
		params.AudioSeconds = value
	}
}

func TestSmokeTestExpr_AcceptsNativeImageAndVoicePrices(t *testing.T) {
	for _, price := range nativeDimensionPriceCases() {
		t.Run(price.model, func(t *testing.T) {
			require.NoError(t, billing_setting.SmokeTestExpr(price.expression))
		})
	}
}

func TestSmokeTestExpr_SyntheticMeasurementsDoNotFillActualUsage(t *testing.T) {
	for _, price := range nativeDimensionPriceCases() {
		t.Run(price.model, func(t *testing.T) {
			require.NoError(t, billing_setting.SmokeTestExpr(price.expression))
			params := billingexpr.TokenParams{P: 100, C: 10, Img: 20, ImgO: 30}
			zero := float64(0)
			for _, dimension := range price.dimensions {
				setNativeMeasuredDimension(&params, dimension, &zero)
			}

			// An observed zero is billable according to the other dimensions.
			cost, trace, err := billingexpr.RunExpr(price.expression, params)
			require.NoError(t, err)
			assert.InDelta(t, price.zeroCost, cost, 1e-9)
			assert.Equal(t, "base", trace.MatchedTier)

			for _, dimension := range price.dimensions {
				t.Run("missing_"+dimension, func(t *testing.T) {
					missing := params
					setNativeMeasuredDimension(&missing, dimension, nil)
					// Cached compilation after a successful smoke test must still reject
					// absent provider measurements instead of producing a free charge.
					_, _, err := billingexpr.RunExpr(price.expression, missing)
					require.Error(t, err)
					assert.Contains(t, err.Error(), dimension)
				})
			}
		})
	}
}

func TestBillingSetting_NativePricesPersistenceRoundTrip(t *testing.T) {
	settings := &billing_setting.BillingSetting{
		BillingMode: make(map[string]string),
		BillingExpr: make(map[string]string),
	}
	for _, price := range nativeDimensionPriceCases() {
		require.NoError(t, billing_setting.SmokeTestExpr(price.expression))
		settings.BillingMode[price.model] = billing_setting.BillingModeTieredExpr
		settings.BillingExpr[price.model] = price.expression
	}

	manager := config.NewConfigManager()
	manager.Register("billing_setting", settings)
	saved := make(map[string]string)
	require.NoError(t, manager.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	require.Len(t, saved, 2)
	var savedModes, savedExpressions map[string]string
	require.NoError(t, json.Unmarshal([]byte(saved["billing_setting.billing_mode"]), &savedModes))
	require.NoError(t, json.Unmarshal([]byte(saved["billing_setting.billing_expr"]), &savedExpressions))
	assert.Equal(t, settings.BillingMode, savedModes)
	assert.Equal(t, settings.BillingExpr, savedExpressions)

	// A fresh manager models process restart without changing GlobalConfig or
	// opening a database. SaveToDB's callback captures the durable option values.
	reloaded := &billing_setting.BillingSetting{}
	reloadManager := config.NewConfigManager()
	reloadManager.Register("billing_setting", reloaded)
	require.NoError(t, reloadManager.LoadFromDB(saved))
	assert.Equal(t, settings.BillingMode, reloaded.BillingMode)
	assert.Equal(t, settings.BillingExpr, reloaded.BillingExpr)
	for _, price := range nativeDimensionPriceCases() {
		assert.Equal(t, billing_setting.BillingModeTieredExpr, reloaded.BillingMode[price.model])
		assert.Equal(t, price.expression, reloaded.BillingExpr[price.model])
		require.NoError(t, billing_setting.SmokeTestExpr(reloaded.BillingExpr[price.model]))
	}
}
