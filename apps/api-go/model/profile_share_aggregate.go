package model

import (
	"encoding/json"
	"gorm.io/gorm"
)

// Snapshots are explicitly supplied by the owner, never authenticated provider
// credentials. Nil metrics mean unknown, including when another metric is zero.
type ProfileUsageSnapshot struct {
	Tokens      *int64 `json:"tokens,omitempty"`
	Requests    *int64 `json:"requests,omitempty"`
	Messages    *int64 `json:"messages,omitempty"`
	Period      string `json:"period"`
	PeriodStart string `json:"period_start,omitempty"`
	PeriodEnd   string `json:"period_end,omitempty"`
	ObservedAt  string `json:"observed_at"`
	Approximate bool   `json:"approximate"`
	Source      string `json:"source,omitempty"`
}

type ProfileLinkedProfile struct {
	Provider string                `json:"provider"`
	URL      string                `json:"url"`
	Label    string                `json:"label,omitempty"`
	Snapshot *ProfileUsageSnapshot `json:"snapshot,omitempty"`
}

// Apply only explicitly provided settings, atomically. The token predicate
// prevents an update from recreating or changing a concurrently revoked URL.
func SetProfileShareSettings(share *ProfileShare, modelUsage, aggregateUsage *bool, linkedProfiles *[]ProfileLinkedProfile) (*ProfileShare, error) {
	if share == nil || share.UserID <= 0 || share.Token == "" {
		return nil, gorm.ErrInvalidData
	}
	updates := map[string]any{}
	if modelUsage != nil {
		updates["model_usage_enabled"] = *modelUsage
	}
	if aggregateUsage != nil {
		updates["aggregate_usage_enabled"] = *aggregateUsage
	}
	if linkedProfiles != nil {
		encoded, err := json.Marshal(*linkedProfiles)
		if err != nil {
			return nil, err
		}
		updates["linked_profiles"] = string(encoded)
	}
	var updated ProfileShare
	err := DB.Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&ProfileShare{}).Where("user_id = ? AND token = ?", share.UserID, share.Token)
		if len(updates) > 0 {
			if err := query.Updates(updates).Error; err != nil {
				return err
			}
		}
		return tx.Where("user_id = ? AND token = ?", share.UserID, share.Token).Take(&updated).Error
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// Clamp corrupt negative buckets and exclude future buckets from the bounded
// native aggregate. Each source keeps its own time window and unit.
func GetProfileShareAggregateUsage(userID int, start, end int64) (ProfileShareUsage, error) {
	var usage ProfileShareUsage
	if userID <= 0 || start < 0 || end < start {
		return usage, gorm.ErrInvalidData
	}
	err := DB.Table("quota_data").Select("COALESCE(SUM(CASE WHEN token_used > 0 THEN token_used ELSE 0 END), 0) AS tokens, COALESCE(SUM(CASE WHEN count > 0 THEN count ELSE 0 END), 0) AS requests").Where("user_id = ? AND created_at >= ? AND created_at <= ?", userID, start, end).Scan(&usage).Error
	return usage, err
}
