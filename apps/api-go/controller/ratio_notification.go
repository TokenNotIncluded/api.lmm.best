package controller

import (
	"net/http"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func ListRatioNotifications(c *gin.Context) {
	var user model.User
	if err := model.DB.First(&user, c.GetInt("id")).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var events []model.RatioNotification
	query := model.DB.Order("effective_at DESC, id DESC").Limit(50)
	// Stable compound cursor handles multiple batches in the same second.
	if cursor := c.Query("before"); cursor != "" {
		var before model.RatioNotification
		if err := model.DB.First(&before, "id = ?", cursor).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid cursor"})
			return
		}
		query = query.Where("effective_at < ? OR (effective_at = ? AND id < ?)", before.EffectiveAt, before.EffectiveAt, before.ID)
	}
	if err := query.Find(&events).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	data := []gin.H{}
	for _, event := range events {
		changes, err := service.VisibleRatioChanges(event, user)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if len(changes) > 0 {
			data = append(data, gin.H{"event_id": event.ID, "effective_at": event.EffectiveAt, "type": "ratio.changed", "changes": changes})
		}
	}
	next := ""
	if len(events) == 50 {
		next = events[len(events)-1].ID
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data, "next": next})
}

func ListRatioDeliveries(c *gin.Context) {
	var rows []model.RatioDelivery
	after, _ := strconv.ParseUint(c.Query("after"), 10, 64)
	if err := model.DB.Where("id > ?", after).Order("id").Limit(100).Find(&rows).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rows})
}

func RetryRatioDelivery(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false})
		return
	}
	result := model.DB.Model(&model.RatioDelivery{}).Where("id = ? AND status = ?", id, "failed").Updates(map[string]any{"status": "pending", "attempts": 0, "next_at": 0, "last_error": ""})
	if result.Error != nil {
		common.ApiError(c, result.Error)
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "delivery is not failed"})
		return
	}
	recordManageAudit(c, "ratio_notification.retry", map[string]interface{}{"delivery_id": id})
	c.JSON(http.StatusOK, gin.H{"success": true})
}
