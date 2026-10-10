package model

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketOAuthClientsUseLiveScopedCredentialsWithoutManage(t *testing.T) {
	const issuer = "https://oauth.example.test"
	const resource = issuer + "/api/oauth2"
	const scopes = "market:discover market:invoke"
	for _, test := range []struct {
		name      string
		modify    func(*OAuthServerGrant, *OAuthServerToken, int64)
		eligible  bool
		omitToken bool
	}{
		{name: "invoke without manage", eligible: true},
		{name: "refresh after access expiry", eligible: true, modify: func(_ *OAuthServerGrant, token *OAuthServerToken, _ int64) { token.Kind = "refresh" }},
		{name: "discover only", modify: func(grant *OAuthServerGrant, token *OAuthServerToken, _ int64) {
			grant.Scope, token.Scope = "market:discover", "market:discover"
		}},
		{name: "manage without invoke", modify: func(grant *OAuthServerGrant, token *OAuthServerToken, _ int64) {
			grant.Scope, token.Scope = "market:discover market:manage", "market:discover market:manage"
		}},
		{name: "narrowed token", modify: func(_ *OAuthServerGrant, token *OAuthServerToken, _ int64) { token.Scope = "market:discover" }},
		{name: "token cannot widen consent", modify: func(grant *OAuthServerGrant, _ *OAuthServerToken, _ int64) { grant.Scope = "market:discover" }},
		{name: "scope substring", modify: func(_ *OAuthServerGrant, token *OAuthServerToken, _ int64) {
			token.Scope = "market:discover market:invoke-extra"
		}},
		{name: "expired access", modify: func(_ *OAuthServerGrant, token *OAuthServerToken, now int64) { token.ExpiresAtMs = now - 1 }},
		{name: "expired refresh", modify: func(_ *OAuthServerGrant, token *OAuthServerToken, now int64) {
			token.Kind, token.ExpiresAtMs = "refresh", now-1
		}},
		{name: "used refresh", modify: func(_ *OAuthServerGrant, token *OAuthServerToken, now int64) {
			token.Kind, token.UsedAtMs = "refresh", now
		}},
		{name: "revoked family", modify: func(grant *OAuthServerGrant, _ *OAuthServerToken, now int64) { grant.RevokedAtMs = now }},
		{name: "expired family", modify: func(grant *OAuthServerGrant, _ *OAuthServerToken, now int64) { grant.AbsoluteExpiresAtMs = now - 1 }},
		{name: "different issuer", modify: func(grant *OAuthServerGrant, token *OAuthServerToken, _ int64) {
			grant.Issuer, token.Issuer = "https://other.example.test", "https://other.example.test"
		}},
		{name: "mismatched token issuer", modify: func(_ *OAuthServerGrant, token *OAuthServerToken, _ int64) {
			token.Issuer = "https://other.example.test"
		}},
		{name: "different resource", modify: func(grant *OAuthServerGrant, _ *OAuthServerToken, _ int64) { grant.Resource = issuer + "/unrelated" }},
		{name: "unregistered client", modify: func(grant *OAuthServerGrant, _ *OAuthServerToken, _ int64) { grant.ClientID = "pretend-pi" }},
		{name: "other account", modify: func(grant *OAuthServerGrant, _ *OAuthServerToken, _ int64) { grant.UserID++ }},
		{name: "unsupported binding", modify: func(grant *OAuthServerGrant, _ *OAuthServerToken, _ int64) { grant.BindingMethod = "dpop" }},
		{name: "unexchanged consent", omitToken: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := marketTestDB(t)
			require.NoError(t, MigrateOAuthServer(db))
			user := marketTestUser(t, db, "oauth-client-owner", 0, common.RoleCommonUser)
			now := time.Now().UnixMilli()
			grant := OAuthServerGrant{ID: "family-private", UserID: int64(user.Id), Issuer: issuer, Resource: resource, ClientID: "lmm-pi", Scope: scopes, AbsoluteExpiresAtMs: now + 3600000}
			token := OAuthServerToken{Digest: "private-token-digest", FamilyID: grant.ID, Issuer: issuer, Kind: "access", Scope: scopes, ExpiresAtMs: now + 600000}
			if test.modify != nil {
				test.modify(&grant, &token, now)
			}
			require.NoError(t, db.Create(&grant).Error)
			if !test.omitToken {
				require.NoError(t, db.Create(&token).Error)
			}
			rows, err := ListToolMarketOAuthClients(db, user.Id, issuer, resource, []string{"lmm-pi", "lmm-dsh"}, strings.Fields(scopes), 0, 100)
			require.NoError(t, err)
			if test.eligible {
				require.Equal(t, []ToolMarketOAuthClient{{ClientID: "oauth:lmm-pi"}}, rows)
			} else {
				require.Empty(t, rows)
			}
			// Listing an authorization target neither loads tools nor authorizes
			// them, and never adds a personal credential for a reserved OAuth ID.
			for _, resource := range []any{&ToolMarketToken{}, &ToolMarketInstallation{}, &ToolMarketGrant{}} {
				var count int64
				require.NoError(t, db.Model(resource).Count(&count).Error)
				require.Zero(t, count)
			}
			var after OAuthServerGrant
			require.NoError(t, db.First(&after, "id = ?", grant.ID).Error)
			require.Equal(t, grant.Scope, after.Scope)
		})
	}
}

func TestToolMarketOAuthClientsDeduplicateAndTrackRefreshAndRevocation(t *testing.T) {
	db := marketTestDB(t)
	require.NoError(t, MigrateOAuthServer(db))
	user := marketTestUser(t, db, "oauth-client-owner", 0, common.RoleCommonUser)
	const issuer = "https://oauth.example.test"
	const resource = issuer + "/api/oauth2"
	const scopes = "market:discover market:invoke"
	now := time.Now().UnixMilli()
	for _, item := range []struct{ id, client string }{{"pi-first", "lmm-pi"}, {"pi-second", "lmm-pi"}, {"dsh", "lmm-dsh"}} {
		require.NoError(t, db.Create(&OAuthServerGrant{ID: item.id, UserID: int64(user.Id), Issuer: issuer, Resource: resource, ClientID: item.client, Scope: scopes, AbsoluteExpiresAtMs: now + 3600000}).Error)
		for _, kind := range []string{"access", "refresh"} {
			expires := now + 600000
			if kind == "access" && item.id != "pi-second" {
				expires = now - 1
			}
			require.NoError(t, db.Create(&OAuthServerToken{Digest: item.id + kind, FamilyID: item.id, Issuer: issuer, Kind: kind, Scope: scopes, ExpiresAtMs: expires}).Error)
		}
	}
	list := func(offset, limit int) []ToolMarketOAuthClient {
		rows, err := ListToolMarketOAuthClients(db, user.Id, issuer, resource, []string{"lmm-pi", "lmm-dsh"}, strings.Fields(scopes), offset, limit)
		require.NoError(t, err)
		return rows
	}
	require.Equal(t, []ToolMarketOAuthClient{{ClientID: "oauth:lmm-dsh"}, {ClientID: "oauth:lmm-pi"}}, list(0, 100))
	require.Equal(t, []ToolMarketOAuthClient{{ClientID: "oauth:lmm-pi"}}, list(1, 1), "pagination follows deduplication")
	require.NoError(t, db.Model(&OAuthServerGrant{}).Where("id = ?", "pi-first").Update("revoked_at_ms", now).Error)
	require.Len(t, list(0, 100), 2, "a second eligible family keeps Pi selectable")
	require.NoError(t, db.Model(&OAuthServerToken{}).Where("family_id = ? AND kind = ?", "pi-second", "refresh").Update("used_at_ms", now).Error)
	require.Len(t, list(0, 100), 2, "an unexpired access token remains eligible after its refresh token is consumed")
	require.NoError(t, db.Model(&OAuthServerToken{}).Where("family_id = ? AND kind = ?", "pi-second", "access").Update("expires_at_ms", now-1).Error)
	require.Equal(t, []ToolMarketOAuthClient{{ClientID: "oauth:lmm-dsh"}}, list(0, 100))
	require.NoError(t, db.Model(&OAuthServerGrant{}).Where("id = ?", "dsh").Update("revoked_at_ms", now).Error)
	require.Empty(t, list(0, 100))
}

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
