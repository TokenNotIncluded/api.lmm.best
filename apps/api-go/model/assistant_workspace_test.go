// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package model

import (
	"errors"
	"fmt"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"testing"
	"time"
)

func assistantWorkspaceTestDB(t *testing.T) *gorm.DB {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&Option{}, &TopUp{}, &AuthFlow{}, &UserSession{}, &AssistantOverviewPreference{}, &AssistantSiteIssue{}, &AssistantSiteIssueEvent{}))
	return db
}
func assistantWorkspaceUser(t *testing.T, db *gorm.DB, name string, role int) User {
	level := 1
	user := User{Username: name, AffCode: name, Email: name + "@example.com", Role: role, Status: common.UserStatusEnabled, AuthVersion: 1, TrustLevelOverride: &level}
	require.NoError(t, db.Create(&user).Error)
	return user
}
func TestAssistantWorkspaceGreetingRevisionAndIsolation(t *testing.T) {
	db := assistantWorkspaceTestDB(t)
	first := assistantWorkspaceUser(t, db, "first", common.RoleCommonUser)
	second := assistantWorkspaceUser(t, db, "second", common.RoleCommonUser)
	empty, err := GetAssistantOverviewPreference(first.Id)
	require.NoError(t, err)
	require.Empty(t, empty.Templates)
	require.Zero(t, empty.Revision)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := UpdateAssistantOverviewPreferenceTX(tx, first.Id, "zh", "HI,$name,现在是$time", 0)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := UpdateAssistantOverviewPreferenceTX(tx, first.Id, "zh", "stale", 0)
		return err
	}), ErrAssistantWorkspaceConflict)
	own, err := GetAssistantOverviewPreference(first.Id)
	require.NoError(t, err)
	require.Equal(t, int64(1), own.Revision)
	require.Equal(t, "HI,$name,现在是$time", own.Templates["zh"])
	other, err := GetAssistantOverviewPreference(second.Id)
	require.NoError(t, err)
	require.Empty(t, other.Templates)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := UpdateAssistantOverviewPreferenceTX(tx, first.Id, "en", "$site — $$ $balance", 1)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := UpdateAssistantOverviewPreferenceTX(tx, first.Id, "zh", "", 2)
		return err
	}))
	own, err = GetAssistantOverviewPreference(first.Id)
	require.NoError(t, err)
	require.NotContains(t, own.Templates, "zh")
	require.Contains(t, own.Templates, "en")
	for _, sample := range []string{"$password", "$time() $unknown", "\x00"} {
		require.Error(t, ValidateAssistantOverviewTemplate("en", sample))
	}
	require.Error(t, ValidateAssistantOverviewTemplate("unknown", "hello"))
	var payload struct {
		Name string `json:"name"`
	}
	require.Error(t, DecodeAssistantWorkspacePayload(`{"name":"a"} {}`, &payload))
	require.Error(t, DecodeAssistantWorkspacePayload(`{"unknown":true}`, &payload))
}
func TestAssistantWorkspaceIssueVisibilityAndAdminUpdates(t *testing.T) {
	db := assistantWorkspaceTestDB(t)
	reporter := assistantWorkspaceUser(t, db, "reporter", common.RoleCommonUser)
	stranger := assistantWorkspaceUser(t, db, "stranger", common.RoleCommonUser)
	admin := assistantWorkspaceUser(t, db, "admin", common.RoleAdminUser)
	var issue, security *AssistantSiteIssue
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		issue, err = CreateAssistantSiteIssueTX(tx, reporter.Id, AssistantSiteIssueInput{Kind: "experience", Title: "Small controls", Body: "The buttons are hard to use on a phone.", Visibility: "user"})
		return err
	}))
	_, events, err := GetAssistantSiteIssue(reporter.Id, issue.ID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	_, _, err = GetAssistantSiteIssue(stranger.Id, issue.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		security, err = CreateAssistantSiteIssueTX(tx, reporter.Id, AssistantSiteIssueInput{Kind: "security", Title: "Private report", Body: "Please inspect the session boundary.", Visibility: "user"})
		return err
	}))
	require.Equal(t, "admin", security.Visibility)
	_, _, err = GetAssistantSiteIssue(reporter.Id, security.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	rows, err := ListAssistantSiteIssues(admin.Id, 0)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	input := AssistantSiteIssueUpdate{ID: issue.ID, Revision: 1, Status: "in_progress", Visibility: "user", Note: "Investigating the layout."}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := UpdateAssistantSiteIssueTX(tx, &reporter, input); return err }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := UpdateAssistantSiteIssueTX(tx, &admin, input); return err }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := UpdateAssistantSiteIssueTX(tx, &admin, input); return err }), ErrAssistantWorkspaceConflict)
	updated, events, err := GetAssistantSiteIssue(reporter.Id, issue.ID)
	require.NoError(t, err)
	require.Equal(t, "in_progress", updated.Status)
	require.Len(t, events, 2)
}
func TestAssistantWorkspaceConfirmationIsSessionBoundSingleUseAndAtomic(t *testing.T) {
	db := assistantWorkspaceTestDB(t)
	user := assistantWorkspaceUser(t, db, "confirm", common.RoleCommonUser)
	now := time.Now()
	session := UserSession{SID: "workspace-test-session", UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: UserSessionStatusActive, CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()}
	require.NoError(t, db.Create(&session).Error)
	token, _, err := CreateAuthFlow(AuthFlowCreate{Purpose: AuthFlowPurposeAssistantWorkspace, Provider: "set_overview_greeting", UserId: user.Id, SessionId: session.SID, Payload: `{}`, ExpiresAt: now.Add(time.Minute)})
	require.NoError(t, err)
	match := AuthFlowMatch{Purpose: AuthFlowPurposeAssistantWorkspace, UserId: user.Id, SessionId: session.SID}
	apply := func(tx *gorm.DB, actor *User, _ *AuthFlow) (any, error) {
		return UpdateAssistantOverviewPreferenceTX(tx, actor.Id, "en", "Hello $name", 0)
	}
	wrong := match
	wrong.SessionId = "other-session"
	_, err = ConfirmAssistantWorkspace(token, wrong, 1, 1, apply)
	require.Error(t, err)
	_, err = ConfirmAssistantWorkspace(token, match, 2, 1, apply)
	require.Error(t, err)
	_, err = ConfirmAssistantWorkspace(token, match, 1, 1, func(tx *gorm.DB, actor *User, flow *AuthFlow) (any, error) {
		_, err := apply(tx, actor, flow)
		require.NoError(t, err)
		return nil, errors.New("rollback")
	})
	require.Error(t, err)
	before, err := GetAssistantOverviewPreference(user.Id)
	require.NoError(t, err)
	require.Empty(t, before.Templates)
	// A live policy change invalidates a pending browser preview without consuming it.
	require.NoError(t, db.Create(&Option{Key: setting.AssistantToolPolicyOptionKey, Value: `{"version":1,"tools":{"set_overview_greeting":false}}`}).Error)
	_, err = ConfirmAssistantWorkspace(token, match, 1, 1, apply)
	require.Error(t, err)
	require.NoError(t, db.Model(&Option{}).Where("key = ?", setting.AssistantToolPolicyOptionKey).Update("value", setting.DefaultAssistantToolPolicy).Error)
	_, err = ConfirmAssistantWorkspace(token, match, 1, 1, apply)
	require.NoError(t, err)
	_, err = ConfirmAssistantWorkspace(token, match, 1, 1, apply)
	require.Error(t, err)
	saved, err := GetAssistantOverviewPreference(user.Id)
	require.NoError(t, err)
	require.Equal(t, int64(1), saved.Revision)
}
func TestAssistantWorkspaceMarketConnectionIsOptInAndDoesNotSpend(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	f := newMarketFixture(t, 101)
	require.NoError(t, f.db.AutoMigrate(&TopUp{}))
	require.NoError(t, f.db.Model(&User{}).Where("id = ?", f.buyer.Id).Update("trust_level_override", 1).Error)
	input := AssistantMarketConnection{ServiceID: f.service.ID, ToolID: f.tool.ToolID, VersionID: f.tool.VersionID}
	limits := AssistantMarketLimits{MaxPriceQuota: 101, MaxTotalQuota: 101, MaxCalls: 1, LifetimeSeconds: 600}
	connect := func(tx *gorm.DB) error {
		_, err := ConnectAssistantMarketToolTX(tx, f.buyer.Id, input, limits)
		return err
	}
	require.Error(t, f.db.Transaction(connect))
	policy := fmt.Sprintf(`{"version":1,"rules":{"call_market_tool":{"min_level":1,"max_level":6,"market_service_ids":[%q]}}}`, f.service.ID)
	require.NoError(t, f.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&Option{Key: setting.AssistantToolPolicyOptionKey, Value: policy}).Error)
	require.NoError(t, f.db.Transaction(connect))
	require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
	grants, err := AssistantMarketGrants(f.buyer.Id, f.tool.ToolID, f.tool.VersionID)
	require.NoError(t, err)
	require.Len(t, grants, 1)
	require.Equal(t, AssistantToolMarketClient, grants[0].ClientID)
	other, err := AssistantMarketGrants(f.author.Id, f.tool.ToolID, f.tool.VersionID)
	require.NoError(t, err)
	require.Empty(t, other)
	limits.MaxCalls = 101
	require.Error(t, f.db.Transaction(connect))
}

func TestAssistantWorkspaceWeeklyLimitIsCheckedAtDecisionAndClaim(t *testing.T) {
	db := assistantWorkspaceTestDB(t)
	require.NoError(t, db.AutoMigrate(&DiscountCode{}, &DiscountCodeReservation{}, &AssistantWeeklyDiscount{}))
	user := assistantWorkspaceUser(t, db, "reward-limit", common.RoleCommonUser)
	policy := func(limit int) {
		require.NoError(t, db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&Option{Key: setting.AssistantToolPolicyOptionKey, Value: fmt.Sprintf(`{"version":1,"rules":{"prepare_weekly_discount":{"min_level":0,"max_level":4,"discount_percent_by_level":{"1":%d}}}}`, limit)}).Error)
	}
	now := time.Now().UTC()
	policy(25)
	_, created, err := DecideAssistantWeeklyDiscountAt(user.Id, 1, 26, "Useful contribution", 2, 48, now)
	require.ErrorIs(t, err, ErrAssistantWeeklyDiscountLimit)
	require.False(t, created)
	reward, created, err := DecideAssistantWeeklyDiscountAt(user.Id, 1, 20, "Useful contribution", 2, 48, now)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, 20, reward.DiscountPercent)
	policy(10)
	_, _, err = ClaimAssistantWeeklyDiscountAt(user.Id, now)
	require.ErrorIs(t, err, ErrAssistantWeeklyDiscountLimit)
	var codes int64
	require.NoError(t, db.Model(&DiscountCode{}).Count(&codes).Error)
	require.Zero(t, codes)
	policy(25)
	claimed, already, err := ClaimAssistantWeeklyDiscountAt(user.Id, now)
	require.NoError(t, err)
	require.False(t, already)
	policy(0)
	again, already, err := ClaimAssistantWeeklyDiscountAt(user.Id, now)
	require.NoError(t, err)
	require.True(t, already)
	require.Equal(t, claimed.Code, again.Code)
}

func TestAssistantWorkspaceInternalNotesStayPrivateAfterPublishing(t *testing.T) {
	db := assistantWorkspaceTestDB(t)
	reporter := assistantWorkspaceUser(t, db, "report-privacy", common.RoleCommonUser)
	admin := assistantWorkspaceUser(t, db, "report-admin", common.RoleAdminUser)
	var issue *AssistantSiteIssue
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		issue, err = CreateAssistantSiteIssueTX(tx, reporter.Id, AssistantSiteIssueInput{Kind: "bug", Title: "Hidden report", Body: "A report requiring internal triage.", Visibility: "admin"})
		return err
	}))
	for _, update := range []AssistantSiteIssueUpdate{
		{ID: issue.ID, Revision: 1, Status: "triaged", Visibility: "admin", Note: "Internal investigation note"},
		{ID: issue.ID, Revision: 2, Status: "resolved", Visibility: "user", Note: "The issue is fixed"},
	} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := UpdateAssistantSiteIssueTX(tx, &admin, update); return err }))
	}
	_, publicEvents, err := GetAssistantSiteIssue(reporter.Id, issue.ID)
	require.NoError(t, err)
	require.Len(t, publicEvents, 1)
	require.Equal(t, "The issue is fixed", publicEvents[0].Note)
	_, allEvents, err := GetAssistantSiteIssue(admin.Id, issue.ID)
	require.NoError(t, err)
	require.Len(t, allEvents, 3)
}

func TestAssistantMarketRevocationStopsReservationAndDispatch(t *testing.T) {
	installPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)
	f := newMarketFixture(t, 100)
	require.NoError(t, f.db.AutoMigrate(&TopUp{}))
	require.NoError(t, f.db.Model(&User{}).Where("id = ?", f.buyer.Id).Update("trust_level_override", 1).Error)
	allow := fmt.Sprintf(`{"version":1,"groups":{},"tools":{},"rules":{"call_market_tool":{"min_level":1,"max_level":6,"market_service_ids":[%q]}}}`, f.service.ID)
	setPolicy := func(raw string) {
		require.NoError(t, f.db.Save(&Option{Key: setting.AssistantToolPolicyOptionKey, Value: raw}).Error)
	}
	setPolicy(allow)
	require.NoError(t, SetToolMarketInstallation(f.buyer.Id, AssistantToolMarketClient, f.tool.ToolID, f.tool.VersionID, true))
	grant, err := CreateToolMarketGrant(f.buyer.Id, ToolMarketGrant{ClientID: AssistantToolMarketClient, ToolID: f.tool.ToolID, VersionID: f.tool.VersionID, MaxPriceQuota: 100, MaxTotalQuota: 500, MaxCalls: 5, ExpiresAt: common.GetTimestamp() + 600})
	require.NoError(t, err)
	input := f.input("assistant-before-revoke")
	input.ClientID, input.GrantID = AssistantToolMarketClient, grant.ID
	call, created, err := ReserveToolMarketCall(input)
	require.NoError(t, err)
	require.True(t, created)
	balance := marketTestBalance(t, f.db, f.buyer.Id)
	setPolicy(setting.DefaultAssistantToolPolicy) // empty allowlist, old grant still exists
	started, err := StartToolMarketCall(call.ID)
	require.Error(t, err)
	require.False(t, started)
	input.RequestKey = "assistant-after-revoke"
	_, created, err = ReserveToolMarketCall(input)
	require.Error(t, err)
	require.False(t, created)
	require.Equal(t, balance, marketTestBalance(t, f.db, f.buyer.Id))
	var saved ToolMarketGrant
	require.NoError(t, f.db.First(&saved, "id = ?", grant.ID).Error)
	require.Equal(t, 1, saved.ReservedCalls)
	require.Equal(t, 0, saved.SuccessfulCalls)
}
