package model

import (
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestToolMarketRemoveRecordsRequiresOwnExplicitRevocation(t *testing.T) {
	f := newMarketFixture(t, 0)
	_, token, err := CreateToolMarketToken(f.buyer.Id, "client-a", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	require.ErrorIs(t, RemoveToolMarketGrantRecord(f.buyer.Id, f.grant.ID), ErrToolMarketConflict)
	require.ErrorIs(t, RemoveToolMarketTokenRecord(f.buyer.Id, token.ID), ErrToolMarketConflict)
	require.NoError(t, RevokeToolMarketGrant(f.buyer.Id, f.grant.ID))
	require.NoError(t, RevokeToolMarketToken(f.buyer.Id, token.ID))
	require.ErrorIs(t, RemoveToolMarketGrantRecord(f.author.Id, f.grant.ID), gorm.ErrRecordNotFound)
	require.ErrorIs(t, RemoveToolMarketTokenRecord(f.author.Id, token.ID), gorm.ErrRecordNotFound)
	require.ErrorIs(t, RemoveToolMarketGrantRecord(0, f.grant.ID), ErrToolMarketDenied)
	require.ErrorIs(t, RemoveToolMarketTokenRecord(0, token.ID), ErrToolMarketDenied)
	require.NoError(t, f.db.Model(&User{}).Where("id = ?", f.buyer.Id).Update("status", common.UserStatusDisabled).Error)
	require.ErrorIs(t, RemoveToolMarketGrantRecord(f.buyer.Id, f.grant.ID), ErrToolMarketDenied)
	require.ErrorIs(t, RemoveToolMarketTokenRecord(f.buyer.Id, token.ID), ErrToolMarketDenied)
	var hidden int64
	require.NoError(t, f.db.Model(&ToolMarketEvent{}).Where("action IN ?", []string{toolMarketGrantHiddenAction, toolMarketTokenHiddenAction}).Count(&hidden).Error)
	require.Zero(t, hidden)
}

func TestToolMarketRemoveRecordsPreservesRevocationAndCreatesFreshAuthorization(t *testing.T) {
	f := newMarketFixture(t, 0)
	oldRaw, oldToken, err := CreateToolMarketToken(f.buyer.Id, "client-a", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	require.NoError(t, RevokeToolMarketGrant(f.buyer.Id, f.grant.ID))
	require.NoError(t, RevokeToolMarketToken(f.buyer.Id, oldToken.ID))
	var oldGrant ToolMarketGrant
	var tombstone ToolMarketToken
	require.NoError(t, f.db.First(&oldGrant, "id = ?", f.grant.ID).Error)
	require.NoError(t, f.db.First(&tombstone, "id = ?", oldToken.ID).Error)
	for range 2 {
		require.NoError(t, RemoveToolMarketGrantRecord(f.buyer.Id, f.grant.ID))
		require.NoError(t, RemoveToolMarketTokenRecord(f.buyer.Id, oldToken.ID))
	}
	for _, kind := range []string{"grants", "tokens"} {
		rows, err := ListToolMarketAccountResources(f.buyer.Id, kind, 0, 100)
		require.NoError(t, err)
		require.Empty(t, rows)
	}
	var afterGrant ToolMarketGrant
	var afterToken ToolMarketToken
	require.NoError(t, f.db.First(&afterGrant, "id = ?", oldGrant.ID).Error)
	require.NoError(t, f.db.First(&afterToken, "id = ?", tombstone.ID).Error)
	require.Equal(t, oldGrant, afterGrant, "hiding must not change grant usage or revocation")
	require.Equal(t, tombstone, afterToken, "the complete token tombstone remains unchanged")
	for _, action := range []string{toolMarketGrantHiddenAction, toolMarketTokenHiddenAction} {
		var count int64
		require.NoError(t, f.db.Model(&ToolMarketEvent{}).Where("actor_id = ? AND action = ?", f.buyer.Id, action).Count(&count).Error)
		require.EqualValues(t, 1, count, "repeated removal must not duplicate audit markers")
	}
	newRaw, newToken, err := CreateToolMarketToken(f.buyer.Id, "client-a", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	newGrant, err := CreateToolMarketGrant(f.buyer.Id, ToolMarketGrant{ClientID: "client-a", ToolID: f.tool.ToolID, VersionID: f.tool.VersionID, MaxCalls: 2, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	require.NotEqual(t, oldToken.ID, newToken.ID)
	require.NotEqual(t, oldGrant.ID, newGrant.ID)
	_, err = VerifyToolMarketToken(oldRaw)
	require.ErrorIs(t, err, ErrToolMarketDenied)
	_, err = VerifyToolMarketToken(newRaw)
	require.NoError(t, err)
	_, _, err = ReserveToolMarketCall(f.input("old-hidden-grant"))
	require.ErrorIs(t, err, ErrToolMarketDenied)
	input := f.input("new-visible-grant")
	input.GrantID = newGrant.ID
	_, _, err = ReserveToolMarketCall(input)
	require.NoError(t, err)
	rows, err := ListToolMarketAccountResources(f.buyer.Id, "grants", 0, 1)
	require.NoError(t, err)
	visible := rows.([]ToolMarketGrant)
	require.Len(t, visible, 1, "visibility filtering happens before pagination")
	require.Equal(t, newGrant.ID, visible[0].ID)
}

func TestToolMarketRecordVisibilityIsScopedToOwningAccount(t *testing.T) {
	f := newMarketFixture(t, 0)
	require.NoError(t, marketEvent(f.db, f.author.Id, f.grant.ID, toolMarketGrantHiddenAction, nil))
	rows, err := ListToolMarketAccountResources(f.buyer.Id, "grants", 0, 100)
	require.NoError(t, err)
	require.Equal(t, []ToolMarketGrant{*f.grant}, rows)
}

func TestToolMarketRecordVisibilityMySQLUsesExactResourceAndAction(t *testing.T) {
	fixture := marketTestDB(t)
	connection, err := fixture.DB()
	require.NoError(t, err)
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: connection, SkipInitializeWithVersion: true}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	require.NoError(t, err)
	var rows []ToolMarketGrant
	query := db.Where("user_id = ?", 7).Scopes(marketVisibleAccountRecords(7, toolMarketGrantHiddenAction)).Find(&rows)
	require.NoError(t, query.Error)
	sql := query.Statement.SQL.String()
	require.Contains(t, sql, "user_id = ?")
	require.Contains(t, sql, "actor_id = ?")
	require.Contains(t, sql, "CAST(id AS BINARY) NOT IN (SELECT CAST(object_id AS BINARY)")
	require.Contains(t, sql, "`action` = CAST(? AS BINARY)")
	require.Equal(t, []any{7, 7, toolMarketGrantHiddenAction}, query.Statement.Vars)
}

func TestToolMarketRemoveClientKeepsCallsBudgetsAndSettlement(t *testing.T) {
	f := newMarketFixture(t, 100)
	oldRaw, oldToken, err := CreateToolMarketToken(f.buyer.Id, "client-a", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	require.NoError(t, SetToolMarketBudget(f.buyer.Id, "client", "client-a", 1000))
	call, _, err := ReserveToolMarketCall(f.input("remove-in-flight"))
	require.NoError(t, err)
	started, err := StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.True(t, started)
	_, err = DisconnectToolMarketClient(f.buyer.Id, "client-a")
	require.NoError(t, err)
	var beforeCall ToolMarketCall
	var beforeBudget ToolMarketBudget
	require.NoError(t, f.db.First(&beforeCall, "id = ?", call.ID).Error)
	require.NoError(t, f.db.First(&beforeBudget, "user_id = ? AND scope = ? AND scope_id = ?", f.buyer.Id, "client", "client-a").Error)
	var priorEvents []ToolMarketEvent
	require.NoError(t, f.db.Order("id").Find(&priorEvents).Error)
	result, err := RemoveToolMarketClient(f.buyer.Id, "client-a")
	require.NoError(t, err)
	require.Equal(t, &ToolMarketClientRemoval{ClientID: "client-a", TokensHidden: 1, GrantsHidden: 1}, result)
	for _, kind := range []string{"grants", "tokens", "installations"} {
		rows, err := ListToolMarketAccountResources(f.buyer.Id, kind, 0, 100)
		require.NoError(t, err)
		require.Empty(t, rows)
	}
	var afterCall ToolMarketCall
	var afterBudget ToolMarketBudget
	require.NoError(t, f.db.First(&afterCall, "id = ?", call.ID).Error)
	require.NoError(t, f.db.First(&afterBudget, "user_id = ? AND scope = ? AND scope_id = ?", f.buyer.Id, "client", "client-a").Error)
	require.Equal(t, beforeCall, afterCall)
	require.Equal(t, beforeBudget, afterBudget)
	for _, event := range priorEvents {
		var after ToolMarketEvent
		require.NoError(t, f.db.First(&after, "id = ?", event.ID).Error)
		require.Equal(t, event, after)
	}
	var token ToolMarketToken
	require.NoError(t, f.db.First(&token, "id = ?", oldToken.ID).Error)
	require.NotZero(t, token.RevokedAt)
	require.Equal(t, oldToken.Digest, token.Digest)
	_, err = VerifyToolMarketToken(oldRaw)
	require.ErrorIs(t, err, ErrToolMarketDenied)
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	var settled ToolMarketGrant
	require.NoError(t, f.db.First(&settled, "id = ?", f.grant.ID).Error)
	require.Equal(t, 100, settled.SpentQuota)
	require.Equal(t, 1, settled.SuccessfulCalls)
	require.Zero(t, settled.ReservedQuota)
	var transfers int64
	require.NoError(t, f.db.Model(&ToolMarketTransfer{}).Where("call_id = ?", call.ID).Count(&transfers).Error)
	require.EqualValues(t, 2, transfers)
	result, err = RemoveToolMarketClient(f.buyer.Id, "client-a")
	require.NoError(t, err)
	require.Equal(t, &ToolMarketClientRemoval{ClientID: "client-a"}, result)
}

func TestToolMarketRemoveClientRejectsActiveAccessAndDoesNotCrossAccounts(t *testing.T) {
	f := newMarketFixture(t, 0)
	_, token, err := CreateToolMarketToken(f.buyer.Id, "client-a", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	require.NoError(t, RevokeToolMarketGrant(f.buyer.Id, f.grant.ID))
	_, err = RemoveToolMarketClient(f.buyer.Id, "client-a")
	require.ErrorIs(t, err, ErrToolMarketConflict, "an unrevoked token blocks removing the client")
	require.NoError(t, RevokeToolMarketToken(f.buyer.Id, token.ID))
	otherRaw, _, err := CreateToolMarketToken(f.author.Id, "client-a", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	otherClientRaw, _, err := CreateToolMarketToken(f.buyer.Id, "Client-a", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	result, err := RemoveToolMarketClient(f.buyer.Id, "client-a")
	require.NoError(t, err)
	require.EqualValues(t, 1, result.ToolsUnloaded)
	for _, raw := range []string{otherRaw, otherClientRaw} {
		_, err = VerifyToolMarketToken(raw)
		require.NoError(t, err)
	}
	rows, err := ListToolMarketAccountResources(f.buyer.Id, "tokens", 0, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	_, err = RemoveToolMarketClient(f.buyer.Id, "foreign-client")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	for _, client := range []string{ToolMarketWebClient, "oauth:lmm-pi"} {
		_, err = RemoveToolMarketClient(f.buyer.Id, client)
		require.ErrorIs(t, err, ErrToolMarketInput)
	}
}

func TestToolMarketRemoveClientActiveGrantPreventsAnyHiddenRows(t *testing.T) {
	f := newMarketFixture(t, 0)
	_, token, err := CreateToolMarketToken(f.buyer.Id, "client-a", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	require.NoError(t, RevokeToolMarketToken(f.buyer.Id, token.ID))
	_, err = RemoveToolMarketClient(f.buyer.Id, "client-a")
	require.ErrorIs(t, err, ErrToolMarketConflict)
	rows, err := ListToolMarketAccountResources(f.buyer.Id, "tokens", 0, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1, "a failure must not partially hide the revoked token")
}

func TestToolMarketRemoveClientWithOnlyInstallationsIsIdempotent(t *testing.T) {
	f := newMarketFixture(t, 0)
	require.NoError(t, SetToolMarketInstallation(f.buyer.Id, "loaded-only", f.tool.ToolID, f.tool.VersionID, true))
	result, err := RemoveToolMarketClient(f.buyer.Id, "loaded-only")
	require.NoError(t, err)
	require.Equal(t, &ToolMarketClientRemoval{ClientID: "loaded-only", ToolsUnloaded: 1}, result)
	result, err = RemoveToolMarketClient(f.buyer.Id, "loaded-only")
	require.NoError(t, err)
	require.Equal(t, &ToolMarketClientRemoval{ClientID: "loaded-only"}, result)
	var auditCount int64
	require.NoError(t, f.db.Model(&ToolMarketEvent{}).Where("actor_id = ? AND action = ?", f.buyer.Id, "client.remove").Count(&auditCount).Error)
	require.EqualValues(t, 1, auditCount)
}

func TestToolMarketRemoveClientRollsBackVisibilityWithInstallationFailure(t *testing.T) {
	f := newMarketFixture(t, 0)
	_, token, err := CreateToolMarketToken(f.buyer.Id, "client-a", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	require.NoError(t, RevokeToolMarketGrant(f.buyer.Id, f.grant.ID))
	require.NoError(t, RevokeToolMarketToken(f.buyer.Id, token.ID))
	failure := errors.New("simulated installation removal failure")
	const callback = "test:client-remove-failure"
	require.NoError(t, f.db.Callback().Delete().Before("gorm:delete").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "ToolMarketInstallation" {
			tx.AddError(failure)
		}
	}))
	t.Cleanup(func() { _ = f.db.Callback().Delete().Remove(callback) })
	result, err := RemoveToolMarketClient(f.buyer.Id, "client-a")
	require.ErrorIs(t, err, failure)
	require.Nil(t, result)
	for _, kind := range []string{"grants", "tokens", "installations"} {
		rows, err := ListToolMarketAccountResources(f.buyer.Id, kind, 0, 100)
		require.NoError(t, err)
		require.Len(t, rows, 1)
	}
}
