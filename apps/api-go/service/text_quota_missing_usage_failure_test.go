package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPostTextConsumeQuotaMissingUsageFailureRefundsPrepayment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reason    relaycommon.StreamEndReason
		endError  error
		softError bool
		cancelled bool
	}{
		{name: "created_then_eof", reason: relaycommon.StreamEndReasonEOF, softError: true},
		{name: "provider_failed", reason: relaycommon.StreamEndReasonDone, softError: true},
		{name: "read_error", reason: relaycommon.StreamEndReasonScannerErr, endError: io.ErrUnexpectedEOF},
		{name: "timeout", reason: relaycommon.StreamEndReasonTimeout, endError: context.DeadlineExceeded},
		{name: "client_cancelled", reason: relaycommon.StreamEndReasonDone, cancelled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, history := range []bool{false, true} {
				name := "without_history"
				if history {
					name = "with_history"
				}
				t.Run(name, func(t *testing.T) {
					for _, payload := range []struct {
						name               string
						usage              *dto.Usage
						prompt, completion int
					}{
						{name: "empty", usage: &dto.Usage{}},
						{name: "nil"},
						{name: "measured_input", usage: &dto.Usage{PromptTokens: 37, TotalTokens: 37}, prompt: 37},
						{name: "measured_output", usage: &dto.Usage{CompletionTokens: 11, TotalTokens: 11}, completion: 11},
						{name: "measured_both", usage: &dto.Usage{PromptTokens: 37, CompletionTokens: 11, TotalTokens: 48}, prompt: 37, completion: 11},
					} {
						t.Run(payload.name, func(t *testing.T) {
							db, info, c := subscriptionBillingFixture(t, 100000, true, "subscription_first")
							require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}))
							previousDB, previousLogging := model.LOG_DB, common.LogConsumeEnabled
							model.LOG_DB, common.LogConsumeEnabled = db, true
							t.Cleanup(func() {
								model.LOG_DB, common.LogConsumeEnabled = previousDB, previousLogging
							})

							channel := model.Channel{Name: "missing-usage-failure"}
							require.NoError(t, db.Create(&channel).Error)
							info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: channel.Id}
							info.StartTime = time.Now()
							info.SetEstimatePromptTokens(120)
							info.OriginModelName = t.Name()
							info.PriceData = hosttypes.PriceData{
								ModelRatio: 1, CompletionRatio: 1,
								GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
							}
							info.StreamStatus = relaycommon.NewStreamStatus()
							info.StreamStatus.SetEndReason(tc.reason, tc.endError)
							if tc.softError {
								info.StreamStatus.RecordError("upstream response did not complete")
							}

							session, apiErr := NewBillingSession(c, info, 60000)
							require.Nil(t, apiErr)
							info.Billing = session
							info.FinalPreConsumedQuota = session.GetPreConsumedQuota()
							info.SubscriptionAmountTotal = 0
							require.Equal(t, 60000, info.FinalPreConsumedQuota)
							if history {
								require.NoError(t, db.Create(&model.Log{
									CreatedAt: time.Now().Unix(), Type: model.LogTypeConsume,
									ModelName: info.OriginModelName, PromptTokens: 1, Quota: 12000,
								}).Error)
								estimate, samples, err := model.EstimateRecentModelQuota(info.OriginModelName, info.FinalPreConsumedQuota)
								require.NoError(t, err)
								require.Equal(t, 12000, estimate)
								require.Equal(t, 1, samples)
							}
							if tc.cancelled {
								ctx, cancel := context.WithCancel(context.Background())
								c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
								cancel()
							}

							wantQuota := payload.prompt + payload.completion
							PostTextConsumeQuota(c, info, payload.usage, nil)
							assertSubscriptionBillingBalances(t, db, info, int64(wantQuota), 0, wantQuota)

							var logs []model.Log
							require.NoError(t, db.Where("type = ? AND user_id = ?", model.LogTypeConsume, info.UserId).Find(&logs).Error)
							require.Len(t, logs, 1)
							require.Equal(t, wantQuota, logs[0].Quota)
							require.Equal(t, payload.prompt, logs[0].PromptTokens)
							require.Equal(t, payload.completion, logs[0].CompletionTokens)
							var other map[string]interface{}
							require.NoError(t, json.Unmarshal([]byte(logs[0].Other), &other))
							require.NotContains(t, other, "usage_estimated")
							billing := other["billing_settlement"].(map[string]interface{})
							require.EqualValues(t, wantQuota, billing["actual_quota"])
							require.EqualValues(t, wantQuota, billing["charged_quota"])
							require.Equal(t, "settled", billing["status"])

							var user model.User
							require.NoError(t, db.First(&user, info.UserId).Error)
							require.EqualValues(t, wantQuota, user.UsedQuota)
							wantRequests := 0
							if wantQuota > 0 {
								wantRequests = 1
							}
							require.Equal(t, wantRequests, user.RequestCount)
							require.NoError(t, db.First(&channel, channel.Id).Error)
							require.EqualValues(t, wantQuota, channel.UsedQuota)
						})
					}
				})
			}
		})
	}
}

func TestCalculateTextQuotaSummaryNilUsageCompletionGate(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		reason                         relaycommon.StreamEndReason
		endError                       error
		noStatus, softError, cancelled bool
		wantPrompt                     int
	}{
		{name: "non_stream_success", noStatus: true, wantPrompt: 120},
		{name: "stream_done", reason: relaycommon.StreamEndReasonDone, wantPrompt: 120},
		{name: "stream_eof", reason: relaycommon.StreamEndReasonEOF, wantPrompt: 120},
		{name: "handler_stop", reason: relaycommon.StreamEndReasonHandlerStop, wantPrompt: 120},
		{name: "no_end_reason"},
		{name: "terminal_failure", reason: relaycommon.StreamEndReasonDone, softError: true},
		{name: "transport_failure", reason: relaycommon.StreamEndReasonEOF, endError: io.ErrUnexpectedEOF},
		{name: "cancelled_without_status", noStatus: true, cancelled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			requestCtx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(requestCtx)
			if tc.cancelled {
				cancel()
			}
			info := &relaycommon.RelayInfo{
				ChannelMeta:     &relaycommon.ChannelMeta{},
				OriginModelName: t.Name(),
				StartTime:       time.Now(),
				IsStream:        !tc.noStatus,
				PriceData: hosttypes.PriceData{
					ModelRatio: 1, CompletionRatio: 1,
					GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
				},
			}
			info.SetEstimatePromptTokens(120)
			if !tc.noStatus {
				info.StreamStatus = relaycommon.NewStreamStatus()
				info.StreamStatus.SetEndReason(tc.reason, tc.endError)
				if tc.softError {
					info.StreamStatus.RecordError("upstream response did not complete")
				}
			}
			summary := calculateTextQuotaSummary(c, info, nil)
			require.Equal(t, tc.wantPrompt, summary.PromptTokens)
			require.Zero(t, summary.CompletionTokens)
			require.Equal(t, tc.wantPrompt, summary.TotalTokens)
			require.Equal(t, tc.wantPrompt, summary.Quota)
			require.Equal(t, tc.wantPrompt > 0, summary.hasBillableUsage())
			require.Equal(t, 120, info.GetEstimatePromptTokens(), "billing must not mutate the request estimate")
		})
	}
}
