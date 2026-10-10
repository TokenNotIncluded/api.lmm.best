package controller

import (
	"net/http"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// GetQuotaQuery is a token-scoped persisted snapshot, not an account balance.
func GetQuotaQuery(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	raw, ok := c.Get("quota_query_token")
	token, typed := raw.(*model.Token)
	if !ok || !typed || token == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"valid": false, "error": "invalid_api_key"})
		return
	}
	divisor, err := common.CreditsPerUSD()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"valid": false, "error": "quota_query_unavailable"})
		return
	}
	now := time.Now().UTC()
	amount := func(quota decimal.Decimal) float64 { value, _ := quota.Div(divisor).Float64(); return value }
	snapshot := loadUsageSnapshot(nil, []*model.Token{token})
	current, projector := snapshot.token(token)
	token = current
	usage := projectTokenUsage(projector, token)
	var remaining, total, today, usedTotal any
	if usage.NormalizedUsedQuota != nil {
		usedTotal = amount(decimal.NewFromInt(int64(*usage.NormalizedUsedQuota)))
	}
	if !token.UnlimitedQuota {
		remaining = amount(decimal.NewFromInt(int64(token.RemainQuota)))
		if usage.NormalizedUsedQuota != nil {
			total = amount(decimal.NewFromInt(int64(token.RemainQuota)).Add(decimal.NewFromInt(int64(*usage.NormalizedUsedQuota))))
		}
	}
	if common.LogConsumeEnabled {
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
		var sum decimal.Decimal
		err := model.LOG_DB.WithContext(c.Request.Context()).Model(&model.Log{}).
			Where("user_id = ? AND token_id = ? AND type = ? AND created_at >= ? AND created_at <= ?", token.UserId, token.Id, model.LogTypeConsume, start, now.Unix()).
			Select("COALESCE(SUM(quota), 0)").Scan(&sum).Error
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"valid": false, "error": "quota_query_unavailable"})
			return
		}
		today = amount(sum)
	}
	c.JSON(http.StatusOK, gin.H{
		"valid": true, "currency": "USD", "remaining": remaining,
		"used_today": today, "used_total": usedTotal,
		"used_quota": token.UsedQuota, "normalized_used_quota": usage.NormalizedUsedQuota,
		"usage_projection_available": usage.UsageProjectionAvailable,
		"total_quota":                total, "unlimited": token.UnlimitedQuota, "updated_at": now.Unix(),
		"scope": "token", "day_timezone": "UTC", "used_today_source": "retained_consumption_logs",
		"consistency": "persisted_snapshot",
	})
}
