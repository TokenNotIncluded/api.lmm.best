package model

import (
	"errors"
	"sort"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

type AssistantUsageBreakdown struct {
	Name             string  `json:"name"`
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Quota            int64   `json:"quota"`
	CostUSD          float64 `json:"cost_usd"`
}

type AssistantUsageSummary struct {
	StartTimestamp   int64                     `json:"start_timestamp"`
	EndTimestamp     int64                     `json:"end_timestamp"`
	Requests         int64                     `json:"requests"`
	PromptTokens     int64                     `json:"prompt_tokens"`
	CompletionTokens int64                     `json:"completion_tokens"`
	TotalTokens      int64                     `json:"total_tokens"`
	Quota            int64                     `json:"quota"`
	CostUSD          float64                   `json:"cost_usd"`
	Models           []AssistantUsageBreakdown `json:"models"`
	Groups           []AssistantUsageBreakdown `json:"groups"`
}

// AssistantFundingSummary only includes consume logs explicitly tagged with
// billing_source=assistant.  Assistant requests run as the enabled root
// account, so this separates customer-service spend from the administrator's
// normal relay traffic without exposing any user or request content.
type AssistantFundingSummary struct {
	StartTimestamp   int64   `json:"start_timestamp"`
	EndTimestamp     int64   `json:"end_timestamp"`
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Quota            int64   `json:"quota"`
	CostUSD          float64 `json:"cost_usd"`
}

type assistantUsageAggregate struct {
	Requests         int64 `gorm:"column:requests"`
	PromptTokens     int64 `gorm:"column:prompt_tokens"`
	CompletionTokens int64 `gorm:"column:completion_tokens"`
	Quota            int64 `gorm:"column:quota"`
}

func usageCostUSD(quota int64) (float64, error) {
	usd, err := common.CreditsToUSD(quota)
	if err != nil {
		return 0, err
	}
	return usd.InexactFloat64(), nil
}

func usageBreakdownRows(userID int, startTimestamp int64, endTimestamp int64, limit int, column string) ([]AssistantUsageBreakdown, error) {
	if LOG_DB == nil {
		return nil, errors.New("usage log database is unavailable")
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var rows []struct {
		Name             string `gorm:"column:name"`
		Requests         int64  `gorm:"column:requests"`
		PromptTokens     int64  `gorm:"column:prompt_tokens"`
		CompletionTokens int64  `gorm:"column:completion_tokens"`
		Quota            int64  `gorm:"column:quota"`
	}
	selectColumn := column
	if column == "group" {
		selectColumn = logGroupCol
	}
	query := LOG_DB.Model(&Log{}).
		Select(selectColumn+" AS name, COUNT(*) AS requests, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens, COALESCE(SUM(quota), 0) AS quota").
		Where("user_id = ? AND type = ? AND created_at >= ? AND created_at <= ?", userID, LogTypeConsume, startTimestamp, endTimestamp).
		Group(column).
		Order("requests DESC").
		Limit(limit)
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}

	result := make([]AssistantUsageBreakdown, 0, len(rows))
	for _, row := range rows {
		costUSD, err := usageCostUSD(row.Quota)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(row.Name)
		if name == "" {
			name = "(unknown)"
		}
		totalTokens := row.PromptTokens + row.CompletionTokens
		result = append(result, AssistantUsageBreakdown{
			Name:             name,
			Requests:         row.Requests,
			PromptTokens:     row.PromptTokens,
			CompletionTokens: row.CompletionTokens,
			TotalTokens:      totalTokens,
			Quota:            row.Quota,
			CostUSD:          costUSD,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Requests == result[j].Requests {
			return result[i].Name < result[j].Name
		}
		return result[i].Requests > result[j].Requests
	})
	return result, nil
}

func GetAssistantUsageSummary(userID int, startTimestamp int64, endTimestamp int64, limit int) (AssistantUsageSummary, error) {
	if userID <= 0 || endTimestamp < startTimestamp {
		return AssistantUsageSummary{}, errors.New("invalid usage summary range")
	}
	if LOG_DB == nil {
		return AssistantUsageSummary{}, errors.New("usage log database is unavailable")
	}
	if _, err := common.CreditsPerUSD(); err != nil {
		return AssistantUsageSummary{}, err
	}
	var aggregate assistantUsageAggregate
	if err := LOG_DB.Model(&Log{}).
		Select("COUNT(*) AS requests, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens, COALESCE(SUM(quota), 0) AS quota").
		Where("user_id = ? AND type = ? AND created_at >= ? AND created_at <= ?", userID, LogTypeConsume, startTimestamp, endTimestamp).
		Scan(&aggregate).Error; err != nil {
		return AssistantUsageSummary{}, err
	}

	models, err := usageBreakdownRows(userID, startTimestamp, endTimestamp, limit, "model_name")
	if err != nil {
		return AssistantUsageSummary{}, err
	}
	groups, err := usageBreakdownRows(userID, startTimestamp, endTimestamp, limit, "group")
	if err != nil {
		return AssistantUsageSummary{}, err
	}
	totalTokens := aggregate.PromptTokens + aggregate.CompletionTokens
	costUSD, err := usageCostUSD(aggregate.Quota)
	if err != nil {
		return AssistantUsageSummary{}, err
	}
	return AssistantUsageSummary{
		StartTimestamp:   startTimestamp,
		EndTimestamp:     endTimestamp,
		Requests:         aggregate.Requests,
		PromptTokens:     aggregate.PromptTokens,
		CompletionTokens: aggregate.CompletionTokens,
		TotalTokens:      totalTokens,
		Quota:            aggregate.Quota,
		CostUSD:          costUSD,
		Models:           models,
		Groups:           groups,
	}, nil
}

const (
	assistantBillingSourceCompactLike = `%"billing_source":"assistant"%`
	assistantBillingSourceSpacedLike  = `%"billing_source": "assistant"%`
)

// GetAssistantFundingSummary reports only model calls funded by the
// super-administrator assistant account.  The billing source is stored in the
// consume log's JSON Other field, so this remains compatible with existing log
// schemas and does not require a migration.
func GetAssistantFundingSummary(userID int, startTimestamp int64, endTimestamp int64) (AssistantFundingSummary, error) {
	if userID <= 0 || endTimestamp < startTimestamp {
		return AssistantFundingSummary{}, errors.New("invalid assistant funding summary range")
	}
	if LOG_DB == nil {
		return AssistantFundingSummary{}, errors.New("usage log database is unavailable")
	}
	if _, err := common.CreditsPerUSD(); err != nil {
		return AssistantFundingSummary{}, err
	}

	var aggregate assistantUsageAggregate
	if err := LOG_DB.Model(&Log{}).
		Select("COUNT(*) AS requests, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens, COALESCE(SUM(quota), 0) AS quota").
		Where("user_id = ? AND type = ? AND created_at >= ? AND created_at <= ? AND (other LIKE ? OR other LIKE ?)",
			userID, LogTypeConsume, startTimestamp, endTimestamp, assistantBillingSourceCompactLike, assistantBillingSourceSpacedLike).
		Scan(&aggregate).Error; err != nil {
		return AssistantFundingSummary{}, err
	}

	totalTokens := aggregate.PromptTokens + aggregate.CompletionTokens
	costUSD, err := usageCostUSD(aggregate.Quota)
	if err != nil {
		return AssistantFundingSummary{}, err
	}
	return AssistantFundingSummary{
		StartTimestamp:   startTimestamp,
		EndTimestamp:     endTimestamp,
		Requests:         aggregate.Requests,
		PromptTokens:     aggregate.PromptTokens,
		CompletionTokens: aggregate.CompletionTokens,
		TotalTokens:      totalTokens,
		Quota:            aggregate.Quota,
		CostUSD:          costUSD,
	}, nil
}
