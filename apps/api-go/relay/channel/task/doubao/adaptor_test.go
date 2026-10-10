package doubao

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	seedance10   = "doubao-seedance-1-0-pro-250528"
	seedance15   = "doubao-seedance-1-5-pro-251215"
	seedance20   = "doubao-seedance-2-0-260128"
	seedanceFast = "doubao-seedance-2-0-fast-260128"
)

func videoTestContext(t *testing.T, req relaycommon.TaskSubmitReq) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	data, err := common.Marshal(req)
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(string(data)))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, &relaycommon.RelayInfo{
		OriginModelName: req.Model,
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: req.Model},
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
	}
}

func TestAdvertisedSeedanceProfilesValidateAndBillTheSameTiers(t *testing.T) {
	adaptor := &TaskAdaptor{}
	require.Len(t, videoModelProfiles, len(adaptor.GetModelList()))
	for _, name := range adaptor.GetModelList() {
		t.Run(name, func(t *testing.T) {
			profile, matched, ok := videoProfileForModels(name)
			require.True(t, ok)
			require.Equal(t, name, matched)
			want := []string{"480p", "720p", "1080p"}
			if name == seedance20 {
				want = append(want, "4k")
			}
			if name == seedanceFast {
				want = []string{"480p", "720p"}
			}
			require.Equal(t, want, profile.resolutions)
			require.Len(t, profile.tiers, len(want))
			for _, resolution := range []string{"480p", "720p", "1080p", "4k", "1440p"} {
				t.Run(resolution, func(t *testing.T) {
					req := relaycommon.TaskSubmitReq{Model: name, Prompt: "a cat", Metadata: map[string]any{"resolution": resolution}}
					c, info := videoTestContext(t, req)
					taskErr := adaptor.ValidateRequestAndSetAction(c, info)
					_, supported := profile.tiers[resolution]
					if !supported {
						require.NotNil(t, taskErr)
						require.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
						require.True(t, taskErr.LocalError)
						require.Nil(t, adaptor.EstimateBilling(c, info))
						_, ok := GetVideoInputRatio(name, resolution, false)
						require.False(t, ok)
						return
					}
					require.Nil(t, taskErr)
					billing := adaptor.EstimateBilling(c, info)
					require.NotNil(t, billing)
					reader, err := adaptor.BuildRequestBody(c, info)
					require.NoError(t, err)
					data, err := io.ReadAll(reader)
					require.NoError(t, err)
					var payload requestPayload
					require.NoError(t, common.Unmarshal(data, &payload))
					require.Equal(t, resolution, payload.Resolution)
					ratio, ok := GetVideoInputRatio(name, resolution, false)
					require.True(t, ok)
					price := types.PriceData{}
					price.ReplaceOtherRatios(billing)
					require.InDelta(t, ratio, price.OtherRatioMultiplier(), 1e-12)
				})
			}
		})
	}
}

func TestSeedanceDimensionsBelongToTheirModel(t *testing.T) {
	for _, name := range ModelList {
		for _, audio := range []bool{true, false} {
			req := relaycommon.TaskSubmitReq{Model: name, Prompt: "a cat", Metadata: map[string]any{
				"resolution": "720p", "generate_audio": audio,
				"content": []any{map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://example.com/input.mp4"}}},
			}}
			c, info := videoTestContext(t, req)
			adaptor := &TaskAdaptor{}
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			ratios := adaptor.EstimateBilling(c, info)
			require.Equal(t, 1.0, ratios["resolution"])
			if name == seedance20 {
				require.InDelta(t, 28.0/46.0, ratios["video_input"], 1e-12)
			} else if name == seedanceFast {
				require.InDelta(t, 22.0/37.0, ratios["video_input"], 1e-12)
			} else {
				require.NotContains(t, ratios, "video_input")
			}
			if name == seedance15 {
				want := 1.0
				if !audio {
					want = 0.5
				}
				require.Equal(t, want, ratios["generate_audio"])
			} else {
				require.NotContains(t, ratios, "generate_audio")
			}
		}
	}
	c, info := videoTestContext(t, relaycommon.TaskSubmitReq{Model: seedance15, Prompt: "a cat"})
	adaptor := &TaskAdaptor{}
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	require.Equal(t, 1.0, adaptor.EstimateBilling(c, info)["generate_audio"], "Ark defaults to audio")
}

func TestSeedanceOmittedResolutionReservesSupportedMaximum(t *testing.T) {
	for _, name := range ModelList {
		req := relaycommon.TaskSubmitReq{Model: name, Prompt: "a cat"}
		c, info := videoTestContext(t, req)
		adaptor := &TaskAdaptor{}
		require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
		reservation := adaptor.EstimateBilling(c, info)
		price := types.PriceData{}
		price.ReplaceOtherRatios(reservation)
		for resolution := range videoModelProfiles[name].tiers {
			ratio, ok := GetVideoInputRatio(name, resolution, false)
			require.True(t, ok)
			require.GreaterOrEqual(t, price.OtherRatioMultiplier(), ratio)
		}
		reader, err := adaptor.BuildRequestBody(c, info)
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NotContains(t, string(data), `"resolution"`, "leave provider-selected resolution omitted upstream")
	}
}

func TestCustomEndpointKeepsUnprofiledParameters(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Model: "ep-custom", Prompt: "a cat --rs 1440p", Size: "provider-selected",
		Metadata: map[string]any{"resolution": "ProviderTier"},
	}
	c, info := videoTestContext(t, req)
	adaptor := &TaskAdaptor{}
	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	require.Nil(t, adaptor.EstimateBilling(c, info))
	body, profile, err := adaptor.prepareRequest(&req, info)
	require.NoError(t, err)
	require.Nil(t, profile)
	require.Equal(t, "ProviderTier", body.Resolution)
	require.Equal(t, req.Prompt, body.Content[len(body.Content)-1].Text)
}

func TestSeedanceEffectivePayloadAndMapping(t *testing.T) {
	for _, tc := range []struct {
		name, model, size, resolution, mapping string
		wantResolution                         string
		wantErr                                bool
	}{
		{"fast pixel size rejected", seedanceFast, "1920x1080", "", "", "", true},
		{"fast portrait size rejected", seedanceFast, "2160x3840", "", "", "", true},
		{"metadata wins over size", seedanceFast, "1920x1080", "720P", "", "720p", false},
		{"metadata unsupported wins", seedanceFast, "720p", "1080p", "", "", true},
		{"alias mapped to fast", "video-alias", "4k", "", `{"video-alias":"doubao-seedance-2-0-fast-260128"}`, "", true},
		{"known model mapped to endpoint", seedanceFast, "1080p", "", `{"doubao-seedance-2-0-fast-260128":"ep-example"}`, "", true},
		{"executing model wins", seedance20, "1080p", "", `{"doubao-seedance-2-0-260128":"doubao-seedance-2-0-fast-260128"}`, "", true},
		{"2.0 4k accepted", seedance20, "3840x2160", "", "", "4k", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := relaycommon.TaskSubmitReq{Model: tc.model, Prompt: "a cat", Size: tc.size, Metadata: map[string]any{"model": seedance20}}
			if tc.resolution != "" {
				req.Metadata["resolution"] = tc.resolution
			}
			c, info := videoTestContext(t, req)
			c.Set("model_mapping", tc.mapping)
			before := info.UpstreamModelName
			adaptor := &TaskAdaptor{}
			taskErr := adaptor.ValidateRequestAndSetAction(c, info)
			require.Equal(t, before, info.UpstreamModelName, "validation copy must not mutate channel mapping")
			stored, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			require.Equal(t, seedance20, stored.Metadata["model"], "validation must not mutate metadata")
			if tc.wantErr {
				require.NotNil(t, taskErr)
				require.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
				return
			}
			require.Nil(t, taskErr)
			require.NoError(t, helper.ModelMappedHelper(c, info, nil))
			body, _, err := adaptor.prepareRequest(&stored, info)
			require.NoError(t, err)
			require.Equal(t, tc.wantResolution, body.Resolution)
			require.Equal(t, tc.model, body.Model)
		})
	}
}

func TestSeedancePromptResolutionUsesEffectiveTier(t *testing.T) {
	for _, tc := range []struct {
		name, model, prompt, size, resolution, mapping string
		wantResolution, wantPrompt                     string
		wantErr                                        bool
	}{
		{name: "fast rejects short flag", model: seedanceFast, prompt: "a cat --rs 1080p", wantErr: true},
		{name: "fast rejects long flag", model: seedanceFast, prompt: "a cat --resolution 4k", wantErr: true},
		{name: "reject unknown flag tier", model: seedance20, prompt: "a cat --rs 1440p", wantErr: true},
		{name: "standard accepts 4k flag", model: seedance20, prompt: "a cat --rs 4K --dur 5", wantResolution: "4k", wantPrompt: "a cat --dur 5"},
		{name: "fast accepts long flag", model: seedanceFast, prompt: "a cat --resolution 720p", wantResolution: "720p", wantPrompt: "a cat"},
		{name: "same repeated flags", model: seedance20, prompt: "a cat --rs 720p --resolution 720P", resolution: "720p", wantResolution: "720p", wantPrompt: "a cat"},
		{name: "conflicting repeated flags", model: seedance20, prompt: "a cat --rs 720p --resolution 1080p", wantErr: true},
		{name: "metadata flag conflict", model: seedance20, prompt: "a cat --rs 1080p", resolution: "720p", wantErr: true},
		{name: "size flag conflict", model: seedance20, prompt: "a cat --rs 720p", size: "1920x1080", wantErr: true},
		{name: "missing flag value", model: seedance20, prompt: "a cat --rs", wantErr: true},
		{name: "following flag is not a value", model: seedance20, prompt: "a cat --rs --dur 5", wantErr: true},
		{name: "similarly named text preserved", model: seedance20, prompt: "a cat --resolutionist 4k", wantPrompt: "a cat --resolutionist 4k"},
		{name: "mapped fast rejects flag", model: "video-alias", prompt: "a cat --rs 1080p", mapping: `{"video-alias":"doubao-seedance-2-0-fast-260128"}`, wantErr: true},
		{name: "unknown endpoint passthrough", model: "ep-custom", prompt: "a cat --rs 4k", wantPrompt: "a cat --rs 4k"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := relaycommon.TaskSubmitReq{Model: tc.model, Prompt: tc.prompt, Size: tc.size}
			if tc.resolution != "" {
				req.Metadata = map[string]any{"resolution": tc.resolution}
			}
			c, info := videoTestContext(t, req)
			c.Set("model_mapping", tc.mapping)
			adaptor := &TaskAdaptor{}
			taskErr := adaptor.ValidateRequestAndSetAction(c, info)
			if tc.wantErr {
				require.NotNil(t, taskErr)
				require.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
				require.True(t, taskErr.LocalError)
				return
			}
			require.Nil(t, taskErr)
			require.NoError(t, helper.ModelMappedHelper(c, info, nil))
			stored, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			require.Equal(t, tc.prompt, stored.Prompt, "effective payload must not mutate the stored request")
			reader, err := adaptor.BuildRequestBody(c, info)
			require.NoError(t, err)
			data, err := io.ReadAll(reader)
			require.NoError(t, err)
			var body requestPayload
			require.NoError(t, common.Unmarshal(data, &body))
			require.Equal(t, tc.wantResolution, body.Resolution)
			require.Equal(t, tc.wantPrompt, body.Content[len(body.Content)-1].Text)
			ratios := adaptor.EstimateBilling(c, info)
			profile, _, known := videoProfileForModels(info.UpstreamModelName, info.OriginModelName)
			if !known {
				require.Nil(t, ratios)
				return
			}
			require.Equal(t, profile.billingRatios(body.Resolution, false, true), ratios)
		})
	}
}

func completionTestTask(name string, ratios map[string]float64) *model.Task {
	return &model.Task{
		Group:       "default",
		Properties:  model.Properties{OriginModelName: name, UpstreamModelName: name},
		PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{OriginModelName: name, OtherRatios: ratios}},
	}
}

func TestSeedanceCompletionFactsAndLegacySnapshots(t *testing.T) {
	for _, tc := range []struct {
		name, model, resolution string
		reserved, want          map[string]float64
		audio                   *bool
	}{
		{"2.0 uses supported output", seedance20, "4k", map[string]float64{"resolution": 51.0 / 46, "video_input": 31.0 / 51, "seconds": 5}, map[string]float64{"resolution": 26.0 / 46, "video_input": 16.0 / 26, "seconds": 5}, nil},
		{"fast ignores impossible output", seedanceFast, "1080p", map[string]float64{"resolution": 1, "video_input": 22.0 / 37}, map[string]float64{"resolution": 1, "video_input": 22.0 / 37}, nil},
		{"unknown output preserves reserve", seedance20, "1440p", map[string]float64{"resolution": 51.0 / 46, "video_input": 1}, map[string]float64{"resolution": 51.0 / 46, "video_input": 1}, nil},
		{"missing output preserves reserve", seedance20, "", map[string]float64{"resolution": 51.0 / 46, "video_input": 1}, map[string]float64{"resolution": 51.0 / 46, "video_input": 1}, nil},
		{"legacy combined ratio unchanged", seedance20, "4k", map[string]float64{"video_input": 31.0 / 46}, map[string]float64{"video_input": 31.0 / 46}, nil},
		{"legacy no dimensions unchanged", seedance20, "1080p", map[string]float64{}, map[string]float64{}, nil},
		{"1.5 explicit silent fact", seedance15, "720p", map[string]float64{"resolution": 1, "generate_audio": 1}, map[string]float64{"resolution": 1, "generate_audio": 0.5}, boolPointer(false)},
		{"1.5 missing audio preserves silent", seedance15, "720p", map[string]float64{"resolution": 1, "generate_audio": 0.5}, map[string]float64{"resolution": 1, "generate_audio": 0.5}, nil},
		{"2.0 ignores audio fact", seedance20, "720p", map[string]float64{"resolution": 1, "video_input": 1}, map[string]float64{"resolution": 1, "video_input": 1}, boolPointer(false)},
		{"1.x never acquires reference fact", seedance10, "720p", map[string]float64{"resolution": 1}, map[string]float64{"resolution": 1}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := completionTestTask(tc.model, tc.reserved)
			before, err := json.Marshal(task.PrivateData.BillingContext)
			require.NoError(t, err)
			result := responseTask{Status: "succeeded", Resolution: tc.resolution, GenerateAudio: tc.audio}
			actual := completionBillingRatios(task, result)
			require.Equal(t, tc.want, actual)
			require.Equal(t, actual, completionBillingRatios(task, result), "completion overlay is idempotent")
			after, err := json.Marshal(task.PrivateData.BillingContext)
			require.NoError(t, err)
			require.Equal(t, before, after, "retain reservation and additional ratios")
		})
	}
	// Nested completion resolution has precedence, just as the upstream plugin.
	task := completionTestTask(seedance20, map[string]float64{"resolution": 51.0 / 46, "video_input": 1})
	result := responseTask{Status: "succeeded", Resolution: "4k"}
	result.Content.Resolution = "720p"
	require.Equal(t, 1.0, completionBillingRatios(task, result)["resolution"])
}

func boolPointer(v bool) *bool { return &v }

func TestSeedanceCompletionAcceptsProviderDurationEncodings(t *testing.T) {
	adaptor := &TaskAdaptor{}
	for _, duration := range []string{`5`, `"5"`} {
		t.Run(duration, func(t *testing.T) {
			data := []byte(`{"status":"succeeded","duration":` + duration + `,"content":{"video_url":"https://example.com/video.mp4"},"usage":{"completion_tokens":1234}}`)
			result, err := adaptor.ParseTaskResult(data)
			require.NoError(t, err)
			require.Equal(t, model.TaskStatusSuccess, result.Status)
			require.Equal(t, 1234, result.TotalTokens)
			require.Equal(t, "https://example.com/video.mp4", result.Url)
			_, err = adaptor.ConvertToOpenAIVideo(&model.Task{Data: data, Status: model.TaskStatusSuccess})
			require.NoError(t, err)
		})
	}
}

func TestSeedanceTerminalFailuresDoNotRemainInProgress(t *testing.T) {
	adaptor := &TaskAdaptor{}
	for _, status := range []string{"failed", "cancelled", "expired"} {
		for _, message := range []string{"", "provider stopped the task"} {
			t.Run(status+"/"+message, func(t *testing.T) {
				data, err := common.Marshal(map[string]any{
					"status": status, "error": map[string]any{"code": "TaskStopped", "message": message},
					"usage": map[string]any{"total_tokens": 1234},
				})
				require.NoError(t, err)
				result, err := adaptor.ParseTaskResult(data)
				require.NoError(t, err)
				require.Equal(t, model.TaskStatusFailure, result.Status)
				require.Equal(t, "100%", result.Progress)
				require.Zero(t, result.TotalTokens)
				wantReason := message
				if wantReason == "" {
					wantReason = "task " + status
				}
				require.Equal(t, wantReason, result.Reason)
				response, err := adaptor.ConvertToOpenAIVideo(&model.Task{Data: data, Status: model.TaskStatusFailure})
				require.NoError(t, err)
				var video struct {
					Status string `json:"status"`
					Error  struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				require.NoError(t, common.Unmarshal(response, &video))
				require.Equal(t, "failed", video.Status)
				require.Equal(t, "TaskStopped", video.Error.Code)
				require.Equal(t, wantReason, video.Error.Message)
			})
		}
	}
}

func TestSeedanceCompletionQuotaPreservesConfiguredPricesAndLocks(t *testing.T) {
	modelRatios := ratio_setting.ModelRatio2JSONString()
	modelPrices := ratio_setting.ModelPrice2JSONString()
	groups := ratio_setting.GroupRatio2JSONString()
	specialGroups := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(modelRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groups))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(specialGroups))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"doubao-seedance-2-0-260128":2.3}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1.7}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"default":{"default":1.2}}`))
	task := completionTestTask(seedance20, map[string]float64{"resolution": 51.0 / 46, "video_input": 31.0 / 51, "seconds": 2})
	task.PrivateData.BillingContext.ModelRatio = 2.3
	task.PrivateData.BillingContext.GroupRatio = 1.2
	// Synchronizing lower live prices must not reprice an accepted task.
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"doubao-seedance-2-0-260128":0.1}`))
	task.Data = json.RawMessage(`{"status":"succeeded","duration":"5","resolution":"4k","usage":{"completion_tokens":10000}}`)
	adaptor := &TaskAdaptor{}
	result, err := adaptor.ParseTaskResult(task.Data)
	require.NoError(t, err)
	require.Equal(t, 10000, result.TotalTokens, "completion-only Ark usage remains billable")
	want := 19200 // 10000 * 2.3 * 1.2 * (16/46) * 2, rounded only once.
	require.Equal(t, want, adaptor.AdjustBillingOnComplete(task, result))
	task.Quota = want // polling has written the first settled quota
	for range 100 {
		require.Equal(t, want, adaptor.AdjustBillingOnComplete(task, result))
	}
	require.Equal(t, map[string]float64{"resolution": 51.0 / 46, "video_input": 31.0 / 51, "seconds": 2}, task.PrivateData.BillingContext.OtherRatios)
	require.Equal(t, modelPrices, ratio_setting.ModelPrice2JSONString())
	require.Equal(t, `{"doubao-seedance-2-0-260128":0.1}`, ratio_setting.ModelRatio2JSONString())
	task.PrivateData.BillingContext.PerCallBilling = true
	require.Zero(t, adaptor.AdjustBillingOnComplete(task, result), "fixed prices and task price locks skip settlement")
}
