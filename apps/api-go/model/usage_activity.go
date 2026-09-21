package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// UsageActivityDay is the privacy-safe daily aggregate exposed to an account
// holder. It intentionally contains no model names, request IDs, or content.
type UsageActivityDay struct {
	Date             string `json:"date"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	Quota            int64  `json:"quota"`
}

type UsageActivity struct {
	StartTimestamp   int64              `json:"start_timestamp"`
	EndTimestamp     int64              `json:"end_timestamp"`
	Timezone         string             `json:"timezone"`
	Days             []UsageActivityDay `json:"days"`
	Requests         int64              `json:"requests"`
	PromptTokens     int64              `json:"prompt_tokens"`
	CompletionTokens int64              `json:"completion_tokens"`
	Quota            int64              `json:"quota"`
}

type usageActivityRow struct {
	Date             string `gorm:"column:date"`
	Requests         int64  `gorm:"column:requests"`
	PromptTokens     int64  `gorm:"column:prompt_tokens"`
	CompletionTokens int64  `gorm:"column:completion_tokens"`
	Quota            int64  `gorm:"column:quota"`
}

func usageActivityDateExpression(db *gorm.DB) (string, error) {
	switch db.Dialector.Name() {
	case "sqlite":
		return "strftime('%Y-%m-%d', created_at, 'unixepoch')", nil
	case "postgres":
		return "to_char(to_timestamp(created_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD')", nil
	case "mysql":
		return "DATE_FORMAT(CONVERT_TZ(FROM_UNIXTIME(created_at), @@session.time_zone, '+00:00'), '%Y-%m-%d')", nil
	case "clickhouse":
		return "formatDateTime(toDateTime(created_at, 'UTC'), '%Y-%m-%d')", nil
	default:
		return "", fmt.Errorf("unsupported usage log database dialect %q", db.Dialector.Name())
	}
}

// GetUserUsageActivity aggregates consume logs in UTC. EndTimestamp is
// exclusive, which makes adjacent ranges compose without double counting.
func GetUserUsageActivity(userID int, startTimestamp int64, endTimestamp int64) (UsageActivity, error) {
	if userID <= 0 || startTimestamp < 0 || endTimestamp <= startTimestamp {
		return UsageActivity{}, errors.New("invalid usage activity range")
	}
	if LOG_DB == nil {
		return UsageActivity{}, errors.New("usage log database is unavailable")
	}
	expression, err := usageActivityDateExpression(LOG_DB)
	if err != nil {
		return UsageActivity{}, err
	}
	rows := make([]usageActivityRow, 0)
	query := LOG_DB.Model(&Log{}).
		Select(expression+" AS date, COUNT(*) AS requests, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens, COALESCE(SUM(quota), 0) AS quota").
		Where("user_id = ? AND type = ? AND created_at >= ? AND created_at < ?", userID, LogTypeConsume, startTimestamp, endTimestamp).
		Group("date").Order("date ASC")
	if err := query.Scan(&rows).Error; err != nil {
		return UsageActivity{}, err
	}
	result := UsageActivity{StartTimestamp: startTimestamp, EndTimestamp: endTimestamp, Timezone: "UTC", Days: rowsToUsageActivity(rows)}
	for _, day := range result.Days {
		result.Requests += day.Requests
		result.PromptTokens += day.PromptTokens
		result.CompletionTokens += day.CompletionTokens
		result.Quota += day.Quota
	}
	return result, nil
}

func rowsToUsageActivity(rows []usageActivityRow) []UsageActivityDay {
	result := make([]UsageActivityDay, 0, len(rows))
	for _, row := range rows {
		if row.Date == "" {
			continue
		}
		result = append(result, UsageActivityDay{
			Date: row.Date, Requests: row.Requests, PromptTokens: row.PromptTokens,
			CompletionTokens: row.CompletionTokens, Quota: row.Quota,
		})
	}
	return result
}
