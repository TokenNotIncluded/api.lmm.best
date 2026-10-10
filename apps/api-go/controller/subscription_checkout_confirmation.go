package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Called only by the existing UserAuth + DisableCache /subscription/self route.
// This endpoint does not call a provider, settle an order, or grant quota.
func getSubscriptionCheckoutConfirmation(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userId := c.GetInt("id")
	if userId <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false})
		return
	}
	values := c.Request.URL.Query()["checkout_trade_no"]
	if len(values) != 1 || values[0] == "" || len(values[0]) > 255 || strings.TrimSpace(values[0]) != values[0] {
		c.JSON(http.StatusBadRequest, gin.H{"success": false})
		return
	}
	result, err := model.GetSubscriptionCheckoutConfirmation(c.Request.Context(), userId, values[0])
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, model.ErrSubscriptionOrderNotFound) {
			// An absent order and another user's order have the same response.
			c.JSON(http.StatusNotFound, gin.H{"success": false})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false})
		}
		return
	}
	common.ApiSuccess(c, result)
}
