package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const systemOneAcceptanceRequest = `{"model":"jev-latest","state":{"text":"hello"},"questions":{"safe":{"type":"noul","instructions":"Is this safe?"}}}`

type systemOneRelayFixture struct {
	db      *gorm.DB
	user    model.User
	token   model.Token
	channel model.Channel
	calls   atomic.Int32
	request json.RawMessage
}

// Real authentication, channel selection and billing, with only an isolated
// SQLite fixture and a loopback provider. No external provider is contacted.
func newSystemOneRelayFixture(t *testing.T, status int, upstreamBody string) *systemOneRelayFixture {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousMemory := common.RedisEnabled, common.MemoryCacheEnabled
	previousLogs, previousRetry := common.LogConsumeEnabled, common.RetryTimes
	previousRatios := ratio_setting.ModelRatio2JSONString()
	previousPrices := ratio_setting.ModelPrice2JSONString()
	previousCompletions := ratio_setting.CompletionRatio2JSONString()
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousMemory
		common.LogConsumeEnabled, common.RetryTimes = previousLogs, previousRetry
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(previousCompletions))
		model.InvalidatePricingCache()
	})
	initModelListColumnNames(t)
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.LogConsumeEnabled, common.RetryTimes = true, 0
	require.NoError(t, i18n.Init())
	db, user := createAssistantKeyFixture(t, "systemone-acceptance")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}, &model.PublicRelayPreference{}, &model.SubscriptionPreConsumeRecord{}))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"jev-latest":0.021,"jev-1.13.0":0.021,"my-jev-alias":0.5}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"jev-latest":0,"jev-1.13.0":0,"my-jev-alias":0}`))
	levelOne := model.TrustLevelMinUser + 1
	user.TrustLevelOverride = &levelOne
	user.Quota = common.GetTrustQuota()
	user.Setting = `{"billing_preference":"wallet_only"}`
	require.NoError(t, db.Save(&user).Error)
	fixture := &systemOneRelayFixture{db: db, user: user}
	fixture.token = model.Token{
		UserId: user.Id, Key: strings.Repeat("s", 48), Name: "systemone acceptance", Group: "default",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: common.GetTrustQuota(),
	}
	require.NoError(t, db.Create(&fixture.token).Error)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.calls.Add(1)
		upstreamPath := "/v1/systemone"
		if fixture.channel.Type == constant.ChannelTypeNewAPI {
			upstreamPath = "/typesafe/v1/systemone"
		}
		assert.Equal(t, upstreamPath, r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "Bearer mock-typesafe-provider-key", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); assert.NoError(t, err) {
			fixture.request = append(json.RawMessage(nil), raw...)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(upstreamBody))
	}))
	t.Cleanup(upstream.Close)
	fixture.channel = model.Channel{
		Type: constant.ChannelTypeTypeSafe, Status: common.ChannelStatusEnabled, Name: "mock typesafe",
		Key: "mock-typesafe-provider-key", BaseURL: &upstream.URL, Models: "jev-latest", Group: "default",
	}
	require.NoError(t, db.Create(&fixture.channel).Error)
	require.NoError(t, fixture.channel.AddAbilities(nil))
	model.InvalidatePricingCache()
	t.Cleanup(func() {
		require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond,
			"join billing background workers before restoring shared fixture globals")
	})
	return fixture
}

func (fixture *systemOneRelayFixture) relayRequest(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	fixture.serveRelayRequest(t, path, body, response)
	return response
}

func (fixture *systemOneRelayFixture) serveRelayRequest(t *testing.T, path, body string, response http.ResponseWriter) {
	t.Helper()
	engine := gin.New()
	engine.Use(middleware.BodyStorageCleanup())
	var format types.RelayFormat = types.RelayFormatSystemOne
	if path == "/v1/chat/completions" {
		format = types.RelayFormatOpenAI
	}
	engine.POST(path, middleware.TokenAuth(), middleware.RelayRequestAdmission(), middleware.Distribute(), func(c *gin.Context) { Relay(c, format) })
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+fixture.token.Key)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(response, request)
}

type systemOneBrokenResponseWriter struct {
	*httptest.ResponseRecorder
	writes int
}

func (w *systemOneBrokenResponseWriter) Write(_ []byte) (int, error) {
	w.writes++
	return 0, io.ErrClosedPipe
}

func (fixture *systemOneRelayFixture) verifyCharge(t *testing.T, expected int) model.Log {
	t.Helper()
	var user model.User
	var token model.Token
	require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
	require.NoError(t, fixture.db.First(&token, fixture.token.Id).Error)
	assert.Equal(t, expected, fixture.user.Quota-user.Quota, "wallet debit")
	assert.Equal(t, expected, fixture.token.RemainQuota-token.RemainQuota, "API key debit")
	assert.Equal(t, expected, user.UsedQuota)
	assert.Equal(t, expected, token.UsedQuota)
	var logs []model.Log
	require.NoError(t, fixture.db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, expected, logs[0].Quota)
	assert.Equal(t, fixture.channel.Id, logs[0].ChannelId)
	assert.Equal(t, fixture.token.Id, logs[0].TokenId)
	assert.Equal(t, "default", logs[0].Group)
	assert.NotContains(t, logs[0].Other, fixture.token.Key)
	assert.NotContains(t, logs[0].Other, fixture.channel.Key)
	return logs[0]
}

func TestSystemOneRelaySettlesAuthoritativeInputUsage(t *testing.T) {
	for _, tc := range []struct {
		name, path, usage         string
		input, quota, channelType int
	}{
		{"native", "/v1/systemone", `{"input_tokens":100,"output_tokens":900000}`, 100, 2, constant.ChannelTypeTypeSafe},
		{"upstream_plugin_alias", "/typesafe/v1/systemone", `{"input_tokens":100,"output_tokens":10}`, 100, 2, constant.ChannelTypeTypeSafe},
		{"chained_new_api", "/v1/systemone", `{"input_tokens":100,"output_tokens":10}`, 100, 2, constant.ChannelTypeNewAPI},
		{"authoritative_zero", "/v1/systemone", `{"input_tokens":0,"output_tokens":100}`, 0, 0, constant.ChannelTypeTypeSafe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := `{"model":"jev-1.13.0","answers":{"safe":{"type":"noul","noul":0.9}},"usage":` + tc.usage + `}`
			fixture := newSystemOneRelayFixture(t, http.StatusOK, payload)
			fixture.channel.Type = tc.channelType
			require.NoError(t, fixture.db.Model(&fixture.channel).Update("type", tc.channelType).Error)
			response := fixture.relayRequest(t, tc.path, systemOneAcceptanceRequest)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.JSONEq(t, payload, response.Body.String(), "preserve native answers and provider usage")
			assert.EqualValues(t, 1, fixture.calls.Load())
			assert.JSONEq(t, systemOneAcceptanceRequest, string(fixture.request))
			log := fixture.verifyCharge(t, tc.quota)
			assert.Equal(t, tc.input, log.PromptTokens)
			assert.Zero(t, log.CompletionTokens, "TypeSafe output tokens do not affect billing")
			assert.Equal(t, "jev-latest", log.ModelName)
			var other map[string]any
			require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
			assert.Equal(t, "reported", other["systemone_usage_status"])
			assert.Equal(t, true, other["output_tokens_free"])
		})
	}
}

func TestSystemOneRelayUnknownUsageRetainsBoundedReserve(t *testing.T) {
	for _, tc := range []struct{ name, usage string }{
		{"missing", ""},
		{"null", `,"usage":null`},
		{"missing_input", `,"usage":{"output_tokens":10}`},
		{"negative", `,"usage":{"input_tokens":-1}`},
		{"fractional", `,"usage":{"input_tokens":1.5}`},
		{"string", `,"usage":{"input_tokens":"100"}`},
		{"above_budget", `,"usage":{"input_tokens":65537}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSystemOneRelayFixture(t, http.StatusOK, `{"model":"jev-1.13.0","answers":{"safe":{"type":"noul","noul":0.9}}`+tc.usage+`}`)
			// Generic text billing can estimate missing usage from this cheap
			// historical request. Jev must retain its own frozen reservation.
			require.NoError(t, fixture.db.Create(&model.Log{
				UserId: fixture.user.Id + 100, Type: model.LogTypeConsume, ModelName: "jev-latest",
				Quota: 7, PromptTokens: 333, CreatedAt: time.Now().Unix(), RequestId: "history-" + tc.name,
				Other: `{"acquisition_success_v1":true}`,
			}).Error)
			response := fixture.relayRequest(t, "/v1/systemone", systemOneAcceptanceRequest)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.EqualValues(t, 1, fixture.calls.Load())
			// The protocol budget, rather than guessed local chat tokens, caps
			// the reservation: round(65,536 * 0.021) = 1,376 quota units.
			log := fixture.verifyCharge(t, 1376)
			assert.Zero(t, log.PromptTokens, "the reserved context budget is not measured upstream input")
			assert.Zero(t, log.CompletionTokens)
			var other map[string]any
			require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
			assert.Equal(t, true, other["usage_estimated"])
			assert.Equal(t, "invalid", other["systemone_usage_status"])
			assert.Equal(t, "typesafe_context_reservation", other["usage_estimate_basis"])
			assert.Equal(t, float64(65536), other["usage_estimate_token_budget"])
		})
	}
}

func TestSystemOneRelayMappedAliasUsesOriginalPricingAndLogs(t *testing.T) {
	fixture := newSystemOneRelayFixture(t, http.StatusOK, `{"model":"jev-1.13.0","answers":{"safe":{"type":"noul","noul":0.9}},"usage":{"input_tokens":100,"output_tokens":100}}`)
	mapping := `{"my-jev-alias":"jev-1.13.0"}`
	require.NoError(t, fixture.db.Model(&fixture.channel).Updates(map[string]any{"models": "my-jev-alias", "model_mapping": mapping}).Error)
	require.NoError(t, fixture.db.Model(&model.Ability{}).Where("channel_id = ?", fixture.channel.Id).Update("model", "my-jev-alias").Error)
	model.InvalidatePricingCache()
	request := strings.Replace(systemOneAcceptanceRequest, "jev-latest", "my-jev-alias", 1)
	response := fixture.relayRequest(t, "/v1/systemone", request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, strings.Replace(request, "my-jev-alias", "jev-1.13.0", 1), string(fixture.request))
	log := fixture.verifyCharge(t, 50)
	assert.Equal(t, "my-jev-alias", log.ModelName)
	assert.Equal(t, 100, log.PromptTokens)
	var other map[string]any
	require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
	assert.Equal(t, true, other["is_model_mapped"])
	assert.Equal(t, "jev-1.13.0", other["upstream_model_name"])
	assert.Equal(t, float64(0.5), other["model_ratio"])
}

func TestSystemOneRelayFailureRefundsWalletAndKey(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		status        int
	}{
		{"provider_error", `{"error":{"message":"fixture rejected","type":"invalid_request_error","code":"fixture_rejected"}}`, http.StatusBadRequest},
		{"malformed_success", `{"model":"jev-1.13.0","answers":{}}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSystemOneRelayFixture(t, tc.status, tc.payload)
			response := fixture.relayRequest(t, "/v1/systemone", systemOneAcceptanceRequest)
			require.GreaterOrEqual(t, response.Code, http.StatusBadRequest, response.Body.String())
			assert.EqualValues(t, 1, fixture.calls.Load())
			require.Eventually(t, func() bool {
				var user model.User
				var token model.Token
				return fixture.db.First(&user, fixture.user.Id).Error == nil && fixture.db.First(&token, fixture.token.Id).Error == nil &&
					user.Quota == fixture.user.Quota && token.RemainQuota == fixture.token.RemainQuota
			}, 3*time.Second, 10*time.Millisecond, "provider failure refunds both funding sources")
			var count int64
			require.NoError(t, fixture.db.Model(&model.Log{}).Where("user_id = ? AND type = ?", fixture.user.Id, model.LogTypeConsume).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestSystemOneRelayDownstreamWriteFailureDoesNotReplayProvider(t *testing.T) {
	fixture := newSystemOneRelayFixture(t, http.StatusOK, `{"model":"jev-1.13.0","answers":{"safe":{"type":"noul","noul":0.9}},"usage":{"input_tokens":100,"output_tokens":99}}`)
	common.RetryTimes = 2
	writer := &systemOneBrokenResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	fixture.serveRelayRequest(t, "/v1/systemone", systemOneAcceptanceRequest, writer)
	assert.Positive(t, writer.writes, "simulate the client disconnect when the accepted result body is written")
	assert.EqualValues(t, 1, fixture.calls.Load(), "a downstream disconnect must not replay a completed paid provider call")
	log := fixture.verifyCharge(t, 2)
	assert.Equal(t, 100, log.PromptTokens)
	assert.Zero(t, log.CompletionTokens)
}

func TestSystemOneMappedAliasCannotBypassKeyModelLimits(t *testing.T) {
	fixture := newSystemOneRelayFixture(t, http.StatusOK, `{"model":"jev-1.13.0","answers":{"safe":{"type":"noul","noul":0.9}},"usage":{"input_tokens":100}}`)
	require.NoError(t, fixture.db.Model(&fixture.channel).Updates(map[string]any{
		"models": "my-jev-alias", "model_mapping": `{"my-jev-alias":"jev-1.13.0"}`,
	}).Error)
	require.NoError(t, fixture.db.Model(&model.Ability{}).Where("channel_id = ?", fixture.channel.Id).Update("model", "my-jev-alias").Error)
	require.NoError(t, fixture.db.Model(&fixture.token).Updates(map[string]any{
		"model_limits_enabled": true, "model_limits": "jev-1.13.0",
	}).Error)
	model.InvalidatePricingCache()
	request := strings.Replace(systemOneAcceptanceRequest, "jev-latest", "my-jev-alias", 1)
	response := fixture.relayRequest(t, "/v1/systemone", request)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	assert.Zero(t, fixture.calls.Load(), "permission applies to the requested public model before channel mapping")
	var user model.User
	var token model.Token
	require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
	require.NoError(t, fixture.db.First(&token, fixture.token.Id).Error)
	assert.Equal(t, fixture.user.Quota, user.Quota)
	assert.Equal(t, fixture.token.RemainQuota, token.RemainQuota)
}

func TestSystemOneRelayRejectsBeforeUpstreamOrBilling(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		channelType      int
		mapping          string
		restrictKey      bool
	}{
		{"missing_state", "/v1/systemone", `{"model":"jev-latest","questions":{"safe":{"type":"noul"}}}`, constant.ChannelTypeTypeSafe, "", false},
		{"streaming", "/v1/systemone", strings.TrimSuffix(systemOneAcceptanceRequest, "}") + `,"stream":true}`, constant.ChannelTypeTypeSafe, "", false},
		{"unsupported_question", "/v1/systemone", strings.Replace(systemOneAcceptanceRequest, `"type":"noul"`, `"type":"chat"`, 1), constant.ChannelTypeTypeSafe, "", false},
		{"chat_route", "/v1/chat/completions", `{"model":"jev-latest","messages":[{"role":"user","content":"hello"}]}`, constant.ChannelTypeTypeSafe, "", false},
		{"openai_channel", "/v1/systemone", systemOneAcceptanceRequest, constant.ChannelTypeOpenAI, "", false},
		{"bad_mapped_model", "/v1/systemone", systemOneAcceptanceRequest, constant.ChannelTypeTypeSafe, `{"jev-latest":"gpt-4o"}`, false},
		{"token_model_limit", "/v1/systemone", systemOneAcceptanceRequest, constant.ChannelTypeTypeSafe, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSystemOneRelayFixture(t, http.StatusOK, `{"model":"jev-1.13.0","answers":{"safe":{"type":"noul","noul":0.9}},"usage":{"input_tokens":100}}`)
			require.NoError(t, fixture.db.Model(&fixture.channel).Update("type", tc.channelType).Error)
			if tc.mapping != "" {
				require.NoError(t, fixture.db.Model(&fixture.channel).Update("model_mapping", tc.mapping).Error)
			}
			if tc.restrictKey {
				require.NoError(t, fixture.db.Model(&fixture.token).Updates(map[string]any{"model_limits_enabled": true, "model_limits": "other-model"}).Error)
			}
			response := fixture.relayRequest(t, tc.path, tc.body)
			require.GreaterOrEqual(t, response.Code, http.StatusBadRequest, response.Body.String())
			assert.Zero(t, fixture.calls.Load())
			require.Eventually(t, func() bool {
				var user model.User
				var token model.Token
				return fixture.db.First(&user, fixture.user.Id).Error == nil && fixture.db.First(&token, fixture.token.Id).Error == nil &&
					user.Quota == fixture.user.Quota && token.RemainQuota == fixture.token.RemainQuota
			}, 3*time.Second, 10*time.Millisecond)
		})
	}
}
