package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketDisconnectIsAtomicScopedAndIdempotent(t *testing.T) {
	f := newMarketFixture(t, 100)
	issueToken := func(userID int, clientID string) string {
		raw, _, err := CreateToolMarketToken(userID, clientID, true, true, common.GetTimestamp()+3600)
		require.NoError(t, err)
		return raw
	}
	ownToken := issueToken(f.buyer.Id, "client-a")
	otherClientToken := issueToken(f.buyer.Id, "client-b")
	otherUserToken := issueToken(f.author.Id, "client-a")
	require.NoError(t, SetToolMarketInstallation(f.author.Id, "client-a", f.tool.ToolID, f.tool.VersionID, true))
	otherGrant, err := CreateToolMarketGrant(f.author.Id, ToolMarketGrant{ClientID: "client-a", ToolID: f.tool.ToolID, VersionID: f.tool.VersionID, MaxPriceQuota: 100, MaxTotalQuota: 100, MaxCalls: 1, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	call, _, err := ReserveToolMarketCall(f.input("disconnect-in-flight"))
	require.NoError(t, err)
	started, err := StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.True(t, started)

	result, err := DisconnectToolMarketClient(f.buyer.Id, "client-a")
	require.NoError(t, err)
	require.Equal(t, &ToolMarketClientDisconnect{ClientID: "client-a", TokensRevoked: 1, GrantsRevoked: 1, ToolsUnloaded: 1}, result)
	_, err = VerifyToolMarketToken(ownToken)
	require.ErrorIs(t, err, ErrToolMarketDenied)
	for _, raw := range []string{otherClientToken, otherUserToken} {
		_, err := VerifyToolMarketToken(raw)
		require.NoError(t, err)
	}
	var grant ToolMarketGrant
	require.NoError(t, f.db.First(&grant, "id = ?", otherGrant.ID).Error)
	require.Zero(t, grant.RevokedAt)
	var count int64
	require.NoError(t, f.db.Model(&ToolMarketInstallation{}).Where("user_id = ? AND client_id = ?", f.author.Id, "client-a").Count(&count).Error)
	require.EqualValues(t, 1, count)
	_, _, err = ReserveToolMarketCall(f.input("after-disconnect"))
	require.Error(t, err)

	// Disconnection neither refunds an in-flight hold nor prevents its settlement.
	require.Equal(t, 900, marketTestBalance(t, f.db, f.buyer.Id))
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.Equal(t, 900, marketTestBalance(t, f.db, f.buyer.Id))
	var revoked ToolMarketGrant
	require.NoError(t, f.db.First(&revoked, "id = ?", f.grant.ID).Error)
	require.NotZero(t, revoked.RevokedAt)
	firstRevocation := revoked.RevokedAt
	result, err = DisconnectToolMarketClient(f.buyer.Id, "client-a")
	require.NoError(t, err)
	require.Equal(t, &ToolMarketClientDisconnect{ClientID: "client-a"}, result)
	require.NoError(t, f.db.First(&revoked, "id = ?", f.grant.ID).Error)
	require.Equal(t, firstRevocation, revoked.RevokedAt)
}

func TestToolMarketDisconnectRollsBackAllAccessChanges(t *testing.T) {
	f := newMarketFixture(t, 100)
	raw, _, err := CreateToolMarketToken(f.buyer.Id, "client-a", true, true, common.GetTimestamp()+3600)
	require.NoError(t, err)
	failure := errors.New("simulated installation storage failure")
	const callback = "test:market-disconnect-failure"
	require.NoError(t, f.db.Callback().Delete().Before("gorm:delete").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "ToolMarketInstallation" {
			tx.AddError(failure)
		}
	}))
	t.Cleanup(func() { _ = f.db.Callback().Delete().Remove(callback) })
	result, err := DisconnectToolMarketClient(f.buyer.Id, "client-a")
	require.ErrorIs(t, err, failure)
	require.Nil(t, result)
	_, err = VerifyToolMarketToken(raw)
	require.NoError(t, err)
	var grant ToolMarketGrant
	require.NoError(t, f.db.First(&grant, "id = ?", f.grant.ID).Error)
	require.Zero(t, grant.RevokedAt)
	var count int64
	require.NoError(t, f.db.Model(&ToolMarketInstallation{}).Where("user_id = ? AND client_id = ?", f.buyer.Id, "client-a").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestToolMarketDisconnectRejectsReservedClientsAndDisabledUsers(t *testing.T) {
	f := newMarketFixture(t, 0)
	for _, client := range []string{"", " client-a", "client-a ", ToolMarketWebClient, "oauth:lmm-pi", strings.Repeat("x", 129)} {
		_, err := DisconnectToolMarketClient(f.buyer.Id, client)
		require.ErrorIs(t, err, ErrToolMarketInput)
	}
	_, err := DisconnectToolMarketClient(0, "client-a")
	require.ErrorIs(t, err, ErrToolMarketDenied)
	require.NoError(t, f.db.Model(&User{}).Where("id = ?", f.buyer.Id).Update("status", common.UserStatusDisabled).Error)
	_, err = DisconnectToolMarketClient(f.buyer.Id, "client-a")
	require.ErrorIs(t, err, ErrToolMarketDenied)
}
