package controller

import (
	"context"
	"encoding/json"
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
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const channelTestModerationResponse = `{"id":"mod-test","model":"omni-moderation-latest","results":[{"flagged":false,"categories":{"harassment":false,"new-category":false},"category_scores":{"harassment":0.01,"new-category":0.02},"category_applied_input_types":{"harassment":["text"]}}],"provider_extension":{"preserve":true}}`

func TestModerationChannelTestBuildsNativeInput(t *testing.T) {
	mapping := `{"moderation-alias":"intermediate","intermediate":"omni-moderation-latest"}`
	for _, tt := range []struct {
		modelName, endpoint string
		channel             *model.Channel
	}{
		{modelName: "omni-moderation-latest", channel: &model.Channel{Type: constant.ChannelTypeOpenAI}},
		{modelName: "omni-moderation-2024-09-26", channel: &model.Channel{Type: constant.ChannelTypeNewAPI}},
		{modelName: "text-moderation-stable", channel: &model.Channel{Type: constant.ChannelTypeOpenAI}},
		{modelName: "moderation-alias", channel: &model.Channel{Type: constant.ChannelTypeOpenAI, ModelMapping: &mapping}},
		{modelName: "moderation-alias", endpoint: string(constant.EndpointTypeModeration)},
	} {
		path := resolveChannelTestRequestPath(tt.channel, tt.modelName, tt.endpoint)
		require.Equal(t, "/v1/moderations", path)
		request, ok := buildTestRequest(tt.modelName, tt.endpoint, tt.channel, false).(*dto.GeneralOpenAIRequest)
		require.True(t, ok)
		assert.Equal(t, tt.modelName, request.Model)
		assert.Equal(t, "hello world", request.Input)
		assert.Empty(t, request.Messages)
		assert.Nil(t, request.Stream)
		assert.Nil(t, request.MaxTokens)
		assert.Nil(t, request.MaxCompletionTokens)
	}
}

func TestModerationChannelTestHTTP(t *testing.T) {
	for _, tt := range []struct {
		name, modelName, mapping, endpoint, override string
		channelType                                  int
		stream, forceFormat                          bool
		body, wantError                              string
	}{
		{name: "omni automatic", channelType: constant.ChannelTypeOpenAI, modelName: "omni-moderation-latest", body: channelTestModerationResponse},
		{name: "omni snapshot automatic", channelType: constant.ChannelTypeOpenAI, modelName: "omni-moderation-2024-09-26", body: channelTestModerationResponse},
		{name: "text native endpoint", channelType: constant.ChannelTypeOpenAI, modelName: "text-moderation-stable", endpoint: string(constant.EndpointTypeModeration), body: channelTestModerationResponse},
		{name: "openhuman force format", channelType: constant.ChannelTypeOpenHuman, modelName: "omni-moderation-latest", forceFormat: true, body: channelTestModerationResponse},
		{name: "gateway mapped alias", channelType: constant.ChannelTypeNewAPI, modelName: "moderation-alias", mapping: `{"moderation-alias":"intermediate","intermediate":"omni-moderation-latest"}`, body: channelTestModerationResponse},
		{name: "chat payload rejected", channelType: constant.ChannelTypeOpenAI, modelName: "omni-moderation-latest", body: `{"id":"chat-test","model":"omni-moderation-latest","choices":[]}`, wantError: "results"},
		{name: "empty results rejected", channelType: constant.ChannelTypeOpenAI, modelName: "omni-moderation-latest", body: `{"id":"mod-test","model":"omni-moderation-latest","results":[]}`, wantError: "results"},
		{name: "invalid result rejected", channelType: constant.ChannelTypeOpenAI, modelName: "omni-moderation-latest", body: `{"id":"mod-test","model":"omni-moderation-latest","results":[{"flagged":"false","categories":{},"category_scores":{}}]}`, wantError: "invalid moderation response JSON"},
		{name: "stream rejected", channelType: constant.ChannelTypeOpenAI, modelName: "omni-moderation-latest", stream: true, wantError: "does not support streaming"},
		{name: "override stream rejected", channelType: constant.ChannelTypeOpenAI, modelName: "omni-moderation-latest", override: `{"stream":true}`, wantError: "does not support streaming"},
		{name: "chat endpoint rejected", channelType: constant.ChannelTypeOpenAI, modelName: "omni-moderation-latest", endpoint: string(constant.EndpointTypeOpenAI), wantError: "require the Moderation endpoint"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := setupModelListControllerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.Log{}))
			previousMemoryCache, previousLogging := common.MemoryCacheEnabled, common.LogConsumeEnabled
			previousRatios, previousPrices := ratio_setting.ModelRatio2JSONString(), ratio_setting.ModelPrice2JSONString()
			common.MemoryCacheEnabled, common.LogConsumeEnabled = false, true
			t.Cleanup(func() {
				common.MemoryCacheEnabled, common.LogConsumeEnabled = previousMemoryCache, previousLogging
				require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices))
			})
			// A nonzero token rate must not fabricate token use or a minimum charge.
			ratios, err := json.Marshal(map[string]float64{tt.modelName: 1})
			require.NoError(t, err)
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(ratios)))
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
			user := model.User{Username: "moderation-probe", Group: "default", Status: common.UserStatusEnabled, Quota: 1000000}
			require.NoError(t, db.Create(&user).Error)
			var called atomic.Int32
			var path string
			var request map[string]json.RawMessage
			var decodeErr error
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called.Add(1)
				path = r.URL.Path
				decodeErr = json.NewDecoder(r.Body).Decode(&request)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			service.InitHttpClient()
			channel := model.Channel{Type: tt.channelType, Name: "moderation probe", Key: "test-key", Models: tt.modelName, BaseURL: &server.URL, Status: common.ChannelStatusEnabled}
			if tt.mapping != "" {
				channel.ModelMapping = &tt.mapping
			}
			if tt.override != "" {
				channel.ParamOverride = &tt.override
			}
			if tt.forceFormat {
				setting := `{"force_format":true}`
				channel.Setting = &setting
			}
			require.NoError(t, db.Create(&channel).Error)
			result := testChannel(context.Background(), &channel, user.Id, "", tt.endpoint, tt.stream)
			if tt.wantError != "" {
				require.ErrorContains(t, result.localErr, tt.wantError)
				var count int64
				require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
				assert.Zero(t, count)
				if tt.body == "" {
					require.Zero(t, called.Load())
					return
				}
			} else {
				require.NoError(t, result.localErr)
				var log model.Log
				require.NoError(t, db.First(&log).Error)
				assert.Equal(t, tt.modelName, log.ModelName)
				assert.Zero(t, log.PromptTokens)
				assert.Zero(t, log.CompletionTokens)
				assert.Zero(t, log.Quota)
				assert.False(t, log.IsStream)
			}
			require.EqualValues(t, 1, called.Load())
			require.NoError(t, decodeErr)
			assert.Equal(t, "/v1/moderations", path)
			upstreamModel := tt.modelName
			if tt.mapping != "" {
				upstreamModel = "omni-moderation-latest"
			}
			modelJSON, err := json.Marshal(upstreamModel)
			require.NoError(t, err)
			assert.JSONEq(t, string(modelJSON), string(request["model"]))
			assert.JSONEq(t, `"hello world"`, string(request["input"]))
			assert.NotContains(t, request, "messages")
			assert.NotContains(t, request, "stream")
			assert.NotContains(t, request, "max_tokens")
			assert.NotContains(t, request, "max_completion_tokens")
			var stored model.User
			require.NoError(t, db.First(&stored, user.Id).Error)
			assert.Equal(t, user.Quota, stored.Quota)
		})
	}
}

func TestModerationChannelTestQuotaHasNoTokenMinimum(t *testing.T) {
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeModerations}
	quota, _ := settleTestQuota(info, hosttypes.PriceData{ModelRatio: 1}, &dto.Usage{UsageSource: "moderation_unmetered"})
	require.Zero(t, quota)
	quota, _ = settleTestQuota(info, hosttypes.PriceData{UsePrice: true, ModelPrice: 0.01}, &dto.Usage{})
	require.EqualValues(t, 0.01*common.QuotaPerUnit, quota, "configured fixed pricing remains in effect")
}
