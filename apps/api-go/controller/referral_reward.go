package controller

import (
	"net/http"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func manageReferralModeration(c *gin.Context, req ManageRequest) {
	err := model.ModerateReferralUser(model.ReferralModerationEvent{
		RequestId: req.RequestId, ActorId: c.GetInt("id"), UserId: req.Id,
		Action: req.Action, Reason: req.Reason, Evidence: req.Evidence, Penalize: req.PenalizeInviter,
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
	userID := c.GetInt("id")
	if userID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "authentication required"})
		return
	}
	before := 0
	if raw := c.Query("before"); raw != "" {
		var err error
		before, err = strconv.Atoi(raw)
		if err != nil || before <= 0 {
			common.ApiError(c, model.ErrReferralConflict)
			return
		}
	}
	// Closing the history dialog or disconnecting the client must also cancel
	// its database work, not just discard the eventual HTTP response.
	db := model.DB.WithContext(c.Request.Context())
	entries := []model.ReferralLedgerEntry{}
	query := db.Where("user_id = ?", userID)
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
	if err := db.Select("id", "aff_quota").First(&user, userID).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"entries": entries, "next_cursor": next, "available_quota": max(0, user.AffQuota),
		"debt_quota": max(0, -user.AffQuota), "policy": model.GetReferralPolicy(),
	}})
}
