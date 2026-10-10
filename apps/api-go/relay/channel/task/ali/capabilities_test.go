package ali

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func billingContext(req relaycommon.TaskSubmitReq) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", req)
	return c
}

func TestAdvertisedModelsSelectExplicitCapabilities(t *testing.T) {
	adaptor := &TaskAdaptor{}
	want := map[string][]string{
		"wan2.7-i2v":         {"720P", "1080P"},
		"wan2.7-t2v":         {"720P", "1080P"},
		"wan2.5-i2v-preview": {"480P", "720P", "1080P"},
		"wan2.2-i2v-flash":   {"480P", "720P", "1080P"},
		"wan2.2-i2v-plus":    {"480P", "1080P"},
		"wanx2.1-i2v-plus":   {"720P"},
		"wanx2.1-i2v-turbo":  {"480P", "720P"},
	}
	require.Len(t, adaptor.GetModelList(), len(want))
	for _, modelName := range adaptor.GetModelList() {
		t.Run(modelName, func(t *testing.T) {
			profile, err := getVideoCapability(modelName)
			require.NoError(t, err)
			require.Len(t, profile.resolutionRatios, len(want[modelName]))
			for _, resolution := range want[modelName] {
				require.Contains(t, profile.resolutionRatios, resolution)
			}
			for _, resolution := range []string{"480P", "720P", "1080P", "4K"} {
				req := relaycommon.TaskSubmitReq{Model: modelName, Prompt: "animate", Image: "https://example.com/image.png", Size: resolution}
				_, supported := profile.resolutionRatios[resolution]
				converted, err := adaptor.convertToAliRequest(testRelayInfo(), req)
				if !supported {
					require.Error(t, err, resolution)
					require.Nil(t, adaptor.EstimateBilling(billingContext(req), testRelayInfo()))
					continue
				}
				require.NoError(t, err, resolution)
				require.Equal(t, resolution, converted.Parameters.Resolution)
				require.Equal(t, profile.resolutionRatios[resolution], adaptor.EstimateBilling(billingContext(req), testRelayInfo())["resolution-"+resolution])
			}
		})
	}
	_, err := getVideoCapability("wan2.6-i2v-flash")
	require.Error(t, err, "unadvertised model must not gain an implicit profile")
}

func TestResolutionValidationRunsBeforeBillingIncludingMetadataAndMapping(t *testing.T) {
	for _, test := range []struct {
		name    string
		body    string
		mapping string
	}{
		{"request", `{"model":"wan2.2-i2v-plus","prompt":"animate","size":"720p"}`, ""},
		{"metadata", `{"model":"wan2.2-i2v-plus","prompt":"animate","size":"480p","metadata":{"parameters":{"resolution":"720p"}}}`, ""},
		{"metadata size", `{"model":"wan2.2-i2v-plus","prompt":"animate","metadata":{"parameters":{"size":"1280*720"}}}`, ""},
		{"mapped", `{"model":"video-alias","prompt":"animate","size":"1080p"}`, `{"video-alias":"wanx2.1-i2v-turbo"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(test.body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("model_mapping", test.mapping)
			err := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, testRelayInfo())
			require.NotNil(t, err)
			require.Equal(t, http.StatusBadRequest, err.StatusCode)
			require.True(t, err.LocalError)
		})
	}
}

func TestMetadataBillingMatchesTransmittedResolutionWithoutMutation(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Model: "wan2.5-i2v-preview", Prompt: "animate", Size: "480p",
		Metadata: map[string]interface{}{"parameters": map[string]interface{}{"resolution": "1080p", "audio": false}},
	}
	before, err := common.Marshal(req.Metadata)
	require.NoError(t, err)
	converted, err := adaptor.convertToAliRequest(testRelayInfo(), req)
	require.NoError(t, err)
	require.Equal(t, "1080P", converted.Parameters.Resolution)
	ratios := adaptor.EstimateBilling(billingContext(req), testRelayInfo())
	require.Equal(t, map[string]float64{"seconds": 5, "resolution-1080P": 1 / 0.3}, ratios)
	after, err := common.Marshal(req.Metadata)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
}

func TestOmittedResolutionReservesHighestSupportedTier(t *testing.T) {
	adaptor := &TaskAdaptor{}
	for _, modelName := range adaptor.GetModelList() {
		t.Run(modelName, func(t *testing.T) {
			req := relaycommon.TaskSubmitReq{Model: modelName, Prompt: "animate", Image: "https://example.com/image.png"}
			converted, err := adaptor.convertToAliRequest(testRelayInfo(), req)
			require.NoError(t, err)
			require.Empty(t, converted.Parameters.Resolution)
			require.Empty(t, converted.Parameters.Size)
			profile, err := getVideoCapability(modelName)
			require.NoError(t, err)
			reserved := adaptor.EstimateBilling(billingContext(req), testRelayInfo())
			highest := profile.reservationResolution()
			require.Equal(t, profile.resolutionRatios[highest], reserved["resolution-"+highest])
			for _, ratio := range profile.resolutionRatios {
				require.GreaterOrEqual(t, reserved["resolution-"+highest], ratio)
			}
		})
	}
}

func TestWan27TextToVideoConvertsPixelSizesToNativeResolutionAndRatio(t *testing.T) {
	for _, test := range []struct{ size, resolution, ratio string }{
		{"1920*1080", "1080P", "16:9"},
		{"720*1280", "720P", "9:16"},
		{"1088*832", "720P", "4:3"},
		{"1104*832", "720P", "4:3"},
		{"1248*1648", "1080P", "3:4"},
	} {
		converted, err := (&TaskAdaptor{}).convertToAliRequest(testRelayInfo(), relaycommon.TaskSubmitReq{Model: "wan2.7-t2v", Prompt: "animate", Size: test.size})
		require.NoError(t, err)
		require.Equal(t, test.resolution, converted.Parameters.Resolution)
		require.Equal(t, test.ratio, converted.Parameters.Ratio)
		require.Empty(t, converted.Parameters.Size)
	}
}

func TestCompletionUsesSupportedProviderFactsAndPreservesPriceSnapshot(t *testing.T) {
	for _, test := range []struct {
		name      string
		usage     string
		perCall   bool
		wantQuota int
	}{
		{name: "supported", usage: `{"duration":5,"SR":480,"audio":true}`, wantQuota: 6000},
		{name: "audio has no price dimension", usage: `{"duration":5,"SR":480,"audio":false}`, wantQuota: 6000},
		{name: "fractional duration", usage: `{"duration":4.5,"SR":480}`, wantQuota: 5400},
		{name: "numeric string", usage: `{"duration":"5","SR":"480"}`, wantQuota: 6000},
		{name: "unsupported tier", usage: `{"duration":5,"SR":720}`},
		{name: "missing tier", usage: `{"duration":5}`},
		{name: "missing tier retains reservation", usage: `{"duration":2}`, wantQuota: 12000},
		{name: "per call", usage: `{"duration":5,"SR":480}`, perCall: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			context := &model.TaskBillingContext{ModelPrice: 100, ModelRatio: 0.0008, GroupRatio: 3, PerCallBilling: test.perCall, OtherRatios: map[string]float64{"seconds": 5, "resolution-1080P": 5, "custom": 2}}
			task := &model.Task{Quota: 30000, Properties: model.Properties{OriginModelName: "alias", UpstreamModelName: "wan2.2-i2v-plus"}, PrivateData: model.TaskPrivateData{BillingContext: context}, Data: []byte(fmt.Sprintf(`{"output":{"task_status":"SUCCEEDED"},"usage":%s}`, test.usage))}
			before, err := common.Marshal(context)
			require.NoError(t, err)
			require.Equal(t, test.wantQuota, (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}))
			if test.wantQuota > 0 {
				task.Quota = test.wantQuota
				for repeat := 0; repeat < 20; repeat++ {
					require.Equal(t, test.wantQuota, (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{Status: model.TaskStatusSuccess}), "a polling retry must not reapply the correction to settled quota")
				}
			}
			after, err := common.Marshal(context)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestAliMetadataNullParametersDoesNotPanic(t *testing.T) {
	req := relaycommon.TaskSubmitReq{Model: "wanx2.1-i2v-plus", Prompt: "animate", Metadata: map[string]interface{}{"parameters": nil}}
	converted, err := (&TaskAdaptor{}).convertToAliRequest(testRelayInfo(), req)
	require.NoError(t, err)
	require.Equal(t, 5, converted.Parameters.Duration)
	require.Equal(t, map[string]float64{"seconds": 5, "resolution-720P": 1}, (&TaskAdaptor{}).EstimateBilling(billingContext(req), testRelayInfo()))
}

func TestLegacyPriceTableModelsKeepNativeProtocols(t *testing.T) {
	adaptor := &TaskAdaptor{}
	for _, modelName := range []string{"wan2.6-i2v", "wan2.5-t2v-preview", "wan2.2-t2v-plus", "wan2.2-kf2v-flash", "wan2.2-s2v"} {
		profile, err := getVideoCapability(modelName)
		require.NoError(t, err)
		require.NotContains(t, adaptor.GetModelList(), modelName)
		for resolution, ratio := range profile.resolutionRatios {
			parameters := &AliVideoParameters{Resolution: resolution, Duration: 5}
			if profile.sizeBased {
				parameters.Resolution = ""
				parameters.Size = map[string]string{"480P": "832*480", "720P": "1280*720", "1080P": "1920*1080"}[resolution]
			}
			ratios, err := ProcessAliOtherRatios(&AliVideoRequest{Model: modelName, Parameters: parameters})
			require.NoError(t, err)
			require.Equal(t, ratio, ratios["resolution-"+resolution])
		}
		if profile.sizeBased {
			converted, err := adaptor.convertToAliRequest(testRelayInfo(), relaycommon.TaskSubmitReq{Model: modelName, Prompt: "animate", Size: "832*480"})
			require.NoError(t, err)
			require.Equal(t, "832*480", converted.Parameters.Size)
			require.Empty(t, converted.Parameters.Resolution)
			defaultRequest, err := adaptor.convertToAliRequest(testRelayInfo(), relaycommon.TaskSubmitReq{Model: modelName, Prompt: "animate"})
			require.NoError(t, err)
			require.Equal(t, "1920*1080", defaultRequest.Parameters.Size)
		}
	}
}

func TestCustomModelPassthroughDoesNotGuessBillingDimensions(t *testing.T) {
	adaptor := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{Model: "custom-video", Prompt: "animate", Size: "4k", Metadata: map[string]interface{}{"parameters": map[string]interface{}{"audio": true}}}
	converted, err := adaptor.convertToAliRequest(testRelayInfo(), req)
	require.NoError(t, err)
	require.Equal(t, "custom-video", converted.Model)
	require.Equal(t, "4KP", converted.Parameters.Resolution)
	require.Equal(t, map[string]float64{"seconds": 5}, adaptor.EstimateBilling(billingContext(req), testRelayInfo()))
}
