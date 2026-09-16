// Copyright (C) 2026 LIghtJUNction
// SPDX-License-Identifier: AGPL-3.0-or-later

package controller

import (
	"net/http"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

func getReferralRewardHistory(c *gin.Context, user *model.User) {
	before := int64(0)
	if value := c.Query("before"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			common.ApiErrorMsg(c, "invalid history cursor")
			return
		}
		before = parsed
	}
	entries, next, err := model.GetReferralRewardEntries(user.Id, before)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"items": entries, "next_before": next, "balance_quota": user.AffQuota,
		"debt_quota": user.AffDebtQuota, "policy": operation_setting.GetReferralRewardPolicy(),
	}})
}

func manageReferralReward(c *gin.Context, request ManageRequest) {
	if request.Action == "referral_preview" {
		reward, err := model.GetReferralRewardPreview(request.Id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": reward})
		return
	}
	err := model.ModerateReferralUser(model.ReferralModerationRequest{
		UserID: request.Id, ActorID: c.GetInt("id"), ActorRole: c.GetInt("role"),
		Action: request.Action, Reason: request.Reason, Note: request.Note,
		ConfirmPenalty: request.ConfirmPenalty, OperationKey: request.OperationKey,
		ExpectedRevision: request.ExpectedRevision,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// Auth version was changed with the accounting transaction. Publish after
	// commit and repeat invalidation on retries even when the operation replays.
	if err := model.PublishUserAuthCache(request.Id); err != nil {
		common.ApiError(c, err)
		return
	}
	if _, err := model.RevokeAllUserSessions(request.Id, "referral_moderation"); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.InvalidateUserTokensCache(request.Id); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, request.Id, "user.referral_moderation", map[string]interface{}{
		"action": request.Action, "reason": request.Reason, "operation_key": request.OperationKey,
		"confirmed_penalty": request.ConfirmPenalty,
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}
