package model

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type marketFixture struct {
	db                  *gorm.DB
	buyer, author, root User
	service             *ToolMarketService
	tool                ToolMarketToolVersion
	grant               *ToolMarketGrant
}

func marketTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB, oldRedis := DB, common.RedisEnabled
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, oldLog)
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// SQLite serializes writers; a single connection lets concurrent tests
	// exercise duplicate transactions without unrelated SQLITE_BUSY failures.
	sqlDB.SetMaxOpenConns(1)
	DB = db
	require.NoError(t, db.AutoMigrate(append([]interface{}{&User{}}, toolMarketModels()...)...))
	t.Cleanup(func() {
		DB = oldDB
		common.RedisEnabled = oldRedis
		common.SetDatabaseTypes(oldMain, oldLog)
		_ = sqlDB.Close()
	})
	return db
}

func marketTestUser(t *testing.T, db *gorm.DB, name string, quota, role int) User {
	t.Helper()
	u := User{Username: name, AffCode: name, Status: common.UserStatusEnabled, Role: role, Quota: quota}
	require.NoError(t, db.Create(&u).Error)
	return u
}

func marketTestDraft(price int) ToolMarketDraftInput {
	return ToolMarketDraftInput{Name: "Example MCP", Description: "Read a public record", ExecutionType: "remote", Visibility: "public", Endpoint: "https://example.com/mcp",
		Tools: []ToolMarketToolInput{{Name: "lookup", Description: "Look up a record", InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`), Permissions: []string{"read"}, PriceQuota: price}}}
}

func marketTestPublish(t *testing.T, db *gorm.DB, root int, service *ToolMarketService) ToolMarketToolVersion {
	t.Helper()
	require.NoError(t, SubmitToolMarketDraft(service.OwnerID, service.ID, service.DraftVersionID))
	// Simulate a trusted validator. No production route can write this field.
	require.NoError(t, db.Model(&ToolMarketVersion{}).Where("id = ?", service.DraftVersionID).Update("validation_digest", gorm.Expr("digest")).Error)
	require.NoError(t, ReviewToolMarketVersion(root, service.ID, service.DraftVersionID, true, "Verified test fixture"))
	require.NoError(t, db.First(service, "id = ?", service.ID).Error)
	var tool ToolMarketToolVersion
	require.NoError(t, db.First(&tool, "version_id = ?", service.LiveVersionID).Error)
	return tool
}

func newMarketFixture(t *testing.T, price int) marketFixture {
	t.Helper()
	db := marketTestDB(t)
	f := marketFixture{db: db, buyer: marketTestUser(t, db, "buyer", 1000, common.RoleCommonUser), author: marketTestUser(t, db, "author", 0, common.RoleCommonUser), root: marketTestUser(t, db, "root", 0, common.RoleRootUser)}
	require.NoError(t, SetToolMarketConfig(f.root.Id, ToolMarketConfig{Enabled: true, FeeBPS: 1250, RecipientID: f.root.Id}))
	var err error
	f.service, err = SaveToolMarketDraft(f.author.Id, "", marketTestDraft(price))
	require.NoError(t, err)
	f.tool = marketTestPublish(t, db, f.root.Id, f.service)
	require.NoError(t, SetToolMarketInstallation(f.buyer.Id, "client-a", f.tool.ToolID, f.tool.VersionID, true))
	f.grant, err = CreateToolMarketGrant(f.buyer.Id, ToolMarketGrant{ClientID: "client-a", ToolID: f.tool.ToolID, VersionID: f.tool.VersionID, MaxPriceQuota: 500, MaxTotalQuota: 1000, MaxCalls: 10, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	return f
}

func (f marketFixture) input(key string) ToolMarketReserveInput {
	return ToolMarketReserveInput{UserID: f.buyer.Id, ClientID: "client-a", RequestKey: key, ToolID: f.tool.ToolID, VersionID: f.tool.VersionID, GrantID: f.grant.ID, Arguments: json.RawMessage(`{"q":"hello"}`), ResolveBy: common.GetTimestamp() + 600}
}

func marketTestBalance(t *testing.T, db *gorm.DB, userID int) int {
	t.Helper()
	var user User
	require.NoError(t, db.First(&user, userID).Error)
	return user.Quota
}

func TestToolMarketSuccessTransfersOnceAndSnapshotsFee(t *testing.T) {
	f := newMarketFixture(t, 101)
	call, created, err := ReserveToolMarketCall(f.input("request-1"))
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, 899, marketTestBalance(t, f.db, f.buyer.Id))
	require.Equal(t, 0, marketTestBalance(t, f.db, f.author.Id))
	// Input ordering/spacing is not a new business request.
	retry := f.input("request-1")
	retry.Arguments = json.RawMessage(`{ "q" : "hello" }`)
	replayed, created, err := ReserveToolMarketCall(retry)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, call.ID, replayed.ID)
	require.NoError(t, SetToolMarketConfig(f.root.Id, ToolMarketConfig{Enabled: true, FeeBPS: 9000, RecipientID: f.root.Id}))
	started, err := StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.True(t, started)
	started, err = StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.False(t, started)
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.Equal(t, 89, marketTestBalance(t, f.db, f.author.Id))
	require.Equal(t, 12, marketTestBalance(t, f.db, f.root.Id))
	var transfers []ToolMarketTransfer
	require.NoError(t, f.db.Find(&transfers).Error)
	require.Len(t, transfers, 2)
	for _, tr := range transfers {
		require.Equal(t, 1250, tr.FeeBPS)
		require.Equal(t, call.ID, tr.CallID)
	}
	other := retry
	other.Arguments = json.RawMessage(`{"q":"different"}`)
	_, _, err = ReserveToolMarketCall(other)
	require.ErrorIs(t, err, ErrToolMarketConflict)
	call2, created, err := ReserveToolMarketCall(f.input("request-2"))
	require.NoError(t, err)
	require.True(t, created)
	require.NotEqual(t, call.ID, call2.ID)
}

func TestToolMarketFailureUnknownAndExpiryNeverDoubleRefund(t *testing.T) {
	f := newMarketFixture(t, 100)
	call, _, err := ReserveToolMarketCall(f.input("timeout"))
	require.NoError(t, err)
	started, err := StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.True(t, started)
	require.NoError(t, MarkToolMarketCallUnknown(call.ID))
	require.Equal(t, 900, marketTestBalance(t, f.db, f.buyer.Id))
	require.ErrorIs(t, ExpireToolMarketCall(call.ID), ErrToolMarketConflict)
	require.NoError(t, f.db.Model(&ToolMarketCall{}).Where("id = ?", call.ID).Update("resolve_by", common.GetTimestamp()-1).Error)
	require.NoError(t, ExpireToolMarketCall(call.ID))
	require.NoError(t, ExpireToolMarketCall(call.ID))
	require.ErrorIs(t, FinishToolMarketCall(call.ID, true), ErrToolMarketConflict)
	require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
	require.NoError(t, f.db.First(call, "id = ?", call.ID).Error)
	require.Equal(t, "unknown", call.ExecutionStatus)
	require.Equal(t, "released", call.SettlementStatus)
	call, _, err = ReserveToolMarketCall(f.input("cancel-before-start"))
	require.NoError(t, err)
	require.ErrorIs(t, FinishToolMarketCall(call.ID, true), ErrToolMarketConflict)
	require.NoError(t, FinishToolMarketCall(call.ID, false))
	require.NoError(t, FinishToolMarketCall(call.ID, false))
	require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
}

func TestToolMarketBudgetsIncludeHoldsAndCannotResetHistory(t *testing.T) {
	f := newMarketFixture(t, 100)
	require.NoError(t, SetToolMarketBudget(f.buyer.Id, "account", "", 250))
	require.NoError(t, SetToolMarketBudget(f.buyer.Id, "client", "client-a", 150))
	require.NoError(t, SetToolMarketBudget(f.buyer.Id, "tool", f.tool.ToolID, 300))
	call, _, err := ReserveToolMarketCall(f.input("one"))
	require.NoError(t, err)
	_, _, err = ReserveToolMarketCall(f.input("two"))
	require.ErrorIs(t, err, ErrToolMarketBudget)
	require.ErrorIs(t, SetToolMarketBudget(f.buyer.Id, "account", "", 99), ErrToolMarketBudget)
	_, err = StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.ErrorIs(t, SetToolMarketBudget(f.buyer.Id, "client", "client-a", 50), ErrToolMarketBudget)
	require.NoError(t, SetToolMarketBudget(f.buyer.Id, "client", "client-a", 200))
	_, _, err = ReserveToolMarketCall(f.input("two"))
	require.NoError(t, err)
	_, _, err = ReserveToolMarketCall(f.input("three"))
	require.ErrorIs(t, err, ErrToolMarketBudget)
	var budget ToolMarketBudget
	require.NoError(t, f.db.First(&budget, "user_id = ? AND scope = ?", f.buyer.Id, "account").Error)
	require.Equal(t, 100, budget.SpentQuota)
	require.Equal(t, 100, budget.ReservedQuota)
}

func TestToolMarketBudgetAddedDuringCallReleasesCorrectly(t *testing.T) {
	f := newMarketFixture(t, 100)
	call, _, err := ReserveToolMarketCall(f.input("one"))
	require.NoError(t, err)
	require.NoError(t, SetToolMarketBudget(f.buyer.Id, "account", "", 100))
	require.NoError(t, FinishToolMarketCall(call.ID, false))
	var budget ToolMarketBudget
	require.NoError(t, f.db.First(&budget, "user_id = ?", f.buyer.Id).Error)
	require.Zero(t, budget.ReservedQuota)
}

func TestToolMarketWalletSharedWithModelsAndInsufficientRollback(t *testing.T) {
	f := newMarketFixture(t, 100)
	require.NoError(t, SetToolMarketBudget(f.buyer.Id, "account", "", 500))
	ok, err := TryReserveUserQuota(f.buyer.Id, 950)
	require.NoError(t, err)
	require.True(t, ok)
	_, _, err = ReserveToolMarketCall(f.input("one"))
	require.ErrorIs(t, err, ErrToolMarketBalance)
	require.Equal(t, 50, marketTestBalance(t, f.db, f.buyer.Id))
	var budget ToolMarketBudget
	require.NoError(t, f.db.First(&budget, "user_id = ?", f.buyer.Id).Error)
	require.Zero(t, budget.ReservedQuota)
	var count int64
	require.NoError(t, f.db.Model(&ToolMarketCall{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, f.db.First(f.grant, "id = ?", f.grant.ID).Error)
	require.Zero(t, f.grant.ReservedCalls)
}

func TestToolMarketAuthorizationIsSeparateFromLoadingAndFavorites(t *testing.T) {
	f := newMarketFixture(t, 100)
	require.NoError(t, SetToolMarketFavorite(f.buyer.Id, f.service.ID, true))
	require.NoError(t, SetToolMarketInstallation(f.buyer.Id, "client-a", f.tool.ToolID, f.tool.VersionID, false))
	_, _, err := ReserveToolMarketCall(f.input("one"))
	require.Error(t, err)
	require.NoError(t, SetToolMarketInstallation(f.buyer.Id, "client-a", f.tool.ToolID, f.tool.VersionID, true))
	input := f.input("one")
	input.ClientID = "client-b"
	_, _, err = ReserveToolMarketCall(input)
	require.Error(t, err)
	call, _, err := ReserveToolMarketCall(f.input("one"))
	require.NoError(t, err)
	require.NoError(t, RevokeToolMarketGrant(f.buyer.Id, f.grant.ID))
	started, err := StartToolMarketCall(call.ID)
	require.ErrorIs(t, err, ErrToolMarketDenied)
	require.False(t, started)
	require.NoError(t, FinishToolMarketCall(call.ID, false))
	_, _, err = ReserveToolMarketCall(f.input("two"))
	require.ErrorIs(t, err, ErrToolMarketDenied)
	require.Error(t, RevokeToolMarketGrant(f.author.Id, f.grant.ID))
}

func TestToolMarketGrantPriceTotalAndCallsAreEnforced(t *testing.T) {
	for _, kind := range []string{"price", "total", "calls", "expired"} {
		t.Run(kind, func(t *testing.T) {
			f := newMarketFixture(t, 100)
			updates := map[string]any{}
			switch kind {
			case "price":
				updates["max_price_quota"] = 99
			case "total":
				updates["max_total_quota"] = 99
			case "calls":
				updates["max_calls"] = 0
			case "expired":
				updates["expires_at"] = common.GetTimestamp() - 1
			}
			require.NoError(t, f.db.Model(f.grant).Updates(updates).Error)
			_, _, err := ReserveToolMarketCall(f.input("one"))
			require.Error(t, err)
			require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
		})
	}
}

func TestToolMarketVersionStableToolIdentityAndPrivateDiscovery(t *testing.T) {
	f := newMarketFixture(t, 100)
	oldVersion, oldTool := f.tool.VersionID, f.tool.ToolID
	draft := marketTestDraft(200)
	draft.Visibility = "shared"
	draft.AllowedUsers = []int{f.buyer.Id}
	service, err := SaveToolMarketDraft(f.author.Id, f.service.ID, draft)
	require.NoError(t, err)
	live, err := GetToolMarketDetail(f.buyer.Id, service.ID, false)
	require.NoError(t, err)
	require.Equal(t, 100, live.Tools[0].PriceQuota)
	require.Empty(t, live.Service.DraftVersionID)
	_, err = GetToolMarketDetail(f.buyer.Id, service.ID, true)
	require.Error(t, err)
	f.tool = marketTestPublish(t, f.db, f.root.Id, service)
	require.Equal(t, oldTool, f.tool.ToolID)
	require.NotEqual(t, oldVersion, f.tool.VersionID)
	rows, err := ListToolMarket(0, "", "", 0, 20)
	require.NoError(t, err)
	require.Empty(t, rows)
	rows, err = ListToolMarket(f.buyer.Id, "Example", "remote", 0, 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	_, err = GetToolMarketDetail(0, service.ID, false)
	require.Error(t, err)
	input := f.input("one")
	input.VersionID = oldVersion
	_, _, err = ReserveToolMarketCall(input)
	require.Error(t, err)
	// Publishing never auto-upgrades an installation or broadens a grant.
	var installed ToolMarketInstallation
	require.NoError(t, f.db.First(&installed, "user_id = ?", f.buyer.Id).Error)
	require.Equal(t, oldVersion, installed.VersionID)
	_, _, err = ReserveToolMarketCall(f.input("two"))
	require.Error(t, err)
}

func TestToolMarketCannotPublishUnvalidatedDraftOrEditPending(t *testing.T) {
	f := newMarketFixture(t, 100)
	draft, err := SaveToolMarketDraft(f.author.Id, f.service.ID, marketTestDraft(200))
	require.NoError(t, err)
	require.NoError(t, SubmitToolMarketDraft(f.author.Id, draft.ID, draft.DraftVersionID))
	require.ErrorIs(t, ReviewToolMarketVersion(f.root.Id, draft.ID, draft.DraftVersionID, true, "Not actually validated"), ErrToolMarketDenied)
	_, err = SaveToolMarketDraft(f.author.Id, draft.ID, marketTestDraft(300))
	require.ErrorIs(t, err, ErrToolMarketConflict)
	require.ErrorIs(t, ReviewToolMarketVersion(f.buyer.Id, draft.ID, draft.DraftVersionID, false, "No access"), ErrToolMarketDenied)
	require.NoError(t, ReviewToolMarketVersion(f.root.Id, draft.ID, draft.DraftVersionID, false, "Connection check required"))
	_, err = SaveToolMarketDraft(f.author.Id, draft.ID, marketTestDraft(300))
	require.NoError(t, err)
}

func TestToolMarketSuspensionCannotBeUndoneByAuthorOrNewVersion(t *testing.T) {
	f := newMarketFixture(t, 100)
	require.NoError(t, SetToolMarketPaused(f.root.Id, f.service.ID, true))
	require.ErrorIs(t, SetToolMarketPaused(f.author.Id, f.service.ID, false), ErrToolMarketDenied)
	service, err := SaveToolMarketDraft(f.author.Id, f.service.ID, marketTestDraft(100))
	require.NoError(t, err)
	marketTestPublish(t, f.db, f.root.Id, service)
	require.Equal(t, "suspended", service.Status)
	_, _, err = ReserveToolMarketCall(f.input("one"))
	require.Error(t, err)
}

func TestToolMarketSettlementRollsBackAllBalancesOnCreditFailure(t *testing.T) {
	f := newMarketFixture(t, 100)
	call, _, err := ReserveToolMarketCall(f.input("one"))
	require.NoError(t, err)
	_, err = StartToolMarketCall(call.ID)
	require.NoError(t, err)
	// Author credit runs before the platform credit; force the latter to fail.
	require.NoError(t, f.db.Model(&User{}).Where("id = ?", f.root.Id).Update("quota", common.MaxWalletQuota).Error)
	require.Error(t, FinishToolMarketCall(call.ID, true))
	require.Equal(t, 0, marketTestBalance(t, f.db, f.author.Id))
	var count int64
	require.NoError(t, f.db.Model(&ToolMarketTransfer{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, f.db.First(call, "id = ?", call.ID).Error)
	require.Equal(t, "held", call.SettlementStatus)
	require.NoError(t, f.db.Model(&User{}).Where("id = ?", f.root.Id).Update("quota", 0).Error)
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.Equal(t, 88, marketTestBalance(t, f.db, f.author.Id))
	require.Equal(t, 12, marketTestBalance(t, f.db, f.root.Id))
}

func TestToolMarketConcurrentReplayAndBudgetReservations(t *testing.T) {
	f := newMarketFixture(t, 100)
	require.NoError(t, SetToolMarketBudget(f.buyer.Id, "account", "", 100))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			call, _, err := ReserveToolMarketCall(f.input("same"))
			errs <- err
			if err == nil {
				ids <- call.ID
			}
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		require.NoError(t, err)
	}
	var id string
	for next := range ids {
		if id != "" {
			require.Equal(t, id, next)
		}
		id = next
	}
	_, _, err := ReserveToolMarketCall(f.input("different"))
	require.ErrorIs(t, err, ErrToolMarketBudget)
	_, err = StartToolMarketCall(id)
	require.NoError(t, err)
	errs = make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- FinishToolMarketCall(id, true) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 900, marketTestBalance(t, f.db, f.buyer.Id))
	require.Equal(t, 88, marketTestBalance(t, f.db, f.author.Id))
}

func TestToolMarketFreeCallsAndSameAccountAccounting(t *testing.T) {
	for _, price := range []int{0, 101} {
		t.Run(fmt.Sprint(price), func(t *testing.T) {
			f := newMarketFixture(t, price)
			// A superadmin may own a tool: both credits still conserve the charged sum.
			require.NoError(t, f.db.Model(f.service).Update("owner_id", f.root.Id).Error)
			call, _, err := ReserveToolMarketCall(f.input("one"))
			require.NoError(t, err)
			_, err = StartToolMarketCall(call.ID)
			require.NoError(t, err)
			require.NoError(t, FinishToolMarketCall(call.ID, true))
			require.Equal(t, price, marketTestBalance(t, f.db, f.root.Id))
			require.Equal(t, 1000-price, marketTestBalance(t, f.db, f.buyer.Id))
			if price == 0 {
				var count int64
				require.NoError(t, f.db.Model(&ToolMarketTransfer{}).Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}

}

func TestToolMarketFeeRoundingDoesNotOverflow(t *testing.T) {
	require.Equal(t, common.MaxWalletQuota, marketFee(common.MaxWalletQuota, 10000))
	require.Equal(t, 0, marketFee(1, 1250))
	require.Equal(t, 12, marketFee(101, 1250))
}

func TestToolMarketOptionalOutputSchemaAcceptsOmittedOrNull(t *testing.T) {
	db := marketTestDB(t)
	user := marketTestUser(t, db, "optional-output", 0, common.RoleCommonUser)
	for _, output := range []json.RawMessage{nil, json.RawMessage("null")} {
		input := marketTestDraft(0)
		input.Tools[0].OutputSchema = output
		service, err := SaveToolMarketDraft(user.Id, "", input)
		require.NoError(t, err)
		detail, err := GetToolMarketDetail(user.Id, service.ID, true)
		require.NoError(t, err)
		require.Empty(t, detail.Tools[0].OutputSchema)
	}
	input := marketTestDraft(0)
	input.Tools[0].InputSchema = json.RawMessage("null")
	_, err := SaveToolMarketDraft(user.Id, "", input)
	require.ErrorIs(t, err, ErrToolMarketInput)
}

func TestToolMarketStatusReadReleasesExpiredHoldWithoutWaitingForSweep(t *testing.T) {
	f := newMarketFixture(t, 100)
	call, _, err := ReserveToolMarketCall(f.input("expired-read"))
	require.NoError(t, err)
	_, err = StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.NoError(t, f.db.Model(call).Update("resolve_by", common.GetTimestamp()-1).Error)
	_, err = GetToolMarketCall(f.author.Id, "", call.ID)
	require.Error(t, err)
	require.Equal(t, 900, marketTestBalance(t, f.db, f.buyer.Id))
	state, err := GetToolMarketCall(f.buyer.Id, "client-a", call.ID)
	require.NoError(t, err)
	require.Equal(t, "unknown", state.ExecutionStatus)
	require.Equal(t, "released", state.SettlementStatus)
	require.NoError(t, ExpireToolMarketCall(call.ID))
	require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
}
