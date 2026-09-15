package controller

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// SetAccountBalanceAccess is an owner-authenticated grant, never a relay-key operation.
func SetAccountBalanceAccess(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	if err != nil || id <= 0 || c.GetInt("id") <= 0 || c.ShouldBindJSON(&request) != nil || request.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid balance access request"})
		return
	}
	result := model.DB.WithContext(c.Request.Context()).Model(&model.Token{}).
		Where("id = ? AND user_id = ? AND oauth_managed = ?", id, c.GetInt("id"), false).
		Update("account_balance_read", *request.Enabled)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Balance access unavailable"})
		return
	}
	if result.RowsAffected != 1 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "API key not found"})
		return
	}
	// Balance queries read the persisted token on every request, including revocation.
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetAccountBalance returns only persisted wallet balance, always in USD.
func GetAccountBalance(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	raw, _ := c.Get("quota_query_token")
	token, ok := raw.(*model.Token)
	if !ok || token == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"valid": false, "error": "invalid_api_key"})
		return
	}
	if !token.AccountBalanceRead {
		c.JSON(http.StatusForbidden, gin.H{"valid": false, "error": "account_balance_access_required"})
		return
	}
	if common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"valid": false, "error": "account_balance_unavailable"})
		return
	}
	var user model.User
	if err := model.DB.WithContext(c.Request.Context()).Select("id", "quota").
		Where("id = ? AND status = ?", token.UserId, common.UserStatusEnabled).First(&user).Error; err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"valid": false, "error": "account_balance_unavailable"})
		return
	}
	remaining, _ := decimal.NewFromInt(int64(user.Quota)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Float64()
	c.JSON(http.StatusOK, gin.H{"valid": true, "scope": "account", "currency": "USD",
		"remaining": remaining, "updated_at": time.Now().Unix(), "consistency": "persisted_snapshot"})
}
