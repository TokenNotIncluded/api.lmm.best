package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func GetSelfAnnouncementStatus(c *gin.Context) {
	items, err := model.GetAnnouncementStatus(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, items)
}

func AcknowledgeSelfAnnouncement(c *gin.Context) {
	var input struct {
		ID       int64  `json:"id"`
		Revision string `json:"revision"`
	}
	if c.ShouldBindJSON(&input) != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid announcement acknowledgement"})
		return
	}
	err := model.AcknowledgeAnnouncement(c.GetInt("id"), input.ID, input.Revision)
	if errors.Is(err, model.ErrAnnouncementOrder) {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	GetSelfAnnouncementStatus(c)
}

func GetUserAnnouncementStatus(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !canManageTargetRole(c.GetInt("role"), user.Role) {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	items, err := model.GetAnnouncementStatus(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, items)
}
