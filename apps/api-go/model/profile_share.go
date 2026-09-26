package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// ProfileShare is an explicit public opt-in. Deleting it revokes the old URL.
// The URL token is public once embedded, but random so user IDs cannot be
// enumerated into public usage reports.
type ProfileShare struct {
	UserID    int    `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Token     string `json:"token" gorm:"size:48;not null;uniqueIndex"`
	CreatedAt int64  `json:"created_at" gorm:"not null"`
	// Existing profile URLs must not silently publish model names or spending.
	ModelUsageEnabled bool `json:"model_usage_enabled" gorm:"not null;default:false"`
}

func (ProfileShare) TableName() string { return "profile_shares" }

func GetProfileShare(userID int) (*ProfileShare, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	var share ProfileShare
	err := DB.Where("user_id = ?", userID).Take(&share).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &share, nil
}

func EnableProfileShare(userID int) (*ProfileShare, error) {
	share, err := GetProfileShare(userID)
	if err != nil || share != nil {
		return share, err
	}
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	share = &ProfileShare{
		UserID:    userID,
		Token:     hex.EncodeToString(bytes),
		CreatedAt: time.Now().Unix(),
	}
	if err := DB.Create(share).Error; err != nil {
		// A simultaneous enable request may have won the unique user_id insert.
		if existing, readErr := GetProfileShare(userID); readErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return share, nil
}

func DisableProfileShare(userID int) error {
	if userID <= 0 {
		return gorm.ErrInvalidData
	}
	return DB.Where("user_id = ?", userID).Delete(&ProfileShare{}).Error
}

func GetProfileShareOwner(token string) (*User, error) {
	var share ProfileShare
	if err := DB.Where("token = ?", token).Take(&share).Error; err != nil {
		return nil, err
	}
	var user User
	if err := DB.Select("id", "username", "display_name", "role", "status", "request_count").
		Where("id = ? AND status = ?", share.UserID, common.UserStatusEnabled).
		Take(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

type ProfileShareUsage struct {
	Tokens   int64 `json:"tokens"`
	Requests int64 `json:"requests"`
}

func GetProfileShareUsage(userID int, startTimestamp int64) (ProfileShareUsage, error) {
	if userID <= 0 || startTimestamp < 0 {
		return ProfileShareUsage{}, gorm.ErrInvalidData
	}
	var usage ProfileShareUsage
	query := DB.Table("quota_data").
		Select("COALESCE(SUM(token_used), 0) AS tokens, COALESCE(SUM(count), 0) AS requests").
		Where("user_id = ?", userID)
	if startTimestamp > 0 {
		query = query.Where("created_at >= ?", startTimestamp)
	}
	return usage, query.Scan(&usage).Error
}

type ProfileShareDay struct {
	Day    int64 `gorm:"column:day"`
	Tokens int64 `gorm:"column:tokens"`
}

// GetProfileShareYearDays aggregates before returning rows, keeping public SVG
// requests bounded to at most 371 daily points instead of raw usage records.
func GetProfileShareYearDays(userID int, startDay, endDay int64) ([]ProfileShareDay, error) {
	if userID <= 0 || startDay < 0 || endDay < startDay || endDay-startDay > 370*86400 {
		return nil, gorm.ErrInvalidData
	}
	var days []ProfileShareDay
	err := DB.Table("quota_data").
		Select("created_at - created_at % 86400 AS day, COALESCE(SUM(token_used), 0) AS tokens").
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, startDay, endDay+86400).
		Group("created_at - created_at % 86400").
		Order("day ASC").
		Limit(372).
		Scan(&days).Error
	return days, err
}
