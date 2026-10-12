package ali

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/shopspring/decimal"
)

type videoCapability struct {
	resolutionRatios map[string]float64
	textToVideo      bool
	sizeBased        bool
	legacyDefault    string
}

// These ratios retain the native adaptor's China (Beijing) pricing basis.
// They multiply configured prices; they never replace them. Capabilities:
// https://help.aliyun.com/zh/model-studio/legacy-image-to-video-api-reference/
// https://www.alibabacloud.com/help/en/model-studio/text-to-video-api-reference
// Prices: https://help.aliyun.com/zh/model-studio/model-pricing
// None of the advertised models prices audio separately. In particular,
// wan2.6-i2v-flash's audio dimension must not leak into these profiles.
var videoCapabilities = map[string]videoCapability{
	"wan2.7-i2v":         {resolutionRatios: map[string]float64{"720P": 1, "1080P": 1 / 0.6}},
	"wan2.7-t2v":         {resolutionRatios: map[string]float64{"720P": 1, "1080P": 1 / 0.6}, textToVideo: true},
	"wan2.5-i2v-preview": {resolutionRatios: map[string]float64{"480P": 1, "720P": 2, "1080P": 1 / 0.3}},
	"wan2.2-i2v-flash":   {resolutionRatios: map[string]float64{"480P": 1, "720P": 2, "1080P": 4.8}},
	"wan2.2-i2v-plus":    {resolutionRatios: map[string]float64{"480P": 1, "1080P": 0.7 / 0.14}},
	"wanx2.1-i2v-plus":   {resolutionRatios: map[string]float64{"720P": 1}},
	"wanx2.1-i2v-turbo":  {resolutionRatios: map[string]float64{"480P": 1, "720P": 1}},
	// Preserve native models from the old price table without advertising new
	// IDs. Legacy text-to-video still uses size rather than resolution+ratio.
	"wan2.6-i2v":         {resolutionRatios: map[string]float64{"720P": 1, "1080P": 1 / 0.6}, legacyDefault: "1080P"},
	"wan2.5-t2v-preview": {resolutionRatios: map[string]float64{"480P": 1, "720P": 2, "1080P": 1 / 0.3}, sizeBased: true},
	"wan2.2-t2v-plus":    {resolutionRatios: map[string]float64{"480P": 1, "1080P": 0.7 / 0.14}, sizeBased: true},
	"wan2.2-kf2v-flash":  {resolutionRatios: map[string]float64{"480P": 1, "720P": 2, "1080P": 4.8}, legacyDefault: "720P"},
	"wan2.2-s2v":         {resolutionRatios: map[string]float64{"480P": 1, "720P": 0.9 / 0.5}, legacyDefault: "720P"},
}

func getVideoCapability(modelName string) (videoCapability, error) {
	profile, ok := videoCapabilities[modelName]
	if !ok {
		return videoCapability{}, fmt.Errorf("unsupported Wan model: %s", modelName)
	}
	return profile, nil
}

func normalizeResolution(resolution string) string {
	resolution = strings.ToUpper(strings.TrimSpace(resolution))
	if resolution != "" && !strings.HasSuffix(resolution, "P") {
		resolution += "P"
	}
	return resolution
}

func (profile videoCapability) reservationResolution() string {
	resolution := ""
	for candidate, ratio := range profile.resolutionRatios {
		// Prefer the higher resolution when supported tiers cost the same.
		if resolution == "" || ratio > profile.resolutionRatios[resolution] ||
			(ratio == profile.resolutionRatios[resolution] && candidate > resolution) {
			resolution = candidate
		}
	}
	return resolution
}

func resolutionAndRatioFromSize(size string) (string, string, error) {
	resolution, err := sizeToResolution(size)
	if err != nil {
		return "", "", err
	}
	switch size {
	case "832*480", "1280*720", "1920*1080":
		return resolution, "16:9", nil
	case "480*832", "720*1280", "1080*1920":
		return resolution, "9:16", nil
	case "624*624", "960*960", "1440*1440":
		return resolution, "1:1", nil
	case "1088*832", "1104*832", "1632*1248", "1648*1248":
		return resolution, "4:3", nil
	default:
		return resolution, "3:4", nil
	}
}

// Validate the final parameters, after metadata has been applied. All current
// image models use resolution, and Wan2.7 text-to-video uses resolution+ratio.
// Pixel sizes remain compatibility inputs, never ignored upstream size fields.
func normalizeVideoParameters(request *AliVideoRequest, profile videoCapability) error {
	if request.Parameters == nil {
		request.Parameters = &AliVideoParameters{Duration: 5}
	}
	parameters := request.Parameters
	resolution := normalizeResolution(parameters.Resolution)
	if parameters.Size != "" {
		sizeResolution, ratio, err := resolutionAndRatioFromSize(strings.TrimSpace(parameters.Size))
		if err != nil {
			return err
		}
		if resolution != "" && resolution != sizeResolution {
			return fmt.Errorf("size and resolution must select the same tier")
		}
		resolution = sizeResolution
		if profile.textToVideo && parameters.Ratio == "" {
			parameters.Ratio = ratio
		}
		if profile.sizeBased {
			parameters.Size = strings.TrimSpace(parameters.Size)
		} else {
			parameters.Size = ""
		}
	}
	if resolution != "" {
		if _, ok := profile.resolutionRatios[resolution]; !ok {
			return fmt.Errorf("resolution %s is unsupported for %s", resolution, request.Model)
		}
	}
	parameters.Resolution = resolution
	if profile.sizeBased {
		if parameters.Size == "" {
			switch resolution {
			case "480P":
				parameters.Size = "832*480"
			case "720P":
				parameters.Size = "1280*720"
			case "1080P":
				parameters.Size = "1920*1080"
			}
		}
		parameters.Resolution = ""
	}
	return nil
}

func ProcessAliOtherRatios(request *AliVideoRequest) (map[string]float64, error) {
	if request == nil {
		return nil, fmt.Errorf("video request is required")
	}
	profile, err := getVideoCapability(request.Model)
	if err != nil {
		// Custom model IDs retain native passthrough without inferred dimensions.
		return map[string]float64{}, nil
	}
	// Do not mutate the caller's request when using the shared validation path.
	copyRequest := *request
	if request.Parameters != nil {
		parameters := *request.Parameters
		copyRequest.Parameters = &parameters
	}
	if err := normalizeVideoParameters(&copyRequest, profile); err != nil {
		return nil, err
	}
	resolution := copyRequest.Parameters.Resolution
	if profile.sizeBased && copyRequest.Parameters.Size != "" {
		resolution, err = sizeToResolution(copyRequest.Parameters.Size)
		if err != nil {
			return nil, err
		}
	}
	if resolution == "" {
		resolution = profile.reservationResolution()
	}
	return map[string]float64{"resolution-" + resolution: profile.resolutionRatios[resolution]}, nil
}

// Adjust only native duration/resolution multipliers using the stable submit-time
// snapshot. This preserves configured prices, group multipliers,
// unrelated ratios, and the polling lifecycle's existing per-call exemption.
func (a *TaskAdaptor) AdjustBillingOnComplete(task *model.Task, result *relaycommon.TaskInfo) int {
	if task == nil || result == nil || result.Status != model.TaskStatusSuccess || task.Quota <= 0 {
		return 0
	}
	context := task.PrivateData.BillingContext
	if context == nil || context.PerCallBilling || context.ModelRatio <= 0 || context.GroupRatio <= 0 {
		return 0
	}
	modelName := task.Properties.UpstreamModelName
	if modelName == "" {
		modelName = firstNonEmpty(task.Properties.OriginModelName, context.OriginModelName)
	}
	profile, err := getVideoCapability(modelName)
	if err != nil {
		return 0
	}
	var response AliVideoResponse
	if err := common.Unmarshal(task.Data, &response); err != nil || response.Usage == nil || response.Output.TaskStatus != "SUCCEEDED" {
		return 0
	}
	adjustment := 1.0
	usage := response.Usage
	if seconds := float64(usage.Duration); seconds > 0 && seconds <= relaycommon.MaxTaskDurationSeconds && !math.IsNaN(seconds) {
		if reserved := context.OtherRatios["seconds"]; reserved > 0 && !math.IsInf(reserved, 0) {
			adjustment *= seconds / reserved
		}
	}
	// An unknown/missing provider tier cannot replace the supported reservation.
	resolution := strconv.Itoa(int(usage.SR)) + "P"
	if actual, ok := profile.resolutionRatios[resolution]; ok {
		reserved := 0.0
		for key, ratio := range context.OtherRatios {
			if strings.HasPrefix(key, "resolution-") {
				if _, supported := profile.resolutionRatios[strings.TrimPrefix(key, "resolution-")]; !supported || reserved != 0 {
					return 0
				}
				reserved = ratio
			}
		}
		if reserved > 0 && !math.IsInf(reserved, 0) {
			adjustment *= actual / reserved
		}
	}
	if adjustment == 1 || adjustment <= 0 || math.IsNaN(adjustment) || math.IsInf(adjustment, 0) {
		return 0
	}
	// Reconstruct the unrounded base from the submission snapshot. Neither a
	// previous settlement nor an integer reservation is a new price input.
	if math.IsInf(context.ModelRatio, 0) || math.IsNaN(context.ModelRatio) ||
		math.IsInf(context.GroupRatio, 0) || math.IsNaN(context.GroupRatio) {
		return 0
	}
	base := decimal.NewFromFloat(context.ModelRatio).Div(decimal.NewFromInt(2)).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(decimal.NewFromFloat(context.GroupRatio))
	price := &types.PriceData{}
	price.ReplaceOtherRatios(context.OtherRatios)
	amount := price.ApplyOtherRatiosToDecimal(base).Mul(decimal.NewFromFloat(adjustment))
	quota, _ := common.ChargeQuotaFromDecimalChecked(amount)
	return quota
}
