package service

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestCanonicalAudioQuotesMatchRealtimeSettlement(t *testing.T) {
	oldQ := common.QuotaPerUnit
	oldK, oldKErr := common.CreditsPerUSD()
	oldLegacy, _ := common.LegacyPricingQuotaPerUnit()
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQ
		if oldKErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(oldK, oldLegacy))
		}
	})
	const creditsPerUSD = 2500000
	// These are configured token-rate fixtures for real audio models. The literal
	// one-million-audio-token debits exercise the actual settlement function.
	fixtures := []struct {
		name                                      string
		model, completion, audio, audioCompletion float64
		wantInputUSD, wantOutputUSD               float64
		wantOutputCredits                         int
		omitAudioCompletion                       bool
	}{
		{"gpt-realtime", 2, 4, 8, 2, 6.4, 12.8, 32000000, false},
		{"gpt-realtime-2", 2, 6, 8, 2, 6.4, 12.8, 32000000, false},
		{"gpt-audio", 1.25, 4, 12.8, 2, 6.4, 12.8, 32000000, false},
		{"gpt-audio-mini", .3, 4, 16.666666666666668, 2, 2, 4, 10000000, false},
		{"gpt-4o-realtime-preview", 2.5, 4, 8, 1, 8, 8, 20000000, true},
	}
	for _, legacyQ := range []float64{500000, 1000000, 3000000} {
		common.QuotaPerUnit = legacyQ
		require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(creditsPerUSD)))
		for _, fixture := range fixtures {
			t.Run(fixture.name+"/Q="+decimal.NewFromFloat(legacyQ).String(), func(t *testing.T) {
				legacy := model.Pricing{ModelName: fixture.name, ModelRatio: fixture.model,
					CompletionRatio: fixture.completion, AudioRatio: &fixture.audio,
					AudioCompletionRatio: &fixture.audioCompletion}
				if fixture.omitAudioCompletion {
					legacy.AudioCompletionRatio = nil
				}
				quotes, err := model.NormalizePricingUSD([]model.Pricing{legacy})
				require.NoError(t, err)
				quote := quotes[0]
				require.InDelta(t, fixture.wantInputUSD, *quote.AudioInputPrice, 1e-12)
				require.InDelta(t, fixture.wantOutputUSD, *quote.AudioOutputPrice, 1e-12)
				require.Nil(t, legacy.AudioOutputPrice, "the source billing snapshot remains untouched")
				cache, _ := ratio_setting.GetCacheRatio(fixture.name)
				for _, group := range []float64{1, .485} {
					price := hosttypes.PriceData{ModelRatio: fixture.model, CompletionRatio: fixture.completion,
						AudioRatio: fixture.audio, AudioCompletionRatio: fixture.audioCompletion,
						CacheRatio: cache, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: group}}
					info := &relaycommon.RelayInfo{OriginModelName: fixture.name, PriceData: price}
					usage := &dto.RealtimeUsage{TotalTokens: 1000000, OutputTokens: 1000000,
						OutputTokenDetails: dto.OutputTokenDetails{AudioTokens: 1000000}}
					actual, _, err := realtimeReservationQuota(info, usage)
					require.NoError(t, err)
					want, clamp := common.QuotaFromDecimalChecked(decimal.NewFromInt(int64(fixture.wantOutputCredits)).Mul(decimal.NewFromFloat(group)))
					require.Nil(t, clamp)
					require.Equal(t, want, actual)
					quoted, clamp := common.QuotaFromDecimalChecked(decimal.NewFromFloat(*quote.AudioOutputPrice).
						Mul(decimal.NewFromInt(creditsPerUSD)).Mul(decimal.NewFromFloat(group)))
					require.Nil(t, clamp)
					require.Equal(t, actual, quoted, "public USD audio quote must buy the same measured audio tokens")
					// A mixed response keeps text, classified text-cache and audio
					// categories separate, as the real settlement code does.
					usage = &dto.RealtimeUsage{TotalTokens: 140, InputTokens: 100, OutputTokens: 40,
						InputTokenDetails: dto.InputTokenDetails{TextTokens: 80, AudioTokens: 20, CachedTokens: 20,
							CachedTokensDetails: &dto.CachedTokenDetails{TextTokens: 20}},
						OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 10, AudioTokens: 30}}
					actual, _, err = realtimeReservationQuota(info, usage)
					require.NoError(t, err)
					usdPerMillion := decimal.NewFromFloat(*quote.InputPrice).Mul(decimal.NewFromInt(60)).
						Add(decimal.NewFromFloat(*quote.CacheReadPrice).Mul(decimal.NewFromInt(20))).
						Add(decimal.NewFromFloat(*quote.AudioInputPrice).Mul(decimal.NewFromInt(20))).
						Add(decimal.NewFromFloat(*quote.OutputPrice).Mul(decimal.NewFromInt(10))).
						Add(decimal.NewFromFloat(*quote.AudioOutputPrice).Mul(decimal.NewFromInt(30)))
					quoted, clamp = common.QuotaFromDecimalChecked(usdPerMillion.Div(decimal.NewFromInt(1000000)).
						Mul(decimal.NewFromInt(creditsPerUSD)).Mul(decimal.NewFromFloat(group)))
					require.Nil(t, clamp)
					require.Equal(t, actual, quoted, "mixed public quote preserves the measured text-cache discount")
				}
			})
		}
	}
}
