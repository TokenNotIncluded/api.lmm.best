package model

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketDeletePermissionsAndIdempotency(t *testing.T) {
	f := newMarketFixture(t, 100)
	require.ErrorIs(t, DeleteToolMarketService(f.buyer.Id, f.service.ID), ErrToolMarketDenied)
	var service ToolMarketService
	require.NoError(t, f.db.First(&service, "id = ?", f.service.ID).Error)
	require.Equal(t, "published", service.Status)
	admin := marketTestUser(t, f.db, "delete-admin", 0, common.RoleAdminUser)
	require.NoError(t, DeleteToolMarketService(admin.Id, service.ID))
	require.NoError(t, DeleteToolMarketService(f.author.Id, service.ID))
	var count int64
	require.NoError(t, f.db.Model(&ToolMarketEvent{}).Where("object_id = ? AND action = ?", service.ID, "service.delete").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, f.db.Create(&ToolMarketService{ID: "builtin-delete", OwnerID: 0, Status: "published"}).Error)
	require.ErrorIs(t, DeleteToolMarketService(f.root.Id, "builtin-delete"), ErrToolMarketDenied)
	require.NoError(t, f.db.Model(&User{}).Where("id = ?", admin.Id).Update("status", common.UserStatusDisabled).Error)
	require.ErrorIs(t, DeleteToolMarketService(admin.Id, service.ID), ErrToolMarketDenied)
}

func TestToolMarketRetirementGuardSurvivesLegacyStatusDrift(t *testing.T) {
	f := newMarketFixture(t, 100)
	require.NoError(t, DeleteToolMarketService(f.author.Id, f.service.ID))
	// A legacy report reviewer only knows suspended, not the newer deleted
	// status. The existing draft retirement guard must remain authoritative.
	require.NoError(t, f.db.Model(&ToolMarketService{}).Where("id = ?", f.service.ID).Update("status", "suspended").Error)
	_, err := SaveToolMarketDraft(f.author.Id, f.service.ID, marketTestDraft(100))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.ErrorIs(t, SetToolMarketPaused(f.author.Id, f.service.ID, false), gorm.ErrRecordNotFound)
	require.ErrorIs(t, SubmitToolMarketDraft(f.author.Id, f.service.ID, f.service.LiveVersionID), gorm.ErrRecordNotFound)
	require.ErrorIs(t, ReviewToolMarketVersion(f.root.Id, f.service.ID, f.service.LiveVersionID, true, "must stay retired"), gorm.ErrRecordNotFound)
	_, err = GetToolMarketDetail(f.author.Id, f.service.ID, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	rows, err := ListToolMarketAccountResources(f.author.Id, "services", 0, 20)
	require.NoError(t, err)
	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	require.JSONEq(t, "[]", string(raw))
	require.NoError(t, DeleteToolMarketService(f.author.Id, f.service.ID))
	var count int64
	require.NoError(t, f.db.Model(&ToolMarketEvent{}).Where("object_id = ? AND action = 'service.delete'", f.service.ID).Count(&count).Error)
	require.EqualValues(t, 1, count, "legacy status drift must not create a second deletion")
}

func TestToolMarketRetirementFilterPreservesNullableLegacyDraftPointers(t *testing.T) {
	f := newMarketFixture(t, 100)
	require.NoError(t, f.db.Model(&ToolMarketService{}).Where("id = ?", f.service.ID).UpdateColumn("draft_version_id", nil).Error)
	detail, err := GetToolMarketDetail(f.author.Id, f.service.ID, true)
	require.NoError(t, err)
	require.Equal(t, f.service.LiveVersionID, detail.Version.ID)
	rows, err := ListToolMarketAccountResources(f.author.Id, "services", 0, 20)
	require.NoError(t, err)
	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	require.Contains(t, string(raw), f.service.ID)
	_, err = SaveToolMarketDraft(f.author.Id, f.service.ID, marketTestDraft(100))
	require.NoError(t, err, "SQL NULL is an empty old draft, never the retirement marker")
}

func TestToolMarketDeletePreservesRunningSettlementAndHistory(t *testing.T) {
	f := newMarketFixture(t, 101)
	call, _, err := ReserveToolMarketCall(f.input("running-before-delete"))
	require.NoError(t, err)
	started, err := StartToolMarketCall(call.ID)
	require.NoError(t, err)
	require.True(t, started)
	before := map[string]int64{}
	for _, table := range []string{"tool_market_versions", "tool_market_tools", "tool_market_tool_versions", "tool_market_grants", "tool_market_installations", "tool_market_calls"} {
		var count int64
		require.NoError(t, f.db.Table(table).Count(&count).Error)
		before[table] = count
	}
	require.NoError(t, DeleteToolMarketService(f.author.Id, f.service.ID))
	for table, count := range before {
		var after int64
		require.NoError(t, f.db.Table(table).Count(&after).Error)
		require.Equal(t, count, after, table)
	}
	for _, user := range []int{0, f.buyer.Id, f.author.Id} {
		rows, err := ListToolMarket(user, "", "", 0, 20)
		require.NoError(t, err)
		require.Empty(t, rows)
	}
	rows, err := ListToolMarketAccountResources(f.author.Id, "services", 0, 20)
	require.NoError(t, err)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	require.JSONEq(t, "[]", string(encoded))
	_, err = GetToolMarketDetail(f.author.Id, f.service.ID, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, _, err = ReserveToolMarketCall(f.input("new-after-delete"))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	replayed, created, err := ReserveToolMarketCall(f.input("running-before-delete"))
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, call.ID, replayed.ID)
	result := json.RawMessage(`{"content":[{"type":"text","text":"Original result"}]}`)
	require.NoError(t, RecordToolMarketResult(call.ID, true, result))
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.NoError(t, FinishToolMarketCall(call.ID, true))
	require.Equal(t, 899, marketTestBalance(t, f.db, f.buyer.Id))
	require.Equal(t, 89, marketTestBalance(t, f.db, f.author.Id))
	require.Equal(t, 12, marketTestBalance(t, f.db, f.root.Id))
	delivered, _, err := GetToolMarketResult(f.buyer.Id, call.ClientID, call.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(result), string(delivered))
	_, err = ReportToolMarketCall(f.buyer.Id, call.ID, "Review the original call")
	require.NoError(t, err)
	require.NoError(t, ReviewToolMarketReport(f.root.Id, call.ID, true, "Confirmed report after deletion"))
	var service ToolMarketService
	require.NoError(t, f.db.First(&service, "id = ?", f.service.ID).Error)
	require.Equal(t, ToolMarketServiceDeleted, service.Status)
	require.Empty(t, service.LiveVersionID)
	require.Equal(t, toolMarketRetirementVersionID, service.DraftVersionID)
	calls, err := ListToolMarketCalls(f.buyer.Id, 0, 20)
	require.NoError(t, err)
	require.Len(t, calls, 1)
	income, err := ListToolMarketIncome(f.author.Id, 0, 20)
	require.NoError(t, err)
	require.Len(t, income, 1)
}

func TestToolMarketDeleteHidesActivatedPrivateService(t *testing.T) {
	f := newPrivateMarketFixture(t)
	require.NoError(t, ActivateToolMarketPrivate(f.owner.Id, f.service.ID, f.version.ID))
	require.NoError(t, DeleteToolMarketService(f.owner.Id, f.service.ID))
	rows, err := ListToolMarket(f.owner.Id, "", "", 0, 20)
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = GetToolMarketDetail(f.owner.Id, f.service.ID, true)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, _, err = marketLiveTool(f.db, f.owner.Id, f.tools[0].ToolID, f.version.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.ErrorIs(t, ActivateToolMarketPrivate(f.owner.Id, f.service.ID, f.version.ID), ErrToolMarketConflict)
	var version ToolMarketVersion
	require.NoError(t, f.db.First(&version, "id = ?", f.version.ID).Error)
	require.Equal(t, "published", version.Status)
}

func TestToolMarketDeleteCannotBeUndoneByDraftOrReviewActions(t *testing.T) {
	f := newMarketFixture(t, 100)
	draft, err := SaveToolMarketDraft(f.author.Id, f.service.ID, marketTestDraft(200))
	require.NoError(t, err)
	require.NoError(t, SubmitToolMarketDraft(f.author.Id, draft.ID, draft.DraftVersionID))
	require.NoError(t, DeleteToolMarketService(f.author.Id, f.service.ID))
	_, err = SaveToolMarketDraft(f.author.Id, f.service.ID, marketTestDraft(300))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.ErrorIs(t, SubmitToolMarketDraft(f.author.Id, draft.ID, draft.DraftVersionID), gorm.ErrRecordNotFound)
	require.ErrorIs(t, ReviewToolMarketVersion(f.root.Id, draft.ID, draft.DraftVersionID, false, "Do not resurrect"), gorm.ErrRecordNotFound)
	require.ErrorIs(t, SetToolMarketPaused(f.root.Id, draft.ID, false), gorm.ErrRecordNotFound)
	require.ErrorIs(t, ConfigureToolMarketCredential(f.author.Id, draft.ID, draft.DraftVersionID, "none", "", ""), gorm.ErrRecordNotFound)
	_, err = GetToolMarketReview(f.root.Id, draft.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	queue, err := ListToolMarketReviewQueue(f.root.Id)
	require.NoError(t, err)
	require.Empty(t, queue)
}

func TestToolMarketDeleteSerializesWithConcurrentReservation(t *testing.T) {
	f := newMarketFixture(t, 100)
	var call *ToolMarketCall
	var reserveErr, deleteErr error
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		call, _, reserveErr = ReserveToolMarketCall(f.input("concurrent-delete"))
	}()
	go func() {
		defer wg.Done()
		<-start
		deleteErr = DeleteToolMarketService(f.author.Id, f.service.ID)
	}()
	close(start)
	wg.Wait()
	require.NoError(t, deleteErr)
	if reserveErr == nil {
		started, err := StartToolMarketCall(call.ID)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		require.False(t, started)
		require.NoError(t, FinishToolMarketCall(call.ID, false))
	} else {
		require.ErrorIs(t, reserveErr, gorm.ErrRecordNotFound)
	}
	_, _, err := ReserveToolMarketCall(f.input("after-concurrent-delete"))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.Equal(t, 1000, marketTestBalance(t, f.db, f.buyer.Id))
	require.Equal(t, 0, marketTestBalance(t, f.db, f.author.Id))
}

func TestToolMarketDeletePreventsInFlightAIApproval(t *testing.T) {
	f := newMarketFixture(t, 100)
	marketAIOptions(t, f.db, setting.MarketAIReviewAuto, setting.MarketAIReviewOff)
	draft, err := SaveToolMarketDraft(f.author.Id, f.service.ID, marketTestDraft(100))
	require.NoError(t, err)
	require.NoError(t, SubmitToolMarketDraft(f.author.Id, draft.ID, draft.DraftVersionID))
	job := marketAIClaim(t)
	require.NoError(t, f.db.Model(&ToolMarketVersion{}).Where("id = ?", draft.DraftVersionID).Update("validation_digest", gorm.Expr("digest")).Error)
	require.NoError(t, DeleteToolMarketService(f.author.Id, draft.ID))
	require.NoError(t, marketAIComplete(t, job, false, true))
	var service ToolMarketService
	require.NoError(t, f.db.First(&service, "id = ?", draft.ID).Error)
	require.Equal(t, ToolMarketServiceDeleted, service.Status)
	require.NoError(t, f.db.First(job, "id = ?", job.ID).Error)
	require.Equal(t, ModerationJobCancelled, job.Status)
	require.Equal(t, "stale", job.MarketOutcome)
}
