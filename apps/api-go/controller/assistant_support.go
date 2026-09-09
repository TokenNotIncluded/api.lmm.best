package controller

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

const assistantSupportGuardKey = "assistant_support_guard_enabled"

func assistantSupportError(c *gin.Context, err error) {
	status, code, message := http.StatusInternalServerError, "ASSISTANT_SUPPORT_UNAVAILABLE", "人工支持暂时不可用，请稍后重试。"
	switch {
	case errors.Is(err, model.ErrAssistantSupportNotFound), errors.Is(err, model.ErrAssistantConversationNotFound):
		status, code, message = http.StatusNotFound, "ASSISTANT_SUPPORT_NOT_FOUND", "人工支持请求或对话不存在。"
	case errors.Is(err, model.ErrAssistantSupportForbidden):
		status, code, message = http.StatusForbidden, "ASSISTANT_SUPPORT_FORBIDDEN", "当前账号无权进行此操作。"
	case errors.Is(err, model.ErrAssistantSupportIneligible):
		status, code, message = http.StatusForbidden, "ASSISTANT_SUPPORT_RECHARGE_REQUIRED", "预约技术支持需要已完成的充值记录，你仍可随时转人工。"
	case errors.Is(err, model.ErrAssistantSupportConflict):
		status, code, message = http.StatusConflict, "ASSISTANT_SUPPORT_CONFLICT", "请求状态已变化，请刷新后重试。"
	case errors.Is(err, model.ErrAssistantSupportInvalid):
		status, code, message = http.StatusUnprocessableEntity, "ASSISTANT_SUPPORT_INVALID", "请检查问题描述及预约时间；预约时间须在未来 90 天内。"
	}
	writeAssistantError(c, status, code, errors.New(message))
}

func assistantSupportSession(c *gin.Context) bool {
	if c.GetBool("use_access_token") || assistantActorUserID(c) <= 0 {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_SESSION_REQUIRED", errors.New("人工支持需要浏览器登录会话。"))
		return false
	}
	return true
}

func assistantSupportID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		assistantSupportError(c, model.ErrAssistantSupportNotFound)
		return 0, false
	}
	return id, true
}

func GetAssistantSupportEligibility(c *gin.Context) {
	if !assistantSupportSession(c) {
		return
	}
	eligible, err := model.IsAssistantSupportEligible(assistantActorUserID(c))
	if err != nil {
		assistantSupportError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"eligible": eligible}})
}

func GetAssistantSupportSelf(c *gin.Context) {
	if !assistantSupportSession(c) {
		return
	}
	conversationID, err := strconv.ParseInt(c.DefaultQuery("conversation_id", "0"), 10, 64)
	if err != nil || conversationID < 0 {
		assistantSupportError(c, model.ErrAssistantSupportInvalid)
		return
	}
	var request *model.AssistantSupportRequest
	if conversationID == 0 {
		request, err = model.GetActiveAssistantSupportRequest(assistantActorUserID(c))
	} else {
		request, err = model.GetLatestAssistantSupportRequest(assistantActorUserID(c), conversationID)
	}
	if err != nil {
		assistantSupportError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"request": request}})
}

func CreateAssistantSupport(c *gin.Context) {
	if !assistantSupportSession(c) {
		return
	}
	var input struct {
		ConversationID int64  `json:"conversation_id"`
		Kind           string `json:"kind"`
		Topic          string `json:"topic"`
		PreferredTime  string `json:"preferred_time"`
		ScheduledAt    int64  `json:"scheduled_at"`
	}
	if c.ShouldBindJSON(&input) != nil {
		assistantSupportError(c, model.ErrAssistantSupportInvalid)
		return
	}
	request, created, err := model.CreateAssistantSupportRequest(assistantActorUserID(c), input.ConversationID, input.Kind, input.Topic, input.PreferredTime, input.ScheduledAt)
	if err != nil {
		assistantSupportError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"request": request, "created": created}})
}

func GetAssistantSupport(c *gin.Context) {
	if !assistantSupportSession(c) {
		return
	}
	id, ok := assistantSupportID(c)
	if !ok {
		return
	}
	actorID := assistantActorUserID(c)
	request, err := model.GetAssistantSupportRequest(actorID, id)
	if err != nil {
		assistantSupportError(c, err)
		return
	}
	messages := []model.AssistantHistoryMessageView{}
	// An unassigned administrator can inspect queue metadata. Conversation
	// contents become available only after that administrator claims the request.
	if request.UserId == actorID || request.AssignedAdminId == actorID {
		messages, err = model.GetAssistantSupportMessages(actorID, id)
		if err != nil {
			assistantSupportError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"request": request, "messages": messages}})
}

func SendAssistantSupportMessage(c *gin.Context) {
	if !assistantSupportSession(c) {
		return
	}
	id, ok := assistantSupportID(c)
	if !ok {
		return
	}
	var input struct {
		Content string `json:"content"`
	}
	if c.ShouldBindJSON(&input) != nil {
		assistantSupportError(c, model.ErrAssistantSupportInvalid)
		return
	}
	message, err := model.AddAssistantSupportMessage(assistantActorUserID(c), id, input.Content)
	if err != nil {
		assistantSupportError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"message": message}})
}

func AcceptAssistantSupport(c *gin.Context) {
	if !assistantSupportSession(c) {
		return
	}
	id, ok := assistantSupportID(c)
	if !ok {
		return
	}
	request, err := model.AcceptAssistantSupportRequest(assistantActorUserID(c), id)
	if err != nil {
		assistantSupportError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"request": request}})
}

func CloseAssistantSupport(c *gin.Context) {
	if !assistantSupportSession(c) {
		return
	}
	id, ok := assistantSupportID(c)
	if !ok {
		return
	}
	var input struct {
		Cancel bool `json:"cancel"`
	}
	if c.ShouldBindJSON(&input) != nil {
		assistantSupportError(c, model.ErrAssistantSupportInvalid)
		return
	}
	request, err := model.CloseAssistantSupportRequest(assistantActorUserID(c), id, input.Cancel)
	if err != nil {
		assistantSupportError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"request": request}})
}

var assistantSupportQuotedText = regexp.MustCompile("(?s)\"[^\"]*\"|'[^']*'|`[^`]*`|“[^”]*”|‘[^’]*’|「[^」]*」|『[^』]*』")
var assistantTransferCommand = regexp.MustCompile(`(?i)(?:^|[，,。！!\n])\s*(?:请|請|麻烦|麻煩|现在|現在|马上|馬上|直接|帮我|幫我|我要|我想|我需要|给我|給我|请你|請你|一下|\s)*(?:转人工|轉人工|转接人工|轉接人工|人工客服|找人工客服|找人工技术支持|找人工技術支持|联系人工客服|聯繫人工客服)(?:吧|一下|客服|技术支持|技術支持|服务|服務|\s)*(?:$|[，,。！!])`)
var assistantEnglishTransferCommand = regexp.MustCompile(`(?i)^(?:please\s+)?(?:(?:transfer|connect|switch)\s+me\s+to\s+(?:a\s+)?human(?:\s+(?:agent|support))?|(?:i\s+(?:want|need)\s+to\s+)?(?:speak|talk)\s+to\s+(?:a\s+)?human(?:\s+(?:agent|support))?)[.!]*$`)

func assistantExplicitHumanTransferRequest(text string) bool {
	text = strings.TrimSpace(assistantSupportQuotedText.ReplaceAllString(text, ""))
	return assistantTransferCommand.MatchString(text) || assistantEnglishTransferCommand.MatchString(text)
}

// RouteAssistantHumanSupport runs before model availability and billing. The
// current browser message is the only client text allowed to initiate a handoff.
func RouteAssistantHumanSupport(c *gin.Context) {
	if !assistantSupportSession(c) {
		return
	}
	var input assistantChatInput
	if err := common.UnmarshalBodyReusable(c, &input); err != nil {
		if common.IsRequestBodyTooLargeError(err) {
			writeAssistantError(c, http.StatusRequestEntityTooLarge, "ASSISTANT_REQUEST_TOO_LARGE", common.ErrRequestBodyTooLarge)
		} else {
			assistantSupportError(c, model.ErrAssistantSupportInvalid)
		}
		return
	}
	if input.ConversationID < 0 {
		assistantSupportError(c, model.ErrAssistantSupportInvalid)
		return
	}
	message := strings.TrimSpace(input.Message)
	if len(input.Messages) > 0 {
		_, latest, err := normalizeAssistantConversation(input)
		if err != nil {
			assistantSupportError(c, model.ErrAssistantSupportInvalid)
			return
		}
		message = latest
	}
	if message == "" || utf8.RuneCountInString(message) > assistantMessageMaxRunes {
		assistantSupportError(c, model.ErrAssistantSupportInvalid)
		return
	}
	actorID := assistantActorUserID(c)
	c.Set(assistantActorUserIDKey, actorID)
	c.Set(assistantSupportGuardKey, true)
	request, err := model.GetActiveAssistantSupportRequest(actorID)
	if err != nil {
		assistantSupportError(c, err)
		return
	}
	explicitTransfer := assistantExplicitHumanTransferRequest(message)
	if explicitTransfer {
		request, _, err = model.CreateAssistantSupportRequest(actorID, input.ConversationID, model.AssistantSupportKindHandoff, message, "", 0)
		if err != nil {
			assistantSupportError(c, err)
			return
		}
	}
	if request == nil || (!explicitTransfer && ((input.ConversationID > 0 && request.ConversationId != input.ConversationID) || !assistantSupportPausesAI(request))) {
		c.Next()
		return
	}
	if _, err := model.AddAssistantSupportMessage(actorID, request.Id, message); err != nil {
		assistantSupportError(c, err)
		return
	}
	c.Set("assistant_history_conversation_id", request.ConversationId)
	c.Set("assistant_history_pre_recorded", true)
	body := assistantSupportCompletion(request)
	writeAssistantSupportCompletion(c, body)
}

func assistantSupportPausesAI(request *model.AssistantSupportRequest) bool {
	return request != nil && (request.Status == model.AssistantSupportStatusAccepted || (request.Status == model.AssistantSupportStatusPending && request.Kind == model.AssistantSupportKindHandoff))
}

func assistantSupportCompletion(request *model.AssistantSupportRequest) []byte {
	content := "转人工请求已提交，管理员接单后会直接在本对话回复。你可以继续补充问题。"
	if request.Status == model.AssistantSupportStatusAccepted {
		content = "消息已发送给接待管理员，请在本对话等待回复。"
	}
	body, _ := json.Marshal(gin.H{"choices": []any{gin.H{"message": gin.H{"role": "assistant", "content": content}, "finish_reason": "stop"}}, "support_request": request, "lmm_assistant_history": gin.H{"conversation_id": request.ConversationId, "privacy_notice": model.AssistantHistoryPrivacyNotice}})
	return body
}

func writeAssistantSupportCompletion(c *gin.Context, body []byte) {
	if session := assistantStreamSessionFrom(c); session != nil {
		_ = session.resetContent()
		response, _ := parseAssistantResponse(body)
		// The handoff receipt is server-authored and must bypass the model delta guard.
		session.setSupportCheck(nil)
		if len(response.Choices) > 0 {
			_ = session.appendContent(assistantResponseContent(response.Choices[0].Message.Content))
		}
		_ = session.finish(body)
	} else {
		c.Data(http.StatusOK, "application/json; charset=utf-8", body)
	}
	c.Abort()
}

func assistantSupportGuardError(c *gin.Context) error {
	if c == nil || !c.GetBool(assistantSupportGuardKey) {
		return nil
	}
	blocked, err := model.BlockAssistantAIForSupport(assistantActorUserID(c), assistantHistoryConversationID(c))
	if err != nil {
		return err
	}
	if blocked {
		return model.ErrAssistantSupportAIBlocked
	}
	return nil
}

func assistantSupportInterruptedBody(c *gin.Context, err error) []byte {
	c.Set("assistant_history_pre_recorded", true)
	c.Set("assistant_support_response_replaced", true)
	if errors.Is(err, model.ErrAssistantSupportAIBlocked) {
		if request, lookupErr := model.GetActiveAssistantSupportRequest(assistantActorUserID(c)); lookupErr == nil && assistantSupportPausesAI(request) && (assistantHistoryConversationID(c) == 0 || request.ConversationId == assistantHistoryConversationID(c)) {
			if latest := c.GetString("assistant_history_latest_message"); latest != "" && !c.GetBool("assistant_support_message_forwarded") {
				if _, sendErr := model.AddAssistantSupportMessage(assistantActorUserID(c), request.Id, latest); sendErr != nil {
					body, _ := json.Marshal(gin.H{"success": false, "code": "ASSISTANT_SUPPORT_STATE_CHANGED", "message": "人工支持状态已变化，当前消息未发送，请刷新后重试。"})
					return body
				}
				c.Set("assistant_support_message_forwarded", true)
			}
			c.Set("assistant_history_conversation_id", request.ConversationId)
			return assistantSupportCompletion(request)
		}
	}
	body, _ := json.Marshal(gin.H{"success": false, "code": "ASSISTANT_SUPPORT_STATE_UNAVAILABLE", "message": "暂时无法确认人工支持状态，AI 已暂停，请刷新后重试。"})
	return body
}

func assistantHumanSupportInterrupted(c *gin.Context) bool {
	err := assistantSupportGuardError(c)
	if err == nil {
		return false
	}
	if session := assistantStreamSessionFrom(c); session != nil {
		_ = session.resetContent()
	}
	if !errors.Is(err, model.ErrAssistantSupportAIBlocked) {
		writeAssistantError(c, http.StatusServiceUnavailable, "ASSISTANT_SUPPORT_STATE_UNAVAILABLE", errors.New("暂时无法确认人工支持状态，AI 已暂停，请刷新后重试。"))
		return true
	}
	writeAssistantSupportCompletion(c, assistantSupportInterruptedBody(c, err))
	return true
}

var assistantSupportBookingRequest = regexp.MustCompile(`(?i)(?:预约|預約).{0,60}(?:人工|技术支持|技術支持)|\b(?:book|schedule)\b.{0,60}(?:human|technical).{0,20}(?:support|appointment)`)

var assistantSupportBookingNegation = regexp.MustCompile(`(?i)(?:不想|不需要|无需|無需|不要|不用|别|別|勿|不打算|没打算|沒打算|do not want to|don't want to|do not need to|don't need to|no need to|never|cancel).*(?:预约|預約|book|schedule|appointment)`)

func assistantSupportBookingDecision(text string) int {
	text = strings.ToLower(strings.TrimSpace(assistantSupportQuotedText.ReplaceAllString(text, "")))
	if assistantSupportBookingNegation.MatchString(text) {
		return -1
	}
	switch strings.Trim(text, "。.!！ ") {
	case "取消", "算了", "不用了", "先不用", "暂时不用", "暫時不用", "不要了", "不用人工了", "不需要人工", "cancel", "never mind", "nevermind", "not now":
		return -1
	}
	for _, negative := range []string{"取消预约", "取消預約", "不要预约", "不要預約", "不预约", "不預約", "不用预约", "不用預約", "暂不预约", "暫不預約", "先不预约", "先不預約", "cancel the appointment", "cancel my appointment", "don't book", "do not book"} {
		if strings.Contains(text, negative) {
			return -1
		}
	}
	// Questions in a separately supplied issue description do not revoke an
	// explicit booking command (for example: "预约技术支持，怎么接入 API").
	questionText := text
	if request := assistantSupportBookingRequest.FindStringIndex(text); request != nil {
		if boundary := strings.IndexAny(text[request[1]:], "，,。;；：:\n"); boundary >= 0 {
			questionText = text[:request[1]+boundary]
		}
	}
	for _, question := range []string{"如何", "怎么", "怎麼", "怎样", "怎樣", "能否", "能不能", "可不可以", "可以预约吗", "可以預約嗎", "可以吗", "可以嗎", "what", "how", "如果", "假如", "假设", "假設", " if ", "是否", "这句话", "這句話", "这三个字", "這三個字", "?", "？"} {
		if strings.Contains(questionText, question) {
			return 0
		}
	}
	if assistantSupportBookingRequest.MatchString(text) {
		return 1
	}
	for _, request := range []string{"预约人工", "預約人工", "预约技术支持", "預約技術支持", "book technical support", "book human support", "schedule technical support", "schedule human support", "book a technical support appointment"} {
		if strings.Contains(text, request) {
			return 1
		}
	}
	return 0
}

func assistantSupportBookingAuthorized(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if decision := assistantSupportBookingDecision(c.GetString("assistant_history_latest_message")); decision != 0 {
		return decision > 0
	}
	conversationID := assistantHistoryConversationID(c)
	if conversationID <= 0 {
		return false
	}
	messages, err := model.LoadAssistantConversationMessages(assistantActorUserID(c), conversationID, 24)
	if err != nil {
		return false
	}
	latestRequest, err := model.GetLatestAssistantSupportRequest(assistantActorUserID(c), conversationID)
	if err != nil {
		return false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		// A completed or cancelled request consumed the earlier booking intent.
		// A new explicit request after that boundary can authorize another booking.
		if latestRequest != nil && latestRequest.ClosedAt > 0 && messages[i].Id <= latestRequest.ClosedHistoryMessageId {
			break
		}
		if messages[i].Role != model.AssistantHistoryRoleUser {
			continue
		}
		if decision := assistantSupportBookingDecision(messages[i].Content); decision != 0 {
			return decision > 0
		}
	}
	return false
}

func executeAssistantHumanSupportStatusTool(c *gin.Context) map[string]any {
	actorID := assistantActorUserID(c)
	eligible, err := model.IsAssistantSupportEligible(actorID)
	if err != nil {
		return map[string]any{"ok": false, "error": "support eligibility is unavailable"}
	}
	request, err := model.GetActiveAssistantSupportRequest(actorID)
	if err != nil {
		return map[string]any{"ok": false, "error": "support request is unavailable"}
	}
	return map[string]any{"ok": true, "eligible": eligible, "request": request, "handoff_available": true, "current_time_utc": time.Now().UTC().Format(time.RFC3339), "next_step": "A signed-in user can say 转人工 at any time. Eligible users may explicitly ask to book technical support; collect their topic, future date/time and timezone before booking. Appointments are requests awaiting administrator acceptance, not a guaranteed time slot."}
}

func executeAssistantBookTechnicalSupportTool(c *gin.Context, input map[string]any) map[string]any {
	if !assistantSupportBookingAuthorized(c) {
		return map[string]any{"ok": false, "status": "explicit_request_required", "error": "The signed-in user must explicitly request a technical support appointment."}
	}
	scheduledAt, valid := inputNumber(input, "scheduled_at")
	if !valid || scheduledAt <= 0 || scheduledAt >= float64(math.MaxInt64) || math.Trunc(scheduledAt) != scheduledAt {
		return map[string]any{"ok": false, "error": "scheduled_at must be an integer Unix timestamp in seconds"}
	}
	request, created, err := model.CreateAssistantSupportRequest(assistantActorUserID(c), assistantHistoryConversationID(c), model.AssistantSupportKindAppointment, inputString(input, "topic"), inputString(input, "preferred_time"), int64(scheduledAt))
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	c.Set("assistant_history_conversation_id", request.ConversationId)
	message := "Appointment request submitted to all administrators' in-site todos. The requested time awaits acceptance; continue in this conversation."
	if !created {
		message = "An active support request already exists. No new appointment was booked and no requested time was changed. Report the existing request's actual kind, status and time."
	}
	return map[string]any{"ok": true, "created": created, "request": request, "message": message}
}
