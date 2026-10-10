package service

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// These behavioral tests also compile against the pre-fix source. They test
// actual settlement entry points, not copies of the billing implementation.
func TestBillingBaseCostAudioUsesCompleteFrozenPrice(t *testing.T) {
	for _, tc := range []struct {
		name  string
		price hosttypes.PriceData
		usage dto.Usage
		want  int
	}{
		{"fixed USD", hosttypes.PriceData{UsePrice: true, ModelPrice: .001}, dto.Usage{PromptTokens: 1, TotalTokens: 1}, 500},
		{"fixed with detail-only usage", hosttypes.PriceData{UsePrice: true, ModelPrice: .001}, dto.Usage{PromptTokensDetails: dto.InputTokenDetails{AudioTokens: 2}}, 500},
		{"frozen modality rates", hosttypes.PriceData{ModelPrice: -1, ModelRatio: 1.04, CompletionRatio: 3, AudioRatio: 4, AudioCompletionRatio: 2}, dto.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7, PromptTokensDetails: dto.InputTokenDetails{TextTokens: 1, AudioTokens: 2}, CompletionTokenDetails: dto.OutputTokenDetails{TextTokens: 3, AudioTokens: 1}}, 28},
		{"zero price stays free", hosttypes.PriceData{UsePrice: true, ModelPrice: 0}, dto.Usage{PromptTokens: 1, TotalTokens: 1}, 0},
		{"no usage refunds reservation", hosttypes.PriceData{UsePrice: true, ModelPrice: .001}, dto.Usage{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.price.GroupRatioInfo.GroupRatio = 1
			db, ctx, info := realtimeBillingFixture(t, tc.price)
			original := tc.usage
			PostAudioConsumeQuota(ctx, info, &tc.usage, "")
			assertRealtimeBalances(t, db, info, tc.want)
			require.Equal(t, original, tc.usage, "billing must not mutate the client usage")
			var log model.Log
			require.NoError(t, db.Where("type = ?", model.LogTypeConsume).First(&log).Error)
			require.Equal(t, tc.want, log.Quota)
		})
	}
}

func TestBillingBaseCostAudioAppliesOtherRatios(t *testing.T) {
	price := hosttypes.PriceData{UsePrice: true, ModelPrice: .0001, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
	price.AddOtherRatio("duration", 2.01)
	db, ctx, info := realtimeBillingFixture(t, price)
	PostAudioConsumeQuota(ctx, info, &dto.Usage{PromptTokens: 1, TotalTokens: 1}, "")
	assertRealtimeBalances(t, db, info, 101) // 50 * 2.01 = 100.5, one final ceiling.
}

func TestBillingBaseCostTextRetainsMeasuredCacheOnlyUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage dto.Usage
		want  int
	}{
		{"cache read", dto.Usage{PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 100}}, 10},
		{"aggregate cache write", dto.Usage{PromptTokensDetails: dto.InputTokenDetails{CachedCreationTokens: 100}}, 125},
		{"one hour cache write", dto.Usage{ClaudeCacheCreation1hTokens: 10}, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			tc.usage.UsageSemantic = "anthropic"
			info := &relaycommon.RelayInfo{StartTime: time.Now(), PriceData: hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 2, CacheRatio: .1, CacheCreationRatio: 1.25, CacheCreation5mRatio: 1.25, CacheCreation1hRatio: 2, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
			summary := calculateTextQuotaSummary(ctx, info, &tc.usage)
			require.True(t, summary.hasBillableUsage())
			require.Equal(t, tc.want, summary.Quota)
		})
	}
}

func TestBillingBaseCostTieredRetainsAggregateAndPartialCacheWrites(t *testing.T) {
	for _, tc := range []struct {
		name              string
		total, five, hour int
		semantic          string
		wantFive          int
	}{
		{"aggregate only", 100, 0, 0, "anthropic", 100},
		{"partial split", 100, 10, 20, "anthropic", 80},
		{"duration without semantic marker", 100, 0, 100, "", 0},
		{"split without aggregate", 0, 80, 20, "anthropic", 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := &dto.Usage{UsageSemantic: tc.semantic, PromptTokens: 5, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 20, CachedCreationTokens: tc.total}, ClaudeCacheCreation5mTokens: tc.five, ClaudeCacheCreation1hTokens: tc.hour}
			expr := `len > 100 ? tier("long", p * 4 + cr * 0.4 + cc * 5 + cc1h * 8) : tier("short", p * 2 + cr * 0.2 + cc * 2.5 + cc1h * 4)`
			params := BuildTieredTokenParams(usage, true, billingexpr.UsedVars(expr))
			require.Equal(t, float64(tc.wantFive), params.CC)
			require.Equal(t, float64(tc.hour), params.CC1h)
			require.Equal(t, 125.0, params.Len)
			result, err := billingexpr.ComputeTieredQuota(makeSnapshot(expr, 1, 0, 0), params)
			require.NoError(t, err)
			require.Equal(t, "long", result.MatchedTier)
			require.Equal(t, (20+8+tc.wantFive*5+tc.hour*8)/2, result.ActualQuotaAfterGroup)
		})
	}
}

func TestBillingBaseCostRoundingAndUSDAnchor(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{StartTime: time.Now(), PriceData: hosttypes.PriceData{UsePrice: true, ModelPrice: .01, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
	require.Equal(t, 5000, calculateTextQuotaSummary(ctx, info, &dto.Usage{PromptTokens: 1}).Quota)
	info.PriceData = hosttypes.PriceData{ModelPrice: -1, ModelRatio: 1.04, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
	require.Equal(t, 11, calculateTextQuotaSummary(ctx, info, &dto.Usage{PromptTokens: 10}).Quota)
	info.PriceData.CacheRatio = 0
	require.Zero(t, calculateTextQuotaSummary(ctx, info, &dto.Usage{PromptTokens: 10, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 10}}).Quota, "a configured free component is not a positive charge")
}

func TestBillingBaseCostTieredAndToolRoundOnce(t *testing.T) {
	for _, tc := range []struct {
		expr, extra string
		want        int
	}{
		{`tier("default", p * 0.8)`, "0.4", 1}, // 0.4 + 0.4, not ceil(0.4) twice.
		{`tier("default", p * 2.4)`, "0.1", 2}, // 1.2 + 0.1, not round(1.2).
	} {
		info := makeRelayInfo(tc.expr, 1, 1, 0)
		result, err := billingexpr.ComputeTieredQuota(info.TieredBillingSnapshot, billingexpr.TokenParams{P: 1})
		require.NoError(t, err)
		summary := textQuotaSummary{ToolCallSurchargeQuota: decimal.RequireFromString(tc.extra)}
		require.Equal(t, tc.want, composeTieredTextQuota(info, summary, result.ActualQuotaAfterGroup, &result))
	}
}

func TestBillingBaseCostTaskUsesSubmissionPrices(t *testing.T) {
	truncate(t)
	old := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(old)) })
	// Simulate an administrator reducing the live price during the task.
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"test-model":0.001}`))
	seedUser(t, 7101, 900)
	seedToken(t, 7101, 7101, "billing-snapshot-fixture", 900)
	seedChannel(t, 7101)
	task := makeTask(7101, 7101, 100, 7101, BillingSourceWallet, 0)
	task.PrivateData.BillingContext.ModelRatio = 1.04
	task.PrivateData.BillingContext.GroupRatio = 1
	task.PrivateData.BillingContext.OtherRatios = map[string]float64{"duration": 2}
	require.NoError(t, task.Insert())
	RecalculateTaskQuotaByTokens(context.Background(), task, 100)
	require.Equal(t, 208, task.Quota)
	require.Equal(t, 792, getUserQuota(t, 7101))
	require.Equal(t, 792, getTokenRemainQuota(t, 7101))
	// Repeating the same measured result must not charge its delta twice.
	RecalculateTaskQuotaByTokens(context.Background(), task, 100)
	require.Equal(t, 792, getUserQuota(t, 7101))
}
