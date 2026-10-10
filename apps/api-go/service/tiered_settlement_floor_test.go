package service

import (
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const shortTieredExpr = `tier("default", p * 0.1)`

func tieredMinimumSettlementFixture(t *testing.T) (*gorm.DB, *relaycommon.RelayInfo, *gin.Context, model.Channel) {
	t.Helper()
	db, info, ctx := subscriptionBillingFixture(t, 1000, true, "subscription_first")
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}, &model.PublicRelayContribution{}))
	previousLogDB, previousLogging := model.LOG_DB, common.LogConsumeEnabled
	model.LOG_DB, common.LogConsumeEnabled = db, true
	t.Cleanup(func() {
		model.LOG_DB, common.LogConsumeEnabled = previousLogDB, previousLogging
	})

	channel := model.Channel{Name: t.Name()}
	require.NoError(t, db.Create(&channel).Error)
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: channel.Id}
	info.StartTime = time.Now()
	info.OriginModelName = t.Name()
	info.PriceData = hosttypes.PriceData{
		CompletionRatio: 1,
		GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
	}
	info.TieredBillingSnapshot = makeRelayInfo(shortTieredExpr, 1, 1000, 0).TieredBillingSnapshot
	require.Zero(t, info.PriceData.ModelRatio, "tiered expressions do not set a model ratio")
	require.Equal(t, 50, info.TieredBillingSnapshot.EstimatedQuotaAfterGroup)

	// The expression's positive cost is rounded by the shared consumption
	// policy; the service no longer needs a separate minimum-charge patch.
	raw, err := billingexpr.ComputeTieredQuota(info.TieredBillingSnapshot, billingexpr.TokenParams{P: 1})
	require.NoError(t, err)
	require.Positive(t, raw.ActualQuotaBeforeGroup)
	require.Equal(t, 1, raw.ActualQuotaAfterGroup)

	session, apiErr := NewBillingSession(ctx, info, info.TieredBillingSnapshot.EstimatedQuotaAfterGroup)
	require.Nil(t, apiErr)
	info.Billing = session
	info.FinalPreConsumedQuota = session.GetPreConsumedQuota()
	require.Equal(t, 50, info.FinalPreConsumedQuota)
	return db, info, ctx, channel
}

func assertOneTieredQuotaSettled(t *testing.T, db *gorm.DB, info *relaycommon.RelayInfo, channel model.Channel) model.Log {
	t.Helper()
	var log model.Log
	require.NoError(t, db.Where("type = ? AND user_id = ?", model.LogTypeConsume, info.UserId).First(&log).Error)
	require.Equal(t, 1, log.Quota)
	assertSubscriptionBillingBalances(t, db, info, 1, 0, 1)
	var user model.User
	require.NoError(t, db.First(&user, info.UserId).Error)
	require.Equal(t, 1, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)
	require.NoError(t, db.First(&channel, channel.Id).Error)
	require.EqualValues(t, 1, channel.UsedQuota)
	return log
}

func TestPostTextConsumeQuotaTieredMinimum(t *testing.T) {
	for _, withToolSurcharge := range []bool{false, true} {
		name := "without_tool_surcharge"
		if withToolSurcharge {
			name = "with_sub_quota_tool_surcharge"
		}
		t.Run(name, func(t *testing.T) {
			db, info, ctx, channel := tieredMinimumSettlementFixture(t)
			if withToolSurcharge {
				// A separately priced tool call enters composeTieredTextQuota.
				// The 0.05 surcharge and 0.05 base must be rounded together.
				operation_setting.SetToolPriceForTest(dto.BuildInToolWebSearch, 0.0001)
				t.Cleanup(func() { operation_setting.DeleteToolPriceForTest(dto.BuildInToolWebSearch) })
				ctx.Set("claude_web_search_requests", 1)
			}
			usage := &dto.Usage{PromptTokens: 1, TotalTokens: 1}
			if withToolSurcharge {
				summary := calculateTextQuotaSummary(ctx, info, usage)
				require.InDelta(t, 0.05, summary.ToolCallSurchargeQuota.InexactFloat64(), 1e-9)
			}

			PostTextConsumeQuota(ctx, info, usage, nil)
			log := assertOneTieredQuotaSettled(t, db, info, channel)
			require.Equal(t, 1, log.PromptTokens)
			if withToolSurcharge {
				require.Contains(t, log.Other, "tool_surcharges")
			}
		})
	}
}

func TestPostAudioConsumeQuotaTieredMinimum(t *testing.T) {
	db, info, ctx, channel := tieredMinimumSettlementFixture(t)
	usage := &dto.Usage{
		PromptTokens: 1,
		TotalTokens:  1,
		PromptTokensDetails: dto.InputTokenDetails{
			TextTokens: 1,
		},
	}

	PostAudioConsumeQuota(ctx, info, usage, "")
	log := assertOneTieredQuotaSettled(t, db, info, channel)
	require.Equal(t, 1, log.PromptTokens)
}

func TestPostWssConsumeQuotaTieredMinimum(t *testing.T) {
	db, info, ctx, channel := tieredMinimumSettlementFixture(t)
	usage := &dto.RealtimeUsage{
		InputTokens: 1,
		TotalTokens: 1,
		InputTokenDetails: dto.InputTokenDetails{
			TextTokens: 1,
		},
	}

	PostWssConsumeQuota(ctx, info, info.OriginModelName, usage, "")
	log := assertOneTieredQuotaSettled(t, db, info, channel)
	require.Equal(t, 1, log.PromptTokens)
}
