/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package model

import (
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantWeeklyDiscountIsOnePerWeekAndClaimIsIdempotent(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&Option{}, &TopUp{}, &DiscountCode{}, &DiscountCodeReservation{}, &AssistantWeeklyDiscount{}))
	user := User{Username: "weekly-discount-user", Email: "weekly@example.com", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	reward, created, err := DecideAssistantWeeklyDiscountAt(user.Id, 12, 7, "Clear and constructive use of the service.", 2, 48, now)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, AssistantWeeklyDiscountOffered, reward.Status)

	retry, created, err := DecideAssistantWeeklyDiscountAt(user.Id, 13, 10, "A retry must not replace this week's decision.", 3, 70, now)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, reward.Id, retry.Id)
	assert.Equal(t, 7, retry.DiscountPercent)

	claimed, alreadyClaimed, err := ClaimAssistantWeeklyDiscountAt(user.Id, now)
	require.NoError(t, err)
	assert.False(t, alreadyClaimed)
	assert.Equal(t, AssistantWeeklyDiscountClaimed, claimed.Status)
	assert.Len(t, claimed.Code, 23)
	assert.Equal(t, 7, claimed.DiscountPercent)
	var storedCode DiscountCode
	require.NoError(t, db.Where("id = ?", claimed.CodeId).First(&storedCode).Error)
	assert.Equal(t, int64(1), storedCode.MaxUses)
	_, err = ValidateDiscountCodeForUser(claimed.Code, 1, now.Unix(), user.Id)
	assert.NoError(t, err)
	require.NoError(t, db.Model(&DiscountCode{}).Where("id = ?", claimed.CodeId).Update("used_count", 1).Error)
	_, err = ValidateDiscountCodeForUser(claimed.Code, 1, now.Unix(), user.Id)
	assert.ErrorIs(t, err, ErrDiscountCodeExhausted)

	claimedAgain, alreadyClaimed, err := ClaimAssistantWeeklyDiscountAt(user.Id, now)
	require.NoError(t, err)
	assert.True(t, alreadyClaimed)
	assert.Equal(t, claimed.Code, claimedAgain.Code)
	_, err = ValidateDiscountCodeForUser(claimed.Code, 1, now.Unix(), user.Id+1)
	assert.ErrorIs(t, err, ErrDiscountCodeNotFound)
	var codes int64
	require.NoError(t, db.Model(&DiscountCode{}).Where("owner_user_id = ?", user.Id).Count(&codes).Error)
	assert.Equal(t, int64(1), codes)
}

func TestAssistantWeeklyDiscountIncompleteConversationDoesNotConsumeWeek(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&Option{}, &TopUp{}, &DiscountCode{}, &AssistantWeeklyDiscount{}))
	user := User{Username: "weekly-retry-user", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	now := time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)
	_, _, err := DecideAssistantWeeklyDiscountAt(user.Id, 1, 5, "use case missing", 1, 80, now)
	require.ErrorIs(t, err, ErrAssistantWeeklyDiscountConversationRequired)
	require.ErrorIs(t, err, ErrAssistantWeeklyDiscountInvalid)
	var count int64
	require.NoError(t, db.Model(&AssistantWeeklyDiscount{}).Count(&count).Error)
	require.Zero(t, count)
	reward, created, err := DecideAssistantWeeklyDiscountAt(user.Id, 1, 5, "Chat, research and small coding tasks", 2, 80, now)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, AssistantWeeklyDiscountOffered, reward.Status)
}
