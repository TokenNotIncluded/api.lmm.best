package middleware

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// QuotaQueryAuth reads persisted records without changing token status, quota,
// accessed time or cache state. Exhausted keys can still monitor their quota.
func QuotaQueryAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		fail := func(status int, message string) {
			c.AbortWithStatusJSON(status, gin.H{"valid": false, "error": message})
		}
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			fail(http.StatusUnauthorized, "invalid_api_key")
			return
		}
		var token model.Token
		key := strings.TrimPrefix(parts[1], "sk-")
		// Credential predicates must never be expanded into SQL error logs.
		err := model.DB.Session(&gorm.Session{Logger: logger.Discard}).WithContext(c.Request.Context()).Where(map[string]any{"key": key}).First(&token).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(http.StatusUnauthorized, "invalid_api_key")
			return
		}
		if err != nil {
			fail(http.StatusInternalServerError, "quota_query_unavailable")
			return
		}
		if (token.Status != common.TokenStatusEnabled && token.Status != common.TokenStatusExhausted) ||
			(token.ExpiredTime != -1 && token.ExpiredTime <= time.Now().Unix()) {
			fail(http.StatusUnauthorized, "invalid_api_key")
			return
		}
		if ips := token.GetIpLimits(); len(ips) > 0 && !common.IsIpInCIDRList(net.ParseIP(c.ClientIP()), ips) {
			fail(http.StatusForbidden, "ip_not_allowed")
			return
		}
		var user model.User
		err = model.DB.WithContext(c.Request.Context()).Select("id", "status").First(&user, token.UserId).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && user.Status != common.UserStatusEnabled) {
			fail(http.StatusUnauthorized, "invalid_api_key")
			return
		}
		if err != nil {
			fail(http.StatusInternalServerError, "quota_query_unavailable")
			return
		}
		c.Set("id", token.UserId)
		c.Set("quota_query_token", &token)
		c.Next()
	}
}

// Per account, regardless of how many keys are used: at most 30 queries/minute.
func QuotaQueryRateLimit() gin.HandlerFunc {
	return userRateLimitFactory(30, 60, "quota-query")
}

// PricingQueryAccess follows relay visibility without ValidateUserToken, whose
// lazy expired/exhausted transitions are writes and may log credential SQL.
// Register after QuotaQueryAuth so the key and account have been read safely.
func PricingQueryAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, _ := c.Get("quota_query_token")
		token, ok := raw.(*model.Token)
		if !ok || token == nil || token.Status != common.TokenStatusEnabled || (!token.UnlimitedQuota && token.RemainQuota <= 0) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "message": "API key unavailable"})
			return
		}
		user, err := model.GetUserCache(token.UserId)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "pricing context unavailable"})
			return
		}
		if allowed, err := trustLevelAllowsDeveloperAccess(user); err != nil || !allowed {
			abortRelayAsNotFound(c)
			return
		}
		group := user.Group
		if token.Group != "" {
			if _, allowed := service.GetUserUsableGroups(user.Group)[token.Group]; !allowed || (token.Group != "auto" && !ratio_setting.ContainsGroupRatio(token.Group)) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "group unavailable"})
				return
			}
			group = token.Group
		}
		if err := SetupContextForToken(c, token); err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "message": "API key unavailable"})
			return
		}
		user.WriteContext(c)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
		c.Next()
	}
}
