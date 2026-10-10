// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package model

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const AuthFlowPurposeAssistantWorkspace = "assistant_workspace"

var ErrAssistantWorkspaceConflict = errors.New("the record changed; reload it before confirming")
var ErrAssistantWorkspaceInput = errors.New("invalid assistant workspace input")

// Templates are private preferences, not a server-wide option or executable code.
// A separate row avoids overwriting unrelated, concurrently saved user settings.
type AssistantOverviewPreference struct {
	UserID    int               `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Templates map[string]string `json:"templates" gorm:"serializer:json;type:text;not null"`
	Revision  int64             `json:"revision" gorm:"not null"`
	UpdatedAt int64             `json:"updated_at" gorm:"not null"`
}

var overviewVariable = regexp.MustCompile(`\$\$|\$[A-Za-z_][A-Za-z0-9_]*`)

func ValidateAssistantOverviewTemplate(language, template string) error {
	switch language {
	case "en", "zh", "zh-TW", "fr", "ja", "ru", "vi":
	default:
		return ErrAssistantWorkspaceInput
	}
	if !utf8.ValidString(template) || utf8.RuneCountInString(template) > 512 || strings.ContainsRune(template, 0) {
		return ErrAssistantWorkspaceInput
	}
	for _, variable := range overviewVariable.FindAllString(template, -1) {
		switch variable {
		case "$$", "$name", "$time", "$date", "$weekday", "$level", "$site", "$balance":
		default:
			return ErrAssistantWorkspaceInput
		}
	}
	return nil
}
func GetAssistantOverviewPreference(userID int) (*AssistantOverviewPreference, error) {
	if userID <= 0 {
		return nil, ErrAssistantWorkspaceInput
	}
	result := AssistantOverviewPreference{UserID: userID, Templates: map[string]string{}}
	if err := DB.Where("user_id = ?", userID).Limit(1).Find(&result).Error; err != nil {
		return nil, err
	}
	if result.Templates == nil {
		result.Templates = map[string]string{}
	}
	return &result, nil
}
func UpdateAssistantOverviewPreferenceTX(tx *gorm.DB, userID int, language, template string, revision int64) (*AssistantOverviewPreference, error) {
	if userID <= 0 || revision < 0 {
		return nil, ErrAssistantWorkspaceInput
	}
	if err := ValidateAssistantOverviewTemplate(language, template); err != nil {
		return nil, err
	}
	empty := AssistantOverviewPreference{UserID: userID, Templates: map[string]string{}}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&empty).Error; err != nil {
		return nil, err
	}
	var current AssistantOverviewPreference
	if err := lockForUpdate(tx).First(&current, "user_id = ?", userID).Error; err != nil {
		return nil, err
	}
	if current.Revision != revision {
		return nil, ErrAssistantWorkspaceConflict
	}
	if current.Templates == nil {
		current.Templates = map[string]string{}
	}
	if strings.TrimSpace(template) == "" {
		delete(current.Templates, language)
	} else {
		current.Templates[language] = template
	}
	current.Revision++
	current.UpdatedAt = time.Now().Unix()
	if err := tx.Save(&current).Error; err != nil {
		return nil, err
	}
	return &current, nil
}

// User visibility means the reporter and administrators, never all accounts.
// Security reports cannot be made user-visible, even through a stale preview.
type AssistantSiteIssue struct {
	ID         int64  `json:"id" gorm:"primaryKey"`
	ReporterID int    `json:"reporter_id" gorm:"not null;index:assistant_issue_owner,priority:1"`
	Kind       string `json:"kind" gorm:"size:16;not null"`
	Title      string `json:"title" gorm:"size:128;not null"`
	Body       string `json:"body" gorm:"type:text;not null"`
	Visibility string `json:"visibility" gorm:"size:16;not null;index:assistant_issue_owner,priority:2"`
	Status     string `json:"status" gorm:"size:24;not null;index"`
	Revision   int64  `json:"revision" gorm:"not null"`
	CreatedAt  int64  `json:"created_at" gorm:"not null"`
	UpdatedAt  int64  `json:"updated_at" gorm:"not null;index"`
}
type AssistantSiteIssueEvent struct {
	Visibility string `json:"visibility" gorm:"size:16;not null;default:admin"`
	ID         int64  `json:"id" gorm:"primaryKey"`
	IssueID    int64  `json:"issue_id" gorm:"not null;index"`
	ActorID    int    `json:"-" gorm:"not null"`
	Status     string `json:"status" gorm:"size:24;not null"`
	Note       string `json:"note" gorm:"type:text;not null"`
	CreatedAt  int64  `json:"created_at" gorm:"not null"`
}
type AssistantSiteIssueInput struct {
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Visibility string `json:"visibility"`
}
type AssistantSiteIssueUpdate struct {
	ID         int64  `json:"issue_id"`
	Revision   int64  `json:"revision"`
	Status     string `json:"status"`
	Visibility string `json:"visibility"`
	Note       string `json:"note"`
}

func NormalizeAssistantSiteIssue(input AssistantSiteIssueInput) (AssistantSiteIssueInput, error) {
	switch input.Kind {
	case "bug", "security", "experience", "feature":
	default:
		return input, ErrAssistantWorkspaceInput
	}
	if input.Kind == "security" {
		input.Visibility = "admin"
	}
	if input.Visibility != "user" && input.Visibility != "admin" {
		return input, ErrAssistantWorkspaceInput
	}
	input.Title = strings.TrimSpace(redactAssistantHandoffMessage(input.Title))
	input.Body = strings.TrimSpace(redactAssistantHandoffMessage(input.Body))
	if !utf8.ValidString(input.Title) || !utf8.ValidString(input.Body) || utf8.RuneCountInString(input.Title) < 2 || utf8.RuneCountInString(input.Title) > 128 || utf8.RuneCountInString(input.Body) < 5 || utf8.RuneCountInString(input.Body) > 4000 {
		return input, ErrAssistantWorkspaceInput
	}
	return input, nil
}
func CreateAssistantSiteIssueTX(tx *gorm.DB, userID int, input AssistantSiteIssueInput) (*AssistantSiteIssue, error) {
	input, err := NormalizeAssistantSiteIssue(input)
	if err != nil || userID <= 0 {
		return nil, ErrAssistantWorkspaceInput
	}
	issue := AssistantSiteIssue{ReporterID: userID, Kind: input.Kind, Title: input.Title, Body: input.Body, Visibility: input.Visibility, Status: "open", Revision: 1, CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()}
	if err := tx.Create(&issue).Error; err != nil {
		return nil, err
	}
	if err := tx.Create(&AssistantSiteIssueEvent{IssueID: issue.ID, ActorID: userID, Visibility: issue.Visibility, Status: "open", Note: "", CreatedAt: issue.CreatedAt}).Error; err != nil {
		return nil, err
	}
	return &issue, nil
}
func assistantIssueScope(db *gorm.DB, actor *User) *gorm.DB {
	query := db.Model(&AssistantSiteIssue{})
	if actor.Role < common.RoleAdminUser {
		query = query.Where("reporter_id = ? AND visibility = ?", actor.Id, "user")
	}
	return query
}
func ListAssistantSiteIssues(actorID int, beforeID int64) ([]AssistantSiteIssue, error) {
	var actor User
	if err := DB.Select("id", "role", "status").First(&actor, actorID).Error; err != nil {
		return nil, err
	}
	if actor.Status != common.UserStatusEnabled || beforeID < 0 {
		return nil, ErrAssistantWorkspaceInput
	}
	query := assistantIssueScope(DB, &actor)
	if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}
	rows := []AssistantSiteIssue{}
	err := query.Order("id DESC").Limit(20).Find(&rows).Error
	return rows, err
}
func GetAssistantSiteIssue(actorID int, issueID int64) (*AssistantSiteIssue, []AssistantSiteIssueEvent, error) {
	var actor User
	if issueID <= 0 {
		return nil, nil, ErrAssistantWorkspaceInput
	}
	if err := DB.Select("id", "role", "status").First(&actor, actorID).Error; err != nil {
		return nil, nil, err
	}
	if actor.Status != common.UserStatusEnabled {
		return nil, nil, ErrAssistantWorkspaceInput
	}
	var issue AssistantSiteIssue
	if err := assistantIssueScope(DB, &actor).Where("id = ?", issueID).First(&issue).Error; err != nil {
		return nil, nil, err
	}
	events := []AssistantSiteIssueEvent{}
	query := DB.Where("issue_id = ?", issue.ID)
	if actor.Role < common.RoleAdminUser {
		query = query.Where("visibility = ?", "user")
	}
	if err := query.Order("id DESC").Limit(30).Find(&events).Error; err != nil {
		return nil, nil, err
	}
	return &issue, events, nil
}
func UpdateAssistantSiteIssueTX(tx *gorm.DB, actor *User, input AssistantSiteIssueUpdate) (*AssistantSiteIssue, error) {
	if actor == nil || actor.Role < common.RoleAdminUser || actor.Status != common.UserStatusEnabled || input.ID <= 0 || input.Revision < 1 {
		return nil, ErrAssistantWorkspaceInput
	}
	switch input.Status {
	case "open", "triaged", "in_progress", "resolved", "declined":
	default:
		return nil, ErrAssistantWorkspaceInput
	}
	if input.Visibility != "user" && input.Visibility != "admin" {
		return nil, ErrAssistantWorkspaceInput
	}
	input.Note = strings.TrimSpace(redactAssistantHandoffMessage(input.Note))
	if !utf8.ValidString(input.Note) || utf8.RuneCountInString(input.Note) > 1500 {
		return nil, ErrAssistantWorkspaceInput
	}
	var issue AssistantSiteIssue
	if err := lockForUpdate(tx).First(&issue, input.ID).Error; err != nil {
		return nil, err
	}
	if issue.Revision != input.Revision {
		return nil, ErrAssistantWorkspaceConflict
	}
	if issue.Kind == "security" {
		input.Visibility = "admin"
	}
	issue.Status = input.Status
	issue.Visibility = input.Visibility
	issue.Revision++
	issue.UpdatedAt = time.Now().Unix()
	if err := tx.Save(&issue).Error; err != nil {
		return nil, err
	}
	if err := tx.Create(&AssistantSiteIssueEvent{IssueID: issue.ID, ActorID: actor.Id, Visibility: issue.Visibility, Status: issue.Status, Note: input.Note, CreatedAt: issue.UpdatedAt}).Error; err != nil {
		return nil, err
	}
	return &issue, nil
}

// Confirm the exact server-stored draft. The model never gets a reusable
// mutation token and cannot substitute another account, draft or login session.
func ConfirmAssistantWorkspace(token string, match AuthFlowMatch, sessionVersion, authVersion int64, action func(*gorm.DB, *User, *AuthFlow) (any, error)) (any, error) {
	if match.Purpose != AuthFlowPurposeAssistantWorkspace || match.UserId <= 0 || match.SessionId == "" || sessionVersion <= 0 || authVersion <= 0 || action == nil {
		return nil, ErrAssistantProfileSessionInvalid
	}
	var result any
	err := marketTransaction(DB, func(tx *gorm.DB) error {
		flow, err := ConsumeAuthFlowWithTx(tx, token, match)
		if err != nil {
			return err
		}
		var session UserSession
		if err := lockForUpdate(tx).Where("sid = ?", match.SessionId).First(&session).Error; err != nil {
			return ErrAssistantProfileSessionInvalid
		}
		now := time.Now().Unix()
		if session.UserID != match.UserId || session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now || session.Version != sessionVersion || session.UserAuthVersion != authVersion {
			return ErrAssistantProfileSessionInvalid
		}
		var user User
		if err := lockForUpdate(tx).First(&user, match.UserId).Error; err != nil {
			return ErrAssistantProfileSessionInvalid
		}
		if user.Status != common.UserStatusEnabled || user.AuthVersion != authVersion || (user.GetSetting().IsSessionAutoLogoutEnabled() && session.CreatedAt < now-int64(UserSessionAutoLogoutAge/time.Second)) {
			return ErrAssistantProfileSessionInvalid
		}
		_, policy, err := ReadAssistantToolPolicyDB(tx)
		if err != nil {
			return err
		}
		level, err := AssistantToolLevelDB(tx, user.Id)
		if err != nil {
			return err
		}
		if !policy.AllowedAtLevel(flow.Provider, level) {
			return ErrToolMarketDenied
		}
		result, err = action(tx, &user, flow)
		return err
	})
	return result, err
}

// Keep arbitrary JSON maps out of the saved confirmation payload.
func DecodeAssistantWorkspacePayload(raw string, target any) error {
	if len(raw) > 16<<10 {
		return ErrAssistantWorkspaceInput
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrAssistantWorkspaceInput
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrAssistantWorkspaceInput
	}
	return nil
}
