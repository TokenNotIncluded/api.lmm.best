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
	if !requireAssistantToolEnabled(c, "prepare_weekly_discount") {
		return
	}
	reward, alreadyClaimed, err := model.ClaimAssistantWeeklyDiscount(c.GetInt("id"))
	if err != nil {
		if errors.Is(err, model.ErrAssistantWeeklyDiscountDisabled) {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"success": false, "code": "ASSISTANT_WEEKLY_DISCOUNT_DISABLED", "message": "Weekly discounts are disabled for this account by the current policy. No new code was created."})
			return
		}
		if errors.Is(err, model.ErrAssistantWeeklyDiscountLimit) {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"success": false, "code": "ASSISTANT_WEEKLY_DISCOUNT_LIMIT", "message": "The offered discount exceeds the current account limit. Ask support to check the policy; waiting for next week is not a confirmed fix."})
			return
		}
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
	if !ok || math.IsNaN(percent) || math.IsInf(percent, 0) || math.Trunc(percent) != percent || percent < 0 || percent > 99 {
		return map[string]any{"ok": false, "status": "invalid_decision", "error": "discount_percent must be an integer from 0 to 99 within the configured user-level limit"}
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
		if errors.Is(err, model.ErrAssistantWeeklyDiscountDisabled) {
			return map[string]any{"ok": false, "status": "disabled_for_account", "decision_used": false, "retryable": false, "error": "Weekly discounts are disabled by the current account-level policy. No weekly decision was recorded. Do not claim the weekly opportunity was used or promise it will reopen next week."}
		}
		if errors.Is(err, model.ErrAssistantWeeklyDiscountLimit) {
			return map[string]any{"ok": false, "status": "discount_limit_exceeded", "decision_used": false, "retryable": true, "error": "the discount exceeds this account's current limit; no reward decision was recorded"}
		}
		if errors.Is(err, model.ErrAssistantWeeklyDiscountConversationRequired) {
			return map[string]any{
				"ok":     false,
				"status": "more_conversation_needed", "decision_used": false, "retryable": true,
				"error": "continue with at least two substantive user turns before evaluating the weekly discount",
			}
		}
		if errors.Is(err, model.ErrAssistantWeeklyDiscountInvalid) {
			return map[string]any{"ok": false, "status": "invalid_decision", "decision_used": false, "retryable": true, "error": "Use a valid percentage and a reason of 2-240 characters. No weekly decision was recorded."}
		}
		if errors.Is(err, model.ErrAssistantWeeklyDiscountUnavailable) {
			return map[string]any{"ok": false, "status": "unavailable", "error": "the weekly discount is not available"}
		}
		common.SysError("weekly discount decision failed: " + err.Error())
		return map[string]any{"ok": false, "status": "service_error", "retryable": true, "decision_used": "unknown", "error": "The weekly discount request failed. Read get_weekly_discount_status before retrying; do not claim that the entry is closed, the opportunity was used, or the user must wait until next week."}
	}
	if (reward.Status == model.AssistantWeeklyDiscountOffered || reward.Status == model.AssistantWeeklyDiscountClaimed) && c != nil {
		c.Set(assistantClientActionKey, map[string]any{
			"type":             "weekly_discount",
			"discount_percent": reward.DiscountPercent,
			"reason":           reward.Reason,
			"status":           reward.Status,
		})
	}
	nextStep := "The user may claim this weekly discount from the card shown in chat. Never claim it for them."
	switch reward.Status {
	case model.AssistantWeeklyDiscountDeclined:
		nextStep = "This week has a stored zero-discount decision, not a closed application entry. No coupon exists to claim. Explain this without inventing an outage."
	case model.AssistantWeeklyDiscountClaimed:
		nextStep = "The user already claimed this week's discount. Show the existing card; do not generate another code or ask them to claim it again."
	}
	return map[string]any{
		"ok":               true,
		"decision_used":    true,
		"claim_available":  reward.Status == model.AssistantWeeklyDiscountOffered,
		"created":          created,
		"status":           reward.Status,
		"discount_percent": reward.DiscountPercent,
		"reason":           reward.Reason,
		"next_step":        nextStep,
	}
}
