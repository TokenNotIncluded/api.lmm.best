package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/stretchr/testify/require"
)

// A successful relay must never become free merely because the upstream
// omitted usage. Handlers should normally provide locally counted usage; this
// test covers the final safety net when both upstream and handler usage are
// empty and verifies that the pre-consumed quota remains charged.
func TestPostTextConsumeQuotaMissingUsageKeepsPreConsumedQuota(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100000, true, "subscription_first")
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}))

	previousLogDB, previousLogEnabled := model.LOG_DB, common.LogConsumeEnabled
	model.LOG_DB, common.LogConsumeEnabled = db, true
	t.Cleanup(func() {
		model.LOG_DB, common.LogConsumeEnabled = previousLogDB, previousLogEnabled
	})

	channel := model.Channel{Name: "missing-usage-billing"}
	require.NoError(t, db.Create(&channel).Error)
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: channel.Id}
	info.StartTime = time.Now()
	info.OriginModelName = "missing-usage-test"
	info.PriceData = hosttypes.PriceData{
		ModelRatio:      1,
		CompletionRatio: 1,
		GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
	}

	session, apiErr := NewBillingSession(c, info, 60000)
	require.Nil(t, apiErr)
	info.Billing = session
	info.FinalPreConsumedQuota = session.GetPreConsumedQuota()
	info.SubscriptionAmountTotal = 0
	require.Equal(t, 60000, info.FinalPreConsumedQuota)

	// This is a normal completed request: the request context is not cancelled,
	// and there is intentionally no upstream/local usage payload.
	PostTextConsumeQuota(c, info, &dto.Usage{}, nil)

	var log model.Log
	require.NoError(t, db.Where("type = ?", model.LogTypeConsume).First(&log).Error)
	require.Equal(t, 60000, log.Quota)
	require.Contains(t, log.Content, "保留本次预扣额度结算")

	var other map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
	require.Equal(t, true, other["upstream_empty_usage"])
	require.Equal(t, true, other["usage_estimated"])
	require.Equal(t, "preconsumed_fallback_no_history", other["usage_estimate_basis"])

	billing := other["billing_settlement"].(map[string]interface{})
	require.EqualValues(t, 60000, billing["actual_quota"])
	require.EqualValues(t, 60000, billing["charged_quota"])
	require.Equal(t, "settled", billing["status"])

	var user model.User
	require.NoError(t, db.First(&user, info.UserId).Error)
	require.EqualValues(t, 60000, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)

	require.NoError(t, db.First(&channel, channel.Id).Error)
	require.EqualValues(t, 60000, channel.UsedQuota)
}

func TestPostTextConsumeQuotaMissingUsageKeepsEstimateAfterPartialReserve(t *testing.T) {
	db, info, c := subscriptionBillingFixture(t, 100, true, "subscription_first")
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}))
	previousLogDB, previousLogEnabled := model.LOG_DB, common.LogConsumeEnabled
	model.LOG_DB, common.LogConsumeEnabled = db, true
	t.Cleanup(func() { model.LOG_DB, common.LogConsumeEnabled = previousLogDB, previousLogEnabled })
	channel := model.Channel{Name: "partial-missing-usage"}
	require.NoError(t, db.Create(&channel).Error)
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: channel.Id}
	info.StartTime = time.Now()
	info.OriginModelName = "partial-missing-usage"
	info.PriceData = hosttypes.PriceData{
		QuotaToPreConsume: 60000,
		ModelRatio:        1,
		CompletionRatio:   1,
		GroupRatioInfo:    hosttypes.GroupRatioInfo{GroupRatio: 1},
	}
	session, apiErr := NewBillingSession(c, info, 60000)
	require.Nil(t, apiErr)
	info.Billing = session
	info.SubscriptionAmountTotal = 0
	require.Equal(t, 100, info.FinalPreConsumedQuota)
	assertSubscriptionBillingBalances(t, db, info, 100, 0, 60000)

	PostTextConsumeQuota(c, info, &dto.Usage{}, nil)
	assertSubscriptionBillingBalances(t, db, info, 100, 59900, 60000)
	var log model.Log
	require.NoError(t, db.Where("type = ?", model.LogTypeConsume).First(&log).Error)
	require.Equal(t, 60000, log.Quota)
	var other map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
	require.EqualValues(t, 60000, other["usage_estimate_cap"])
	billing := other["billing_settlement"].(map[string]interface{})
	require.EqualValues(t, 60000, billing["charged_quota"])
	require.EqualValues(t, 59900, billing["wallet_quota"])
}

// A transport acceptance frame is not a successful completion. Preserve the
// successful-request fallback above, but never infer usage for an interrupted
// stream whose handler returned no upstream or locally counted consumption.
func TestPostTextConsumeQuotaFailedEmptyStreamRefundsPreConsumedQuota(t *testing.T) {
	for _, reason := range []relaycommon.StreamEndReason{
		relaycommon.StreamEndReasonEOF,
		relaycommon.StreamEndReasonTimeout,
		relaycommon.StreamEndReasonClientGone,
		relaycommon.StreamEndReasonScannerErr,
		relaycommon.StreamEndReasonHandlerStop,
	} {
		t.Run(string(reason), func(t *testing.T) {
			db, info, c := subscriptionBillingFixture(t, 100000, true, "subscription_first")
			require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}))
			previousLogDB, previousLogEnabled := model.LOG_DB, common.LogConsumeEnabled
			model.LOG_DB, common.LogConsumeEnabled = db, true
			t.Cleanup(func() {
				model.LOG_DB, common.LogConsumeEnabled = previousLogDB, previousLogEnabled
			})
			channel := model.Channel{Name: "failed-empty-stream"}
			require.NoError(t, db.Create(&channel).Error)
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: channel.Id}
			info.StartTime = time.Now()
			info.OriginModelName = "failed-empty-stream"
			info.IsStream = true
			info.StreamStatus = relaycommon.NewStreamStatus()
			info.StreamStatus.SetEndReason(reason, nil)
			if reason == relaycommon.StreamEndReasonEOF || reason == relaycommon.StreamEndReasonHandlerStop {
				info.StreamStatus.RecordError("stream ended without a successful terminal event")
			}
			info.PriceData = hosttypes.PriceData{
				ModelRatio: 1, CompletionRatio: 1,
				GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
			}
			session, apiErr := NewBillingSession(c, info, 60000)
			require.Nil(t, apiErr)
			info.Billing = session
			info.FinalPreConsumedQuota = session.GetPreConsumedQuota()
			info.SubscriptionAmountTotal = 0
			require.Equal(t, 60000, info.FinalPreConsumedQuota)

			PostTextConsumeQuota(c, info, &dto.Usage{}, nil)

			var log model.Log
			require.NoError(t, db.Where("type = ?", model.LogTypeConsume).First(&log).Error)
			require.Zero(t, log.Quota)
			var other map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
			require.Equal(t, true, other["upstream_empty_usage"])
			require.NotContains(t, other, "usage_estimated")
			billing := other["billing_settlement"].(map[string]interface{})
			require.Zero(t, billing["actual_quota"])
			require.Zero(t, billing["charged_quota"])
			var user model.User
			require.NoError(t, db.First(&user, info.UserId).Error)
			require.Zero(t, user.UsedQuota)
			require.NoError(t, db.First(&channel, channel.Id).Error)
			require.Zero(t, channel.UsedQuota)
		})
	}
}
