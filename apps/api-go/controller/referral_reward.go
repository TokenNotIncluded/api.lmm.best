package controller

import (
	"net/http"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/referralinput"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func manageReferralModeration(c *gin.Context, req ManageRequest) {
	evidence, err := referralinput.NormalizeEvidence(req.Evidence)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	err = model.ModerateReferralUser(model.ReferralModerationEvent{
		RequestId: req.RequestId, ActorId: c.GetInt("id"), UserId: req.Id,
		Action: req.Action, Reason: req.Reason, Evidence: evidence, Penalize: req.PenalizeInviter,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, req.Id, "user.referral_moderation", map[string]interface{}{
		"action": req.Action, "reason": req.Reason, "request_id": req.RequestId, "penalize_inviter": req.PenalizeInviter,
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

// Caller identity is always taken from authentication, never a query user_id.
// Cursor pagination remains stable while new events are appended.
func GetReferralRewards(c *gin.Context) {
	before := 0
	if raw := c.Query("before"); raw != "" {
		var err error
		before, err = strconv.Atoi(raw)
		if err != nil || before <= 0 {
			common.ApiError(c, model.ErrReferralConflict)
			return
		}
	}
	userID := c.GetInt("id")
	entries := []model.ReferralLedgerEntry{}
	query := model.DB.Where("user_id = ?", userID)
	if before > 0 {
		query = query.Where("id < ?", before)
	}
	if err := query.Order("id DESC").Limit(51).Find(&entries).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	next := 0
	if len(entries) > 50 {
		entries = entries[:50]
		next = entries[49].Id
	}
	var user model.User
	if err := model.DB.Select("id", "aff_quota").First(&user, userID).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"entries": entries, "next_cursor": next, "available_quota": max(0, user.AffQuota),
		"debt_quota": max(0, -user.AffQuota), "policy": model.GetReferralPolicy(),
	}})
}
