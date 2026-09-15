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
