package model

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var acquisitionSelfReportSecret = regexp.MustCompile(`(?i)(https?://|sk-[a-z0-9_-]{8,}|bearer\s+|(?:api[_-]?key|password|token|secret)\s*[:=])`)

// Explicit user testimony, never an input to automatic attribution or rewards.
type AcquisitionSelfReport struct {
	UserID    int    `json:"-" gorm:"primaryKey"`
	Source    string `json:"source" gorm:"type:varchar(32);not null"`
	Detail    string `json:"detail" gorm:"type:varchar(160)"`
	UpdatedAt int64  `json:"updated_at" gorm:"index"`
}

func ReadAcquisitionSelfReport(ctx context.Context, userID int) (*AcquisitionSelfReport, error) {
	var value AcquisitionSelfReport
	err := DB.WithContext(ctx).Where("user_id = ? AND updated_at >= ?", userID, time.Now().Unix()-AcquisitionAccountDays*86400).First(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &value, err
}
func SaveAcquisitionSelfReport(ctx context.Context, userID int, source, detail string) error {
	allowed := map[string]bool{"search": true, "community": true, "social": true, "documentation": true, "friend": true, "client": true, "ai": true, "other": true}
	detail = strings.TrimSpace(detail)
	if userID <= 0 || !allowed[source] || utf8.RuneCountInString(detail) > 160 || strings.ContainsAny(detail, "\r\n\x00") || acquisitionSelfReportSecret.MatchString(detail) {
		return ErrAcquisitionInvalid
	}
	value := AcquisitionSelfReport{UserID: userID, Source: source, Detail: detail, UpdatedAt: time.Now().Unix()}
	return DB.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoUpdates: clause.AssignmentColumns([]string{"source", "detail", "updated_at"})}).Create(&value).Error
}
