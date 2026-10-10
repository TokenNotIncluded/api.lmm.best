package hailuo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseTaskResultPreservesProviderFailure(t *testing.T) {
	service.InitHttpClient()
	var retrieveCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		retrieveCalls.Add(1)
		_, _ = w.Write([]byte(`{"base_resp":{"status_code":0},"file":{"download_url":"https://example.com/video.mp4"}}`))
	}))
	t.Cleanup(server.Close)
	adaptor := &TaskAdaptor{baseURL: server.URL, apiKey: "test-key"}
	for _, status := range []string{"", TaskStatusPreparing, TaskStatusQueueing, TaskStatusProcessing, TaskStatusSuccess, TaskStatusFailed, "unknown"} {
		t.Run(fmt.Sprintf("status=%s", status), func(t *testing.T) {
			body, err := json.Marshal(QueryTaskResponse{
				Status: status,
				FileID: "file-1",
				BaseResp: BaseResp{
					StatusCode: StatusParamError,
					StatusMsg:  "invalid task id",
				},
			})
			require.NoError(t, err)
			result, err := adaptor.ParseTaskResult(body)
			require.NoError(t, err)
			require.Equal(t, model.TaskStatusFailure, result.Status)
			require.Equal(t, "100%", result.Progress)
			require.Equal(t, StatusParamError, result.Code)
			require.Equal(t, "invalid task id", result.Reason)
			require.Empty(t, result.Url)
		})
	}
	require.Zero(t, retrieveCalls.Load(), "provider errors must not fetch a result file")
}

func TestParseTaskResultSuccessfulResponseStatuses(t *testing.T) {
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"base_resp":{"status_code":0},"file":{"download_url":"https://example.com/video.mp4"}}`))
	}))
	t.Cleanup(server.Close)
	adaptor := &TaskAdaptor{baseURL: server.URL, apiKey: "test-key"}
	for _, test := range []struct {
		status, wantStatus, wantProgress, wantURL, wantReason string
	}{
		{TaskStatusPreparing, model.TaskStatusInProgress, "30%", "", ""},
		{TaskStatusQueueing, model.TaskStatusInProgress, "30%", "", ""},
		{TaskStatusProcessing, model.TaskStatusInProgress, "50%", "", ""},
		{TaskStatusSuccess, model.TaskStatusSuccess, "100%", "https://example.com/video.mp4", ""},
		{TaskStatusFailed, model.TaskStatusFailure, "100%", "", "task failed"},
		{"unknown", model.TaskStatusInProgress, "30%", "", ""},
	} {
		t.Run(test.status, func(t *testing.T) {
			body, err := json.Marshal(QueryTaskResponse{Status: test.status, FileID: "file-1"})
			require.NoError(t, err)
			result, err := adaptor.ParseTaskResult(body)
			require.NoError(t, err)
			require.Equal(t, test.wantStatus, result.Status)
			require.Equal(t, test.wantProgress, result.Progress)
			require.Equal(t, test.wantURL, result.Url)
			require.Equal(t, test.wantReason, result.Reason)
			require.Zero(t, result.Code)
		})
	}
}

func TestMetadataCannotOverrideBilledOrMappedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adaptor := &TaskAdaptor{}
	for _, test := range []struct {
		name, origin, upstream, resolution string
	}{
		{"normal model", "T2V-01", "T2V-01", Resolution720P},
		{"mapped model", "video-alias", "MiniMax-Hailuo-02", Resolution768P},
	} {
		for _, key := range []string{"model", "Model", "MODEL", "MoDeL"} {
			t.Run(test.name+"/"+key, func(t *testing.T) {
				metadata := map[string]any{
					key:                 "MiniMax-Hailuo-2.3",
					"first_frame_image": "https://example.com/input.png",
					"resolution":        test.resolution, "duration": 6, "prompt_optimizer": true,
				}
				before, err := json.Marshal(metadata)
				require.NoError(t, err)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Set("task_request", relaycommon.TaskSubmitReq{Model: test.origin, Prompt: "a cat", Metadata: metadata})
				info := &relaycommon.RelayInfo{
					OriginModelName: test.origin,
					ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: test.upstream},
				}
				body, err := adaptor.BuildRequestBody(c, info)
				require.NoError(t, err)
				var payload VideoRequest
				require.NoError(t, json.NewDecoder(body).Decode(&payload))
				require.Equal(t, test.upstream, payload.Model, "only the billed model's channel mapping may choose the upstream model")
				require.Equal(t, test.resolution, payload.Resolution)
				require.NotNil(t, payload.Duration)
				require.Equal(t, 6, *payload.Duration)
				require.Equal(t, "https://example.com/input.png", payload.FirstFrameImage)
				require.NotNil(t, payload.PromptOptimizer)
				require.True(t, *payload.PromptOptimizer)
				after, err := json.Marshal(metadata)
				require.NoError(t, err)
				require.Equal(t, before, after, "building the request must preserve caller metadata")
			})
		}
	}
}

// Hailuo uses the host's configured base price. Its native adaptor has no
// model-family usage schema, so request facts cannot add billing dimensions.
func TestHailuoModelFamiliesKeepConfiguredBaseBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prices, ratios, groups := ratio_setting.ModelPrice2JSONString(), ratio_setting.ModelRatio2JSONString(), ratio_setting.GroupRatio2JSONString()
	quotaUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(ratios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groups))
		common.QuotaPerUnit = quotaUnit
	})
	common.QuotaPerUnit = 500000
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"hailuo-test":2}`))
	adaptor := &TaskAdaptor{}
	require.NotContains(t, adaptor.GetModelList(), "MiniMax-H3", "H3 needs separate provider protocol validation")
	configuredPrices := map[string]float64{}
	configuredRatios := map[string]float64{}
	for _, name := range adaptor.GetModelList() {
		configuredPrices[name] = 0.04
		configuredRatios[name] = 0.08
	}
	priceJSON, err := json.Marshal(configuredPrices)
	require.NoError(t, err)
	ratioJSON, err := json.Marshal(configuredRatios)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(ratioJSON)))
	for _, mode := range []string{"fixed price", "model ratio fallback"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "fixed price" {
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(priceJSON)))
			} else {
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
			}
			for _, name := range adaptor.GetModelList() {
				t.Run(name, func(t *testing.T) {
					profile := GetModelConfig(name)
					for _, request := range []relaycommon.TaskSubmitReq{
						{Model: name, Prompt: "a cat"},
						{
							Model: name, Prompt: "a cat", Duration: profile.SupportedDurations[len(profile.SupportedDurations)-1],
							Size: profile.SupportedResolutions[len(profile.SupportedResolutions)-1],
							Metadata: map[string]any{
								"first_frame_image": "https://example.com/input.png",
								"input_images":      4, "input_video_seconds": 12,
							},
						},
					} {
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						c.Set("task_request", request)
						info := &relaycommon.RelayInfo{
							OriginModelName: name, UserGroup: "hailuo-test", UsingGroup: "hailuo-test",
							ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: name},
						}
						price, err := helper.ModelPriceHelperPerCall(c, info)
						require.NoError(t, err)
						require.Equal(t, mode == "fixed price", price.UsePrice)
						if price.UsePrice {
							require.Equal(t, 0.04, price.ModelPrice)
						} else {
							require.Equal(t, 0.08, price.ModelRatio)
							require.Equal(t, -1.0, price.ModelPrice)
						}
						info.PriceData = price
						require.Nil(t, adaptor.EstimateBilling(c, info))
						require.Nil(t, adaptor.AdjustBillingOnSubmit(info, []byte(`{"task_id":"task-1","base_resp":{"status_code":0}}`)))
						task := &model.Task{Quota: price.Quota, Properties: model.Properties{OriginModelName: name, UpstreamModelName: name}}
						require.Zero(t, adaptor.AdjustBillingOnComplete(task, &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}))
						require.Equal(t, 40000, price.Quota, "configured rate and group ratio determine the base quota")
						require.Equal(t, 40000, task.Quota)
						require.Equal(t, 1.0, info.PriceData.OtherRatioMultiplier())
						require.Empty(t, info.PriceData.OtherRatios())
						require.Equal(t, price, info.PriceData)
					}
				})
			}
		})
	}
}
