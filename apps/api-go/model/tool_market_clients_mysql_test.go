package model

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketClientIdentityMySQL(t *testing.T) {
	db := marketDrawingMySQLDB(t)
	var collation string
	require.NoError(t, db.Raw("SELECT COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'tool_market_installations' AND COLUMN_NAME = 'client_id'").Scan(&collation).Error)
	require.Equal(t, "utf8mb4_0900_ai_ci", collation, "exercise the server's ordinary case/accent-insensitive schema, not a binary substitute")
	root := marketTestUser(t, db, "mysql-client-root", 0, common.RoleRootUser)
	author := marketTestUser(t, db, "mysql-client-author", 0, common.RoleCommonUser)
	require.NoError(t, SetToolMarketConfig(root.Id, ToolMarketConfig{Enabled: true, RecipientID: root.Id}))
	for index, pair := range []struct{ canonical, alias string }{
		{ToolMarketWebClient, "WEB-MARKET"},
		{ToolMarketWebClient, "wéb-market"},
		{"oauth:lmm-pi", "OAUTH:LMM-PI"},
		{"oauth:lmm-pi", "óauth:lmm-pi"},
		{"Agent-A", "agent-a"},
	} {
		t.Run(pair.alias, func(t *testing.T) {
			user := marketTestUser(t, db, fmt.Sprintf("mysql-client-buyer-%d", index), 1000, common.RoleCommonUser)
			service, err := SaveToolMarketDraft(author.Id, "", marketTestDraft(100))
			require.NoError(t, err)
			tool := marketTestPublish(t, db, root.Id, service)
			require.NoError(t, SetToolMarketInstallation(user.Id, pair.canonical, tool.ToolID, tool.VersionID, true))
			grant, err := CreateToolMarketGrant(user.Id, ToolMarketGrant{ClientID: pair.canonical, ToolID: tool.ToolID, VersionID: tool.VersionID, MaxPriceQuota: 100, MaxTotalQuota: 100, MaxCalls: 1, ExpiresAt: common.GetTimestamp() + 3600})
			require.NoError(t, err)
			raw, token, err := CreateToolMarketToken(user.Id, pair.alias, true, false, common.GetTimestamp()+3600)
			require.NoError(t, err, "existing legal client names remain legal identities")
			verified, err := VerifyToolMarketToken(raw)
			require.NoError(t, err)
			require.Equal(t, pair.alias, verified.ClientID)

			t.Run("grant and installation reads", func(t *testing.T) {
				_, err := GetToolMarketExecution(user.Id, verified.ClientID, tool.ToolID, tool.VersionID, grant.ID)
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				rows, err := ListToolMarketExecutions(user.Id, verified.ClientID)
				require.NoError(t, err)
				require.Empty(t, rows)
				canonical, err := GetToolMarketExecution(user.Id, pair.canonical, tool.ToolID, tool.VersionID, grant.ID)
				require.NoError(t, err)
				require.Equal(t, pair.canonical, canonical.Grant.ClientID)
				// An explicit grant for the alias still cannot borrow the
				// canonical client's installation, including at dispatch.
				ownGrant, err := CreateToolMarketGrant(user.Id, ToolMarketGrant{ClientID: pair.alias, ToolID: tool.ToolID, VersionID: tool.VersionID, MaxPriceQuota: 100, MaxTotalQuota: 100, MaxCalls: 1, ExpiresAt: common.GetTimestamp() + 3600})
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, db.Delete(&ToolMarketGrant{}, "id = ?", ownGrant.ID).Error) })
				_, err = GetToolMarketExecution(user.Id, pair.alias, tool.ToolID, tool.VersionID, ownGrant.ID)
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				require.ErrorIs(t, marketAuthorizeCallDispatch(db, ToolMarketCall{UserID: user.Id, ClientID: pair.alias, ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID}), gorm.ErrRecordNotFound)
				require.ErrorIs(t, marketAuthorizeCallDispatch(db, ToolMarketCall{UserID: user.Id, ClientID: pair.alias, ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: ownGrant.ID}), gorm.ErrRecordNotFound)
			})

			call := ToolMarketCall{ID: marketDigest([]any{user.Id, pair.canonical, "existing-call"}), UserID: user.Id, ClientID: pair.canonical, ServiceID: service.ID, ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID, PriceQuota: 7, ExecutionStatus: "succeeded", SettlementStatus: "settled", ResolveBy: common.GetTimestamp() + 3600}
			require.NoError(t, db.Create(&call).Error)
			payload := json.RawMessage(`{"content":[{"type":"text","text":"private fixture result"}]}`)
			require.NoError(t, db.Create(&ToolMarketResult{CallID: call.ID, UserID: user.Id, Success: true, Data: ToolMarketDeliveryData(payload), ExpiresAt: common.GetTimestamp() + 3600}).Error)
			t.Run("call result and reservation", func(t *testing.T) {
				_, err := GetToolMarketCall(user.Id, pair.alias, call.ID)
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				_, _, err = GetToolMarketResult(user.Id, pair.alias, call.ID)
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				_, created, err := ReserveToolMarketCall(ToolMarketReserveInput{UserID: user.Id, ClientID: pair.alias, ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID, RequestKey: "alias-reservation", Arguments: json.RawMessage(`{}`), ResolveBy: common.GetTimestamp() + 120})
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				require.False(t, created)
				require.Equal(t, 1000, marketTestBalance(t, db, user.Id))
				// Also isolate the reservation's grant check: give the alias
				// its own installation while keeping the canonical grant.
				installation := db.Model(&ToolMarketInstallation{}).Where("user_id = ? AND tool_id = ?", user.Id, tool.ToolID)
				require.NoError(t, installation.Update("client_id", pair.alias).Error)
				t.Cleanup(func() {
					require.NoError(t, db.Model(&ToolMarketInstallation{}).Where("user_id = ? AND tool_id = ?", user.Id, tool.ToolID).Update("client_id", pair.canonical).Error)
				})
				_, created, err = ReserveToolMarketCall(ToolMarketReserveInput{UserID: user.Id, ClientID: pair.alias, ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID, RequestKey: "alias-grant-reservation", Arguments: json.RawMessage(`{}`), ResolveBy: common.GetTimestamp() + 120})
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				require.False(t, created)
				require.Equal(t, 1000, marketTestBalance(t, db, user.Id))
				data, _, err := GetToolMarketResult(user.Id, pair.canonical, call.ID)
				require.NoError(t, err)
				require.Equal(t, payload, data)
			})

			t.Run("installation mutations", func(t *testing.T) {
				require.NoError(t, SetToolMarketInstallation(user.Id, pair.alias, tool.ToolID, tool.VersionID, false))
				var installed ToolMarketInstallation
				require.NoError(t, db.First(&installed, "user_id = ? AND tool_id = ?", user.Id, tool.ToolID).Error)
				require.Equal(t, pair.canonical, installed.ClientID)
				require.ErrorIs(t, SetToolMarketInstallation(user.Id, pair.alias, tool.ToolID, tool.VersionID, true), ErrToolMarketConflict, "a collated primary-key collision must not be reported as loaded")
			})

			t.Run("client budgets", func(t *testing.T) {
				require.NoError(t, SetToolMarketBudget(user.Id, "client", pair.alias, 100))
				var budget ToolMarketBudget
				require.NoError(t, db.First(&budget, "user_id = ? AND scope = ?", user.Id, "client").Error)
				require.Equal(t, pair.alias, budget.ScopeID)
				require.Zero(t, budget.SpentQuota, "a client budget must not aggregate another client's charges")
				budgets, err := marketBudgets(db, call)
				require.NoError(t, err)
				require.Empty(t, budgets, "a call must not inherit an alias client's budget")
				require.ErrorIs(t, SetToolMarketBudget(user.Id, "client", pair.canonical, 200), ErrToolMarketConflict)
				require.NoError(t, marketSaveBudget(db, ToolMarketBudget{UserID: user.Id, Scope: "client", ScopeID: pair.canonical, ReservedQuota: 9, SpentQuota: 9}))
				require.NoError(t, db.First(&budget, "user_id = ? AND scope = ?", user.Id, "client").Error)
				require.Equal(t, 100, budget.LimitQuota)
				require.Zero(t, budget.ReservedQuota)
				require.Zero(t, budget.SpentQuota)
			})

			t.Run("disconnect", func(t *testing.T) {
				var canonicalToken string
				if pair.canonical == "Agent-A" {
					canonicalToken, _, err = CreateToolMarketToken(user.Id, pair.canonical, true, false, common.GetTimestamp()+3600)
					require.NoError(t, err)
				}
				result, err := DisconnectToolMarketClient(user.Id, pair.alias)
				require.NoError(t, err)
				require.EqualValues(t, 1, result.TokensRevoked)
				require.Zero(t, result.GrantsRevoked)
				require.Zero(t, result.ToolsUnloaded)
				var revoked ToolMarketToken
				require.NoError(t, db.First(&revoked, "id = ?", token.ID).Error)
				require.NotZero(t, revoked.RevokedAt)
				var canonicalGrant ToolMarketGrant
				require.NoError(t, db.First(&canonicalGrant, "id = ?", grant.ID).Error)
				require.Zero(t, canonicalGrant.RevokedAt)
				_, err = GetToolMarketExecution(user.Id, pair.canonical, tool.ToolID, tool.VersionID, grant.ID)
				require.NoError(t, err)
				if canonicalToken != "" {
					_, err := VerifyToolMarketToken(canonicalToken)
					require.NoError(t, err, "another client's personal token must remain usable")
				}
			})
		})
	}
}
