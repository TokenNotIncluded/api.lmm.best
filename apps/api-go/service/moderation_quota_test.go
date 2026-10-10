package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/stretchr/testify/require"
)

// Native moderation reports classifications rather than token consumption.
// Existing reservations and another call's measured usage must not turn that
// absence into a charge, while explicit administrator prices remain binding.
func TestModerationUnmeteredSettlementIgnoresHistoryAndRefundsReservations(t *testing.T) {
	for _, tc := range []struct {
		name       string
		preference string
		total      int64
		fixed      bool
		price      float64
		expr       string
		actual     int
		sub        int64
		wallet     int
	}{
		{name: "configured free subscription", preference: "subscription_first", total: 100000, fixed: true},
		{name: "configured free partial subscription", preference: "subscription_first", total: 100, fixed: true},
		{name: "configured free wallet", preference: "wallet_first", total: 100000, fixed: true},
		{name: "token price has no measured tokens", preference: "subscription_first", total: 100000},
		{name: "explicit fixed price", preference: "subscription_first", total: 100000, fixed: true, price: 100 / common.QuotaPerUnit, actual: 100, sub: 100},
		{name: "explicit fixed price wallet overflow", preference: "subscription_first", total: 50, fixed: true, price: 100 / common.QuotaPerUnit, actual: 100, sub: 50, wallet: 50},
		{name: "explicit tier minimum", preference: "subscription_first", total: 100000, expr: `tier("minimum", 200 + p * 0.1 + c * 10)`, actual: 100, sub: 100},
		{name: "token only expression", preference: "subscription_first", total: 100000, expr: `tier("input", p * 0.1)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, info, c := subscriptionBillingFixture(t, tc.total, true, tc.preference)
			require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}))
			previousLogDB, previousLogging := model.LOG_DB, common.LogConsumeEnabled
			model.LOG_DB, common.LogConsumeEnabled = db, true
			t.Cleanup(func() { model.LOG_DB, common.LogConsumeEnabled = previousLogDB, previousLogging })

			channel := model.Channel{Name: tc.name}
			require.NoError(t, db.Create(&channel).Error)
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: channel.Id}
			info.StartTime = time.Now()
			info.OriginModelName = "omni-moderation-latest"
			info.RelayFormat = types.RelayFormatOpenAI
			info.RelayMode = relayconstant.RelayModeModerations
			info.SetEstimatePromptTokens(9999)
			info.PriceData = hosttypes.PriceData{
				UsePrice: tc.fixed, ModelPrice: tc.price, ModelRatio: 0.5,
				GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}, QuotaToPreConsume: 60000,
			}
			if tc.expr != "" {
				info.PriceData.ModelRatio = 0
				info.TieredBillingSnapshot = makeRelayInfo(tc.expr, 1, 9999, 0).TieredBillingSnapshot
			}

			// This valid historical sample would charge 50000 quota through the
			// generic missing-usage fallback. It is not this request's usage.
			history := model.Log{
				UserId: info.UserId, Type: model.LogTypeConsume, ModelName: info.OriginModelName,
				Quota: 50000, PromptTokens: 100, CreatedAt: time.Now().Unix() - 60, Other: `{}`,
			}
			require.NoError(t, db.Create(&history).Error)
			historicalQuota, samples, err := model.EstimateRecentModelQuota(info.OriginModelName, 60000)
			require.NoError(t, err)
			require.Equal(t, 50000, historicalQuota)
			require.Equal(t, 1, samples)

			// Reserve before settling to verify refund of existing wallet,
			// subscription and token debits, including a partially funded call.
			session, apiErr := NewBillingSession(c, info, 60000)
			require.Nil(t, apiErr)
			info.Billing = session
			info.SubscriptionAmountTotal = 0
			var reservedToken model.Token
			require.NoError(t, db.First(&reservedToken, info.TokenId).Error)
			require.Equal(t, 60000, reservedToken.UsedQuota)
			if tc.preference == "wallet_first" {
				var reservedUser model.User
				require.NoError(t, db.First(&reservedUser, info.UserId).Error)
				require.Equal(t, 940000, reservedUser.Quota)
			}

			usage := &dto.Usage{UsageSource: "moderation_unmetered"}
			require.NoError(t, PostTextConsumeQuotaWithResult(c, info, usage, nil))
			var subscription model.UserSubscription
			require.NoError(t, db.Where("user_id = ?", info.UserId).First(&subscription).Error)
			require.Equal(t, tc.sub, subscription.AmountUsed)
			var user model.User
			require.NoError(t, db.First(&user, info.UserId).Error)
			require.Equal(t, 1000000-tc.wallet, user.Quota)
			require.Equal(t, tc.actual, user.UsedQuota)
			require.Equal(t, 1, user.RequestCount)
			var token model.Token
			require.NoError(t, db.First(&token, info.TokenId).Error)
			require.Equal(t, 1000000-tc.actual, token.RemainQuota)
			require.Equal(t, tc.actual, token.UsedQuota)
			require.NoError(t, db.First(&channel, channel.Id).Error)
			require.EqualValues(t, tc.actual, channel.UsedQuota)

			var log model.Log
			require.NoError(t, db.Where("type = ? AND id != ?", model.LogTypeConsume, history.Id).First(&log).Error)
			require.Equal(t, tc.actual, log.Quota)
			require.Zero(t, log.PromptTokens, "no upstream token report must stay unmetered, even with a local request estimate")
			require.Zero(t, log.CompletionTokens)
			var other map[string]any
			require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
			require.Equal(t, "unmetered", other["moderation_usage_status"])
			require.NotContains(t, other, "usage_estimated")
			require.NotContains(t, other, "upstream_empty_usage")
			require.NotContains(t, other, "usage_estimate_basis")
			require.NotContains(t, other, "systemone_usage_status")
			require.False(t, info.ResponsesUsageReported)
			admin, ok := other["admin_info"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "moderation-unmetered", admin["usage_billing_path"])
			if tc.expr != "" {
				require.Equal(t, "tiered_expr", other["billing_mode"])
			}
		})
	}
}
