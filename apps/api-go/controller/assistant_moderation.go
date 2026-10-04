package controller

import (
	"context"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// Browser turn IDs survive transport retries. The billing user's relay ID and
// routing group must never become the subject of a user's moderation review.
func assistantModerationRequestID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if turnID := strings.TrimSpace(c.GetString("assistant_client_turn_id")); turnID != "" {
		return "assistant:" + turnID
	}
	return strings.TrimSpace(c.GetString(common.RequestIdKey))
}

func queueAssistantModerationText(c *gin.Context, source, text string) {
	if c == nil || strings.TrimSpace(text) == "" {
		return
	}
	userID := assistantActorUserID(c)
	group := strings.TrimSpace(c.GetString(assistantActorGroupKey))
	requestID := assistantModerationRequestID(c)
	if userID <= 0 || group == "" || requestID == "" {
		return
	}
	ctx := context.Background()
	if c.Request != nil {
		ctx = c.Request.Context()
	}
	if err := service.QueueModeration(ctx, service.ModerationSubmission{
		UserID: userID, Group: group, RequestID: requestID, Source: source, Text: text,
	}); err != nil {
		// No input, credential, provider body or resulting classification belongs
		// in request logs. Queue failures never replace the assistant's answer.
		common.SysError("assistant_moderation_enqueue_unavailable")
	}
}

func queueAssistantModerationResponse(c *gin.Context, status int, body []byte) {
	if status < 200 || status >= 300 {
		return
	}
	response, err := parseAssistantResponse(body)
	if err != nil || len(response.Choices) == 0 {
		return
	}
	content := assistantResponseContent(response.Choices[0].Message.Content)
	if canonical, ok := c.Get("assistant_history_canonical_content"); ok {
		if saved, ok := canonical.(string); ok {
			content = saved
		}
	}
	queueAssistantModerationText(c, service.ModerationSourceAssistantOutput, content)
}
