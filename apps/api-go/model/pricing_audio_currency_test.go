package model

import (
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func audioProjectionRatio(value float64) *float64 { return &value }

func TestUSDAudioProjectionDefaultsMissingRatiosWithoutChangingOtherQuotes(t *testing.T) {
	pricingCurrencyFixture(t, 500000, 2500000)
	oldRead, oldWrite := ratio_setting.CacheRatio2JSONString(), ratio_setting.CreateCacheRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(oldRead))
		require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(oldWrite))
	})
	require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(`{"audio-projection":0.2}`))
	require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(`{"audio-projection":1.25}`))
	for _, tc := range []struct {
		name                  string
		audio, completion     *float64
		wantInput, wantOutput *float64
	}{
		{"neither configured", nil, nil, nil, nil},
		{"audio only", audioProjectionRatio(4), nil, audioProjectionRatio(2), audioProjectionRatio(2)},
		{"completion only", nil, audioProjectionRatio(3), nil, audioProjectionRatio(1.5)},
		{"both configured", audioProjectionRatio(4), audioProjectionRatio(3), audioProjectionRatio(2), audioProjectionRatio(6)},
		{"free audio", audioProjectionRatio(0), audioProjectionRatio(3), audioProjectionRatio(0), audioProjectionRatio(0)},
		{"free output", audioProjectionRatio(4), audioProjectionRatio(0), audioProjectionRatio(2), audioProjectionRatio(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacy := []Pricing{{ModelName: "audio-projection", ModelRatio: 1.25, CompletionRatio: 2,
				ImageRatio: audioProjectionRatio(6), AudioRatio: tc.audio, AudioCompletionRatio: tc.completion}}
			before := clonePricing(legacy)
			quote, err := NormalizePricingUSD(legacy)
			require.NoError(t, err)
			require.Equal(t, tc.wantInput, quote[0].AudioInputPrice)
			require.Equal(t, tc.wantOutput, quote[0].AudioOutputPrice)
			require.Equal(t, 0.5, *quote[0].InputPrice)
			require.Equal(t, 1.0, *quote[0].OutputPrice)
			require.Equal(t, 3.0, *quote[0].ImagePrice)
			require.Equal(t, 0.1, *quote[0].CacheReadPrice)
			require.Equal(t, 0.625, *quote[0].CacheWritePrice)
			require.Equal(t, before, legacy, "public quotes must not rewrite legacy billing snapshots")
		})
	}
}

func TestUSDAudioOutputRejectsInvalidAndUnrepresentableRates(t *testing.T) {
	pricingCurrencyFixture(t, 500000, 2500000)
	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1), math.SmallestNonzeroFloat64} {
		for _, ratios := range [][2]*float64{
			{audioProjectionRatio(invalid), nil},
			{nil, audioProjectionRatio(invalid)},
			{audioProjectionRatio(0.5), audioProjectionRatio(invalid)},
		} {
			quote, err := NormalizePricingUSD([]Pricing{{ModelRatio: 1.25, CompletionRatio: 2,
				AudioRatio: ratios[0], AudioCompletionRatio: ratios[1]}})
			require.Error(t, err)
			require.Nil(t, quote, "invalid audio quotes must never be published as free")
		}
	}
	for _, ratios := range [][2]float64{{math.MaxFloat64, 4}, {-2, -3}, {0, -1}} {
		quote, err := NormalizePricingUSD([]Pricing{{ModelRatio: 1.25, CompletionRatio: 2,
			AudioRatio: audioProjectionRatio(ratios[0]), AudioCompletionRatio: audioProjectionRatio(ratios[1])}})
		require.Error(t, err)
		require.Nil(t, quote)
	}
}

func TestUSDAudioOutputIgnoresStaleDerivedInputWhenAudioRatioIsAbsent(t *testing.T) {
	pricingCurrencyFixture(t, 500000, 2500000)
	legacy := Pricing{ModelRatio: 1.25, CompletionRatio: 2,
		AudioCompletionRatio: audioProjectionRatio(3), AudioInputPrice: audioProjectionRatio(999)}
	quote, err := NormalizePricingUSD([]Pricing{legacy})
	require.NoError(t, err)
	require.Equal(t, 1.5, *quote[0].AudioOutputPrice,
		"absent audio ratio defaults to one rather than reading an old derived amount")
	require.Equal(t, 999.0, *legacy.AudioInputPrice)
	require.Nil(t, legacy.AudioOutputPrice)
}
