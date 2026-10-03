/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package controller

import (
	"errors"
	"math"
	"net/http"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func GetAssistantWeeklyDiscount(c *gin.Context) {
	reward, err := model.GetAssistantWeeklyDiscount(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, reward)
}

func ClaimAssistantWeeklyDiscount(c *gin.Context) {
	reward, alreadyClaimed, err := model.ClaimAssistantWeeklyDiscount(c.GetInt("id"))
	if err != nil {
		if errors.Is(err, model.ErrAssistantWeeklyDiscountUnavailable) {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{
				"success": false,
				"code":    "ASSISTANT_WEEKLY_DISCOUNT_UNAVAILABLE",
				"message": "weekly discount is not available",
			})
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"discount":        reward,
		"already_claimed": alreadyClaimed,
	})
}

func executeAssistantWeeklyDiscountTool(c *gin.Context, userID int, input map[string]any) map[string]any {
	if assistantRewardReadOnlyRequest(c) {
		return assistantWeeklyDiscountReadOnlyRequestResult()
	}
	percent, ok := inputNumber(input, "discount_percent")
	if !ok || math.IsNaN(percent) || math.IsInf(percent, 0) || math.Trunc(percent) != percent || percent < 0 || percent > 10 {
		return map[string]any{"ok": false, "status": "invalid_decision", "error": "discount_percent must be an integer from 0 to 10"}
	}
	turns, runes := assistantConversationEvidence(c)
	reward, created, err := model.DecideAssistantWeeklyDiscount(
		userID,
		assistantHistoryConversationID(c),
		int(percent),
		inputString(input, "reason"),
		turns,
		runes,
	)
	if err != nil {
		if errors.Is(err, model.ErrAssistantWeeklyDiscountInvalid) {
			return map[string]any{
				"ok":     false,
				"status": "more_conversation_needed",
				"error":  "continue with at least two substantive user turns before evaluating the weekly discount",
			}
		}
		if errors.Is(err, model.ErrAssistantWeeklyDiscountUnavailable) {
			return map[string]any{"ok": false, "status": "unavailable", "error": "the weekly discount is not available"}
		}
		return map[string]any{"ok": false, "status": "unavailable", "error": "the weekly discount decision could not be saved"}
	}
	if reward.Status == model.AssistantWeeklyDiscountOffered && c != nil {
		c.Set(assistantClientActionKey, map[string]any{
			"type":             "weekly_discount",
			"discount_percent": reward.DiscountPercent,
			"reason":           reward.Reason,
			"status":           reward.Status,
		})
	}
	return map[string]any{
		"ok":               true,
		"created":          created,
		"status":           reward.Status,
		"discount_percent": reward.DiscountPercent,
		"reason":           reward.Reason,
		"next_step":        "The user may claim this weekly discount from the card shown in chat. It can be claimed once during the current UTC week; never claim it for them.",
	}
}
