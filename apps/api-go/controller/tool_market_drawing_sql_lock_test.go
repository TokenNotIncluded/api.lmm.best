package controller

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func assertRejectedDrawingReleasedWithoutCharges(t *testing.T, fixture *drawingParityFixture, in model.ToolMarketReserveInput, requestState string, response *service.ToolMarketExecutionResponse) {
	t.Helper()
	require.Equal(t, "failed", response.Call.ExecutionStatus)
	require.Equal(t, "released", response.Call.SettlementStatus)
	var result mcp.CallToolResult
	require.NoError(t, json.Unmarshal(response.Result, &result))
	require.True(t, result.IsError)
	require.Eventually(t, func() bool {
		var user model.User
		var token model.Token
		return fixture.db.First(&user, fixture.user.Id).Error == nil && fixture.db.First(&token, fixture.token.Id).Error == nil && user.Quota == fixture.user.Quota && token.RemainQuota == fixture.token.RemainQuota && user.UsedQuota == 0 && token.UsedQuota == 0
	}, 3*time.Second, time.Millisecond, "a rejected image must refund the wallet and token without recording consumption")
	var grant model.ToolMarketGrant
	require.NoError(t, fixture.db.First(&grant, "id = ?", in.GrantID).Error)
	require.Zero(t, grant.ReservedCalls)
	require.Zero(t, grant.SuccessfulCalls)
	require.Zero(t, grant.ReservedQuota)
	require.Zero(t, grant.SpentQuota)
	var budgets []model.ToolMarketBudget
	require.NoError(t, fixture.db.Where("user_id = ?", in.UserID).Find(&budgets).Error)
	for _, budget := range budgets {
		require.Zero(t, budget.ReservedQuota)
		require.Zero(t, budget.SpentQuota)
	}
	for _, table := range []any{&model.Log{}, &model.ToolMarketTransfer{}} {
		var count int64
		require.NoError(t, fixture.db.Model(table).Count(&count).Error)
		require.Zero(t, count)
	}
	replay, err := ExecuteToolMarketWithBuiltins(context.Background(), in, requestState, acceptMarketDrawing())
	require.NoError(t, err)
	require.Equal(t, response.Call.ID, replay.Call.ID)
	require.Equal(t, response.Result, replay.Result)
	require.Equal(t, "failed", replay.Call.ExecutionStatus)
	require.Equal(t, "released", replay.Call.SettlementStatus)
	require.EqualValues(t, 1, fixture.upstream.Load(), "settlement and replay must not generate another image")
}

func TestDrawingParityFixtureQueuesGenerationBehindActiveSQLTransaction(t *testing.T) {
	fixture, in, pending := setupMarketDrawingFailureTest(t, json.RawMessage(`{}`))
	sqlDB, err := fixture.db.DB()
	require.NoError(t, err)
	reader := fixture.db.Begin()
	require.NoError(t, reader.Error)
	t.Cleanup(func() { _ = reader.Rollback().Error })
	var grant model.ToolMarketGrant
	require.NoError(t, reader.First(&grant, "id = ?", in.GrantID).Error)
	borrowed, continueQuery := make(chan struct{}), make(chan struct{})
	var firstQuery, resumeQuery sync.Once
	callback := "drawing-sql-fixture-first-query"
	require.NoError(t, fixture.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "tool_market_calls" {
			firstQuery.Do(func() {
				close(borrowed)
				<-continueQuery
			})
		}
	}))
	t.Cleanup(func() { _ = fixture.db.Callback().Query().Remove(callback) })

	type outcome struct {
		response *service.ToolMarketExecutionResponse
		err      error
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan outcome, 1)
	waits := sqlDB.Stats().WaitCount
	go func() {
		response, err := ExecuteToolMarketWithBuiltins(ctx, in, pending.RequestState, acceptMarketDrawing())
		done <- outcome{response, err}
	}()
	var completed *outcome
	t.Cleanup(func() {
		_ = reader.Rollback().Error
		cancel()
		resumeQuery.Do(func() { close(continueQuery) })
		if completed == nil {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("drawing did not stop after releasing its SQL transaction")
			}
		}
	})
	// Keep the first real call lookup at a channel barrier if it borrowed a
	// second connection. Otherwise observe its known SQL-pool admission event.
	// Neither path depends on scheduling the refund and settlement just right.
	borrowedBeforeRelease := false
	require.Eventually(t, func() bool {
		select {
		case <-borrowed:
			borrowedBeforeRelease = true
			return true
		default:
			return sqlDB.Stats().WaitCount > waits
		}
	}, 3*time.Second, time.Millisecond)
	require.False(t, borrowedBeforeRelease, "the drawing fixture must queue work behind its active transaction, instead of letting shared-cache connections compete during refund and settlement")
	require.Zero(t, fixture.upstream.Load(), "queued work must not contact the provider yet")
	require.NoError(t, reader.Commit().Error)
	resumeQuery.Do(func() { close(continueQuery) })
	select {
	case result := <-done:
		completed = &result
	case <-time.After(3 * time.Second):
		t.Fatal("drawing did not resume after its SQL transaction committed")
	}
	require.NoError(t, completed.err)
	assertRejectedDrawingReleasedWithoutCharges(t, fixture, in, pending.RequestState, completed.response)
}

func TestToolMarketDrawingGrantSettlementErrorRemainsPendingUntilRecovery(t *testing.T) {
	fixture, in, pending := setupMarketDrawingFailureTest(t, json.RawMessage(`{}`))
	var attempts atomic.Int32
	fault := errors.New("injected grant settlement storage failure")
	callback := "drawing-grant-settlement-error"
	require.NoError(t, fixture.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*model.ToolMarketGrant); ok {
			attempts.Add(1)
			tx.AddError(fault)
		}
	}))
	t.Cleanup(func() { _ = fixture.db.Callback().Update().Remove(callback) })
	response, err := ExecuteToolMarketWithBuiltins(context.Background(), in, pending.RequestState, acceptMarketDrawing())
	require.NoError(t, err)
	require.EqualValues(t, 1, attempts.Load())
	require.Equal(t, "running", response.Call.ExecutionStatus)
	require.Equal(t, "held", response.Call.SettlementStatus)
	require.Equal(t, "TOOL_MARKET_SETTLEMENT_PENDING", response.ErrorCode)
	var grant model.ToolMarketGrant
	require.NoError(t, fixture.db.First(&grant, "id = ?", in.GrantID).Error)
	require.Equal(t, 1, grant.ReservedCalls, "the failed transaction must retain the original hold")
	require.Zero(t, grant.SuccessfulCalls)
	var result model.ToolMarketResult
	require.NoError(t, fixture.db.First(&result, "call_id = ?", response.Call.ID).Error)
	require.False(t, result.Success, "the provider failure must remain durably recoverable")
	require.False(t, result.BuiltinBillingPending)
	require.NoError(t, fixture.db.Callback().Update().Remove(callback))
	recovered, err := model.RecoverToolMarketCalls(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	status, err := GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, response.Call.ID)
	require.NoError(t, err)
	assertRejectedDrawingReleasedWithoutCharges(t, fixture, in, pending.RequestState, status)
	recovered, err = model.RecoverToolMarketCalls(context.Background())
	require.NoError(t, err)
	require.Zero(t, recovered, "a replayed recovery must not release or credit the call again")
}
