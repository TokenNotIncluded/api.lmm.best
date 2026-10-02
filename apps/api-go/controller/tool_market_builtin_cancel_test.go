package controller

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketBuiltinCancellationAfterSideEffectPreservesUnknownAndReplay(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	tool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.create_draft")
	grant := builtinControllerGrant(t, user.Id, "agent", tool)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callbackName := "builtin-cancel-after-committed-project"
	require.NoError(t, db.Callback().Create().After("gorm:commit_or_rollback_transaction").Register(callbackName, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*model.OpenSourceBountyProject); ok {
			// The real draft is committed independently of the SDK request
			// context. Let cancellation reach its waiting client before the
			// handler can send its final result.
			cancel()
			time.Sleep(75 * time.Millisecond)
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callbackName) })
	in := builtinDraftCreationInput(user.Id, tool, grant.ID, "cancelled-draft")
	response, err := ExecuteToolMarketWithBuiltins(ctx, in, "", nil)
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, "unknown", response.Call.ExecutionStatus, "cancellation cannot prove a completed write failed")
	require.Equal(t, "held", response.Call.SettlementStatus)
	require.Equal(t, "TOOL_MARKET_RESULT_UNKNOWN", response.ErrorCode)
	assertBuiltinDraftCreatedOnce(t, db, user.Id)
	replayed, err := ExecuteToolMarketWithBuiltins(context.Background(), in, "", nil)
	require.NoError(t, err)
	require.Equal(t, response.Call.ID, replayed.Call.ID)
	require.Equal(t, "unknown", replayed.Call.ExecutionStatus)
	require.Equal(t, "TOOL_MARKET_RESULT_UNKNOWN", replayed.ErrorCode)
	assertBuiltinDraftCreatedOnce(t, db, user.Id)
	var current model.ToolMarketGrant
	require.NoError(t, db.First(&current, "id = ?", grant.ID).Error)
	require.Zero(t, current.SuccessfulCalls)
	require.Equal(t, 1, current.ReservedCalls, "uncertain calls retain their original reservation until reconciliation or expiry")
}

func TestToolMarketBuiltinResultPersistenceFailurePreservesSideEffectAndReplay(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	tool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.create_draft")
	grant := builtinControllerGrant(t, user.Id, "agent", tool)
	callbackName := "builtin-fail-final-result-save"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*model.ToolMarketResult); ok {
			tx.AddError(errors.New("injected result persistence failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove(callbackName) })
	in := builtinDraftCreationInput(user.Id, tool, grant.ID, "result-save-failed")
	response, err := ExecuteToolMarketWithBuiltins(context.Background(), in, "", nil)
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, "unknown", response.Call.ExecutionStatus)
	require.Equal(t, "TOOL_MARKET_RESULT_UNKNOWN", response.ErrorCode)
	assertBuiltinDraftCreatedOnce(t, db, user.Id)
	replayed, err := ExecuteToolMarketWithBuiltins(context.Background(), in, "", nil)
	require.NoError(t, err)
	require.Equal(t, response.Call.ID, replayed.Call.ID)
	require.Equal(t, "unknown", replayed.Call.ExecutionStatus)
	require.Equal(t, "TOOL_MARKET_RESULT_UNKNOWN", replayed.ErrorCode)
	assertBuiltinDraftCreatedOnce(t, db, user.Id)
}

func builtinDraftCreationInput(userID int, tool model.ToolMarketToolVersion, grantID, key string) model.ToolMarketReserveInput {
	return model.ToolMarketReserveInput{
		UserID: userID, ClientID: "agent", ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grantID, RequestKey: key,
		Arguments: json.RawMessage(`{"repository_url":"https://github.com/example/cancel-builtin","title":"Fix a real market defect","description":"Provide a concrete reproduction and focused correction","rules":"Supply evidence and relevant validation","reward_quota":333,"reward_slots":3}`),
	}
}

func assertBuiltinDraftCreatedOnce(t *testing.T, db *gorm.DB, userID int) {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&model.OpenSourceBountyProject{}).Where("owner_user_id = ?", userID).Count(&count).Error)
	require.EqualValues(t, 1, count, "the real write happened and the same market request must not repeat it")
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	require.Equal(t, 10_000, user.Quota, "draft creation and its zero-price dispatch do not spend wallet balance")
}
