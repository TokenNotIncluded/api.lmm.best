package model

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AssistantRequestReviewStatusCompleted = "completed"
	AssistantRequestReviewStatusFailed    = "failed"
	AssistantRequestReviewMaxRules        = 12
	AssistantRequestReviewMaxText         = 2048
	AssistantRequestReviewMaxPreview      = 768
	AssistantRequestReviewPageMax         = 100
)

// AssistantRequestReview is the durable, administrator-only result of a
// sampled background review. User text is already redacted and bounded before
// it reaches this model; secrets, credentials, and provider responses are not
// retained here.
type AssistantRequestReview struct {
	ID              int64  `json:"id" gorm:"primaryKey"`
	UserID          int    `json:"user_id" gorm:"not null;index:idx_assistant_review_user_created,priority:1"`
	ConversationID  int64  `json:"conversation_id" gorm:"not null;index"`
	RequestID       string `json:"request_id,omitempty" gorm:"type:varchar(128);not null;index"`
	Group           string `json:"group" gorm:"type:varchar(64);not null;index"`
	ReviewModel     string `json:"model" gorm:"type:varchar(128);not null"`
	Intensity       string `json:"intensity" gorm:"type:varchar(16);not null"`
	Status          string `json:"status" gorm:"type:varchar(16);not null;index"`
	Violation       bool   `json:"violation" gorm:"not null;index"`
	Abuse           bool   `json:"abuse" gorm:"not null;index"`
	RulesJSON       string `json:"-" gorm:"type:text;not null"`
	Explanation     string `json:"explanation,omitempty" gorm:"type:varchar(2048);not null"`
	RequestPreview  string `json:"request_preview,omitempty" gorm:"type:varchar(768);not null"`
	ResponsePreview string `json:"response_preview,omitempty" gorm:"type:varchar(768);not null"`
	ErrorMessage    string `json:"error,omitempty" gorm:"type:varchar(512);not null"`
	CreatedAt       int64  `json:"created_at" gorm:"not null;index:idx_assistant_review_user_created,priority:2"`
	UpdatedAt       int64  `json:"updated_at" gorm:"not null"`
}

func (AssistantRequestReview) TableName() string { return "assistant_request_reviews" }

type AssistantReviewReset struct {
	UserID    int   `json:"user_id" gorm:"primaryKey"`
	ResetAt   int64 `json:"reset_at" gorm:"not null"`
	UpdatedAt int64 `json:"updated_at" gorm:"not null"`
}

func (AssistantReviewReset) TableName() string { return "assistant_review_resets" }

type AssistantRequestReviewView struct {
	AssistantRequestReview
	Rules []string `json:"rules"`
}

// AssistantRequestReviewFilter is the narrow, administrator-facing filter
// used by the Advanced Security page. It intentionally has no text-search
// fields so the endpoint cannot become a bulk transcript export.
type AssistantRequestReviewFilter struct {
	StartTimestamp int64
	EndTimestamp   int64
	UserID         int
	Category       string
	Group          string
	Decision       string
	ViolationsOnly bool
	ClearOnly      bool
	Limit          int
	Offset         int
}

func boundedAssistantReviewText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

func normalizeAssistantReviewRules(rules []string) []string {
	result := make([]string, 0, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		rule = boundedAssistantReviewText(rule, 160)
		if rule == "" {
			continue
		}
		if _, ok := seen[rule]; ok {
			continue
		}
		seen[rule] = struct{}{}
		result = append(result, rule)
		if len(result) >= AssistantRequestReviewMaxRules {
			break
		}
	}
	return result
}

func SaveAssistantRequestReview(review *AssistantRequestReview, rules []string) error {
	if DB == nil || review == nil || review.UserID <= 0 {
		return errors.New("assistant request review is invalid")
	}
	if review.Status != AssistantRequestReviewStatusCompleted && review.Status != AssistantRequestReviewStatusFailed {
		return errors.New("assistant request review status is invalid")
	}
	if review.CreatedAt <= 0 {
		review.CreatedAt = common.GetTimestamp()
	}
	if review.UpdatedAt <= 0 {
		review.UpdatedAt = review.CreatedAt
	}
	review.Group = boundedAssistantReviewText(review.Group, 64)
	review.ReviewModel = boundedAssistantReviewText(review.ReviewModel, 128)
	review.Intensity = boundedAssistantReviewText(review.Intensity, 16)
	review.Explanation = boundedAssistantReviewText(review.Explanation, AssistantRequestReviewMaxText)
	review.RequestPreview = boundedAssistantReviewText(review.RequestPreview, AssistantRequestReviewMaxPreview)
	review.ResponsePreview = boundedAssistantReviewText(review.ResponsePreview, AssistantRequestReviewMaxPreview)
	review.ErrorMessage = boundedAssistantReviewText(review.ErrorMessage, 512)
	if !review.Violation {
		// A negative verdict intentionally carries no explanation or rules.
		review.Abuse = false
		review.Explanation = ""
		rules = nil
	}
	normalizedRules := normalizeAssistantReviewRules(rules)
	encoded, err := json.Marshal(normalizedRules)
	if err != nil {
		return err
	}
	review.RulesJSON = string(encoded)
	return DB.Create(review).Error
}

func (review AssistantRequestReview) Rules() []string {
	if !review.Violation || strings.TrimSpace(review.RulesJSON) == "" {
		return []string{}
	}
	var rules []string
	if err := json.Unmarshal([]byte(review.RulesJSON), &rules); err != nil {
		return []string{}
	}
	return normalizeAssistantReviewRules(rules)
}

func ListAssistantRequestReviews(userID int, violationsOnly bool, offset, limit int) ([]AssistantRequestReviewView, int64, error) {
	if DB == nil || userID <= 0 {
		return nil, 0, errors.New("assistant review user is invalid")
	}
	if !assistantReviewTablesAvailable(DB) {
		return []AssistantRequestReviewView{}, 0, nil
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > AssistantRequestReviewPageMax {
		limit = AssistantRequestReviewPageMax
	}
	query := DB.Model(&AssistantRequestReview{}).Where("user_id = ?", userID)
	if violationsOnly {
		query = query.Where("violation = ?", true)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []AssistantRequestReview
	if err := query.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	views := make([]AssistantRequestReviewView, 0, len(rows))
	for _, row := range rows {
		views = append(views, AssistantRequestReviewView{AssistantRequestReview: row, Rules: row.Rules()})
	}
	return views, total, nil
}

// ListAssistantRequestReviewsForSecurity returns bounded rows for the unified
// Advanced Security view. The controller projects these rows to metadata and
// never serializes request/response previews.
func ListAssistantRequestReviewsForSecurity(filter AssistantRequestReviewFilter) ([]AssistantRequestReviewView, int64, error) {
	if DB == nil {
		return nil, 0, errors.New("database is not initialized")
	}
	if !assistantReviewTablesAvailable(DB) {
		return []AssistantRequestReviewView{}, 0, nil
	}
	limit := filter.Limit
	if limit <= 0 || limit > AssistantRequestReviewPageMax {
		limit = AssistantRequestReviewPageMax
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	query := DB.Model(&AssistantRequestReview{})
	if filter.StartTimestamp > 0 {
		query = query.Where("created_at >= ?", filter.StartTimestamp)
	}
	if filter.EndTimestamp > 0 {
		query = query.Where("created_at <= ?", filter.EndTimestamp)
	}
	if filter.UserID > 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.Category != "" && filter.Category != "assistant_review" {
		query = query.Where("1 = 0")
	}
	if filter.Group != "" {
		// clause.Column lets GORM quote GROUP for the active dialect while the
		// value remains a bound query argument.
		query = query.Where(clause.Eq{Column: clause.Column{Name: "group"}, Value: filter.Group})
	}
	if filter.Decision != "" && filter.Decision != "violation" && filter.Decision != "clear" {
		query = query.Where("1 = 0")
	}
	if filter.ViolationsOnly || filter.Decision == "violation" {
		query = query.Where("violation = ?", true)
	}
	if filter.ClearOnly || filter.Decision == "clear" {
		query = query.Where("status = ? AND violation = ?", AssistantRequestReviewStatusCompleted, false)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []AssistantRequestReview
	// The security projection never needs the retained request/response
	// previews or failure text. Do not load those columns into Go memory for a
	// paginated admin list; the user-scoped history endpoint above remains the
	// only path that can request them.
	groupColumn := query.Statement.Quote("group")
	rowQuery := query.Select("id, user_id, conversation_id, request_id, " + groupColumn + ", review_model, intensity, status, violation, abuse, rules_json, explanation, created_at, updated_at")
	if err := rowQuery.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	views := make([]AssistantRequestReviewView, 0, len(rows))
	for _, row := range rows {
		views = append(views, AssistantRequestReviewView{AssistantRequestReview: row, Rules: row.Rules()})
	}
	return views, total, nil
}

func ResetAssistantReviewViolations(userID int, now int64) error {
	if DB == nil || userID <= 0 {
		return errors.New("assistant review user is invalid")
	}
	if !assistantReviewTablesAvailable(DB) {
		return errors.New("assistant review storage is unavailable")
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	reset := AssistantReviewReset{UserID: userID, ResetAt: now, UpdatedAt: now}
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"reset_at", "updated_at"}),
	}).Create(&reset).Error
}

func AssistantReviewResetAt(userID int) (int64, error) {
	if DB == nil || userID <= 0 {
		return 0, errors.New("assistant review user is invalid")
	}
	if !assistantReviewTablesAvailable(DB) {
		return 0, nil
	}
	var reset AssistantReviewReset
	if err := DB.Where("user_id = ?", userID).First(&reset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return reset.ResetAt, nil
}

func AssistantReviewViolationCount(userID int) (int64, error) {
	if DB == nil || userID <= 0 {
		return 0, errors.New("assistant review user is invalid")
	}
	if !assistantReviewTablesAvailable(DB) {
		return 0, nil
	}
	var row struct {
		Count int64 `gorm:"column:violation_count"`
	}
	err := AssistantReviewViolationTotals(DB).
		Where("assistant_request_reviews.user_id = ?", userID).
		Scan(&row).Error
	return row.Count, err
}

func AssistantReviewViolationTotals(tx *gorm.DB) *gorm.DB {
	if tx == nil {
		tx = DB
	}
	return tx.Model(&AssistantRequestReview{}).
		Select("assistant_request_reviews.user_id, COUNT(*) AS violation_count").
		Joins("LEFT JOIN assistant_review_resets ON assistant_review_resets.user_id = assistant_request_reviews.user_id").
		Where("assistant_request_reviews.status = ? AND assistant_request_reviews.violation = ? AND assistant_request_reviews.created_at > COALESCE(assistant_review_resets.reset_at, 0)", AssistantRequestReviewStatusCompleted, true).
		Group("assistant_request_reviews.user_id")
}

func assistantReviewTablesAvailable(tx *gorm.DB) bool {
	if tx == nil {
		return false
	}
	return tx.Migrator().HasTable(&AssistantRequestReview{}) && tx.Migrator().HasTable(&AssistantReviewReset{})
}

// AssistantRequestReviewTablesAvailable lets bounded admin projections tell
// the UI whether per-request records are present in this deployment. It does
// not expose schema details or allow callers to bypass the normal ACL.
func AssistantRequestReviewTablesAvailable() bool {
	return assistantReviewTablesAvailable(DB)
}

func canViewAssistantReviewViolations(viewerUserID, viewerRole int, user *User) bool {
	if user == nil || user.Id <= 0 || viewerUserID <= 0 {
		return false
	}
	if viewerUserID == user.Id {
		return true
	}
	return viewerRole >= common.RoleAdminUser && viewerRole > user.Role
}

// PopulateAssistantReviewViolationCountsForViewer attaches counts only to
// accounts the viewer may inspect. Keeping the ACL in the projection layer is
// important: hiding the button in the web UI must not be the only protection.
func PopulateAssistantReviewViolationCountsForViewer(users []*User, viewerUserID, viewerRole int) error {
	return PopulateAssistantReviewViolationCountsForViewerContext(context.Background(), users, viewerUserID, viewerRole)
}

func PopulateAssistantReviewViolationCountsForViewerContext(ctx context.Context, users []*User, viewerUserID, viewerRole int) error {
	if len(users) == 0 {
		return nil
	}
	authorizedIDs := make([]int, 0, len(users))
	if !assistantReviewTablesAvailable(DB.WithContext(ctx)) {
		for _, user := range users {
			if canViewAssistantReviewViolations(viewerUserID, viewerRole, user) {
				zero := int64(0)
				user.AssistantViolationCount = &zero
			} else if user != nil {
				user.AssistantViolationCount = nil
			}
		}
		return nil
	}
	for _, user := range users {
		if !canViewAssistantReviewViolations(viewerUserID, viewerRole, user) {
			if user != nil {
				user.AssistantViolationCount = nil
			}
			continue
		}
		authorizedIDs = append(authorizedIDs, user.Id)
		zero := int64(0)
		user.AssistantViolationCount = &zero
	}
	if len(authorizedIDs) == 0 {
		return nil
	}
	var rows []struct {
		UserID         int   `gorm:"column:user_id"`
		ViolationCount int64 `gorm:"column:violation_count"`
	}
	query := AssistantReviewViolationTotals(DB.WithContext(ctx)).Where("assistant_request_reviews.user_id IN ?", authorizedIDs)
	if err := query.Scan(&rows).Error; err != nil {
		return err
	}
	byID := make(map[int]int64, len(rows))
	for _, row := range rows {
		byID[row.UserID] = row.ViolationCount
	}
	for _, user := range users {
		if user != nil {
			count := byID[user.Id]
			user.AssistantViolationCount = &count
		}
	}
	return nil
}

// PopulateAssistantReviewViolationCounts is retained for internal callers
// that do not have viewer context. Without that context it must fail closed.
func PopulateAssistantReviewViolationCounts(users []*User) error {
	return PopulateAssistantReviewViolationCountsForViewer(users, 0, 0)
}
