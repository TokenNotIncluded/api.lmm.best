package controller

import (
	"context"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"net/http"
	"time"
)

func AcquisitionSelfReport(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	userID := c.GetInt("id")
	if userID <= 0 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	switch c.Request.Method {
	case http.MethodPut:
		var input struct {
			Source string `json:"source"`
			Detail string `json:"detail"`
		}
		if c.ShouldBindJSON(&input) != nil {
			common.ApiError(c, model.ErrAcquisitionInvalid)
			return
		}
		if err := model.SaveAcquisitionSelfReport(ctx, userID, input.Source, input.Detail); err != nil {
			common.ApiError(c, err)
			return
		}
	case http.MethodDelete:
		if err := model.DB.WithContext(ctx).Where("user_id = ?", userID).Delete(&model.AcquisitionSelfReport{}).Error; err != nil {
			common.ApiError(c, err)
			return
		}
	}
	value, err := model.ReadAcquisitionSelfReport(ctx, userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": value})
}
