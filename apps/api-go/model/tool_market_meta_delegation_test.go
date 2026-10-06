package model

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func marketMetaTestSubject(t *testing.T, f marketFixture, client string, invoke, manage bool) ToolMarketMetaSubject {
	t.Helper()
	_, token, err := CreateToolMarketToken(f.buyer.Id, client, invoke, manage, common.GetTimestamp()+3600)
	require.NoError(t, err)
	return ToolMarketMetaSubject{UserID: f.buyer.Id, ClientID: client, CredentialKind: "personal", CredentialID: token.ID, CanInvoke: invoke, CanManage: manage}
}

func TestToolMarketMetaLegacyManageDoesNotDelegateSpending(t *testing.T) {
	f := newMarketFixture(t, 101)
	subject := marketMetaTestSubject(t, f, "client-a", true, true)
	view, err := GetToolMarketMetaDelegation(subject)
	require.NoError(t, err)
	require.False(t, view.Enabled)
	_, err = AuthorizeToolMarketMeta(subject, ToolMarketGrant{ToolID: f.tool.ToolID, VersionID: f.tool.VersionID, MaxPriceQuota: 101, MaxTotalQuota: 150, MaxCalls: 1, ExpiresAt: common.GetTimestamp() + 60})
	require.ErrorIs(t, err, ErrToolMarketDenied)
	require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
}

func TestToolMarketMetaDelegationIsCredentialBoundAndFailClosed(t *testing.T) {
	f := newMarketFixture(t, 0)
	subject := marketMetaTestSubject(t, f, "client-a", true, true)
	_, err := SetToolMarketMetaDelegation(subject, ToolMarketMetaDelegation{Enabled: true, MaxTotalQuota: 0})
	require.NoError(t, err)
	other := marketMetaTestSubject(t, f, "client-a", true, true)
	view, err := GetToolMarketMetaDelegation(other)
	require.NoError(t, err)
	require.False(t, view.Enabled, "a second token for the same client does not inherit delegation")
	restricted := marketMetaTestSubject(t, f, "client-a", true, false)
	restricted.CanManage = true
	_, err = SetToolMarketMetaDelegation(restricted, ToolMarketMetaDelegation{Enabled: true})
	require.ErrorIs(t, err, ErrToolMarketDenied, "caller-supplied flags cannot widen persisted token scopes")
	spoof := subject
	spoof.ClientID = "Client-a"
	_, err = SetToolMarketMetaDelegation(spoof, ToolMarketMetaDelegation{Enabled: true})
	require.ErrorIs(t, err, ErrToolMarketDenied)
	spoof = subject
	spoof.UserID = f.author.Id
	_, err = SetToolMarketMetaDelegation(spoof, ToolMarketMetaDelegation{Enabled: true})
	require.ErrorIs(t, err, ErrToolMarketDenied)
}

func TestToolMarketMetaPolicyUpdatesHaveStableCurrentStateAndAppendAudit(t *testing.T) {
	f := newMarketFixture(t, 0)
	subject := marketMetaTestSubject(t, f, "client-a", true, true)
	for _, enabled := range []bool{true, false, true, false} {
		_, err := SetToolMarketMetaDelegation(subject, ToolMarketMetaDelegation{Enabled: enabled})
		require.NoError(t, err)
		view, err := GetToolMarketMetaDelegation(subject)
		require.NoError(t, err)
		require.Equal(t, enabled, view.Enabled, "same-second changes must not depend on random event UUID order")
	}
	var policies, audits int64
	require.NoError(t, f.db.Model(&ToolMarketEvent{}).Where("action = ?", toolMarketMetaPolicyAction).Count(&policies).Error)
	require.NoError(t, f.db.Model(&ToolMarketEvent{}).Where("action = ?", "meta.delegation.set").Count(&audits).Error)
	require.EqualValues(t, 1, policies)
	require.EqualValues(t, 4, audits)
}

func TestToolMarketMetaRevocationExpiryAndAuthVersionInvalidateDelegation(t *testing.T) {
	for _, cause := range []string{"revoked", "expired", "auth_version"} {
		t.Run(cause, func(t *testing.T) {
			f := newMarketFixture(t, 0)
			subject := marketMetaTestSubject(t, f, "client-a", true, true)
			_, err := SetToolMarketMetaDelegation(subject, ToolMarketMetaDelegation{Enabled: true})
			require.NoError(t, err)
			switch cause {
			case "revoked":
				require.NoError(t, RevokeToolMarketToken(f.buyer.Id, subject.CredentialID))
			case "expired":
				require.NoError(t, f.db.Model(&ToolMarketToken{}).Where("id = ?", subject.CredentialID).Update("expires_at", common.GetTimestamp()-1).Error)
			case "auth_version":
				require.NoError(t, f.db.Model(&User{}).Where("id = ?", f.buyer.Id).Update("auth_version", gorm.Expr("auth_version + 1")).Error)
			}
			view, err := GetToolMarketMetaDelegation(subject)
			require.NoError(t, err)
			require.False(t, view.Enabled)
			require.ErrorIs(t, SetToolMarketMetaClientBudget(subject, 0), ErrToolMarketDenied)
		})
	}
}

func TestToolMarketMetaZeroCapFreeGrantAndManagementNeverCharge(t *testing.T) {
	f := newMarketFixture(t, 0)
	subject := marketMetaTestSubject(t, f, "client-a", true, true)
	view, err := SetToolMarketMetaDelegation(subject, ToolMarketMetaDelegation{Enabled: true})
	require.NoError(t, err)
	require.Equal(t, 0, view.MaxTotalQuota)
	require.Greater(t, view.ExpiresAt, common.GetTimestamp())
	input := ToolMarketGrant{ToolID: f.tool.ToolID, VersionID: f.tool.VersionID, MaxCalls: 1, ExpiresAt: common.GetTimestamp() + 60}
	grant, err := AuthorizeToolMarketMeta(subject, input)
	require.NoError(t, err)
	require.Equal(t, 0, grant.MaxTotalQuota)
	require.Equal(t, f.grant.ID, grant.ID)
	repeated, err := AuthorizeToolMarketMeta(subject, input)
	require.NoError(t, err)
	require.Equal(t, grant.ID, repeated.ID)
	input.MaxPriceQuota, input.MaxTotalQuota = 1, 1
	_, err = AuthorizeToolMarketMeta(subject, input)
	require.ErrorIs(t, err, ErrToolMarketBudget)
	var calls, transfers int64
	require.NoError(t, f.db.Model(&ToolMarketCall{}).Count(&calls).Error)
	require.NoError(t, f.db.Model(&ToolMarketTransfer{}).Count(&transfers).Error)
	require.Zero(t, calls)
	require.Zero(t, transfers)
	require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
}

func TestToolMarketMetaPreservesHeldSpentAndStricterClientBudget(t *testing.T) {
	f := newMarketFixture(t, 101)
	call, created, err := ReserveToolMarketCall(f.input("held-before-delegation"))
	require.NoError(t, err)
	require.True(t, created)
	subject := marketMetaTestSubject(t, f, "client-a", true, true)
	_, err = SetToolMarketMetaDelegation(subject, ToolMarketMetaDelegation{Enabled: true, MaxTotalQuota: 150})
	require.NoError(t, err)
	view, err := SetToolMarketMetaDelegation(subject, ToolMarketMetaDelegation{Enabled: true, MaxTotalQuota: 500})
	require.NoError(t, err)
	require.Equal(t, 150, view.MaxTotalQuota, "owner opt-in does not silently replace a stricter client budget")
	require.ErrorIs(t, SetToolMarketMetaClientBudget(subject, 100), ErrToolMarketBudget)
	require.ErrorIs(t, SetToolMarketMetaClientBudget(subject, 151), ErrToolMarketBudget)
	require.NoError(t, SetToolMarketMetaToolBudget(subject, f.grant.ID, f.tool.ToolID, f.tool.VersionID, 150))
	var held ToolMarketGrant
	require.NoError(t, f.db.First(&held, "id = ?", f.grant.ID).Error)
	require.Equal(t, 101, held.ReservedQuota)
	require.Equal(t, 1, held.ReservedCalls)
	require.Equal(t, f.grant.MaxCalls, held.MaxCalls)
	started, err := StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.True(t, started)
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	usage, err := GetToolMarketMetaUsage(f.buyer.Id, "client-a", 0, 20)
	require.NoError(t, err)
	require.Equal(t, 101, usage.Budget.SpentQuota)
	require.Equal(t, 0, usage.Budget.ReservedQuota)
	require.NoError(t, SetToolMarketMetaClientBudget(subject, 120))
	require.ErrorIs(t, SetToolMarketMetaClientBudget(subject, 150), ErrToolMarketBudget, "AI cannot restore a cap after tightening it")
	require.ErrorIs(t, SetToolMarketMetaToolBudget(subject, f.grant.ID, f.tool.ToolID, f.tool.VersionID, 100), ErrToolMarketBudget)
	var settled ToolMarketCall
	require.NoError(t, f.db.First(&settled, "id = ?", call.ID).Error)
	require.Equal(t, call.PriceQuota, settled.PriceQuota)
	require.Equal(t, call.FeeBPS, settled.FeeBPS)
}

func TestToolMarketMetaMultipleGrantsCannotResetClientSpending(t *testing.T) {
	f := newMarketFixture(t, 101)
	subject := marketMetaTestSubject(t, f, "client-a", true, true)
	_, err := SetToolMarketMetaDelegation(subject, ToolMarketMetaDelegation{Enabled: true, MaxTotalQuota: 150})
	require.NoError(t, err)
	firstInput := ToolMarketGrant{ToolID: f.tool.ToolID, VersionID: f.tool.VersionID, MaxPriceQuota: 101, MaxTotalQuota: 150, MaxCalls: 10, ExpiresAt: common.GetTimestamp() + 300}
	first, err := AuthorizeToolMarketMeta(subject, firstInput)
	require.NoError(t, err)
	otherService, err := SaveToolMarketDraft(f.author.Id, "", marketTestDraft(101))
	require.NoError(t, err)
	otherTool := marketTestPublish(t, f.db, f.root.Id, otherService)
	require.NoError(t, SetToolMarketInstallation(f.buyer.Id, "client-a", otherTool.ToolID, otherTool.VersionID, true))
	secondInput := firstInput
	secondInput.ToolID, secondInput.VersionID = otherTool.ToolID, otherTool.VersionID
	second, err := AuthorizeToolMarketMeta(subject, secondInput)
	require.NoError(t, err)
	callInput := f.input("first-authorization")
	callInput.GrantID = first.ID
	call, _, err := ReserveToolMarketCall(callInput)
	require.NoError(t, err)
	_, err = StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	callInput.RequestKey, callInput.ToolID, callInput.VersionID, callInput.GrantID = "second-authorization", otherTool.ToolID, otherTool.VersionID, second.ID
	_, _, err = ReserveToolMarketCall(callInput)
	require.ErrorIs(t, err, ErrToolMarketBudget)
	repeated, err := AuthorizeToolMarketMeta(subject, firstInput)
	require.NoError(t, err)
	require.Equal(t, first.ID, repeated.ID)
	require.Equal(t, 101, repeated.SpentQuota)
	require.Equal(t, 899, marketTestBalance(t, f.db, f.buyer.Id))
}

func TestToolMarketMetaCannotBypassAccountOrGlobalToolBudget(t *testing.T) {
	for _, scope := range []string{"account", "tool"} {
		t.Run(scope, func(t *testing.T) {
			f := newMarketFixture(t, 101)
			scopeID := ""
			if scope == "tool" {
				scopeID = f.tool.ToolID
			}
			require.NoError(t, SetToolMarketBudget(f.buyer.Id, scope, scopeID, 0))
			subject := marketMetaTestSubject(t, f, "client-a", true, true)
			_, err := SetToolMarketMetaDelegation(subject, ToolMarketMetaDelegation{Enabled: true, MaxTotalQuota: 150})
			require.NoError(t, err)
			grant, err := AuthorizeToolMarketMeta(subject, ToolMarketGrant{ToolID: f.tool.ToolID, VersionID: f.tool.VersionID, MaxPriceQuota: 101, MaxTotalQuota: 150, MaxCalls: 1, ExpiresAt: common.GetTimestamp() + 60})
			require.NoError(t, err)
			input := f.input("stricter-budget")
			input.GrantID = grant.ID
			_, _, err = ReserveToolMarketCall(input)
			require.ErrorIs(t, err, ErrToolMarketBudget)
			require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
		})
	}
}

func TestToolMarketMetaHistoryIsExactClientPaginatedAndSafe(t *testing.T) {
	f := newMarketFixture(t, 0)
	for i, client := range []string{"client-a", "client-a", "Client-a", "other-client"} {
		row := ToolMarketCall{ID: string(rune('a' + i)), UserID: f.buyer.Id, ClientID: client, ServiceID: f.service.ID, ToolID: f.tool.ToolID, VersionID: f.tool.VersionID,
			GrantID: f.grant.ID, InputDigest: "private-argument-digest", UsageReport: "private-provider-report", ResultDigest: "private-result-digest", ExecutionStatus: "succeeded", SettlementStatus: "settled", CreatedAt: int64(i + 1)}
		require.NoError(t, f.db.Create(&row).Error)
	}
	rows, err := ListToolMarketMetaCalls(f.buyer.Id, "client-a", 0, 1)
	require.NoError(t, err)
	require.True(t, rows.More)
	require.Len(t, rows.Calls, 1)
	require.Equal(t, "b", rows.Calls[0].ID)
	next, err := ListToolMarketMetaCalls(f.buyer.Id, "client-a", 1, 1)
	require.NoError(t, err)
	require.False(t, next.More)
	require.Equal(t, "a", next.Calls[0].ID)
	data, err := json.Marshal(rows)
	require.NoError(t, err)
	for _, secret := range []string{"private-", "user_id", "client_id", "grant_id", "input_digest", "usage_report", "result_digest"} {
		require.NotContains(t, string(data), secret)
	}
	other, err := ListToolMarketMetaCalls(f.author.Id, "client-a", 0, 10)
	require.NoError(t, err)
	require.Empty(t, other.Calls)
}

func TestToolMarketMetaOAuthBindingScopesAndRevocation(t *testing.T) {
	f := newMarketFixture(t, 0)
	require.NoError(t, f.db.AutoMigrate(&OAuthServerGrant{}))
	issuer, resource := "https://issuer.example.test", "https://issuer.example.test/api/oauth2"
	family := OAuthServerGrant{ID: "meta-family", UserID: int64(f.buyer.Id), Issuer: issuer, Resource: resource, ClientID: "native", Scope: "market:discover market:invoke market:manage", AbsoluteExpiresAtMs: (common.GetTimestamp() + 600) * 1000}
	require.NoError(t, f.db.Create(&family).Error)
	subjects, err := ToolMarketMetaOAuthSubjects(f.buyer.Id, "oauth:native", issuer, resource)
	require.NoError(t, err)
	require.Len(t, subjects, 1)
	_, err = SetToolMarketMetaDelegations(subjects, ToolMarketMetaDelegation{Enabled: true})
	require.NoError(t, err)
	view, err := GetToolMarketMetaDelegation(subjects[0])
	require.NoError(t, err)
	require.True(t, view.Enabled)
	other := subjects[0]
	other.CredentialID = "another-family"
	view, err = GetToolMarketMetaDelegation(other)
	require.NoError(t, err)
	require.False(t, view.Enabled)
	narrowed := subjects[0]
	narrowed.CanManage = false
	view, err = GetToolMarketMetaDelegation(narrowed)
	require.NoError(t, err)
	require.False(t, view.Enabled, "a narrowed authenticated token does not inherit its family's wider scopes")
	wrongResource := subjects[0]
	wrongResource.OAuthResource += "/other"
	_, err = SetToolMarketMetaDelegation(wrongResource, ToolMarketMetaDelegation{Enabled: true})
	require.ErrorIs(t, err, ErrToolMarketDenied)
	require.NoError(t, f.db.Model(&family).Update("revoked_at_ms", common.GetTimestamp()*1000).Error)
	view, err = GetToolMarketMetaDelegation(subjects[0])
	require.NoError(t, err)
	require.False(t, view.Enabled)
}

func TestToolMarketMetaOAuthBatchRollsBackWhenAnyCredentialIsInvalid(t *testing.T) {
	f := newMarketFixture(t, 0)
	require.NoError(t, f.db.AutoMigrate(&OAuthServerGrant{}))
	issuer, resource := "https://issuer.example.test", "https://issuer.example.test/api/oauth2"
	for _, id := range []string{"first-family", "second-family"} {
		row := OAuthServerGrant{ID: id, UserID: int64(f.buyer.Id), Issuer: issuer, Resource: resource, ClientID: "native", Scope: "market:discover market:invoke market:manage", AbsoluteExpiresAtMs: (common.GetTimestamp() + 600) * 1000}
		require.NoError(t, f.db.Create(&row).Error)
	}
	subjects, err := ToolMarketMetaOAuthSubjects(f.buyer.Id, "oauth:native", issuer, resource)
	require.NoError(t, err)
	require.Len(t, subjects, 2)
	require.NoError(t, f.db.Model(&OAuthServerGrant{}).Where("id = ?", subjects[1].CredentialID).Update("scope", "market:discover").Error)
	_, err = SetToolMarketMetaDelegations(subjects, ToolMarketMetaDelegation{Enabled: true})
	require.ErrorIs(t, err, ErrToolMarketDenied)
	var current, audit, budget int64
	require.NoError(t, f.db.Model(&ToolMarketEvent{}).Where("action = ?", toolMarketMetaPolicyAction).Count(&current).Error)
	require.NoError(t, f.db.Model(&ToolMarketEvent{}).Where("action = ?", "meta.delegation.set").Count(&audit).Error)
	require.NoError(t, f.db.Model(&ToolMarketBudget{}).Where("scope = 'client' AND scope_id = ?", "oauth:native").Count(&budget).Error)
	require.Zero(t, current)
	require.Zero(t, audit)
	require.Zero(t, budget)
}
