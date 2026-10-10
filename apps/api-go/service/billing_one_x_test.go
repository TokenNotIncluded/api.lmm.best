package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestOneXMeasuredCacheOnlyUsageIsBillable(t *testing.T) {
	for _, test := range []struct {
		name string
		read, write, want int
	}{
		{"read", 100, 0, 10},
		{"write", 0, 100, 125},
		{"both", 100, 100, 135},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{
				FinalRequestRelayFormat: types.RelayFormatClaude,
				StartTime: time.Now(),
				PriceData: hosttypes.PriceData{
					ModelRatio: 1, CompletionRatio: 1, CacheRatio: 0.1,
					CacheCreationRatio: 1.25, CacheCreation5mRatio: 1.25, CacheCreation1hRatio: 2,
					GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
				},
			}
			usage := &dto.Usage{UsageSemantic: "anthropic", PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens: test.read, CachedCreationTokens: test.write,
			}}
			summary := calculateTextQuotaSummary(ctx, info, usage)
			require.True(t, summary.hasBillableUsage())
			require.Equal(t, test.want, summary.Quota)
			require.Zero(t, usage.PromptTokens, "billing must not alter upstream usage")
		})
	}
}

func TestOneXTieredCacheCreationKeepsAggregateAndDuration(t *testing.T) {
	for _, test := range []struct {
		name, semantic string
		aggregate, five, hour int
		wantFive, wantHour, wantLen float64
	}{
		{"aggregate only", "anthropic", 100, 0, 0, 100, 0, 110},
		{"partial split", "anthropic", 100, 20, 30, 70, 30, 110},
		{"split exceeds aggregate", "anthropic", 10, 20, 30, 20, 30, 60},
		{"native format without semantic marker", "", 100, 20, 30, 70, 30, 110},
	} {
		t.Run(test.name, func(t *testing.T) {
			usage := &dto.Usage{
				PromptTokens: 10, UsageSemantic: test.semantic,
				PromptTokensDetails: dto.InputTokenDetails{CachedCreationTokens: test.aggregate},
				ClaudeCacheCreation5mTokens: test.five, ClaudeCacheCreation1hTokens: test.hour,
			}
			got := BuildTieredTokenParams(usage, true, map[string]bool{"cc": true, "cc1h": true})
			require.Equal(t, test.wantFive, got.CC)
			require.Equal(t, test.wantHour, got.CC1h)
			require.Equal(t, test.wantLen, got.Len)
			require.Equal(t, test.aggregate, usage.PromptTokensDetails.CachedCreationTokens)
		})
	}
}

func TestOneXTextChargeRoundsOnlyTheCompletePositiveAmount(t *testing.T) {
	for _, test := range []struct {
		name string
		ratio, cache float64
		want int
	}{
		{"fraction", 10.4, 1, 11},
		{"integer", 10, 1, 10},
		{"tiny positive", 0.01, 1, 1},
		{"explicit free cache", 1, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{StartTime: time.Now(), PriceData: hosttypes.PriceData{
				ModelRatio: test.ratio, CacheRatio: test.cache, CompletionRatio: 1,
				GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
			}}
			usage := &dto.Usage{PromptTokens: 1, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 1}}
			require.Equal(t, test.want, calculateTextQuotaSummary(ctx, info, usage).Quota)
		})
	}
}

func TestOneXTieredToolCompositionRoundsOnce(t *testing.T) {
	info := &relaycommon.RelayInfo{TieredBillingSnapshot: &billingexpr.BillingSnapshot{GroupRatio: 1}}
	summary := textQuotaSummary{ToolCallSurchargeQuota: decimal.RequireFromString("0.4")}
	result := &billingexpr.TieredResult{ActualQuotaBeforeGroup: 10.4}
	// Rebuild the unrounded request cost, rather than round each component.
	require.Equal(t, 11, composeTieredTextQuota(info, summary, 10, result))
}
