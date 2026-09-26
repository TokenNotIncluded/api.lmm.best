package model

import (
	"database/sql"

	"gorm.io/gorm"
)

// SetProfileShareModelUsage is a separate consent scope. Updating appearance in
// a public URL must never grant access to previously private model statistics.
func SetProfileShareModelUsage(share *ProfileShare, enabled bool) (*ProfileShare, error) {
	if share == nil || share.UserID <= 0 || share.Token == "" {
		return nil, gorm.ErrInvalidData
	}
	query := DB.Model(&ProfileShare{}).Where("user_id = ? AND token = ?", share.UserID, share.Token)
	if err := query.Update("model_usage_enabled", enabled).Error; err != nil {
		return nil, err
	}
	var updated ProfileShare
	// Do not recreate a concurrently revoked URL, including an idempotent update.
	if err := DB.Where("user_id = ? AND token = ?", share.UserID, share.Token).Take(&updated).Error; err != nil {
		return nil, err
	}
	return &updated, nil
}

type ProfileShareModelRow struct {
	ModelName string
	Tokens    int64
	Requests  int64
	Quota     int64
}

type ProfileShareModelUsage struct {
	Tokens     int64
	Requests   int64
	Quota      int64
	ModelCount int64
	Models     []ProfileShareModelRow `gorm:"-"`
}

// Keep public reads bounded in both time and returned cardinality. The totals
// cover every model; the renderer derives an "Other models" row from the rest.
// Canonical names and non-negative counters match the authenticated report.
func GetProfileShareModelUsage(userID int, start, end int64, limit int) (ProfileShareModelUsage, error) {
	var usage ProfileShareModelUsage
	if userID <= 0 || start < 0 || end < start || end-start > 365*86400 || limit < 3 || limit > 12 {
		return usage, gorm.ErrInvalidData
	}
	const nameSQL = "COALESCE(NULLIF(TRIM(model_name), ''), 'unknown')"
	const sumsSQL = "COALESCE(SUM(CASE WHEN token_used > 0 THEN token_used ELSE 0 END), 0) AS tokens, " +
		"COALESCE(SUM(CASE WHEN count > 0 THEN count ELSE 0 END), 0) AS requests, " +
		"COALESCE(SUM(CASE WHEN quota > 0 THEN quota ELSE 0 END), 0) AS quota"
	err := DB.Transaction(func(tx *gorm.DB) error {
		window := func() *gorm.DB {
			return tx.Table("quota_data").Where("user_id = ? AND created_at >= ? AND created_at <= ?", userID, start, end)
		}
		if err := window().Select(sumsSQL + ", COUNT(DISTINCT " + nameSQL + ") AS model_count").Scan(&usage).Error; err != nil {
			return err
		}
		return window().Select(nameSQL + " AS model_name, " + sumsSQL).
			Group(nameSQL).Order("quota DESC, tokens DESC, requests DESC, model_name ASC").
			Limit(limit).Scan(&usage.Models).Error
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return usage, err
}
