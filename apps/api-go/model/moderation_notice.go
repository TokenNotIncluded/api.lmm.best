package model

import (
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

// ModerationNotice is an owner-only in-site warning or penalty receipt. It
// contains no submitted text and is created in the result/wallet transaction.
type ModerationNotice struct {
	ID             int    `json:"id" gorm:"primaryKey"`
	UserID         int    `json:"-" gorm:"not null;index;uniqueIndex:idx_moderation_notice_request,priority:1"`
	JobID          int64  `json:"job_id" gorm:"not null;index"`
	RequestID      string `json:"request_id" gorm:"type:varchar(128);not null;uniqueIndex:idx_moderation_notice_request,priority:2"`
	Source         string `json:"source" gorm:"type:varchar(24);not null;uniqueIndex:idx_moderation_notice_request,priority:3"`
	Mode           string `json:"mode" gorm:"type:varchar(16);not null"`
	CategoriesJSON string `json:"-" gorm:"type:text;not null"`
	FeeRecordID    uint   `json:"fee_record_id" gorm:"not null;default:0"`
	RequestedQuota int    `json:"requested_quota" gorm:"type:bigint;not null;default:0"`
	ChargedQuota   int    `json:"charged_quota" gorm:"type:bigint;not null;default:0"`
	CreatedAt      int64  `json:"created_at" gorm:"not null;index"`
	UpdatedAt      int64  `json:"updated_at" gorm:"not null"`
}

func (ModerationNotice) TableName() string { return "moderation_notices" }

func unifiedModerationNoticeQuery(db *gorm.DB, userID int) *gorm.DB {
	if !db.Migrator().HasTable(&ModerationNotice{}) {
		return db.Table("users AS notice").Where("1 = 0")
	}
	// Even administrators use the owner predicate in the notification inbox.
	// The separate security administration view has its own metadata projection.
	return db.Table("moderation_notices AS notice").Where("notice.user_id = ?", userID)
}

func unifiedModerationNoticeCount(db *gorm.DB, userID int, unreadOnly bool) (int64, error) {
	query := unifiedModerationNoticeQuery(db, userID)
	if unreadOnly {
		query = query.Where(`NOT EXISTS (SELECT 1 FROM unified_todo_reads AS read_marker WHERE read_marker.user_id = ? AND read_marker.category = ? AND read_marker.item_id = notice.id)`, userID, UnifiedTodoCategoryModeration)
	}
	return unifiedTodoCount(query)
}

func unifiedModerationNoticeCandidates(db *gorm.DB, userID int, ids []int) ([]unifiedTodoCandidate, error) {
	if len(ids) == 0 {
		return []unifiedTodoCandidate{}, nil
	}
	var rows []ModerationNotice
	if err := unifiedModerationNoticeQuery(db, userID).Where("notice.id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]unifiedTodoCandidate, 0, len(rows))
	for _, notice := range rows {
		var categories []string
		if json.Unmarshal([]byte(notice.CategoriesJSON), &categories) != nil {
			categories = []string{}
		}
		summary := "OpenAI Moderation flagged submitted content; review the safety rules."
		if notice.Source == ModerationSourceAssistantOutput {
			summary = "OpenAI Moderation flagged the assistant's output. Use the answer with caution; this warning does not penalize your account."
		}
		if notice.ChargedQuota > 0 {
			summary = fmt.Sprintf("OpenAI Moderation flagged submitted content; wallet penalty: %d quota units.", notice.ChargedQuota)
		}
		items = append(items, unifiedTodoCandidate{Item: UnifiedTodoItem{
			Id: unifiedTodoItemID(UnifiedTodoCategoryModeration, notice.ID), SourceId: notice.ID, Category: UnifiedTodoCategoryModeration, Type: "moderation_warning", Title: "moderation.warning", Summary: summary, CreatedAt: notice.CreatedAt, UpdatedAt: notice.UpdatedAt,
			Details: map[string]any{"request_id": notice.RequestID, "source": notice.Source, "mode": notice.Mode, "categories": categories, "fee_record_id": notice.FeeRecordID, "requested_quota": notice.RequestedQuota, "charged_quota": notice.ChargedQuota},
		}})
	}
	return items, nil
}
