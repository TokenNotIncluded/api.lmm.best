package controller

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/internal/agent"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
)

const (
	assistantReviewQueueCapacity = 64
	assistantReviewWorkerCount   = 2
	assistantReviewTimeout       = 25 * time.Second
	assistantReviewMaxContext    = 16 << 10
	// The answer is queued separately from the conversation. Keep it bounded
	// as well; otherwise a single long model response could occupy hundreds of
	// KiB in every waiting job even though the reviewer only receives a small
	// context window.
	assistantReviewMaxAnswer = 16 << 10
)

type assistantRequestReviewJob struct {
	UserID          int
	ConversationID  int64
	RequestID       string
	Group           string
	Intensity       string
	RouteGroup      string
	Model           string
	ReasoningEffort string
	Conversation    []assistantOpenAIMessage
	Answer          string
}

var (
	assistantReviewQueueOnce sync.Once
	// The queue is published atomically because an administrator may inspect
	// its health while the first sampled request is starting the workers.
	// Keeping the queue bounded is intentional: review work must never add
	// latency or an unbounded goroutine to the user-facing request.
	assistantReviewQueue         atomic.Pointer[chan assistantRequestReviewJob]
	assistantReviewQueueEnqueued atomic.Uint64
	assistantReviewQueueDropped  atomic.Uint64
	assistantReviewDropAlertAt   atomic.Int64
	assistantReviewSample        = sampleAssistantReview
)

type assistantReviewQueueStats struct {
	Capacity int    `json:"capacity"`
	Depth    int    `json:"depth"`
	Enqueued uint64 `json:"enqueued"`
	Dropped  uint64 `json:"dropped"`
}

func currentAssistantReviewQueue() chan assistantRequestReviewJob {
	queue := assistantReviewQueue.Load()
	if queue == nil {
		return nil
	}
	return *queue
}

func assistantReviewQueueStatsSnapshot() assistantReviewQueueStats {
	queue := currentAssistantReviewQueue()
	stats := assistantReviewQueueStats{
		Capacity: assistantReviewQueueCapacity,
		Enqueued: assistantReviewQueueEnqueued.Load(),
		Dropped:  assistantReviewQueueDropped.Load(),
	}
	if queue != nil {
		stats.Depth = len(queue)
	}
	return stats
}

func noteAssistantReviewQueueDrop() {
	dropped := assistantReviewQueueDropped.Add(1)
	// A saturated queue can be triggered by a burst across many users. Emit a
	// rate-limited cumulative alert so administrators can see the coverage gap
	// without turning an attack into synchronous log I/O on every request.
	now := time.Now().UnixNano()
	last := assistantReviewDropAlertAt.Load()
	if last != 0 && now-last < int64(time.Minute) {
		return
	}
	if !assistantReviewDropAlertAt.CompareAndSwap(last, now) {
		return
	}
	stats := assistantReviewQueueStatsSnapshot()
	common.SysError(fmt.Sprintf(
		"assistant request review queue saturated; dropped=%d enqueued=%d depth=%d capacity=%d",
		dropped, stats.Enqueued, stats.Depth, stats.Capacity,
	))
}

func offerAssistantReviewJob(job assistantRequestReviewJob) bool {
	queue := currentAssistantReviewQueue()
	if queue == nil {
		return false
	}
	select {
	case queue <- job:
		assistantReviewQueueEnqueued.Add(1)
		return true
	default:
		noteAssistantReviewQueueDrop()
		return false
	}
}

func assistantReviewPolicy(settings setting.AssistantSettings, group string) (float64, string, bool) {
	if !settings.ReviewEnabled {
		return 0, "off", false
	}
	probability := settings.ReviewProbability
	intensity := setting.AssistantReviewDefaultIntensity
	if policy, ok := setting.AssistantReviewPolicyForGroup(group); ok {
		probability = policy.Probability
		intensity = policy.Intensity
	}
	intensity = strings.ToLower(strings.TrimSpace(intensity))
	if !setting.IsAssistantReviewIntensity(intensity) || intensity == "off" || probability <= 0 {
		return 0, intensity, false
	}
	if probability > 100 {
		probability = 100
	}
	return probability, intensity, true
}

func sampleAssistantReview(probability float64) bool {
	if probability <= 0 {
		return false
	}
	if probability >= 100 {
		return true
	}
	// Draw in basis points to avoid a floating-point comparison at the
	// boundary. crypto/rand makes the decision independent of request order.
	limit := int64(probability * 100)
	draw, err := cryptorand.Int(cryptorand.Reader, big.NewInt(10000))
	return err == nil && draw.Int64() < limit
}

func assistantReviewGroup(c *gin.Context) string {
	if c == nil {
		return "default"
	}
	if group := strings.TrimSpace(c.GetString(assistantActorGroupKey)); group != "" {
		return group
	}
	if group := common.GetContextKeyString(c, constant.ContextKeyUserGroup); group != "" {
		return group
	}
	if group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup); group != "" {
		return group
	}
	return "default"
}

func assistantReviewAnswer(body []byte) string {
	response, err := parseAssistantResponse(body)
	if err != nil || len(response.Choices) == 0 {
		return ""
	}
	return truncateReviewBytes(
		model.RedactAssistantHistoryContent(assistantResponseContent(response.Choices[0].Message.Content)),
		assistantReviewMaxAnswer,
	)
}

func assistantReviewConversation(messages []assistantOpenAIMessage) []assistantOpenAIMessage {
	// A review is about the latest request/answer. Walk backwards so a long
	// historical transcript cannot consume the bounded context before the turn
	// that was actually sampled; reverse once at the end to preserve chronology
	// for the reviewer model.
	reversed := make([]assistantOpenAIMessage, 0, len(messages))
	remaining := assistantReviewMaxContext
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		content := model.RedactAssistantHistoryContent(message.Content)
		if content == "" || remaining <= 0 {
			continue
		}
		if len(content) > remaining {
			content = truncateReviewBytes(content, remaining)
		}
		remaining -= len(content)
		reversed = append(reversed, assistantOpenAIMessage{Role: message.Role, Content: content})
	}
	result := make([]assistantOpenAIMessage, len(reversed))
	for index := range reversed {
		result[len(reversed)-1-index] = reversed[index]
	}
	return result
}

func truncateReviewBytes(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	// Do not split a UTF-8 code point at the review boundary. The input is
	// normally valid UTF-8, but the loop also makes malformed provider text
	// fail closed rather than retaining an invalid suffix.
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func enqueueAssistantRequestReview(c *gin.Context, settings setting.AssistantSettings, conversation []assistantOpenAIMessage, body []byte) {
	if c == nil || len(conversation) == 0 || len(body) == 0 {
		return
	}
	probability, intensity, enabled := assistantReviewPolicy(settings, assistantReviewGroup(c))
	if !enabled || assistantReviewSample(probability) == false {
		return
	}
	userID := assistantActorUserID(c)
	if userID <= 0 {
		return
	}
	answer := assistantReviewAnswer(body)
	if answer == "" {
		return
	}
	reviewGroup := strings.TrimSpace(settings.ReviewGroup)
	if reviewGroup == "" {
		reviewGroup = setting.DefaultAssistantReviewGroup
	}
	job := assistantRequestReviewJob{
		UserID:          userID,
		ConversationID:  assistantHistoryConversationID(c),
		RequestID:       strings.TrimSpace(c.GetString(common.RequestIdKey)),
		Group:           assistantReviewGroup(c),
		Intensity:       intensity,
		RouteGroup:      reviewGroup,
		Model:           strings.TrimSpace(settings.ReviewModel),
		ReasoningEffort: assistantReviewReasoningEffort(settings),
		Conversation:    assistantReviewConversation(conversation),
		Answer:          answer,
	}
	if job.Model == "" || len(job.Conversation) == 0 {
		return
	}
	startAssistantReviewWorkers()
	// Review is deliberately best-effort. A burst must never add latency or
	// unbounded goroutines to the user-facing assistant request. A bounded,
	// rate-limited alert above makes dropped coverage visible to administrators.
	_ = offerAssistantReviewJob(job)
}

func startAssistantReviewWorkers() {
	assistantReviewQueueOnce.Do(func() {
		queue := make(chan assistantRequestReviewJob, assistantReviewQueueCapacity)
		assistantReviewQueue.Store(&queue)
		for index := 0; index < assistantReviewWorkerCount; index++ {
			go assistantReviewWorker(index)
		}
	})
}

func assistantReviewWorker(_ int) {
	queue := currentAssistantReviewQueue()
	if queue == nil {
		return
	}
	for job := range queue {
		if err := runAssistantRequestReview(job); err != nil {
			common.SysError(fmt.Sprintf("assistant request review failed: %v", err))
		}
	}
}

type assistantReviewDecision struct {
	Violation   bool     `json:"violation"`
	Abuse       bool     `json:"abuse"`
	Rules       []string `json:"rules"`
	Explanation string   `json:"explanation"`
}

func parseAssistantReviewDecision(body []byte) (assistantReviewDecision, error) {
	text := strings.TrimSpace(string(body))
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end <= start {
		return assistantReviewDecision{}, errors.New("review model did not return a JSON object")
	}
	var decision assistantReviewDecision
	if err := json.Unmarshal([]byte(text[start:end+1]), &decision); err != nil {
		return assistantReviewDecision{}, errors.New("review model returned invalid JSON")
	}
	if !decision.Violation {
		return assistantReviewDecision{}, nil
	}
	decision.Explanation = model.RedactAssistantHistoryContent(decision.Explanation)
	if strings.TrimSpace(decision.Explanation) == "" {
		decision.Explanation = "可能触犯平台使用规则，需要管理员复核。"
	}
	if len(decision.Rules) == 0 {
		decision.Rules = []string{"可能违反平台安全或滥用规则"}
	}
	return decision, nil
}

func reviewPrompt(job assistantRequestReviewJob) (string, string) {
	intensityInstruction := map[string]string{
		"low":      "仅标记明确的恶意滥用、绕过安全控制、欺诈或违法请求；普通技术讨论不要标记。",
		"standard": "按平台安全、滥用、隐私和服务条款判断，保持谨慎并避免把正常技术讨论误报。",
		"high":     "进行严格审查，特别关注安全控制绕过、批量滥用、欺诈、隐私窃取和规避检测；仍需给出可解释证据。",
	}[job.Intensity]
	if intensityInstruction == "" {
		intensityInstruction = "按平台安全和滥用规则判断。"
	}
	system := "你是 LMM 平台的后台请求审查员。只输出一个 JSON 对象，不要 Markdown，不要复述原文。字段必须是：violation（布尔值）、abuse（布尔值）、rules（字符串数组）、explanation（字符串）。若 violation=false，rules 必须为空数组且 explanation 必须为空字符串；若 violation=true，指出可能触犯的规则，并说明是否属于滥用。" + intensityInstruction
	var user strings.Builder
	user.Grow(len(job.Answer) + assistantReviewMaxContext + 128)
	user.WriteString("请审查下面这次已脱敏的对话和助手答复。不要根据用户身份、邮箱、分组名称本身作判断。\n\n对话：\n")
	for _, message := range job.Conversation {
		user.WriteString(message.Role)
		user.WriteString(": ")
		user.WriteString(message.Content)
		user.WriteByte('\n')
	}
	user.WriteString("\n助手答复：\n")
	user.WriteString(job.Answer)
	return system, user.String()
}

func runAssistantRequestReview(job assistantRequestReviewJob) error {
	root, err := loadAssistantBillingUser()
	if err != nil || root == nil {
		return errors.New("assistant review billing account is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), assistantReviewTimeout)
	defer cancel()
	ginContext, recorder, err := newAssistantReviewContext(ctx, root, job.RouteGroup)
	if err != nil {
		return err
	}
	systemPrompt, userPrompt := reviewPrompt(job)
	request := assistantOpenAIRequest{
		Model:           job.Model,
		Messages:        []assistantOpenAIMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}},
		Stream:          false,
		Temperature:     0,
		MaxTokens:       320,
		ReasoningEffort: job.ReasoningEffort,
	}
	status, body, relayErr := relayAssistantTurnWithRetryUsing(ginContext, request, job.RequestID, 0, relayAssistantTurn)
	if relayErr != nil {
		return saveFailedAssistantRequestReview(job, relayErr)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return saveFailedAssistantRequestReview(job, fmt.Errorf("review model returned status %d", status))
	}
	decision, parseErr := parseAssistantReviewDecision([]byte(agent.Text(mustReviewResponseContent(body))))
	if parseErr != nil {
		return saveFailedAssistantRequestReview(job, parseErr)
	}
	review := &model.AssistantRequestReview{
		UserID: job.UserID, ConversationID: job.ConversationID, RequestID: job.RequestID,
		Group: job.Group, ReviewModel: job.Model, Intensity: job.Intensity,
		Status: model.AssistantRequestReviewStatusCompleted, Violation: decision.Violation,
		Abuse: decision.Abuse, Explanation: decision.Explanation,
		RequestPreview: reviewRequestPreview(job.Conversation), ResponsePreview: boundedReviewPreview(job.Answer),
	}
	if err := model.SaveAssistantRequestReview(review, decision.Rules); err != nil {
		return err
	}
	_ = recorder
	return nil
}

func mustReviewResponseContent(body []byte) json.RawMessage {
	response, err := parseAssistantResponse(body)
	if err != nil || len(response.Choices) == 0 {
		return nil
	}
	return response.Choices[0].Message.Content
}

func reviewRequestPreview(messages []assistantOpenAIMessage) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" {
			return boundedReviewPreview(messages[index].Content)
		}
	}
	return ""
}

func boundedReviewPreview(value string) string {
	value = model.RedactAssistantHistoryContent(value)
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > model.AssistantRequestReviewMaxPreview {
		runes = runes[:model.AssistantRequestReviewMaxPreview]
	}
	return string(runes)
}

func saveFailedAssistantRequestReview(job assistantRequestReviewJob, failure error) error {
	review := &model.AssistantRequestReview{
		UserID: job.UserID, ConversationID: job.ConversationID, RequestID: job.RequestID,
		Group: job.Group, ReviewModel: job.Model, Intensity: job.Intensity,
		Status: model.AssistantRequestReviewStatusFailed, ErrorMessage: boundedReviewPreview(failure.Error()),
		RequestPreview: reviewRequestPreview(job.Conversation), ResponsePreview: boundedReviewPreview(job.Answer),
	}
	return model.SaveAssistantRequestReview(review, nil)
}

func newAssistantReviewContext(ctx context.Context, root *model.User, routeGroup string) (*gin.Context, *httptest.ResponseRecorder, error) {
	routeGroup = strings.TrimSpace(routeGroup)
	if routeGroup == "" {
		routeGroup = setting.DefaultAssistantReviewGroup
	}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "http://assistant-review/v1/chat/completions", io.NopCloser(strings.NewReader("{}")))
	ginContext.Request = ginContext.Request.WithContext(ctx)
	ginContext.Set(common.RequestIdKey, common.NewRequestId())
	ginContext.Set("id", root.Id)
	ginContext.Set("username", root.Username)
	ginContext.Set("role", root.Role)
	ginContext.Set("group", routeGroup)
	root.ToBaseUser().WriteContext(ginContext)
	token := &model.Token{UserId: root.Id, Name: "assistant-review", Group: routeGroup, UnlimitedQuota: true}
	if err := middleware.SetupContextForToken(ginContext, token); err != nil {
		return nil, nil, err
	}
	common.SetContextKey(ginContext, constant.ContextKeyUsingGroup, routeGroup)
	common.SetContextKey(ginContext, constant.ContextKeyRequestStartTime, time.Now())
	return ginContext, recorder, nil
}
