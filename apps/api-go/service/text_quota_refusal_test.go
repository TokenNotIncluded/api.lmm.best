package service

import (
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

func TestPostTextConsumeQuotaClaudeRefusalRefundsPrepayment(t *testing.T) {
	for _, funding := range []string{"wallet_only", "subscription_first"} {
		for _, pricing := range []string{"ratio", "fixed", "tiered", "invalid_tiered"} {
			for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
				t.Run(funding+"/"+pricing+"/"+string(format), func(t *testing.T) {
					db, info, c := subscriptionBillingFixture(t, 100000, true, funding)
					require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}, &model.QuotaData{}))
					previousDB, previousLogging, previousExport := model.LOG_DB, common.LogConsumeEnabled, common.DataExportEnabled
					model.LOG_DB, common.LogConsumeEnabled, common.DataExportEnabled = db, true, true
					model.CacheQuotaDataLock.Lock()
					previousQuotaData := model.CacheQuotaData
					model.CacheQuotaData = map[string]*model.QuotaData{}
					model.CacheQuotaDataLock.Unlock()
					t.Cleanup(func() {
						model.LOG_DB, common.LogConsumeEnabled, common.DataExportEnabled = previousDB, previousLogging, previousExport
						model.CacheQuotaDataLock.Lock()
						model.CacheQuotaData = previousQuotaData
						model.CacheQuotaDataLock.Unlock()
					})
					channel := model.Channel{Name: "claude-refusal"}
					require.NoError(t, db.Create(&channel).Error)
					info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: channel.Id}
					info.OriginModelName = t.Name()
					info.StartTime = time.Now()
					info.RelayFormat = format
					info.FinalRequestRelayFormat = types.RelayFormatClaude
					info.SetEstimatePromptTokens(400)
					info.PriceData = hosttypes.PriceData{
						ModelRatio: 3, CompletionRatio: 5,
						GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 2, GroupSpecialRatio: 0.8},
					}
					info.PriceData.AddOtherRatio("n", 3)
					if pricing == "fixed" {
						info.PriceData.UsePrice, info.PriceData.ModelPrice = true, 0.02
					}
					if pricing == "tiered" || pricing == "invalid_tiered" {
						expr := `tier("refusal", p * 10 + 100000)`
						if pricing == "invalid_tiered" {
							expr = `invalid +-+ expr`
						}
						info.TieredBillingSnapshot = makeSnapshot(expr, 2, 400, 100)
					}
					// Even provider-reported tool accounting must not compose a
					// surcharge after the validated no-output exemption.
					c.Set("claude_web_search_requests", 4)
					info.ResponsesUsageInfo = &relaycommon.ResponsesUsageInfo{BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
						dto.BuildInToolImageGeneration: {CallCount: 2},
					}}
					require.Nil(t, PreConsumeBilling(c, 60000, info))
					require.Equal(t, 60000, info.FinalPreConsumedQuota)
					var token model.Token
					require.NoError(t, db.First(&token, info.TokenId).Error)
					require.Equal(t, 60000, token.UsedQuota)
					// A successful history sample must not turn an exempt
					// request into a missing-usage estimate.
					require.NoError(t, db.Create(&model.Log{Type: model.LogTypeConsume, ModelName: info.OriginModelName, PromptTokens: 1, Quota: 12000, CreatedAt: time.Now().Unix()}).Error)
					common.SetContextKey(c, constant.ContextKeyBillingExemptReason, constant.BillingExemptReasonClaudeRefusalNoOutput)
					common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "claude_stop_reason=refusal")
					upstream := &dto.ClaudeUsage{InputTokens: 100, OutputTokens: 0, CacheReadInputTokens: 30, CacheCreationInputTokens: 20}
					usage := &dto.Usage{PromptTokens: 150, InputTokens: 150, TotalTokens: 150, UsageSemantic: "openai", UsageSource: "converted", BillingUsage: dto.NewClaudeMessagesBillingUsage(upstream)}
					before, err := json.Marshal(usage)
					require.NoError(t, err)
					PostTextConsumeQuota(c, info, usage, nil)
					after, err := json.Marshal(usage)
					require.NoError(t, err)
					require.Equal(t, before, after, "settlement must not change the usage returned to the client")
					if funding == "subscription_first" {
						assertSubscriptionBillingBalances(t, db, info, 0, 0, 0)
					} else {
						var user model.User
						require.NoError(t, db.First(&user, info.UserId).Error)
						require.Equal(t, 1000000, user.Quota)
						require.NoError(t, db.First(&token, info.TokenId).Error)
						require.Equal(t, 1000000, token.RemainQuota)
						require.Zero(t, token.UsedQuota)
					}
					var user model.User
					require.NoError(t, db.First(&user, info.UserId).Error)
					require.Zero(t, user.UsedQuota)
					require.Equal(t, 1, user.RequestCount)
					require.NoError(t, db.First(&channel, channel.Id).Error)
					require.Zero(t, channel.UsedQuota)
					var logs []model.Log
					require.NoError(t, db.Where("user_id = ? AND type = ?", info.UserId, model.LogTypeConsume).Find(&logs).Error)
					require.Len(t, logs, 1)
					require.Zero(t, logs[0].Quota)
					require.Equal(t, 100, logs[0].PromptTokens, "retain measured upstream token facts in logs")
					require.Zero(t, logs[0].CompletionTokens)
					var other map[string]interface{}
					require.NoError(t, json.Unmarshal([]byte(logs[0].Other), &other))
					require.Equal(t, constant.BillingExemptReasonClaudeRefusalNoOutput, other["billing_exempt_reason"])
					require.NotContains(t, other, "reject_reason")
					require.Equal(t, "claude_stop_reason=refusal", other["admin_info"].(map[string]interface{})["reject_reason"])
					require.NotContains(t, other, "usage_estimated")
					require.NotContains(t, other, "upstream_empty_usage")
					require.NotContains(t, other, "tool_surcharges")
					require.NotContains(t, other, "tiered_billing")
					model.SaveQuotaDataCache()
					var exported []model.QuotaData
					require.NoError(t, db.Find(&exported).Error)
					require.Len(t, exported, 1)
					require.Equal(t, 1, exported[0].Count)
					require.Zero(t, exported[0].TokenUsed)
					require.Zero(t, exported[0].Quota)
				})
			}
		}
	}
}

func TestTextBillingExemptionRequiresValidatedReasonAndZeroMeasuredOutput(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		usage        *dto.Usage
		wantExempt   bool
	}{
		{name: "no_marker", usage: &dto.Usage{PromptTokens: 100}},
		{name: "unknown_marker", reason: "free", usage: &dto.Usage{PromptTokens: 100}},
		{name: "missing_usage", reason: constant.BillingExemptReasonClaudeRefusalNoOutput},
		{name: "positive_output", reason: constant.BillingExemptReasonClaudeRefusalNoOutput, usage: &dto.Usage{PromptTokens: 100, CompletionTokens: 1}},
		{name: "negative_output", reason: constant.BillingExemptReasonClaudeRefusalNoOutput, usage: &dto.Usage{CompletionTokens: -1}},
		{name: "validated_zero_output", reason: constant.BillingExemptReasonClaudeRefusalNoOutput, usage: &dto.Usage{PromptTokens: 100}, wantExempt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, info, c := subscriptionBillingFixture(t, 100000, true, "wallet_only")
			info.StartTime = time.Now()
			info.PriceData = hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
			info.SetEstimatePromptTokens(100)
			common.SetContextKey(c, constant.ContextKeyBillingExemptReason, tc.reason)
			summary := calculateTextQuotaSummary(c, info, tc.usage)
			require.Equal(t, tc.wantExempt, summary.BillingExemptReason != "")
			if tc.wantExempt {
				require.Zero(t, summary.Quota)
				require.False(t, summary.hasBillableUsage())
			} else if tc.name != "negative_output" {
				require.Greater(t, summary.Quota, 0)
			}
		})
	}
}
