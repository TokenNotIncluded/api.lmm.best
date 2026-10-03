package doubao

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	taskdto "github.com/LIghtJUNction/api.lmm.best/dto"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/task/taskcommon"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/LIghtJUNction/api.lmm.best/types"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/samber/lo"
)

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type     string    `json:"type,omitempty"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
	AudioURL *MediaURL `json:"audio_url,omitempty"`
	Role     string    `json:"role,omitempty"`
}

type MediaURL struct {
	URL string `json:"url,omitempty"`
}

type requestPayload struct {
	Model                 string         `json:"model"`
	Content               []ContentItem  `json:"content,omitempty"`
	CallbackURL           string         `json:"callback_url,omitempty"`
	ReturnLastFrame       *dto.BoolValue `json:"return_last_frame,omitempty"`
	ServiceTier           string         `json:"service_tier,omitempty"`
	ExecutionExpiresAfter *dto.IntValue  `json:"execution_expires_after,omitempty"`
	GenerateAudio         *dto.BoolValue `json:"generate_audio,omitempty"`
	Draft                 *dto.BoolValue `json:"draft,omitempty"`
	Tools                 []struct {
		Type string `json:"type,omitempty"`
	} `json:"tools,omitempty"`
	SafetyIdentifier string         `json:"safety_identifier,omitempty"`
	Priority         *dto.IntValue  `json:"priority,omitempty"`
	Resolution       string         `json:"resolution,omitempty"`
	Ratio            string         `json:"ratio,omitempty"`
	Duration         *dto.IntValue  `json:"duration,omitempty"`
	Frames           *dto.IntValue  `json:"frames,omitempty"`
	Seed             *dto.IntValue  `json:"seed,omitempty"`
	CameraFixed      *dto.BoolValue `json:"camera_fixed,omitempty"`
	Watermark        *dto.BoolValue `json:"watermark,omitempty"`
}

type responsePayload struct {
	ID string `json:"id"` // task_id
}

type responseTask struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Status  string `json:"status"`
	Content struct {
		VideoURL   string `json:"video_url"`
		Resolution string `json:"resolution"`
	} `json:"content"`
	GenerateAudio   *bool        `json:"generate_audio"`
	Seed            int          `json:"seed"`
	Resolution      string       `json:"resolution"`
	Duration        dto.IntValue `json:"duration"`
	Ratio           string       `json:"ratio"`
	FramesPerSecond int          `json:"framespersecond"`
	ServiceTier     string       `json:"service_tier"`
	Tools           []struct {
		Type string `json:"type"`
	} `json:"tools"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		ToolUsage        struct {
			WebSearch int `json:"web_search"`
		} `json:"tool_usage"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction parses body, validates fields and sets default action.
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *taskdto.TaskError) {
	if taskErr := relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); taskErr != nil {
		return taskErr
	}
	// The relay applies mapping after validation. Resolve it on a copy here so
	// unsupported tiers return a local 400 before pricing or pre-charge.
	validationInfo := *info
	if info.ChannelMeta != nil {
		channelMeta := *info.ChannelMeta
		validationInfo.ChannelMeta = &channelMeta
	} else {
		validationInfo.ChannelMeta = &relaycommon.ChannelMeta{}
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if validationInfo.OriginModelName == "" {
		validationInfo.OriginModelName = req.Model
	}
	validationInfo.UpstreamModelName = validationInfo.OriginModelName
	if err := helper.ModelMappedHelper(c, &validationInfo, nil); err != nil {
		return service.TaskErrorWrapperLocal(err, "model_mapping_failed", http.StatusBadRequest)
	}
	if _, _, err := a.prepareRequest(&req, &validationInfo); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	return nil
}

// BuildRequestURL constructs the upstream URL.
func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/api/v3/contents/generations/tasks", a.baseURL), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// EstimateBilling uses the same effective payload and profile as validation.
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	body, profile, err := a.prepareRequest(&req, info)
	if err != nil || profile == nil {
		return nil
	}
	audio := body.GenerateAudio == nil || bool(*body.GenerateAudio)
	return profile.billingRatios(body.Resolution, hasVideoContent(body.Content), audio)
}

func hasVideoContent(content []ContentItem) bool {
	for _, item := range content {
		if item.Type == "video_url" || item.VideoURL != nil {
			return true
		}
	}
	return false
}

// BuildRequestBody converts request into Doubao specific format.
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	body, _, err := a.prepareRequest(&req, info)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	if !info.IsModelMapped {
		info.UpstreamModelName = body.Model
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := common.ReadResponseBody(resp)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// Parse Doubao response
	var dResp responsePayload
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if dResp.ID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return dResp.ID, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/api/v3/contents/generations/tasks/%s", baseUrl, taskID)

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq) (*requestPayload, error) {
	r := requestPayload{
		Model:   req.Model,
		Content: []ContentItem{},
	}

	// Add images if present
	if req.HasImage() {
		for _, imgURL := range req.Images {
			r.Content = append(r.Content, ContentItem{
				Type: "image_url",
				ImageURL: &MediaURL{
					URL: imgURL,
				},
			})
		}
	}

	if err := req.UnmarshalMetadata(&r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}
	// Metadata must not choose a different model than the one being billed.
	r.Model = req.Model

	if sec, _ := strconv.Atoi(req.Seconds); sec > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(sec))
	}

	r.Content = lo.Reject(r.Content, func(c ContentItem, _ int) bool { return c.Type == "text" })
	r.Content = append(r.Content, ContentItem{
		Type: "text",
		Text: req.Prompt,
	})

	return &r, nil
}

func (a *TaskAdaptor) prepareRequest(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*requestPayload, *videoModelProfile, error) {
	body, err := a.convertToRequestPayload(req)
	if err != nil {
		return nil, nil, err
	}
	if info.IsModelMapped {
		body.Model = info.UpstreamModelName
	}
	profile, name, ok := videoProfileForModels(body.Model, info.OriginModelName)
	if !ok {
		// Preserve custom endpoint passthrough. Unrecognized endpoint IDs do not
		// acquire capabilities or prices from an unrelated advertised model.
		return body, nil, nil
	}
	body.Resolution = strings.ToLower(strings.TrimSpace(body.Resolution))
	if body.Resolution == "" && strings.TrimSpace(req.Size) != "" {
		resolution, err := resolutionFromSize(req.Size)
		if err != nil {
			return nil, nil, err
		}
		body.Resolution = resolution
	}
	if err := applyPromptResolution(body); err != nil {
		return nil, nil, err
	}
	if err := profile.validateResolution(name, body.Resolution); err != nil {
		return nil, nil, err
	}
	return body, &profile, nil
}

var promptResolutionPattern = regexp.MustCompile(`(?:^|[[:space:]])--(?:rs|resolution)(?:[[:space:]]+([^[:space:]]+)|$)`)

// Ark also accepts resolution parameters in the prompt. Move them into the
// structured field so validation, reservation and the submitted tier agree.
// Reject conflicting declarations rather than guessing Ark's precedence.
func applyPromptResolution(body *requestPayload) error {
	resolution := body.Resolution
	for i := range body.Content {
		item := &body.Content[i]
		if item.Type != "text" {
			continue
		}
		matches := promptResolutionPattern.FindAllStringSubmatchIndex(item.Text, -1)
		if len(matches) == 0 {
			continue
		}
		var text strings.Builder
		last := 0
		for _, match := range matches {
			if match[2] < 0 {
				return fmt.Errorf("prompt resolution parameter requires a value")
			}
			value := strings.ToLower(item.Text[match[2]:match[3]])
			if strings.HasPrefix(value, "--") {
				return fmt.Errorf("prompt resolution parameter requires a value")
			}
			if resolution != "" && resolution != value {
				return fmt.Errorf("resolution conflicts with prompt resolution parameter")
			}
			resolution = value
			text.WriteString(item.Text[last:match[0]])
			last = match[1]
		}
		text.WriteString(item.Text[last:])
		item.Text = strings.TrimSpace(text.String())
	}
	body.Resolution = resolution
	return nil
}

func resolutionFromSize(size string) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(size))
	switch raw {
	case "480p", "720p", "1080p", "4k":
		return raw, nil
	}
	parts := strings.Split(strings.ReplaceAll(raw, "*", "x"), "x")
	if len(parts) != 2 {
		return "", fmt.Errorf("size must be a resolution tier or width x height")
	}
	width, errWidth := strconv.Atoi(parts[0])
	height, errHeight := strconv.Atoi(parts[1])
	if errWidth != nil || errHeight != nil || width <= 0 || height <= 0 {
		return "", fmt.Errorf("size must contain positive pixel dimensions")
	}
	// Use both orientations, matching Ark's 16:9 resolution presets.
	longSide := max(width, height)
	switch {
	case longSide >= 3840:
		return "4k", nil
	case longSide >= 1920:
		return "1080p", nil
	case longSide >= 1280:
		return "720p", nil
	default:
		return "480p", nil
	}
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	// Map Doubao status to internal status
	switch resTask.Status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = resTask.Content.VideoURL
		// 解析 usage 信息用于按倍率计费
		taskResult.CompletionTokens = resTask.Usage.CompletionTokens
		taskResult.TotalTokens = resTask.Usage.TotalTokens
		if taskResult.TotalTokens <= 0 {
			taskResult.TotalTokens = taskResult.CompletionTokens
		}
	case "failed", "cancelled", "expired":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = resTask.Error.Message
		if taskResult.Reason == "" {
			taskResult.Reason = "task " + resTask.Status
		}
	default:
		// Unknown status, treat as processing
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

// completionBillingRatios overlays only provider facts supported by the model.
// Keep the reservation snapshot immutable, including additional caller ratios.
// Legacy video_input combined multipliers cannot reveal reference-video state,
// so they retain their original multiplier rather than guessing at that state.
func completionBillingRatios(task *model.Task, result responseTask) map[string]float64 {
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.PerCallBilling || result.Status != "succeeded" {
		return nil
	}
	profile, _, ok := videoProfileForModels(task.Properties.UpstreamModelName, bc.OriginModelName, task.Properties.OriginModelName)
	if !ok {
		return nil
	}
	ratios := make(map[string]float64, len(bc.OtherRatios))
	for key, ratio := range bc.OtherRatios {
		ratios[key] = ratio
	}
	resolution := strings.ToLower(strings.TrimSpace(result.Content.Resolution))
	if resolution == "" {
		resolution = strings.ToLower(strings.TrimSpace(result.Resolution))
	}
	_, hasResolution := ratios["resolution"]
	videoRatio, hasVideoRatio := ratios["video_input"]
	if _, supported := profile.tiers[resolution]; supported && hasResolution && (!profile.referenceVideo || hasVideoRatio) {
		actual := profile.billingRatios(resolution, videoRatio < 1, true)
		ratios["resolution"] = actual["resolution"]
		if profile.referenceVideo {
			ratios["video_input"] = actual["video_input"]
		}
	}
	if _, hasAudio := ratios["generate_audio"]; hasAudio && profile.silentRatio > 0 && result.GenerateAudio != nil {
		ratios["generate_audio"] = 1
		if !*result.GenerateAudio {
			ratios["generate_audio"] = profile.silentRatio
		}
	}
	return ratios
}

// AdjustBillingOnComplete corrects capability multipliers inside the existing
// polling hook. Current model/group configuration and per-call price locks keep
// the same semantics as service.RecalculateTaskQuotaByTokens.
func (a *TaskAdaptor) AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int {
	if task == nil || taskResult == nil || taskResult.Status != model.TaskStatusSuccess {
		return 0
	}
	var result responseTask
	if err := common.Unmarshal(task.Data, &result); err != nil {
		return 0
	}
	ratios := completionBillingRatios(task, result)
	if ratios == nil {
		return 0
	}
	bc := task.PrivateData.BillingContext
	changed := false
	for key, ratio := range ratios {
		if ratio != bc.OtherRatios[key] {
			changed = true
			break
		}
	}
	if !changed {
		return 0
	}
	tokens := taskResult.TotalTokens
	if tokens <= 0 {
		tokens = taskResult.CompletionTokens
	}
	if tokens <= 0 {
		return 0
	}
	name := bc.OriginModelName
	if name == "" {
		name = task.Properties.OriginModelName
	}
	modelRatio, configured, _ := ratio_setting.GetModelRatio(name)
	if !configured || modelRatio <= 0 {
		return 0
	}
	group := task.Group
	if group == "" {
		if user, err := model.GetUserById(task.UserId, false); err == nil {
			group = user.Group
		}
	}
	if group == "" {
		return 0
	}
	groupRatio := ratio_setting.GetGroupRatio(group)
	if specialRatio, ok := ratio_setting.GetGroupGroupRatio(group, group); ok {
		groupRatio = specialRatio
	}
	priceData := types.PriceData{}
	priceData.ReplaceOtherRatios(ratios)
	validRatios := priceData.OtherRatios()
	keys := make([]string, 0, len(validRatios))
	for key := range validRatios {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	multiplier := 1.0
	for _, key := range keys {
		multiplier *= validRatios[key]
	}
	quota, _ := common.QuotaFromFloatChecked(float64(tokens) * modelRatio * groupRatio * multiplier)
	return quota
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var dResp responseTask
	if err := common.Unmarshal(originTask.Data, &dResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal doubao task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.SetMetadata("url", dResp.Content.VideoURL)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName

	switch dResp.Status {
	case "failed", "cancelled", "expired":
		message := dResp.Error.Message
		if message == "" {
			message = "task " + dResp.Status
		}
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: message,
			Code:    dResp.Error.Code,
		}
	}

	return common.Marshal(openAIVideo)
}
