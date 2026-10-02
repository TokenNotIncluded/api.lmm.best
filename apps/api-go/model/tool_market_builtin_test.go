package model

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func marketTestBuiltinDefinition() ToolMarketBuiltinServiceInput {
	return ToolMarketBuiltinServiceInput{
		Key: "test-system-utilities", Name: "System utilities", Description: "Trusted system tools",
		Tools: []ToolMarketToolInput{{
			Name: "system.echo", Description: "Echo a message",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`),
			Permissions: []string{"read"}, PriceQuota: 100,
		}},
	}
}

func newMarketBuiltinFixture(t *testing.T, maxCalls int) marketFixture {
	t.Helper()
	db := marketTestDB(t)
	f := marketFixture{db: db, buyer: marketTestUser(t, db, "builtin-buyer", 1000, common.RoleCommonUser), author: marketTestUser(t, db, "builtin-other", 0, common.RoleCommonUser), root: marketTestUser(t, db, "builtin-root", 0, common.RoleRootUser)}
	require.NoError(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{marketTestBuiltinDefinition()}))
	rows, err := ListToolMarket(f.buyer.Id, "System utilities", "builtin", 0, 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	detail, err := GetToolMarketDetail(f.buyer.Id, rows[0].ID, false)
	require.NoError(t, err)
	f.service = &detail.Service
	require.Len(t, detail.Tools, 1)
	f.tool = detail.Tools[0]
	require.NoError(t, SetToolMarketInstallation(f.buyer.Id, "client-a", f.tool.ToolID, f.tool.VersionID, true))
	f.grant, err = CreateToolMarketGrant(f.buyer.Id, ToolMarketGrant{ClientID: "client-a", ToolID: f.tool.ToolID, VersionID: f.tool.VersionID, MaxCalls: maxCalls, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	return f
}

func marketTestBuiltinInput(f marketFixture, key string) ToolMarketReserveInput {
	in := f.input(key)
	in.Arguments = json.RawMessage(`{"message":"hello"}`)
	return in
}

func marketTestBuiltinGrant(t *testing.T, f marketFixture) ToolMarketGrant {
	t.Helper()
	var grant ToolMarketGrant
	require.NoError(t, f.db.First(&grant, "id = ?", f.grant.ID).Error)
	return grant
}

func marketTestBuiltinAwait(t *testing.T, f marketFixture, key, state string) *ToolMarketCall {
	t.Helper()
	call, created, err := ReserveToolMarketCall(marketTestBuiltinInput(f, key))
	require.NoError(t, err)
	require.True(t, created)
	started, err := StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.True(t, started)
	require.NoError(t, RecordToolMarketBuiltinConfirmation(call.ID, state, json.RawMessage(`{"requestState":"confirm-state","inputRequests":[{"type":"confirmation"}]}`)))
	return call
}

func TestToolMarketBuiltinCatalogIsPublicFreeAndStable(t *testing.T) {
	db := marketTestDB(t)
	in := marketTestBuiltinDefinition()
	require.NoError(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{in}))
	rows, err := ListToolMarket(0, "", "builtin", 0, 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	first, err := GetToolMarketDetail(0, rows[0].ID, false)
	require.NoError(t, err)
	expectedVersionID, err := ToolMarketBuiltinVersionID(in)
	require.NoError(t, err)
	require.Equal(t, expectedVersionID, first.Version.ID)
	require.Zero(t, first.Service.OwnerID)
	require.Equal(t, "published", first.Service.Status)
	require.Equal(t, "published", first.Version.Status)
	require.Equal(t, "builtin", first.Version.ExecutionType)
	require.Equal(t, "public", first.Version.Visibility)
	require.Empty(t, first.Version.Endpoint)
	require.True(t, first.Validated)
	require.Equal(t, "free", first.Pricing)
	require.Len(t, first.Tools, 1)
	require.Zero(t, first.Tools[0].PriceQuota)
	for _, id := range []string{first.Service.ID, first.Version.ID, first.Tools[0].ToolID} {
		_, err := uuid.Parse(id)
		require.NoError(t, err)
	}
	require.Equal(t, 100, in.Tools[0].PriceQuota, "registration must not rewrite the caller's definitions")
	require.NoError(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{in}))
	second, err := GetToolMarketDetail(0, rows[0].ID, false)
	require.NoError(t, err)
	require.Equal(t, first.Service.ID, second.Service.ID)
	require.Equal(t, first.Version.ID, second.Version.ID)
	require.Equal(t, first.Tools[0].ToolID, second.Tools[0].ToolID)
	var versions, tools int64
	require.NoError(t, db.Model(&ToolMarketVersion{}).Count(&versions).Error)
	require.NoError(t, db.Model(&ToolMarketTool{}).Count(&tools).Error)
	require.EqualValues(t, 1, versions)
	require.EqualValues(t, 1, tools)
}

func TestToolMarketBuiltinEquivalentDefinitionsRetainVersionAndSchemaPrecision(t *testing.T) {
	db := marketTestDB(t)
	first := marketTestBuiltinDefinition()
	first.Tools[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"counter":{"type":"integer","minimum":9007199254740993}},"required":["counter"]}`)
	first.Tools[0].Permissions = []string{"write", "read"}
	first.Tools = append(first.Tools, ToolMarketToolInput{Name: "system.second", InputSchema: json.RawMessage(`{"type":"object"}`), PriceQuota: 10})
	require.NoError(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{first}))
	rows, err := ListToolMarket(0, "", "builtin", 0, 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	before, err := GetToolMarketDetail(0, rows[0].ID, false)
	require.NoError(t, err)
	require.Len(t, before.Tools, 2)
	require.Contains(t, before.Tools[0].InputSchema, "9007199254740993")
	second := marketTestBuiltinDefinition()
	second.Tools[0].InputSchema = json.RawMessage(`{
		"required": ["counter"],
		"properties": {"counter": {"minimum": 9007199254740993, "type": "integer"}},
		"type": "object"
	}`)
	second.Tools[0].Permissions = []string{"read", "write"}
	second.Tools[0].OutputSchema = json.RawMessage(`null`)
	second.Tools[0].PriceQuota = 777
	second.Tools = append([]ToolMarketToolInput{{Name: "system.second", InputSchema: json.RawMessage(`{"type":"object"}`)}}, second.Tools...)
	require.NoError(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{second}))
	after, err := GetToolMarketDetail(0, rows[0].ID, false)
	require.NoError(t, err)
	firstVersionID, err := ToolMarketBuiltinVersionID(first)
	require.NoError(t, err)
	secondVersionID, err := ToolMarketBuiltinVersionID(second)
	require.NoError(t, err)
	require.Equal(t, firstVersionID, secondVersionID)
	require.Equal(t, firstVersionID, after.Version.ID)
	require.Equal(t, before.Version.ID, after.Version.ID)
	require.Equal(t, before.Tools, after.Tools)
	var versions int64
	require.NoError(t, db.Model(&ToolMarketVersion{}).Count(&versions).Error)
	require.EqualValues(t, 1, versions)
}

func TestToolMarketBuiltinSchemaUpdatesRequireNewAuthorization(t *testing.T) {
	f := newMarketBuiltinFixture(t, 3)
	in := marketTestBuiltinDefinition()
	in.Tools[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"},"format":{"type":"string"}},"required":["message"]}`)
	require.NoError(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{in}))
	detail, err := GetToolMarketDetail(f.buyer.Id, f.service.ID, false)
	require.NoError(t, err)
	require.NotEqual(t, f.tool.VersionID, detail.Version.ID)
	require.Equal(t, f.tool.ToolID, detail.Tools[0].ToolID)
	_, _, err = ReserveToolMarketCall(marketTestBuiltinInput(f, "old-schema"))
	require.Error(t, err)
	updated := marketTestBuiltinInput(f, "updated-schema-old-grant")
	updated.VersionID = detail.Version.ID
	_, _, err = ReserveToolMarketCall(updated)
	require.Error(t, err)
	require.NoError(t, SetToolMarketInstallation(f.buyer.Id, "client-a", f.tool.ToolID, detail.Version.ID, true))
	_, _, err = ReserveToolMarketCall(updated)
	require.Error(t, err, "updating the installation must not expand an old grant")
	grant, err := CreateToolMarketGrant(f.buyer.Id, ToolMarketGrant{ClientID: "client-a", ToolID: f.tool.ToolID, VersionID: detail.Version.ID, MaxCalls: 1, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	updated.GrantID = grant.ID
	call, created, err := ReserveToolMarketCall(updated)
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, FinishToolMarketCall(call.ID, false))
}

func TestToolMarketBuiltinCannotBeForgedEditedOrPaused(t *testing.T) {
	f := newMarketBuiltinFixture(t, 2)
	draft := marketTestDraft(0)
	draft.ExecutionType, draft.Endpoint = "builtin", ""
	for _, actor := range []int{f.buyer.Id, f.root.Id} {
		_, err := SaveToolMarketDraft(actor, "", draft)
		require.ErrorIs(t, err, ErrToolMarketInput)
		_, err = SaveToolMarketDraft(actor, f.service.ID, marketTestDraft(0))
		require.Error(t, err)
		require.Error(t, SubmitToolMarketDraft(actor, f.service.ID, f.tool.VersionID))
		for _, paused := range []bool{true, false} {
			require.Error(t, SetToolMarketPaused(actor, f.service.ID, paused))
		}
	}
	detail, err := GetToolMarketDetail(f.buyer.Id, f.service.ID, false)
	require.NoError(t, err)
	require.Zero(t, detail.Service.OwnerID)
	require.Equal(t, "published", detail.Service.Status)
	require.Equal(t, f.tool.VersionID, detail.Version.ID)
	var count int64
	require.NoError(t, f.db.Model(&ToolMarketVersion{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestToolMarketBuiltinRegistrationDoesNotClaimUserOwnedCollision(t *testing.T) {
	f := newMarketBuiltinFixture(t, 2)
	require.NoError(t, f.db.Model(&ToolMarketService{}).Where("id = ?", f.service.ID).Update("owner_id", f.author.Id).Error)
	require.Error(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{marketTestBuiltinDefinition()}))
	var service ToolMarketService
	require.NoError(t, f.db.First(&service, "id = ?", f.service.ID).Error)
	require.Equal(t, f.author.Id, service.OwnerID)
	require.Equal(t, f.tool.VersionID, service.LiveVersionID)
}

func TestToolMarketBuiltinInvalidDefinitionsDoNotPartiallyRegister(t *testing.T) {
	for _, name := range []string{"key", "duplicate-key", "name", "missing-tools", "duplicate-tools", "schema-non-object", "schema-wrong-type", "schema-trailing-json", "schema-trailing-garbage"} {
		t.Run(name, func(t *testing.T) {
			db := marketTestDB(t)
			valid := marketTestBuiltinDefinition()
			invalid := marketTestBuiltinDefinition()
			invalid.Key = "other-system-service"
			switch name {
			case "key":
				invalid.Key = "invalid key"
			case "duplicate-key":
				invalid.Key = valid.Key
			case "name":
				invalid.Name = ""
			case "missing-tools":
				invalid.Tools = nil
			case "duplicate-tools":
				invalid.Tools = append(invalid.Tools, invalid.Tools[0])
			case "schema-non-object":
				invalid.Tools[0].InputSchema = json.RawMessage(`[]`)
			case "schema-wrong-type":
				invalid.Tools[0].InputSchema = json.RawMessage(`{"type":"string"}`)
			case "schema-trailing-json":
				invalid.Tools[0].InputSchema = json.RawMessage(`{"type":"object"} {"type":"object"}`)
			case "schema-trailing-garbage":
				invalid.Tools[0].InputSchema = json.RawMessage(`{"type":"object"} invalid`)
			}
			if name != "duplicate-key" {
				_, err := ToolMarketBuiltinVersionID(invalid)
				require.ErrorIs(t, err, ErrToolMarketInput)
			}
			require.ErrorIs(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{valid, invalid}), ErrToolMarketInput)
			var services int64
			require.NoError(t, db.Model(&ToolMarketService{}).Count(&services).Error)
			require.Zero(t, services)
		})
	}
}

func TestToolMarketBuiltinBypassesOnlyThirdPartyMarketSwitch(t *testing.T) {
	for _, config := range []string{"absent", "disabled", "disabled-invalid-recipient"} {
		t.Run(config, func(t *testing.T) {
			f := newMarketBuiltinFixture(t, 2)
			if config != "absent" {
				recipient := f.root.Id
				if config == "disabled-invalid-recipient" {
					recipient = 0
				}
				require.NoError(t, f.db.Create(&ToolMarketConfig{ID: 1, Enabled: false, FeeBPS: 1250, RecipientID: recipient}).Error)
			}
			require.NoError(t, SetToolMarketBudget(f.buyer.Id, "account", "", 0))
			require.NoError(t, SetToolMarketBudget(f.buyer.Id, "client", "client-a", 0))
			require.NoError(t, SetToolMarketBudget(f.buyer.Id, "tool", f.tool.ToolID, 0))
			call, created, err := ReserveToolMarketCall(marketTestBuiltinInput(f, "builtin-free"))
			require.NoError(t, err)
			require.True(t, created)
			require.Zero(t, call.PriceQuota)
			require.Zero(t, call.FeeQuota)
			started, err := StartToolMarketCall(call.ID)
			require.NoError(t, err)
			require.True(t, started)
			require.NoError(t, FinishToolMarketCall(call.ID, true))
			require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
			grant := marketTestBuiltinGrant(t, f)
			require.Zero(t, grant.ReservedCalls)
			require.Equal(t, 1, grant.SuccessfulCalls)
			require.Zero(t, grant.SpentQuota)
			var transfers int64
			require.NoError(t, f.db.Model(&ToolMarketTransfer{}).Count(&transfers).Error)
			require.Zero(t, transfers)
			remote, err := SaveToolMarketDraft(f.author.Id, "", marketTestDraft(0))
			require.NoError(t, err)
			tool := marketTestPublish(t, f.db, f.root.Id, remote)
			require.NoError(t, SetToolMarketInstallation(f.buyer.Id, "client-a", tool.ToolID, tool.VersionID, true))
			remoteGrant, err := CreateToolMarketGrant(f.buyer.Id, ToolMarketGrant{ClientID: "client-a", ToolID: tool.ToolID, VersionID: tool.VersionID, MaxCalls: 1, ExpiresAt: common.GetTimestamp() + 3600})
			require.NoError(t, err)
			input := marketTestBuiltinInput(f, "remote-still-paused")
			input.ToolID, input.VersionID, input.GrantID = tool.ToolID, tool.VersionID, remoteGrant.ID
			_, _, err = ReserveToolMarketCall(input)
			require.Error(t, err, "system tools must not enable third-party execution, even when its price is zero")
		})
	}
}

func TestToolMarketBuiltinStillRequiresInstallationAndScopedGrant(t *testing.T) {
	f := newMarketBuiltinFixture(t, 2)
	require.NoError(t, SetToolMarketInstallation(f.buyer.Id, "client-a", f.tool.ToolID, f.tool.VersionID, false))
	_, _, err := ReserveToolMarketCall(marketTestBuiltinInput(f, "not-loaded"))
	require.Error(t, err)
	require.NoError(t, SetToolMarketInstallation(f.buyer.Id, "client-a", f.tool.ToolID, f.tool.VersionID, true))
	for _, change := range []func(*ToolMarketReserveInput){
		func(in *ToolMarketReserveInput) { in.GrantID = "" },
		func(in *ToolMarketReserveInput) { in.ClientID = "client-b" },
		func(in *ToolMarketReserveInput) { in.UserID = f.author.Id },
	} {
		in := marketTestBuiltinInput(f, "wrong-authorization")
		change(&in)
		_, _, err := ReserveToolMarketCall(in)
		require.Error(t, err)
	}
	require.NoError(t, RevokeToolMarketGrant(f.buyer.Id, f.grant.ID))
	_, _, err = ReserveToolMarketCall(marketTestBuiltinInput(f, "revoked"))
	require.ErrorIs(t, err, ErrToolMarketDenied)
}

func TestToolMarketBuiltinConfirmationKeepsCallBudgetAndScopedData(t *testing.T) {
	f := newMarketBuiltinFixture(t, 1)
	call := marketTestBuiltinAwait(t, f, "needs-confirmation", "confirm-state")
	grant := marketTestBuiltinGrant(t, f)
	require.Equal(t, 1, grant.ReservedCalls)
	require.Zero(t, grant.SuccessfulCalls)
	replay, created, err := ReserveToolMarketCall(marketTestBuiltinInput(f, "needs-confirmation"))
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, call.ID, replay.ID)
	conflicting := marketTestBuiltinInput(f, "needs-confirmation")
	conflicting.Arguments = json.RawMessage(`{"message":"different"}`)
	_, _, err = ReserveToolMarketCall(conflicting)
	require.ErrorIs(t, err, ErrToolMarketConflict)
	_, _, err = ReserveToolMarketCall(marketTestBuiltinInput(f, "second-call"))
	require.ErrorIs(t, err, ErrToolMarketBudget)
	data, err := GetToolMarketBuiltinConfirmation(f.buyer.Id, "client-a", call.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"requestState":"confirm-state","inputRequests":[{"type":"confirmation"}]}`, string(data))
	for _, identity := range []struct {
		user   int
		client string
	}{{f.author.Id, "client-a"}, {f.buyer.Id, "client-b"}} {
		data, err := GetToolMarketBuiltinConfirmation(identity.user, identity.client, call.ID)
		require.Error(t, err)
		require.Empty(t, data)
	}
	originalDeadline := common.GetTimestamp() + 10
	require.NoError(t, f.db.Model(&ToolMarketCall{}).Where("id = ?", call.ID).Update("resolve_by", originalDeadline).Error)
	resumed, err := ResumeToolMarketBuiltinCall(call.ID, "wrong-state")
	require.Error(t, err)
	require.False(t, resumed)
	var storedCall ToolMarketCall
	require.NoError(t, f.db.First(&storedCall, "id = ?", call.ID).Error)
	require.Equal(t, originalDeadline, storedCall.ResolveBy, "a wrong confirmation cannot extend execution time")
	grant = marketTestBuiltinGrant(t, f)
	require.Equal(t, 1, grant.ReservedCalls)
	resumeTime := common.GetTimestamp()
	resumed, err = ResumeToolMarketBuiltinCall(call.ID, "confirm-state")
	require.NoError(t, err)
	require.True(t, resumed)
	require.NoError(t, f.db.First(&storedCall, "id = ?", call.ID).Error)
	require.GreaterOrEqual(t, storedCall.ResolveBy, resumeTime+120)
	require.Greater(t, storedCall.ResolveBy, originalDeadline)
	_, err = GetToolMarketBuiltinConfirmation(f.buyer.Id, "client-a", call.ID)
	require.Error(t, err)
	require.NoError(t, RecordToolMarketResult(call.ID, true, json.RawMessage(`{"content":[{"type":"text","text":"done"}]}`)))
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	result, _, err := GetToolMarketResult(f.buyer.Id, "client-a", call.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"content":[{"type":"text","text":"done"}]}`, string(result))
	grant = marketTestBuiltinGrant(t, f)
	require.Zero(t, grant.ReservedCalls)
	require.Equal(t, 1, grant.SuccessfulCalls)
	_, _, err = ReserveToolMarketCall(marketTestBuiltinInput(f, "after-success"))
	require.ErrorIs(t, err, ErrToolMarketBudget)
	require.Error(t, RecordToolMarketBuiltinConfirmation(call.ID, "another-state", json.RawMessage(`{}`)))
}

func TestToolMarketBuiltinMultipleConfirmationsRemainOneCall(t *testing.T) {
	f := newMarketBuiltinFixture(t, 1)
	call := marketTestBuiltinAwait(t, f, "multiple-confirmations", "confirm-state")
	started, err := ResumeToolMarketBuiltinCall(call.ID, "confirm-state")
	require.NoError(t, err)
	require.True(t, started)
	require.NoError(t, RecordToolMarketBuiltinConfirmation(call.ID, "second-state", json.RawMessage(`{"requestState":"second-state","inputRequests":[{"type":"confirmation"}]}`)))
	started, err = ResumeToolMarketBuiltinCall(call.ID, "confirm-state")
	require.Error(t, err)
	require.False(t, started, "a previous round's confirmation must not authorize the new round")
	grant := marketTestBuiltinGrant(t, f)
	require.Equal(t, 1, grant.ReservedCalls)
	require.Zero(t, grant.SuccessfulCalls)
	_, _, err = GetToolMarketResult(f.buyer.Id, "client-a", call.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	started, err = ResumeToolMarketBuiltinCall(call.ID, "second-state")
	require.NoError(t, err)
	require.True(t, started)
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	grant = marketTestBuiltinGrant(t, f)
	require.Zero(t, grant.ReservedCalls)
	require.Equal(t, 1, grant.SuccessfulCalls)
}

func TestToolMarketBuiltinConfirmationResumeHasOneConcurrentWinner(t *testing.T) {
	f := newMarketBuiltinFixture(t, 1)
	call := marketTestBuiltinAwait(t, f, "concurrent-confirmation", "confirm-state")
	const workers = 8
	var wg sync.WaitGroup
	results := make(chan bool, workers)
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resumed, err := ResumeToolMarketBuiltinCall(call.ID, "confirm-state")
			results <- resumed
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	winners := 0
	for resumed := range results {
		if resumed {
			winners++
		}
	}
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 1, winners, "one continuation may execute its underlying side effect")
	require.Equal(t, 1, marketTestBuiltinGrant(t, f).ReservedCalls)
	require.NoError(t, FinishToolMarketCall(call.ID, false))
	require.Zero(t, marketTestBuiltinGrant(t, f).ReservedCalls)
}

func TestToolMarketBuiltinConfirmationCannotResumeAfterAccessRevocation(t *testing.T) {
	for _, revoke := range []string{"grant", "installation", "account", "auth_version"} {
		t.Run(revoke, func(t *testing.T) {
			f := newMarketBuiltinFixture(t, 1)
			call := marketTestBuiltinAwait(t, f, "access-revoked", "confirm-state")
			originalDeadline := common.GetTimestamp() + 10
			require.NoError(t, f.db.Model(&ToolMarketCall{}).Where("id = ?", call.ID).Update("resolve_by", originalDeadline).Error)
			switch revoke {
			case "grant":
				require.NoError(t, RevokeToolMarketGrant(f.buyer.Id, f.grant.ID))
			case "installation":
				require.NoError(t, SetToolMarketInstallation(f.buyer.Id, "client-a", f.tool.ToolID, f.tool.VersionID, false))
			case "account":
				require.NoError(t, f.db.Model(&User{}).Where("id = ?", f.buyer.Id).Update("status", common.UserStatusDisabled).Error)
			case "auth_version":
				require.NoError(t, f.db.Model(&User{}).Where("id = ?", f.buyer.Id).Update("auth_version", gorm.Expr("auth_version + ?", 1)).Error)
			}
			resumed, err := ResumeToolMarketBuiltinCall(call.ID, "confirm-state")
			require.Error(t, err)
			require.False(t, resumed)
			var storedCall ToolMarketCall
			require.NoError(t, f.db.First(&storedCall, "id = ?", call.ID).Error)
			require.Equal(t, originalDeadline, storedCall.ResolveBy, "failed authorization cannot extend execution time")
			require.NoError(t, FinishToolMarketCall(call.ID, false))
			require.Zero(t, marketTestBuiltinGrant(t, f).ReservedCalls)
		})
	}
}

func TestToolMarketBuiltinConfirmationExpiryReleasesCallAndPrivateData(t *testing.T) {
	f := newMarketBuiltinFixture(t, 1)
	call := marketTestBuiltinAwait(t, f, "expired-confirmation", "confirm-state")
	require.NoError(t, f.db.Model(&ToolMarketCall{}).Where("id = ?", call.ID).Update("resolve_by", common.GetTimestamp()-1).Error)
	data, err := GetToolMarketBuiltinConfirmation(f.buyer.Id, "client-a", call.ID)
	require.Error(t, err)
	require.Empty(t, data)
	state, err := GetToolMarketCall(f.buyer.Id, "client-a", call.ID)
	require.NoError(t, err)
	require.Equal(t, "released", state.SettlementStatus)
	require.Equal(t, "cancelled", state.ExecutionStatus)
	require.Zero(t, marketTestBuiltinGrant(t, f).ReservedCalls)
	require.NoError(t, ExpireToolMarketCall(call.ID))
	resumed, err := ResumeToolMarketBuiltinCall(call.ID, "confirm-state")
	require.NoError(t, err)
	require.False(t, resumed)
	require.ErrorIs(t, FinishToolMarketCall(call.ID, true), ErrToolMarketConflict)
	next, created, err := ReserveToolMarketCall(marketTestBuiltinInput(f, "new-call-after-expiry"))
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, FinishToolMarketCall(next.ID, false))
}

func TestToolMarketBuiltinConfirmationRejectsInvalidStateAndPayload(t *testing.T) {
	f := newMarketBuiltinFixture(t, 1)
	call, _, err := ReserveToolMarketCall(marketTestBuiltinInput(f, "invalid-confirmation"))
	require.NoError(t, err)
	started, err := StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.True(t, started)
	for _, in := range []struct {
		state string
		data  json.RawMessage
	}{{"", json.RawMessage(`{}`)}, {strings.Repeat("s", 513), json.RawMessage(`{}`)}, {"valid-state", json.RawMessage(`{"invalid":`)}} {
		require.ErrorIs(t, RecordToolMarketBuiltinConfirmation(call.ID, in.state, in.data), ErrToolMarketInput)
	}
	_, err = GetToolMarketBuiltinConfirmation(f.buyer.Id, "client-a", call.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.Equal(t, 1, marketTestBuiltinGrant(t, f).ReservedCalls)
	require.NoError(t, FinishToolMarketCall(call.ID, false))
}

func marketTestRemoteValidationFixture(t *testing.T) (marketFixture, ToolMarketVersion) {
	t.Helper()
	db := marketTestDB(t)
	f := marketFixture{db: db, buyer: marketTestUser(t, db, "validation-other", 0, common.RoleCommonUser), author: marketTestUser(t, db, "validation-owner", 0, common.RoleCommonUser), root: marketTestUser(t, db, "validation-root", 0, common.RoleRootUser)}
	var err error
	f.service, err = SaveToolMarketDraft(f.author.Id, "", marketTestDraft(0))
	require.NoError(t, err)
	var version ToolMarketVersion
	require.NoError(t, db.First(&version, "id = ?", f.service.DraftVersionID).Error)
	require.NoError(t, db.First(&f.tool, "version_id = ?", version.ID).Error)
	return f, version
}

func marketTestRemoteCredential(f marketFixture, version ToolMarketVersion) ToolMarketCredential {
	return ToolMarketCredential{ID: uuid.NewString(), OwnerID: f.author.Id, ServiceID: f.service.ID, VersionID: version.ID,
		EndpointDigest: marketDigest(version.Endpoint), Mode: "bearer", Ciphertext: "test-only-encrypted-placeholder", UpdatedAt: common.GetTimestamp()}
}

func marketTestRemoteValidationUnchanged(t *testing.T, f marketFixture, version ToolMarketVersion) {
	t.Helper()
	var storedVersion ToolMarketVersion
	var storedTool ToolMarketToolVersion
	require.NoError(t, f.db.First(&storedVersion, "id = ?", version.ID).Error)
	require.NoError(t, f.db.First(&storedTool, "version_id = ? AND tool_id = ?", version.ID, f.tool.ToolID).Error)
	require.Empty(t, storedVersion.ValidationDigest, "a stale credential cannot validate the draft")
	require.Empty(t, storedTool.RemoteDigest, "failed validation cannot publish a remote tool fingerprint")
}

func marketTestRemoteValidationStored(t *testing.T, f marketFixture, version ToolMarketVersion, remoteDigest string) {
	t.Helper()
	var storedVersion ToolMarketVersion
	var storedTool ToolMarketToolVersion
	require.NoError(t, f.db.First(&storedVersion, "id = ?", version.ID).Error)
	require.NoError(t, f.db.First(&storedTool, "version_id = ? AND tool_id = ?", version.ID, f.tool.ToolID).Error)
	require.Equal(t, version.Digest, storedVersion.ValidationDigest)
	require.Equal(t, remoteDigest, storedTool.RemoteDigest)
}

func TestToolMarketValidationWithoutCredentialRetainsCompatibility(t *testing.T) {
	for _, actor := range []string{"owner", "administrator"} {
		t.Run(actor, func(t *testing.T) {
			f, version := marketTestRemoteValidationFixture(t)
			actorID := f.author.Id
			if actor == "administrator" {
				actorID = f.root.Id
			}
			remoteDigest := strings.Repeat("a", 64)
			require.NoError(t, RecordToolMarketValidation(actorID, f.service.ID, version.ID, version.Digest, map[string]string{f.tool.ToolID: remoteDigest}))
			marketTestRemoteValidationStored(t, f, version, remoteDigest)
		})
	}
}

func TestToolMarketValidationUsesExactCurrentCredential(t *testing.T) {
	f, version := marketTestRemoteValidationFixture(t)
	credential := marketTestRemoteCredential(f, version)
	require.NoError(t, f.db.Create(&credential).Error)
	remoteDigest := strings.Repeat("a", 64)
	require.NoError(t, RecordToolMarketValidationWithCredential(f.author.Id, f.service.ID, version.ID, version.Digest, map[string]string{f.tool.ToolID: remoteDigest}, credential.ID))
	marketTestRemoteValidationStored(t, f, version, remoteDigest)
}

func TestToolMarketValidationRejectsCredentialRotatedDuringDiscovery(t *testing.T) {
	f, version := marketTestRemoteValidationFixture(t)
	oldCredential := marketTestRemoteCredential(f, version)
	require.NoError(t, f.db.Create(&oldCredential).Error)
	// Discovery captured A, but connection management replaced it with B before
	// the network result returned. Validation must compare the committed row.
	require.NoError(t, f.db.Delete(&oldCredential).Error)
	currentCredential := marketTestRemoteCredential(f, version)
	require.NoError(t, f.db.Create(&currentCredential).Error)
	tools := map[string]string{f.tool.ToolID: strings.Repeat("a", 64)}
	require.ErrorIs(t, RecordToolMarketValidationWithCredential(f.author.Id, f.service.ID, version.ID, version.Digest, tools, oldCredential.ID), ErrToolMarketConflict)
	marketTestRemoteValidationUnchanged(t, f, version)
	require.ErrorIs(t, RecordToolMarketValidation(f.author.Id, f.service.ID, version.ID, version.Digest, tools), ErrToolMarketConflict)
	marketTestRemoteValidationUnchanged(t, f, version)
	require.NoError(t, RecordToolMarketValidationWithCredential(f.author.Id, f.service.ID, version.ID, version.Digest, tools, currentCredential.ID))
	marketTestRemoteValidationStored(t, f, version, tools[f.tool.ToolID])
}

func TestToolMarketValidationRejectsCredentialRemovedDuringDiscovery(t *testing.T) {
	f, version := marketTestRemoteValidationFixture(t)
	credential := marketTestRemoteCredential(f, version)
	require.NoError(t, f.db.Create(&credential).Error)
	require.NoError(t, f.db.Delete(&credential).Error)
	tools := map[string]string{f.tool.ToolID: strings.Repeat("a", 64)}
	require.ErrorIs(t, RecordToolMarketValidationWithCredential(f.author.Id, f.service.ID, version.ID, version.Digest, tools, credential.ID), ErrToolMarketConflict)
	marketTestRemoteValidationUnchanged(t, f, version)
	require.NoError(t, RecordToolMarketValidation(f.author.Id, f.service.ID, version.ID, version.Digest, tools))
	marketTestRemoteValidationStored(t, f, version, tools[f.tool.ToolID])
}

func TestToolMarketValidationRejectsCredentialOwnerOrServiceMismatch(t *testing.T) {
	for _, mismatch := range []string{"owner", "service"} {
		t.Run(mismatch, func(t *testing.T) {
			f, version := marketTestRemoteValidationFixture(t)
			credential := marketTestRemoteCredential(f, version)
			if mismatch == "owner" {
				credential.OwnerID = f.buyer.Id
			} else {
				credential.ServiceID = uuid.NewString()
			}
			require.NoError(t, f.db.Create(&credential).Error)
			tools := map[string]string{f.tool.ToolID: strings.Repeat("a", 64)}
			require.ErrorIs(t, RecordToolMarketValidationWithCredential(f.author.Id, f.service.ID, version.ID, version.Digest, tools, credential.ID), ErrToolMarketConflict)
			marketTestRemoteValidationUnchanged(t, f, version)
		})
	}
}

func TestToolMarketValidationFailureCannotOverwritePriorFingerprint(t *testing.T) {
	f, version := marketTestRemoteValidationFixture(t)
	credential := marketTestRemoteCredential(f, version)
	require.NoError(t, f.db.Create(&credential).Error)
	firstDigest := strings.Repeat("a", 64)
	require.NoError(t, RecordToolMarketValidationWithCredential(f.author.Id, f.service.ID, version.ID, version.Digest, map[string]string{f.tool.ToolID: firstDigest}, credential.ID))
	require.ErrorIs(t, RecordToolMarketValidationWithCredential(f.author.Id, f.service.ID, version.ID, version.Digest, map[string]string{f.tool.ToolID: strings.Repeat("b", 64)}, uuid.NewString()), ErrToolMarketConflict)
	marketTestRemoteValidationStored(t, f, version, firstDigest)
}

func marketTestCatalogTool(name, description string, price int) ToolMarketToolInput {
	return ToolMarketToolInput{Name: name, Description: description, InputSchema: json.RawMessage(`{"type":"object"}`), Permissions: []string{"read"}, PriceQuota: price}
}

func marketTestCatalogPublish(t *testing.T, f marketFixture, name, visibility string, tools []ToolMarketToolInput) *ToolMarketService {
	t.Helper()
	in := marketTestDraft(0)
	in.Name, in.Description, in.Visibility, in.Tools = name, "Remote catalog host", visibility, tools
	service, err := SaveToolMarketDraft(f.author.Id, "", in)
	require.NoError(t, err)
	marketTestPublish(t, f.db, f.root.Id, service)
	return service
}

func TestToolMarketCatalogSearchMatchesPublishedToolNameAndDescription(t *testing.T) {
	f := newMarketBuiltinFixture(t, 1)
	service := marketTestCatalogPublish(t, f, "Catalog host", "public", []ToolMarketToolInput{
		marketTestCatalogTool("catalog.lookup", "only-through-tool-description", 0),
		marketTestCatalogTool("catalog.another", "An unrelated operation", 0),
	})
	for _, query := range []string{"catalog.lookup", "CATALOG.LOOKUP", "only-through-tool-description"} {
		rows, err := ListToolMarket(f.buyer.Id, query, "", 0, 20)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, service.ID, rows[0].ID)
	}
	rows, err := ListToolMarket(f.buyer.Id, "catalog.lookup", "builtin", 0, 20)
	require.NoError(t, err)
	require.Empty(t, rows, "a tool match still obeys the requested execution type")
	updated := marketTestDraft(0)
	updated.Name, updated.Description = "Catalog host", "Remote catalog host"
	updated.Tools = []ToolMarketToolInput{marketTestCatalogTool("catalog.current", "current-tool-description", 0)}
	service, err = SaveToolMarketDraft(f.author.Id, service.ID, updated)
	require.NoError(t, err)
	marketTestPublish(t, f.db, f.root.Id, service)
	for _, oldQuery := range []string{"catalog.lookup", "only-through-tool-description"} {
		rows, err := ListToolMarket(f.buyer.Id, oldQuery, "", 0, 20)
		require.NoError(t, err)
		require.Empty(t, rows, "retired tool versions must not match the current service")
	}
	updated.Tools = []ToolMarketToolInput{marketTestCatalogTool("catalog.draft", "unpublished-tool-description", 0)}
	_, err = SaveToolMarketDraft(f.author.Id, service.ID, updated)
	require.NoError(t, err)
	for _, draftQuery := range []string{"catalog.draft", "unpublished-tool-description"} {
		rows, err := ListToolMarket(f.buyer.Id, draftQuery, "", 0, 20)
		require.NoError(t, err)
		require.Empty(t, rows, "private author drafts must not enter discovery")
	}
	rows, err = ListToolMarket(f.buyer.Id, "catalog.current", "", 0, 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, service.ID, rows[0].ID)
}

func TestToolMarketCatalogToolSearchTreatsWildcardCharactersLiterally(t *testing.T) {
	f := newMarketBuiltinFixture(t, 1)
	service := marketTestCatalogPublish(t, f, "Catalog host", "public", []ToolMarketToolInput{marketTestCatalogTool("catalog.lookup", "Ordinary operation", 0)})
	for _, query := range []string{"%", "_", "!"} {
		rows, err := ListToolMarket(f.buyer.Id, query, "", 0, 20)
		require.NoError(t, err)
		require.Empty(t, rows)
	}
	require.NoError(t, f.db.Model(&ToolMarketToolVersion{}).Where("version_id = ?", service.LiveVersionID).Update("description", "100% literal_tool!").Error)
	for _, query := range []string{"%", "_", "!", "100%", "literal_tool!"} {
		rows, err := ListToolMarket(f.buyer.Id, query, "", 0, 20)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, service.ID, rows[0].ID)
	}
}

func TestToolMarketCatalogToolSearchCannotRevealPrivateService(t *testing.T) {
	f := newMarketBuiltinFixture(t, 1)
	service := marketTestCatalogPublish(t, f, "Private host", "private", []ToolMarketToolInput{marketTestCatalogTool("catalog.secret", "private-tool-description-needle", 17)})
	for _, query := range []string{"catalog.secret", "private-tool-description-needle"} {
		for _, userID := range []int{0, f.buyer.Id} {
			rows, err := ListToolMarket(userID, query, "", 0, 20)
			require.NoError(t, err)
			require.Empty(t, rows, "matching a private tool must not bypass service visibility")
		}
		rows, err := ListToolMarket(f.author.Id, query, "", 0, 20)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, service.ID, rows[0].ID)
		require.Equal(t, 1, rows[0].ToolCount)
		require.Equal(t, 17, rows[0].MinPriceQuota)
		require.Equal(t, 17, rows[0].MaxPriceQuota)
	}
}

func TestToolMarketCatalogSummariesMatchLiveDetailAndFavorites(t *testing.T) {
	f := newMarketBuiltinFixture(t, 1)
	builtin := marketTestBuiltinDefinition()
	builtin.Tools = append(builtin.Tools, marketTestCatalogTool("system.second", "Second system operation", 99))
	require.NoError(t, EnsureToolMarketBuiltinServices([]ToolMarketBuiltinServiceInput{builtin}))
	mixed := marketTestCatalogPublish(t, f, "Mixed price host", "public", []ToolMarketToolInput{
		marketTestCatalogTool("catalog.freeone", "Free lookup", 0),
		marketTestCatalogTool("catalog.freetwo", "Free status", 0),
		marketTestCatalogTool("catalog.medium", "Paid lookup", 37),
		marketTestCatalogTool("catalog.large", "Paid export", 121),
	})
	paid := marketTestCatalogPublish(t, f, "Paid host", "public", []ToolMarketToolInput{
		marketTestCatalogTool("catalog.small", "Paid lookup", 25),
		marketTestCatalogTool("catalog.export", "Paid export", 67),
	})
	// An unpublished edit has different prices and tool count; all public
	// summaries must continue describing the immutable live version.
	draft := marketTestDraft(999)
	draft.Name = "Mixed price unpublished draft"
	_, err := SaveToolMarketDraft(f.author.Id, mixed.ID, draft)
	require.NoError(t, err)
	expected := map[string]struct{ count, min, max int }{
		f.service.ID: {2, 0, 0}, mixed.ID: {4, 0, 121}, paid.ID: {2, 25, 67},
	}
	rows, err := ListToolMarket(f.buyer.Id, "", "", 0, 20)
	require.NoError(t, err)
	require.Len(t, rows, len(expected))
	byID := make(map[string]ToolMarketListItem, len(rows))
	for _, row := range rows {
		want, exists := expected[row.ID]
		require.True(t, exists)
		require.Equal(t, want.count, row.ToolCount)
		require.Equal(t, want.min, row.MinPriceQuota)
		require.Equal(t, want.max, row.MaxPriceQuota)
		detail, err := GetToolMarketDetail(f.buyer.Id, row.ID, false)
		require.NoError(t, err)
		require.Len(t, detail.Tools, row.ToolCount)
		require.Equal(t, detail.Version.ID, row.VersionID)
		for _, tool := range detail.Tools {
			require.GreaterOrEqual(t, tool.PriceQuota, row.MinPriceQuota)
			require.LessOrEqual(t, tool.PriceQuota, row.MaxPriceQuota)
		}
		require.NoError(t, SetToolMarketFavorite(f.buyer.Id, row.ID, true))
		byID[row.ID] = row
	}
	resource, err := ListToolMarketAccountResources(f.buyer.Id, "favorites", 0, 20)
	require.NoError(t, err)
	favorites, ok := resource.([]ToolMarketListItem)
	require.True(t, ok)
	require.Len(t, favorites, len(rows))
	for _, favorite := range favorites {
		require.Equal(t, byID[favorite.ID], favorite)
	}
}

func TestToolMarketCatalogPlacesBuiltinBeforeNewerRemoteServices(t *testing.T) {
	f := newMarketBuiltinFixture(t, 1)
	remote := marketTestCatalogPublish(t, f, "New remote host", "public", []ToolMarketToolInput{marketTestCatalogTool("catalog.lookup", "Remote lookup", 0)})
	require.NoError(t, f.db.Model(&ToolMarketVersion{}).Where("id = ?", f.tool.VersionID).Update("published_at", common.GetTimestamp()-3600).Error)
	require.NoError(t, f.db.Model(&ToolMarketVersion{}).Where("id = ?", remote.LiveVersionID).Update("published_at", common.GetTimestamp()).Error)
	rows, err := ListToolMarket(f.buyer.Id, "", "", 0, 20)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, f.service.ID, rows[0].ID)
	require.Equal(t, "builtin", rows[0].ExecutionType)
	require.Equal(t, remote.ID, rows[1].ID)
	require.Greater(t, rows[1].PublishedAt, rows[0].PublishedAt)
	firstPage, err := ListToolMarket(f.buyer.Id, "", "", 0, 1)
	require.NoError(t, err)
	require.Len(t, firstPage, 1)
	require.Equal(t, f.service.ID, firstPage[0].ID)
}
