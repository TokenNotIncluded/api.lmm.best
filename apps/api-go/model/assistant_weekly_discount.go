/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package model

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AssistantWeeklyDiscountOffered    = "offered"
	AssistantWeeklyDiscountClaimed    = "claimed"
	AssistantWeeklyDiscountDeclined   = "declined"
	assistantWeeklyDiscountMaxPercent = 10
)

var (
	ErrAssistantWeeklyDiscountInvalid     = errors.New("weekly assistant discount decision is invalid")
	ErrAssistantWeeklyDiscountUnavailable = errors.New("weekly assistant discount is not available")
)

// AssistantWeeklyDiscount is one server-owned reward decision per user and
// UTC week. The unique user/week index is the idempotency boundary across
// retries and multiple API instances. Code is only populated after claim.
type AssistantWeeklyDiscount struct {
	Id              int64  `json:"id" gorm:"primaryKey"`
	UserId          int    `json:"-" gorm:"not null;uniqueIndex:assistant_weekly_discount_user_week,priority:1"`
	WeekStart       int64  `json:"week_start" gorm:"not null;uniqueIndex:assistant_weekly_discount_user_week,priority:2"`
	ConversationId  int64  `json:"conversation_id,omitempty" gorm:"not null;index"`
	DiscountPercent int    `json:"discount_percent" gorm:"not null"`
	Status          string `json:"status" gorm:"type:varchar(20);not null;index"`
	Reason          string `json:"reason" gorm:"type:varchar(240);not null"`
	CodeId          int    `json:"-" gorm:"index"`
	CreatedAt       int64  `json:"created_at" gorm:"not null;index"`
	ClaimedAt       int64  `json:"claimed_at,omitempty"`
	Code            string `json:"code,omitempty" gorm:"-"`
}

func (AssistantWeeklyDiscount) TableName() string { return "assistant_weekly_discounts" }

func assistantWeekStart(now time.Time) int64 {
	utc := now.UTC()
	day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	daysSinceMonday := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -daysSinceMonday).Unix()
}

func GetAssistantWeeklyDiscount(userID int) (*AssistantWeeklyDiscount, error) {
	return GetAssistantWeeklyDiscountAt(userID, time.Now().UTC())
}

func GetAssistantWeeklyDiscountAt(userID int, now time.Time) (*AssistantWeeklyDiscount, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	var reward AssistantWeeklyDiscount
	err := DB.Where("user_id = ? AND week_start = ?", userID, assistantWeekStart(now)).First(&reward).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := attachAssistantWeeklyDiscountCode(&reward); err != nil {
		return nil, err
	}
	return &reward, nil
}

func attachAssistantWeeklyDiscountCode(reward *AssistantWeeklyDiscount) error {
	if reward == nil || reward.CodeId <= 0 || reward.Status != AssistantWeeklyDiscountClaimed {
		return nil
	}
	var code DiscountCode
	if err := DB.Select("code").Where("id = ? AND owner_user_id = ?", reward.CodeId, reward.UserId).First(&code).Error; err != nil {
		return err
	}
	reward.Code = code.Code
	return nil
}

// DecideAssistantWeeklyDiscountAt stores one bounded AI decision for the
// given UTC week. A repeated call returns the original decision unchanged.
// The conversation evidence is counted by trusted controller code, never by
// client input.
func DecideAssistantWeeklyDiscountAt(userID int, conversationID int64, percent int, reason string, substantiveTurns, substantiveRunes int, now time.Time) (*AssistantWeeklyDiscount, bool, error) {
	if userID <= 0 || conversationID < 0 || percent < 0 || percent > assistantWeeklyDiscountMaxPercent {
		return nil, false, ErrAssistantWeeklyDiscountInvalid
	}
	if substantiveTurns < 2 || substantiveRunes < 8 {
		return nil, false, ErrAssistantWeeklyDiscountInvalid
	}
	reason = strings.TrimSpace(redactAssistantHandoffMessage(reason))
	if len([]rune(reason)) < 2 || len([]rune(reason)) > 240 {
		return nil, false, ErrAssistantWeeklyDiscountInvalid
	}
	var user User
	if err := DB.Select("id", "status").First(&user, "id = ?", userID).Error; err != nil {
		return nil, false, err
	}
	if user.Status != common.UserStatusEnabled {
		return nil, false, ErrAssistantWeeklyDiscountUnavailable
	}

	weekStart := assistantWeekStart(now)
	reward := AssistantWeeklyDiscount{
		UserId:          userID,
		WeekStart:       weekStart,
		ConversationId:  conversationID,
		DiscountPercent: percent,
		Status:          AssistantWeeklyDiscountOffered,
		Reason:          reason,
		CreatedAt:       now.UTC().Unix(),
	}
	if percent == 0 {
		reward.Status = AssistantWeeklyDiscountDeclined
	}
	created := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		var existing AssistantWeeklyDiscount
		findErr := lockForUpdate(tx).Where("user_id = ? AND week_start = ?", userID, weekStart).First(&existing).Error
		if findErr == nil {
			reward = existing
			return nil
		}
		if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&reward)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return lockForUpdate(tx).Where("user_id = ? AND week_start = ?", userID, weekStart).First(&reward).Error
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &reward, created, nil
}

func DecideAssistantWeeklyDiscount(userID int, conversationID int64, percent int, reason string, substantiveTurns, substantiveRunes int) (*AssistantWeeklyDiscount, bool, error) {
	return DecideAssistantWeeklyDiscountAt(userID, conversationID, percent, reason, substantiveTurns, substantiveRunes, time.Now().UTC())
}

func newAssistantWeeklyDiscountCode() (string, error) {
	raw := make([]byte, 8)
	if _, err := cryptorand.Read(raw); err != nil {
		return "", err
	}
	return "AIWEEK-" + strings.ToUpper(hex.EncodeToString(raw)), nil
}

// ClaimAssistantWeeklyDiscount creates a private discount code exactly once.
// Repeated claims return the same code and never create another DiscountCode.
func ClaimAssistantWeeklyDiscount(userID int) (*AssistantWeeklyDiscount, bool, error) {
	return ClaimAssistantWeeklyDiscountAt(userID, time.Now().UTC())
}

// ClaimAssistantWeeklyDiscountAt is the clock-injected implementation used by
// deterministic tests and replay-safe callers. Production callers should use
// ClaimAssistantWeeklyDiscount so the current UTC week is selected.
func ClaimAssistantWeeklyDiscountAt(userID int, now time.Time) (*AssistantWeeklyDiscount, bool, error) {
	if userID <= 0 {
		return nil, false, gorm.ErrInvalidData
	}
	now = now.UTC()
	var reward AssistantWeeklyDiscount
	alreadyClaimed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("user_id = ? AND week_start = ?", userID, assistantWeekStart(now)).First(&reward).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAssistantWeeklyDiscountUnavailable
			}
			return err
		}
		if reward.Status == AssistantWeeklyDiscountClaimed {
			alreadyClaimed = true
			return nil
		}
		if reward.Status != AssistantWeeklyDiscountOffered || reward.DiscountPercent <= 0 || reward.DiscountPercent > assistantWeeklyDiscountMaxPercent {
			return ErrAssistantWeeklyDiscountUnavailable
		}
		codeValue, err := newAssistantWeeklyDiscountCode()
		if err != nil {
			return err
		}
		claimedAt := now.Unix()
		code := DiscountCode{
			Code:            codeValue,
			Name:            "AI weekly conversation discount",
			OwnerUserID:     userID,
			DiscountPercent: reward.DiscountPercent,
			MaxUses:         1,
			Status:          DiscountCodeStatusEnabled,
			CreatedBy:       userID,
			CreatedTime:     claimedAt,
			UpdatedTime:     claimedAt,
			StartsTime:      claimedAt,
			ExpiredTime:     reward.WeekStart + 7*24*60*60,
		}
		if err := tx.Create(&code).Error; err != nil {
			return err
		}
		reward.Status = AssistantWeeklyDiscountClaimed
		reward.CodeId = code.Id
		reward.ClaimedAt = claimedAt
		reward.Code = code.Code
		return tx.Model(&AssistantWeeklyDiscount{}).Where("id = ?", reward.Id).Updates(map[string]any{
			"status":     reward.Status,
			"code_id":    reward.CodeId,
			"claimed_at": reward.ClaimedAt,
		}).Error
	})
	if err != nil {
		return nil, false, err
	}
	if alreadyClaimed {
		if err := attachAssistantWeeklyDiscountCode(&reward); err != nil {
			return nil, false, err
		}
	}
	return &reward, alreadyClaimed, nil
}
