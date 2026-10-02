package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestToolMarketBuiltinDrawingFreeDispatchRetainsModelBillingAndReplay(t *testing.T) {
	fixture := newDrawingParityFixture(t, common.GetTrustQuota(), http.StatusOK)
	db := fixture.db
	require.NoError(t, db.AutoMigrate(
		&model.ToolMarketService{}, &model.ToolMarketVersion{}, &model.ToolMarketTool{}, &model.ToolMarketToolVersion{},
		&model.ToolMarketAccess{}, &model.ToolMarketInstallation{}, &model.ToolMarketGrant{}, &model.ToolMarketEvent{},
		&model.ToolMarketToken{}, &model.ToolMarketCall{}, &model.ToolMarketResult{}, &model.ToolMarketConfig{},
		&model.ToolMarketBudget{}, &model.ToolMarketTransfer{}, &model.ToolMarketBuiltinContinuation{},
		&model.OpenSourceBountyMCPConfirmation{}, &model.OpenSourceBountyMCPOperation{},
	))
	require.NoError(t, EnsureToolMarketBuiltinCatalog(context.Background()))
	tool := builtinControllerTool(t, fixture.user.Id, "drawing", "drawing.generate")
	grant := builtinControllerGrant(t, fixture.user.Id, "drawing-agent", tool)

	toolMarketBuiltinDrawingRelay.Lock()
	previousRelay := toolMarketBuiltinDrawingRelay.handler
	toolMarketBuiltinDrawingRelay.Unlock()
	SetToolMarketBuiltinDrawingRelay(newDrawingMCPRelayEngine(middleware.RelayRequestAdmission()))
	t.Cleanup(func() {
		toolMarketBuiltinDrawingRelay.Lock()
		toolMarketBuiltinDrawingRelay.handler = previousRelay
		toolMarketBuiltinDrawingRelay.Unlock()
	})
	in := model.ToolMarketReserveInput{
		UserID: fixture.user.Id, ClientID: "drawing-agent", ToolID: tool.ToolID, VersionID: tool.VersionID,
		GrantID: grant.ID, RequestKey: "confirmed-image-once",
		Arguments: json.RawMessage(`{"prompt":"a local market billing fixture","group":"image-2","model":"` + drawingParityModel + `","n":2}`),
	}
	pendingResponse, err := ExecuteToolMarketWithBuiltins(context.Background(), in, "", nil)
	require.NoError(t, err)
	require.Equal(t, "awaiting_confirmation", pendingResponse.Call.ExecutionStatus)
	require.Zero(t, pendingResponse.Call.PriceQuota)
	require.Zero(t, pendingResponse.Call.FeeQuota)
	var pending mcp.CallToolResult
	require.NoError(t, json.Unmarshal(pendingResponse.Result, &pending))
	require.True(t, pending.NeedsInput())
	require.NotEmpty(t, pending.RequestState)
	confirmation, ok := pending.InputRequests["confirmation"].(*mcp.ElicitParams)
	require.True(t, ok)
	require.Contains(t, confirmation.Message, drawingParityModel)
	require.Contains(t, confirmation.Message, "may incur charges")
	require.Zero(t, fixture.upstream.Load(), "requesting confirmation must not invoke the provider")
	var user model.User
	var token model.Token
	require.NoError(t, db.First(&user, fixture.user.Id).Error)
	require.NoError(t, db.First(&token, fixture.token.Id).Error)
	require.Equal(t, fixture.user.Quota, user.Quota)
	require.Equal(t, fixture.token.RemainQuota, token.RemainQuota)
	require.Zero(t, user.UsedQuota)
	require.Zero(t, token.UsedQuota)
	var logCount int64
	require.NoError(t, db.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Count(&logCount).Error)
	require.Zero(t, logCount)

	confirmed := mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}}
	response, err := ExecuteToolMarketWithBuiltins(context.Background(), in, pending.RequestState, confirmed)
	require.NoError(t, err)
	require.Equal(t, "succeeded", response.Call.ExecutionStatus, string(response.Result))
	require.Equal(t, "settled", response.Call.SettlementStatus)
	require.Zero(t, response.Call.PriceQuota)
	require.Zero(t, response.Call.FeeQuota)
	require.EqualValues(t, 1, fixture.upstream.Load())
	require.NoError(t, db.First(&user, fixture.user.Id).Error)
	require.NoError(t, db.First(&token, fixture.token.Id).Error)
	charge := fixture.user.Quota - user.Quota
	require.Positive(t, charge, "free tool dispatch must keep normal image-model charges")
	require.Equal(t, charge, user.UsedQuota)
	require.Equal(t, charge, token.UsedQuota)
	require.Equal(t, charge, fixture.token.RemainQuota-token.RemainQuota)
	var logs []model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
	require.Len(t, logs, 1)
	require.Equal(t, charge, logs[0].Quota)
	require.Equal(t, token.Id, logs[0].TokenId)
	require.Equal(t, fixture.channel.Id, logs[0].ChannelId)
	require.Equal(t, drawingParityModel, logs[0].ModelName)
	require.Equal(t, "image-2", logs[0].Group)
	require.NotContains(t, string(response.Result), token.Key)
	var transfers int64
	require.NoError(t, db.Model(&model.ToolMarketTransfer{}).Count(&transfers).Error)
	require.Zero(t, transfers, "model charges must not turn into marketplace author/platform payments")
	var currentGrant model.ToolMarketGrant
	require.NoError(t, db.First(&currentGrant, "id = ?", grant.ID).Error)
	require.Equal(t, 1, currentGrant.SuccessfulCalls)
	require.Zero(t, currentGrant.ReservedCalls)
	require.Zero(t, currentGrant.SpentQuota)

	replay, err := ExecuteToolMarketWithBuiltins(context.Background(), in, pending.RequestState, confirmed)
	require.NoError(t, err)
	require.Equal(t, response.Call.ID, replay.Call.ID)
	require.JSONEq(t, string(response.Result), string(replay.Result))
	require.EqualValues(t, 1, fixture.upstream.Load(), "the same confirmed market request must not generate another image")
	require.NoError(t, db.First(&user, fixture.user.Id).Error)
	require.NoError(t, db.First(&token, fixture.token.Id).Error)
	require.Equal(t, charge, fixture.user.Quota-user.Quota)
	require.Equal(t, charge, token.UsedQuota)
	require.NoError(t, db.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Count(&logCount).Error)
	require.EqualValues(t, 1, logCount, "confirmed replay must not create another model-consumption log")
}
