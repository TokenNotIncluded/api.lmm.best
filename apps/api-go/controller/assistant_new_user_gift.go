package controller

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func GetAssistantNewUserGift(c *gin.Context) {
	gift, err := model.GetAssistantNewUserGift(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gift)
}

func ClaimAssistantNewUserGift(c *gin.Context) {
	gift, alreadyClaimed, err := model.ClaimAssistantNewUserGift(c.GetInt("id"))
	if err != nil {
		status := http.StatusConflict
		code := "ASSISTANT_NEW_USER_GIFT_UNAVAILABLE"
		if !errors.Is(err, model.ErrAssistantGiftUnavailable) {
			common.ApiError(c, err)
			return
		}
		c.AbortWithStatusJSON(status, gin.H{"success": false, "code": code, "message": err.Error()})
		return
	}
	if !alreadyClaimed {
		model.RecordLog(c.GetInt("id"), model.LogTypeTopup, fmt.Sprintf("领取 AI 新用户礼包，获得额度 %s", logger.LogQuota(gift.Quota)))
	}
	common.ApiSuccess(c, gin.H{"gift": gift, "already_claimed": alreadyClaimed})
}

func GetAssistantJourney(c *gin.Context) {
	journey, err := model.GetAssistantJourney(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, journey)
}

func assistantConversationEvidence(c *gin.Context) (turns int, runes int) {
	if c == nil {
		return 0, 0
	}
	// Compression markers and omitted excerpts are model context only. Count
	// substantive evidence from the original owned text so summaries cannot
	// manufacture turns or inflate a short user's request into reward merit.
	raw, exists := c.Get(assistantPolicyConversationKey)
	if !exists {
		raw, exists = c.Get("assistant_conversation")
	}
	if !exists {
		return 0, 0
	}
	messages, ok := raw.([]assistantOpenAIMessage)
	if !ok {
		return 0, 0
	}
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		content := strings.TrimSpace(message.Content)
		length := utf8.RuneCountInString(content)
		if length < 4 {
			continue
		}
		turns++
		runes += length
	}
	return turns, runes
}

func executeAssistantNewUserGiftTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	amount, ok := inputNumber(input, "amount_cents")
	if !ok || math.IsNaN(amount) || math.IsInf(amount, 0) || math.Trunc(amount) != amount {
		return map[string]any{"ok": false, "status": "invalid_decision", "error": "amount_cents must be an integer from 0 to 1000"}
	}
	turns, runes := assistantConversationEvidence(c)
	gift, created, err := model.DecideAssistantNewUserGift(
		userID,
		assistantHistoryConversationID(c),
		int(amount),
		inputString(input, "reason"),
		turns,
		runes,
		c.ClientIP(),
	)
	if err != nil {
		reasonCode := model.AssistantGiftErrorCode(err)
		switch {
		case errors.Is(err, model.ErrAssistantGiftIneligible), errors.Is(err, model.ErrAssistantGiftAbuse):
			if reasonCode == "" {
				reasonCode = "ineligible"
			}
			return map[string]any{"ok": false, "status": "ineligible", "reason_code": reasonCode, "error": "this account is not eligible for a new-user gift"}
		case errors.Is(err, model.ErrAssistantGiftInvalid):
			if reasonCode == "" {
				reasonCode = "invalid_decision"
			}
			if reasonCode == "insufficient_conversation" {
				return map[string]any{"ok": false, "status": "more_conversation_needed", "reason_code": reasonCode, "error": "continue the conversation with at least two substantive user turns before evaluating the one-time gift"}
			}
			return map[string]any{"ok": false, "status": "invalid_decision", "reason_code": reasonCode, "error": "the one-time gift decision was invalid"}
		default:
			return map[string]any{"ok": false, "status": "unavailable", "error": "the gift decision could not be saved"}
		}
	}
	if gift.Status == model.AssistantGiftOffered && c != nil {
		c.Set(assistantClientActionKey, map[string]any{
			"type":         "new_user_gift",
			"amount_cents": gift.AmountCents,
			"reason":       gift.Reason,
			"status":       gift.Status,
		})
	}
	return map[string]any{
		"ok":           true,
		"created":      created,
		"status":       gift.Status,
		"amount_cents": gift.AmountCents,
		"reason":       gift.Reason,
		"next_step":    "The user claims an offered gift from the gift shown in the chat. Never claim it for them.",
	}
}
