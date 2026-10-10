package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemOneChannelTestEndpointAndRequest(t *testing.T) {
	mapping := `{"customer-decision":"intermediate","intermediate":"jev-preview"}`
	for _, tt := range []struct {
		name, modelName, endpoint string
		channel                   *model.Channel
	}{
		{name: "vendor", modelName: "jev-latest", channel: &model.Channel{Type: constant.ChannelTypeTypeSafe}},
		{name: "vendor alias", modelName: "vendor-alias", channel: &model.Channel{Type: constant.ChannelTypeTypeSafe}},
		{name: "gateway", modelName: "jev-1.13.0", channel: &model.Channel{Type: constant.ChannelTypeNewAPI}},
		{name: "gateway alias", modelName: "customer-decision", channel: &model.Channel{Type: constant.ChannelTypeNewAPI, ModelMapping: &mapping}},
		{name: "explicit", modelName: "jev-preview", endpoint: string(constant.EndpointTypeSystemOne)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := resolveChannelTestRequestPath(tt.channel, tt.modelName, tt.endpoint)
			require.Equal(t, "/typesafe/v1/systemone", path)
			require.Equal(t, types.RelayFormat(types.RelayFormatSystemOne), channelTestRelayFormat(tt.endpoint, path))
			request, ok := buildTestRequest(tt.modelName, tt.endpoint, tt.channel, false).(*dto.SystemOneRequest)
			require.True(t, ok)
			require.NoError(t, request.Validate())
			require.JSONEq(t, `{"message":"hello world"}`, string(request.State))
			require.Equal(t, "noul", request.Questions["greeting"].Type)
			assert.Equal(t, tt.modelName, request.Model)
		})
	}
	request := buildTestRequest("jev-latest", "", &model.Channel{Type: constant.ChannelTypeTypeSafe}, true).(*dto.SystemOneRequest)
	require.ErrorContains(t, request.Validate(), "does not support streaming")
}

func TestSystemOneChannelTestHTTP(t *testing.T) {
	for _, tt := range []struct {
		name, models, modelMapping, endpoint, usage string
		paramOverride, wantQuestionType             string
		tieredExpression                            string
		channelType                                 int
		stream                                      bool
		answer                                      string
		wantError                                   string
		wantPath, wantModel                         string
		wantTokens, wantQuota                       int
	}{
		{name: "vendor default", channelType: constant.ChannelTypeTypeSafe, usage: `{"input_tokens":1000,"output_tokens":99}`, answer: `{"type":"noul","value":true}`, wantPath: "/v1/systemone", wantModel: "jev-latest", wantTokens: 1000, wantQuota: 21},
		{name: "vendor mapped alias", channelType: constant.ChannelTypeTypeSafe, models: "customer-decision", modelMapping: `{"customer-decision":"jev-preview"}`, usage: `{"input_tokens":1000}`, answer: `{"type":"noul"}`, wantPath: "/v1/systemone", wantModel: "jev-preview", wantTokens: 1000, wantQuota: 21},
		{name: "gateway mapped alias", channelType: constant.ChannelTypeNewAPI, models: "customer-decision", modelMapping: `{"customer-decision":"intermediate","intermediate":"jev-1.13.0"}`, usage: `{"input_tokens":1000}`, answer: `{"type":"noul"}`, wantPath: "/typesafe/v1/systemone", wantModel: "jev-1.13.0", wantTokens: 1000, wantQuota: 21},
		{name: "authoritative zero", channelType: constant.ChannelTypeTypeSafe, models: "jev-latest", usage: `{"input_tokens":0}`, answer: `{"type":"noul"}`, wantPath: "/v1/systemone", wantModel: "jev-latest"},
		{name: "missing usage reserves context", channelType: constant.ChannelTypeTypeSafe, models: "jev-latest", usage: `{}`, answer: `{"type":"noul"}`, wantPath: "/v1/systemone", wantModel: "jev-latest", wantQuota: 1376},
		{name: "wrong answer type", channelType: constant.ChannelTypeTypeSafe, models: "jev-latest", usage: `{"input_tokens":1000}`, answer: `{"type":"choice"}`, wantError: "matching answer", wantPath: "/v1/systemone", wantModel: "jev-latest"},
		{name: "stream rejected", channelType: constant.ChannelTypeTypeSafe, models: "jev-latest", stream: true, wantError: "does not support streaming"},
		{name: "chat endpoint rejected", channelType: constant.ChannelTypeTypeSafe, models: "jev-latest", endpoint: string(constant.EndpointTypeOpenAI), wantError: "require the System One endpoint"},
		{name: "override stream rejected", channelType: constant.ChannelTypeTypeSafe, models: "jev-latest", paramOverride: `{"stream":true}`, wantError: "does not support streaming"},
		{name: "override model rejected", channelType: constant.ChannelTypeTypeSafe, models: "jev-latest", paramOverride: `{"model":"jev-preview"}`, wantError: "model overrides must use channel model mapping"},
		{name: "override credentials stripped", channelType: constant.ChannelTypeTypeSafe, models: "jev-latest", paramOverride: `{"api_key":"secret-body-key","authorization":"secret-body-auth","key":"secret-body-key","stream":false}`, usage: `{"input_tokens":1000}`, answer: `{"type":"noul"}`, wantPath: "/v1/systemone", wantModel: "jev-latest", wantTokens: 1000, wantQuota: 21},
		{name: "override questions validated", channelType: constant.ChannelTypeTypeSafe, models: "jev-latest", paramOverride: `{"questions":{"greeting":{"type":"score","criteria":["no greeting","contains greeting"]}}}`, usage: `{"input_tokens":1000}`, answer: `{"type":"score"}`, wantQuestionType: "score", wantPath: "/v1/systemone", wantModel: "jev-latest", wantTokens: 1000, wantQuota: 21},
		{name: "tiered billing preserves original alias and questions", channelType: constant.ChannelTypeTypeSafe, models: "customer-decision", modelMapping: `{"customer-decision":"jev-preview"}`, paramOverride: `{"questions":{"greeting":{"type":"score","criteria":["no greeting","contains greeting"]}}}`, tieredExpression: `param("model") == "customer-decision" && param("questions.greeting.type") == "noul" ? tier("original", p * 2) : tier("mutated", p * 8)`, usage: `{"input_tokens":1000}`, answer: `{"type":"score"}`, wantQuestionType: "score", wantPath: "/v1/systemone", wantModel: "jev-preview", wantTokens: 1000, wantQuota: 1000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := setupModelListControllerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.Log{}))
			previousMemoryCache, previousLogging := common.MemoryCacheEnabled, common.LogConsumeEnabled
			common.MemoryCacheEnabled, common.LogConsumeEnabled = false, true
			previousModels := ratio_setting.ModelRatio2JSONString()
			previousCompletions := ratio_setting.CompletionRatio2JSONString()
			previousPrices := ratio_setting.ModelPrice2JSONString()
			t.Cleanup(func() {
				common.MemoryCacheEnabled, common.LogConsumeEnabled = previousMemoryCache, previousLogging
				require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModels))
				require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(previousCompletions))
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices))
			})
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"jev-latest":0.021,"customer-decision":0.021}`))
			require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"jev-latest":0,"customer-decision":0}`))
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
			if tt.tieredExpression != "" {
				saved := map[string]string{}
				require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
					saved[key] = value
					return nil
				}))
				t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(saved)) })
				expressions, err := json.Marshal(map[string]string{tt.models: tt.tieredExpression})
				require.NoError(t, err)
				modes, err := json.Marshal(map[string]string{tt.models: "tiered_expr"})
				require.NoError(t, err)
				require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
					"billing_setting.billing_mode":    string(modes),
					"billing_setting.billing_expr":    string(expressions),
					"group_ratio_setting.group_ratio": `{"default":1}`,
				}))
			}
			user := model.User{Username: "systemone-probe", Group: "default", Status: common.UserStatusEnabled, Quota: 1000000}
			require.NoError(t, db.Create(&user).Error)
			var called atomic.Int32
			var upstreamPath, authorization string
			var upstreamRequest map[string]json.RawMessage
			var decodeErr error
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called.Add(1)
				upstreamPath = r.URL.Path
				authorization = r.Header.Get("Authorization")
				decodeErr = json.NewDecoder(r.Body).Decode(&upstreamRequest)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, fmt.Sprintf(`{"model":%q,"answers":{"greeting":%s},"usage":%s}`, tt.wantModel, tt.answer, tt.usage))
			}))
			defer server.Close()
			service.InitHttpClient()
			baseURL := server.URL + "/v1/"
			channel := model.Channel{Type: tt.channelType, Name: "systemone probe", Key: "test-key", Models: tt.models, BaseURL: &baseURL, Status: common.ChannelStatusEnabled}
			if tt.modelMapping != "" {
				channel.ModelMapping = &tt.modelMapping
			}
			if tt.paramOverride != "" {
				channel.ParamOverride = &tt.paramOverride
			}
			require.NoError(t, db.Create(&channel).Error)
			result := testChannel(context.Background(), &channel, user.Id, "", tt.endpoint, tt.stream)
			if tt.wantError != "" {
				require.ErrorContains(t, result.localErr, tt.wantError)
				var count int64
				require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
				require.Zero(t, count)
				if tt.wantPath == "" {
					require.Zero(t, called.Load())
					return
				}
			} else {
				require.NoError(t, result.localErr)
				var log model.Log
				require.NoError(t, db.First(&log).Error)
				assert.Equal(t, tt.wantTokens, log.PromptTokens)
				assert.Zero(t, log.CompletionTokens)
				assert.Equal(t, tt.wantQuota, log.Quota)
				assert.False(t, log.IsStream)
				var other map[string]any
				require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
				if tt.tieredExpression != "" {
					assert.Equal(t, "original", other["matched_tier"])
				}
				assert.Equal(t, true, other["output_tokens_free"])
				if tt.usage == `{}` {
					assert.Equal(t, "invalid", other["systemone_usage_status"])
					assert.Equal(t, true, other["usage_estimated"])
					assert.EqualValues(t, dto.SystemOneMaxInputTokens, other["usage_estimate_token_budget"])
				} else {
					assert.Equal(t, "reported", other["systemone_usage_status"])
				}
			}
			require.EqualValues(t, 1, called.Load())
			require.NoError(t, decodeErr)
			assert.Equal(t, tt.wantPath, upstreamPath)
			assert.Equal(t, "Bearer test-key", authorization)
			require.JSONEq(t, fmt.Sprintf("%q", tt.wantModel), string(upstreamRequest["model"]))
			require.JSONEq(t, `{"message":"hello world"}`, string(upstreamRequest["state"]))
			assert.Contains(t, upstreamRequest, "questions")
			var questions map[string]dto.SystemOneQuestion
			require.NoError(t, json.Unmarshal(upstreamRequest["questions"], &questions))
			wantQuestionType := tt.wantQuestionType
			if wantQuestionType == "" {
				wantQuestionType = "noul"
			}
			assert.Equal(t, wantQuestionType, questions["greeting"].Type)
			assert.NotContains(t, upstreamRequest, "messages")
			assert.NotContains(t, upstreamRequest, "stream")
			assert.Len(t, upstreamRequest, 3, "native request contains only model, state, and questions")
			var stored model.User
			require.NoError(t, db.First(&stored, user.Id).Error)
			assert.Equal(t, user.Quota, stored.Quota, "admin channel tests record the price without consuming user quota")
		})
	}
}

func TestSystemOneChannelTestQuotaPreservesAuthoritativeZero(t *testing.T) {
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeSystemOne, SystemOneUsageStatus: "reported"}
	quota, _ := settleTestQuota(info, hosttypes.PriceData{ModelRatio: 0.021}, &dto.Usage{})
	require.Zero(t, quota)
	info.SystemOneUsageStatus = "invalid"
	quota, _ = settleTestQuota(info, hosttypes.PriceData{ModelRatio: 0.021, QuotaToPreConsume: 1376}, &dto.Usage{})
	require.Equal(t, 1376, quota)
}
