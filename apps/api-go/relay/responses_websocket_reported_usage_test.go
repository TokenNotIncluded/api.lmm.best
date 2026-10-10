package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	appconstant "github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	appmodel "github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/openai"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const responsesWSUsageTestBalance = 200000
const responsesWSUsageTestPrepayment = 60000
const responsesWSUsageTestPromptEstimate = 17
const responsesWSUsageTestDelta = "observed text from the provider"

type responsesWSUsageSQLFixture struct {
	db                   *gorm.DB
	c                    *gin.Context
	user                 appmodel.User
	token                appmodel.Token
	channel              appmodel.Channel
	model                string
	session              *responsesWSSession
	downstream, provider *websocket.Conn
}

func newResponsesWSUsageSQLFixture(t *testing.T) *responsesWSUsageSQLFixture {
	t.Helper()
	previousDB, previousLogDB := appmodel.DB, appmodel.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousMemory, previousBatch, previousLog := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	previousPost := postResponsesWSConsumeQuota
	previousRateEnabled, previousDuration, previousCount, previousSuccess := setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitDurationMinutes, setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount
	previousGroups := setting.ModelRequestRateLimitGroup2JSONString()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:responses-ws-usage-%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&appmodel.User{}, &appmodel.Token{}, &appmodel.Channel{}, &appmodel.Log{}, &appmodel.SubscriptionPreConsumeRecord{}, &appmodel.TopUp{}))
	appmodel.DB, appmodel.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, false, true
	postResponsesWSConsumeQuota = service.PostTextConsumeQuota
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes, setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = 1, 0, 1
	require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(`{}`))
	t.Cleanup(func() {
		require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond,
			"settlement background work must finish before restoring database globals")
		appmodel.DB, appmodel.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = previousRedis, previousMemory, previousBatch, previousLog
		postResponsesWSConsumeQuota = previousPost
		setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitDurationMinutes, setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = previousRateEnabled, previousDuration, previousCount, previousSuccess
		require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(previousGroups))
		_ = sqlDB.Close()
	})
	fixture := &responsesWSUsageSQLFixture{db: db, model: "ws-reported-usage-" + t.Name()}
	fixture.user = appmodel.User{Id: int(time.Now().UnixNano()), Username: "ws-usage-owner", Quota: responsesWSUsageTestBalance, Status: common.UserStatusEnabled, Setting: `{"billing_preference":"wallet_only"}`}
	require.NoError(t, db.Create(&fixture.user).Error)
	fixture.token = appmodel.Token{UserId: fixture.user.Id, Key: "ws-reported-usage-key", Name: "ws usage token", Status: common.TokenStatusEnabled, RemainQuota: responsesWSUsageTestBalance, ExpiredTime: -1}
	require.NoError(t, db.Create(&fixture.token).Error)
	fixture.channel = appmodel.Channel{Type: appconstant.ChannelTypeOpenAI, Name: "ws-usage-provider", Key: "provider-key", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&fixture.channel).Error)
	fixture.c, _ = gin.CreateTestContext(httptest.NewRecorder())
	fixture.c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	fixture.c.Set("id", fixture.user.Id)
	fixture.c.Set("username", fixture.user.Username)
	fixture.c.Set("token_name", fixture.token.Name)
	common.SetContextKey(fixture.c, appconstant.ContextKeyUserId, fixture.user.Id)
	common.SetContextKey(fixture.c, appconstant.ContextKeyTokenId, fixture.token.Id)
	common.SetContextKey(fixture.c, appconstant.ContextKeyTokenKey, fixture.token.Key)
	common.SetContextKey(fixture.c, appconstant.ContextKeyOriginalModel, fixture.model)
	common.SetContextKey(fixture.c, appconstant.ContextKeyUsingGroup, "default")
	common.SetContextKey(fixture.c, appconstant.ContextKeyUserGroup, "default")
	common.SetContextKey(fixture.c, appconstant.ContextKeyTokenGroup, "default")
	common.SetContextKey(fixture.c, appconstant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: "wallet_only"})
	common.SetContextKey(fixture.c, appconstant.ContextKeyChannelType, fixture.channel.Type)
	common.SetContextKey(fixture.c, appconstant.ContextKeyChannelId, fixture.channel.Id)
	common.SetContextKey(fixture.c, appconstant.ContextKeyChannelKey, fixture.channel.Key)
	common.SetContextKey(fixture.c, common.RequestIdKey, common.NewRequestId())
	client, downstream := responsesWSTestPair(t)
	target, provider := responsesWSTestPair(t)
	fixture.downstream, fixture.provider = downstream, provider
	fixture.session = &responsesWSSession{c: fixture.c, client: client, target: target}
	t.Cleanup(func() { fixture.session.closeTarget(); _ = client.Close() })
	return fixture
}

func (fixture *responsesWSUsageSQLFixture) reserve(t *testing.T) (*responsesWSCallState, <-chan bool, *atomic.Int32) {
	t.Helper()
	info := relaycommon.GenRelayInfoResponses(fixture.c, &dto.OpenAIResponsesRequest{Model: fixture.model})
	info.InitChannelMeta(fixture.c)
	info.ForcePreConsume, info.IsStream = true, true
	info.PriceData = hosttypes.PriceData{QuotaToPreConsume: responsesWSUsageTestPrepayment, ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
	info.SetEstimatePromptTokens(responsesWSUsageTestPromptEstimate)
	billing, apiErr := service.NewBillingSession(fixture.c, info, responsesWSUsageTestPrepayment)
	require.Nil(t, apiErr)
	info.Billing = billing
	fixture.assertBalances(t, responsesWSUsageTestPrepayment, 0, 0)
	commitRate, apiErr := middleware.CheckModelRequestRateLimit(fixture.c)
	require.Nil(t, apiErr)
	committed, commits := make(chan bool, 2), &atomic.Int32{}
	state := &responsesWSCallState{info: info, usage: &dto.Usage{}, outputText: newResponsesWSOutputTextBuffer(fixture.c), commitRate: func(success bool) {
		commitRate(success)
		commits.Add(1)
		committed <- success
	}}
	require.True(t, fixture.session.tryReserveCurrent(state))
	fixture.session.startTargetReader()
	return state, committed, commits
}

func (fixture *responsesWSUsageSQLFixture) roundTrip(t *testing.T, event string) {
	t.Helper()
	require.NoError(t, fixture.provider.WriteMessage(websocket.TextMessage, []byte(event)))
	require.NoError(t, fixture.downstream.SetReadDeadline(time.Now().Add(3*time.Second)))
	kind, body, err := fixture.downstream.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, kind)
	require.JSONEq(t, event, string(body), "usage authority must not change the wire payload")
}

func (fixture *responsesWSUsageSQLFixture) assertBalances(t *testing.T, charged, used, requests int) {
	t.Helper()
	var user appmodel.User
	var token appmodel.Token
	var channel appmodel.Channel
	require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
	require.NoError(t, fixture.db.First(&token, fixture.token.Id).Error)
	require.NoError(t, fixture.db.First(&channel, fixture.channel.Id).Error)
	require.Equal(t, responsesWSUsageTestBalance-charged, user.Quota, "wallet debit/refund")
	require.Equal(t, responsesWSUsageTestBalance-charged, token.RemainQuota, "finite token debit/refund")
	require.Equal(t, used, user.UsedQuota)
	require.Equal(t, charged, token.UsedQuota, "token usage includes the still-reserved prepayment")
	require.Equal(t, int64(used), channel.UsedQuota)
	require.Equal(t, requests, user.RequestCount)
}

func TestResponsesWSReportedUsageSettlesSQLWalletTokenAndLogs(t *testing.T) {
	type testCase struct {
		name, terminal, usage, placeholder string
		deltaType, terminalFields          string
		terminalText                       string
		itemFields, tool                   string
		lifecycle, lifecycleUsage          string
		delta, history                     bool
		noLog                              bool
		input, output, quota               int
		reported, successful               bool
		estimateBasis                      string
	}
	cases := []testCase{
		{name: "zero_completed_no_output_no_history", terminal: "response.completed", usage: `,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, reported: true, successful: true},
		{name: "zero_completed_no_output_with_history", terminal: "response.completed", usage: `,"usage":{}`, history: true, reported: true, successful: true},
		{name: "zero_completed_delta_no_history", terminal: "response.completed", usage: `,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, delta: true, reported: true, successful: true},
		{name: "zero_completed_delta_with_history", terminal: "response.completed", usage: `,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, delta: true, history: true, reported: true, successful: true},
		{name: "zero_done_with_delta", terminal: "response.done", usage: `,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, delta: true, history: true, reported: true, successful: true},
		{name: "missing_usage_delta", terminal: "response.completed", delta: true, successful: true},
		{name: "created_zero_placeholder", terminal: "response.completed", placeholder: "response.created", delta: true, successful: true},
		{name: "in_progress_zero_placeholder", terminal: "response.completed", placeholder: "response.in_progress", delta: true, successful: true},
		{name: "reported_input_preserves_zero_output", terminal: "response.completed", usage: `,"usage":{"input_tokens":37,"output_tokens":0,"total_tokens":37}`, delta: true, input: 37, quota: 37, reported: true, successful: true},
		{name: "reported_output_preserves_zero_input", terminal: "response.completed", usage: `,"usage":{"input_tokens":0,"output_tokens":11,"total_tokens":11}`, delta: true, output: 11, quota: 11, reported: true, successful: true},
		{name: "failed_zero_overrides_partial_output", terminal: "response.failed", usage: `,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, delta: true, reported: true},
		{name: "incomplete_zero_overrides_partial_output", terminal: "response.incomplete", usage: `,"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, delta: true, reported: true},
		{name: "failed_missing_usage_keeps_partial_output", terminal: "response.failed", delta: true},
		{name: "incomplete_missing_usage_keeps_partial_output", terminal: "response.incomplete", delta: true},
		{name: "failed_input_preserves_zero_output", terminal: "response.failed", usage: `,"usage":{"input_tokens":37,"output_tokens":0,"total_tokens":37}`, delta: true, input: 37, quota: 37, reported: true},
		{name: "failed_output_preserves_zero_input", terminal: "response.failed", usage: `,"usage":{"input_tokens":0,"output_tokens":11,"total_tokens":11}`, delta: true, output: 11, quota: 11, reported: true},
		{name: "missing_no_output_keeps_prepayment_fallback", terminal: "response.completed", quota: responsesWSUsageTestPrepayment, successful: true, estimateBasis: "preconsumed_fallback_no_history"},
		{name: "missing_no_output_keeps_history_fallback", terminal: "response.completed", history: true, quota: 48000, successful: true, estimateBasis: "same_model_recent_success_average"},
		{name: "delta_and_snapshot_are_not_counted_twice", terminal: "response.completed", delta: true, terminalFields: `,"output":[{"type":"message","content":[{"type":"output_text","text":"observed text from the provider plus a longer snapshot"}]}]`, successful: true},
		{name: "reported_zero_suppresses_snapshot_estimate", terminal: "response.completed", usage: `,"usage":{}`, terminalFields: `,"output":[{"type":"message","content":[{"type":"output_text","text":"terminal text"}]}]`, reported: true, successful: true},
		{name: "completed_nested_error_does_not_seed_snapshot", terminal: "response.completed", terminalFields: `,"status":"completed","error":{"code":"upstream_failed"},"output":[{"type":"message","content":[{"type":"output_text","text":"terminal text"}]}]`, history: true, noLog: true},
		{name: "completed_nested_error_keeps_previous_partial", terminal: "response.completed", delta: true, terminalFields: `,"status":"completed","error":{"code":"upstream_failed"},"output":[{"type":"message","content":[{"type":"output_text","text":"terminal text plus snapshot"}]}]`},
		{name: "completed_failed_status_does_not_seed_snapshot", terminal: "response.completed", terminalFields: `,"status":"failed","output":[{"type":"message","content":[{"type":"output_text","text":"terminal text"}]}]`, history: true, noLog: true},
		{name: "completed_failed_status_reported_zero_is_authoritative", terminal: "response.completed", terminalFields: `,"status":"failed"`, usage: `,"usage":{}`, delta: true, reported: true},
		{name: "completed_failed_status_reported_input_is_authoritative", terminal: "response.completed", terminalFields: `,"status":"failed"`, usage: `,"usage":{"input_tokens":37,"output_tokens":0,"total_tokens":37}`, input: 37, quota: 37, reported: true},
		{name: "reported_zero_keeps_actual_web_search_charge", terminal: "response.completed", usage: `,"usage":{}`, itemFields: `"type":"web_search_call","id":"search-1","status":"completed"`, tool: dto.BuildInToolWebSearchPreview, reported: true, successful: true},
		{name: "failed_reported_zero_keeps_actual_image_charge", terminal: "response.failed", usage: `,"usage":{}`, itemFields: `"type":"image_generation_call","id":"image-1","status":"completed","result":"base64-image"`, tool: dto.BuildInToolImageGeneration, reported: true},
		{name: "completed_nested_error_keeps_actual_image_charge", terminal: "response.completed", terminalFields: `,"status":"completed","error":{"code":"upstream_failed"}`, itemFields: `"type":"image_generation_call","id":"image-1","status":"completed","result":"base64-image"`, tool: dto.BuildInToolImageGeneration},
		{name: "encrypted_reasoning_and_tool_metadata_are_not_text", terminal: "response.completed", terminalFields: `,"output":[{"type":"reasoning","encrypted_content":"opaque-text"},{"type":"web_search_call","action":{"query":"tool metadata"}}]`, history: true, quota: 48000, successful: true, estimateBasis: "same_model_recent_success_average"},
		{name: "created_measured_usage_survives_zero_placeholder", lifecycle: "response.created", lifecycleUsage: `{"input_tokens":37,"output_tokens":0,"total_tokens":37}`, placeholder: "response.in_progress", terminal: "response.completed", delta: true, input: 37, quota: 37, reported: true, successful: true},
		{name: "in_progress_measured_input_survives_missing_failed_terminal", lifecycle: "response.in_progress", lifecycleUsage: `{"input_tokens":11,"output_tokens":0,"total_tokens":11}`, terminal: "response.failed", delta: true, input: 11, quota: 11, reported: true},
		{name: "in_progress_measured_output_preserves_zero_input", lifecycle: "response.in_progress", lifecycleUsage: `{"input_tokens":0,"output_tokens":11,"total_tokens":11}`, terminal: "response.failed", delta: true, output: 11, quota: 11, reported: true},
		{name: "terminal_zero_replaces_earlier_measured_usage", lifecycle: "response.created", lifecycleUsage: `{"input_tokens":37,"output_tokens":11,"total_tokens":48}`, terminal: "response.completed", usage: `,"usage":{}`, delta: true, reported: true, successful: true},
	}
	for _, deltaType := range []string{"output_text", "refusal", "function_call_arguments", "reasoning_text", "reasoning_summary_text"} {
		cases = append(cases, testCase{name: "missing_usage_" + deltaType + "_delta", terminal: "response.completed", delta: true, deltaType: deltaType, successful: true})
	}
	outputs := []struct{ name, fields string }{
		{"text", `,"output":[{"type":"message","content":[{"type":"output_text","text":"terminal text"}]}]`},
		{"refusal", `,"output":[{"type":"message","content":[{"type":"refusal","refusal":"terminal text"}]}]`},
		{"function_arguments", `,"output":[{"type":"function_call","arguments":"terminal text"}]`},
		{"reasoning", `,"output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"terminal "}],"summary":[{"type":"summary_text","text":"text"}]}]`},
	}
	for _, terminal := range []string{"response.completed", "response.done"} {
		for _, output := range outputs {
			cases = append(cases, testCase{name: terminal + "_terminal_only_" + output.name, terminal: terminal, terminalFields: output.fields, terminalText: "terminal text", history: true, successful: true})
		}
	}
	for _, terminal := range []string{"response.failed", "response.incomplete", "response.cancelled", "response.canceled", "response.error", "error"} {
		cases = append(cases,
			testCase{name: terminal + "_reported_zero", terminal: terminal, usage: `,"usage":{}`, delta: true, reported: true},
			testCase{name: terminal + "_snapshot_is_not_partial_output", terminal: terminal, terminalFields: outputs[0].fields, history: true, noLog: true},
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newResponsesWSUsageSQLFixture(t)
			if tc.history {
				require.NoError(t, fixture.db.Create(&appmodel.Log{Type: appmodel.LogTypeConsume, ModelName: fixture.model, Quota: 48000, PromptTokens: 20, CompletionTokens: 10, CreatedAt: time.Now().Unix()}).Error)
			}
			state, committed, commits := fixture.reserve(t)
			if tc.lifecycle != "" {
				fixture.roundTrip(t, fmt.Sprintf(`{"type":%q,"response":{"id":"response-test","usage":%s}}`, tc.lifecycle, tc.lifecycleUsage))
				require.True(t, state.info.ResponsesUsageReported, "measured lifecycle reports are authoritative")
			}
			if tc.placeholder != "" {
				fixture.roundTrip(t, fmt.Sprintf(`{"type":%q,"response":{"id":"response-test","usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`, tc.placeholder))
				require.Equal(t, tc.lifecycle != "", state.info.ResponsesUsageReported, "zero placeholders neither report nor replace measured usage")
			}
			if tc.delta {
				deltaType := tc.deltaType
				if deltaType == "" {
					deltaType = "output_text"
				}
				fixture.roundTrip(t, fmt.Sprintf(`{"type":%q,"delta":%q}`, "response."+deltaType+".delta", responsesWSUsageTestDelta))
			}
			if tc.itemFields != "" {
				fixture.roundTrip(t, fmt.Sprintf(`{"type":"response.output_item.done","output_index":0,"item":{%s}}`, tc.itemFields))
				price := operation_setting.GetToolPriceForModel(tc.tool, fixture.model)
				require.Positive(t, price)
				tc.quota = common.QuotaFromDecimal(decimal.NewFromFloat(price).Div(decimal.NewFromInt(1000)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
			}
			if !tc.reported && (tc.delta || tc.terminalText != "") {
				text := tc.terminalText
				if tc.delta {
					text = responsesWSUsageTestDelta
				}
				tc.input, tc.output = responsesWSUsageTestPromptEstimate, service.CountTextToken(text, fixture.model)
				tc.quota = tc.input + tc.output
				require.Positive(t, tc.output)
			}
			terminal := fmt.Sprintf(`{"type":%q,"response":{"id":"response-test"%s%s}}`, tc.terminal, tc.usage, tc.terminalFields)
			fixture.roundTrip(t, terminal)
			select {
			case successful := <-committed:
				require.Equal(t, tc.successful, successful)
			case <-time.After(time.Second):
				t.Fatal("terminal delivery did not finalize its successful-request reservation")
			}
			require.Nil(t, fixture.session.getCurrent())
			if tc.noLog {
				require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond, "failed empty response refunds prepayment")
			}
			requests := 0
			if tc.quota > 0 {
				requests = 1
			}
			fixture.assertBalances(t, tc.quota, tc.quota, requests)
			require.Equal(t, tc.reported, state.info.ResponsesUsageReported)
			var logs []appmodel.Log
			require.NoError(t, fixture.db.Where("user_id = ? AND type = ?", fixture.user.Id, appmodel.LogTypeConsume).Find(&logs).Error)
			logCount := 1
			if tc.noLog {
				logCount = 0
			}
			require.Len(t, logs, logCount)
			if !tc.noLog {
				require.Equal(t, tc.quota, logs[0].Quota)
				require.Equal(t, tc.input, logs[0].PromptTokens)
				require.Equal(t, tc.output, logs[0].CompletionTokens)
				var other map[string]any
				require.NoError(t, json.Unmarshal([]byte(logs[0].Other), &other))
				if tc.estimateBasis == "" {
					require.NotContains(t, other, "usage_estimated")
					require.NotContains(t, other, "usage_estimate_basis")
				} else {
					require.Equal(t, true, other["usage_estimated"])
					require.Equal(t, tc.estimateBasis, other["usage_estimate_basis"])
				}
			} else {
				require.Zero(t, state.outputText.Len(), "unsuccessful snapshots must not enter observed text")
			}
			// Replayed terminals still pass through to the client but cannot
			// debit another time, write another log, or consume another slot.
			fixture.roundTrip(t, terminal)
			require.Equal(t, int32(1), commits.Load())
			fixture.assertBalances(t, tc.quota, tc.quota, requests)
			var count int64
			require.NoError(t, fixture.db.Model(&appmodel.Log{}).Where("user_id = ? AND type = ?", fixture.user.Id, appmodel.LogTypeConsume).Count(&count).Error)
			require.Equal(t, int64(logCount), count)
			nextCommit, apiErr := middleware.CheckModelRequestRateLimit(fixture.c)
			if tc.successful {
				require.NotNil(t, apiErr)
				require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
			} else {
				require.Nil(t, apiErr, "billable failed output releases the success slot")
				nextCommit(false)
			}
		})
	}
}

func TestResponsesWSMissingUsageBoundsLocalPromptEstimate(t *testing.T) {
	for _, tc := range []struct {
		name                string
		estimate, wantInput int
		reported            bool
	}{
		{name: "negative", estimate: -17},
		{name: "normal", estimate: 50000, wantInput: 50000},
		{name: "above_cap", estimate: 2000000, wantInput: openai.MaxUnverifiedResponsesInputTokens},
		{name: "maximum_int", estimate: int(^uint(0) >> 1), wantInput: openai.MaxUnverifiedResponsesInputTokens},
		{name: "reported_not_capped", estimate: 2000000, wantInput: 2000000, reported: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "ws-local-prompt-cap"}, ResponsesUsageReported: tc.reported}
			info.SetEstimatePromptTokens(tc.estimate)
			state := &responsesWSCallState{info: info, usage: &dto.Usage{}, outputText: newResponsesWSOutputTextBuffer(c)}
			state.outputText.WriteString(c, responsesWSUsageTestDelta)
			if tc.reported {
				state.usage.PromptTokens, state.usage.TotalTokens = tc.wantInput, tc.wantInput
			}
			finalizeResponsesWSUsage(state)
			require.Equal(t, tc.wantInput, state.usage.PromptTokens)
			require.Equal(t, tc.estimate, info.GetEstimatePromptTokens(), "clamping must not change the original estimate")
			if tc.reported {
				require.Zero(t, state.usage.CompletionTokens, "reported zero output is authoritative")
			} else {
				require.Positive(t, state.usage.CompletionTokens)
			}
			require.Equal(t, state.usage.PromptTokens+state.usage.CompletionTokens, state.usage.TotalTokens)
		})
	}
}

func TestResponsesWSPrepareCallDoesNotCarryReportedUsageIntoAnotherAttempt(t *testing.T) {
	fixture := newResponsesWSUsageSQLFixture(t)
	previousRatios := ratio_setting.ModelRatio2JSONString()
	ratios := ratio_setting.GetModelRatioCopy()
	ratios[fixture.model] = 0
	raw, err := common.Marshal(ratios)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(raw)))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios)) })
	var previous *responsesWSCallState
	for attempt := 0; attempt < 3; attempt++ {
		state, _, apiErr := fixture.session.prepareCall(responsesWSCreateRequest{Request: dto.OpenAIResponsesRequest{Model: fixture.model, Input: common.RawMessage(`"prompt"`)}}, func(bool) {})
		require.Nil(t, apiErr)
		require.False(t, state.info.ResponsesUsageReported)
		require.Zero(t, state.usage.TotalTokens)
		if previous != nil {
			require.NotSame(t, previous.info, state.info)
			require.NotSame(t, previous.usage, state.usage)
		}
		state.info.ResponsesUsageReported = true
		state.usage.TotalTokens = 99
		state.refund(fixture.c)
		previous = state
	}
}
