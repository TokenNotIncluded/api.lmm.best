package service

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/stretchr/testify/require"
)

func TestSystemOneSettlementUsesUsageAndFrozenReservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		tokens int
		total  int64
		actual int
		sub    int64
		wallet int
		fixed  bool
		expr   string
	}{
		{"reported normal", "reported", 100, 100000, 50, 50, 0, false, ""},
		{"reported zero", "reported", 0, 100000, 0, 0, 0, false, ""},
		{"invalid usage reservation", "invalid", 0, 100000, 60000, 60000, 0, false, ""},
		{"invalid partial subscription", "invalid", 0, 100, 60000, 100, 59900, false, ""},
		{"invalid tier expression retains provenance", "invalid", 0, 100000, 60000, 60000, 0, false, `tier("frozen", p * 0.1)`},
		{"zero partial subscription", "reported", 0, 100, 0, 0, 0, false, ""},
		{"zero configured per call", "reported", 0, 100000, 100, 100, 0, true, ""},
		{"zero configured tier minimum", "reported", 0, 100000, 100, 100, 0, false, `tier("minimum", 200 + p * 0.1 + c * 10)`},
		{"zero token only expression", "reported", 0, 100000, 0, 0, 0, false, `tier("input", p * 0.1)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, info, c := subscriptionBillingFixture(t, tc.total, true, "subscription_first")
			require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}))
			previousLogDB, previousLogEnabled := model.LOG_DB, common.LogConsumeEnabled
			model.LOG_DB, common.LogConsumeEnabled = db, true
			t.Cleanup(func() { model.LOG_DB, common.LogConsumeEnabled = previousLogDB, previousLogEnabled })
			provider := model.Channel{Name: tc.name}
			require.NoError(t, db.Create(&provider).Error)
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: provider.Id}
			info.StartTime = time.Now()
			info.OriginModelName = "jev-latest"
			info.RelayFormat = types.RelayFormatSystemOne
			info.SystemOneUsageStatus = tc.status
			info.PriceData = hosttypes.PriceData{ModelRatio: 0.5, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}, QuotaToPreConsume: 60000}
			if tc.fixed {
				info.PriceData.UsePrice = true
				info.PriceData.ModelPrice = 100 / common.QuotaPerUnit
			}
			if tc.expr != "" {
				info.PriceData.ModelRatio = 0
				info.TieredBillingSnapshot = makeRelayInfo(tc.expr, 1, dto.SystemOneMaxInputTokens, 0).TieredBillingSnapshot
			}
			session, apiErr := NewBillingSession(c, info, 60000)
			require.Nil(t, apiErr)
			info.Billing = session
			info.SubscriptionAmountTotal = 0
			require.NoError(t, PostTextConsumeQuotaWithResult(c, info, &dto.Usage{PromptTokens: tc.tokens, InputTokens: tc.tokens, TotalTokens: tc.tokens, UsageSource: "typesafe"}, nil))
			assertSubscriptionBillingBalances(t, db, info, tc.sub, tc.wallet, tc.actual)
			var log model.Log
			require.NoError(t, db.Where("type = ?", model.LogTypeConsume).First(&log).Error)
			require.Equal(t, tc.actual, log.Quota)
			require.Equal(t, tc.tokens, log.PromptTokens)
			require.Zero(t, log.CompletionTokens)
			var other map[string]any
			require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
			require.Equal(t, tc.status, other["systemone_usage_status"])
			if tc.expr != "" {
				require.Equal(t, "tiered_expr", other["billing_mode"])
				encoded, ok := other["expr_b64"].(string)
				require.True(t, ok)
				decoded, err := base64.StdEncoding.DecodeString(encoded)
				require.NoError(t, err)
				require.Equal(t, tc.expr, string(decoded))
				if tc.status == "invalid" {
					require.NotContains(t, other, "matched_tier")
					require.NotContains(t, other, "request_rules")
				}
			}
			if tc.status == "invalid" {
				require.Equal(t, true, other["usage_estimated"])
				require.Equal(t, "typesafe_context_reservation", other["usage_estimate_basis"])
				require.EqualValues(t, dto.SystemOneMaxInputTokens, other["usage_estimate_token_budget"])
			}
		})
	}
}

func TestSystemOneReserveIndependentOfTokenCounting(t *testing.T) {
	previous := constant.CountToken
	t.Cleanup(func() { constant.CountToken = previous })
	// The path uses the vendor's context budget even if local token counting
	// has been disabled; it must not infer the proprietary tokenizer.
	for _, enabled := range []bool{false, true} {
		constant.CountToken = enabled
		got, err := EstimateRequestToken(nil, nil, &relaycommon.RelayInfo{RelayFormat: types.RelayFormatSystemOne})
		require.NoError(t, err)
		require.Equal(t, dto.SystemOneMaxInputTokens, got)
	}
}
