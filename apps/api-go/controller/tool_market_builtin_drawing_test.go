package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketBuiltinDrawingFreeDispatchRetainsModelBillingAndReplay(t *testing.T) {
	// Real, decodable PNGs: one exceeds the review's 768 KiB example; the
	// second also exceeds the former UI's 3 MiB base64 ceiling.
	images := []string{marketDrawingPNG(t, 512), marketDrawingPNG(t, 1024)}
	payload, err := json.Marshal(map[string]any{"created": 1, "data": []any{map[string]any{"b64_json": images[0]}, map[string]any{"b64_json": images[1]}}})
	require.NoError(t, err)
	fixture := newDrawingParityFixture(t, common.GetTrustQuota(), http.StatusOK, payload)
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
	require.Greater(t, len(response.Result), model.ToolMarketResultMaxBytes)
	require.LessOrEqual(t, len(response.Result), model.ToolMarketDrawingResultMaxBytes)
	for _, encoded := range images {
		require.Equal(t, 1, strings.Count(string(response.Result), encoded), "base64 is retained once, not duplicated into JSON text")
	}
	status, err := GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, response.Call.ID)
	require.NoError(t, err)
	require.Equal(t, response.Result, status.Result, "the persistent status path returns the entire generated image")
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

func marketDrawingPNG(t *testing.T, size int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	state := uint32(1)
	for index := 0; index < len(img.Pix); index += 4 {
		for channel := range 3 {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			img.Pix[index+channel] = byte(state)
		}
		img.Pix[index+3] = 255
	}
	var body bytes.Buffer
	require.NoError(t, png.Encode(&body, img))
	return base64.StdEncoding.EncodeToString(body.Bytes())
}

func setupMarketDrawingFailureTest(t *testing.T, payload json.RawMessage) (*drawingParityFixture, model.ToolMarketReserveInput, *mcp.CallToolResult) {
	t.Helper()
	fixture := newDrawingParityFixture(t, common.GetTrustQuota(), http.StatusOK, payload)
	require.NoError(t, fixture.db.AutoMigrate(
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
	previous := toolMarketBuiltinDrawingRelay.handler
	toolMarketBuiltinDrawingRelay.Unlock()
	SetToolMarketBuiltinDrawingRelay(newDrawingMCPRelayEngine(middleware.RelayRequestAdmission()))
	t.Cleanup(func() {
		toolMarketBuiltinDrawingRelay.Lock()
		toolMarketBuiltinDrawingRelay.handler = previous
		toolMarketBuiltinDrawingRelay.Unlock()
	})
	in := model.ToolMarketReserveInput{UserID: fixture.user.Id, ClientID: "drawing-agent", ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID, RequestKey: t.Name(), Arguments: json.RawMessage(`{"prompt":"a durable drawing fixture","group":"image-2","model":"` + drawingParityModel + `","n":2}`)}
	pending, err := ExecuteToolMarketWithBuiltins(context.Background(), in, "", nil)
	require.NoError(t, err)
	require.Equal(t, "awaiting_confirmation", pending.Call.ExecutionStatus)
	var result mcp.CallToolResult
	require.NoError(t, json.Unmarshal(pending.Result, &result))
	require.Zero(t, fixture.upstream.Load())
	return fixture, in, &result
}

func acceptMarketDrawing() mcp.InputResponseMap {
	return mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}}
}

func TestToolMarketBuiltinDrawingPersistenceFailureBeforeBillingRefundsWithoutRetry(t *testing.T) {
	fixture, in, pending := setupMarketDrawingFailureTest(t, json.RawMessage(`{"data":[{"url":"https://example.test/retained.png"}]}`))
	callback := "market-drawing-fail-before-model-billing"
	require.NoError(t, fixture.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*model.ToolMarketResult); ok {
			tx.AddError(errors.New("injected retained-result storage failure"))
		}
	}))
	t.Cleanup(func() { _ = fixture.db.Callback().Create().Remove(callback) })
	response, err := ExecuteToolMarketWithBuiltins(context.Background(), in, pending.RequestState, acceptMarketDrawing())
	require.NoError(t, err)
	require.Equal(t, "unknown", response.Call.ExecutionStatus)
	require.Empty(t, response.Result)
	require.Eventually(t, func() bool {
		var user model.User
		var token model.Token
		return fixture.db.First(&user, fixture.user.Id).Error == nil && fixture.db.First(&token, fixture.token.Id).Error == nil && user.Quota == fixture.user.Quota && token.RemainQuota == fixture.token.RemainQuota
	}, 3*time.Second, 10*time.Millisecond, "the existing asynchronous refund must restore local prepayment")
	var logs int64
	require.NoError(t, fixture.db.Model(&model.Log{}).Count(&logs).Error)
	require.Zero(t, logs, "normal model consumption never runs after retention fails")
	replay, err := ExecuteToolMarketWithBuiltins(context.Background(), in, pending.RequestState, acceptMarketDrawing())
	require.NoError(t, err)
	require.Equal(t, response.Call.ID, replay.Call.ID)
	require.EqualValues(t, 1, fixture.upstream.Load(), "retention failure cannot trigger another provider generation")
}

func TestToolMarketBuiltinDrawingBillingCompletionFailureRetainsImageThroughRecoveryAndExpiry(t *testing.T) {
	imageData := marketDrawingPNG(t, 512)
	payload, err := json.Marshal(map[string]any{"data": []any{map[string]any{"b64_json": imageData}}})
	require.NoError(t, err)
	fixture, in, pending := setupMarketDrawingFailureTest(t, payload)
	callback := "market-drawing-fail-after-model-billing"
	require.NoError(t, fixture.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*model.ToolMarketResult); ok {
			tx.AddError(errors.New("injected completion storage failure"))
		}
	}))
	t.Cleanup(func() { _ = fixture.db.Callback().Update().Remove(callback) })
	response, err := ExecuteToolMarketWithBuiltins(context.Background(), in, pending.RequestState, acceptMarketDrawing())
	require.NoError(t, err)
	require.Equal(t, "unknown", response.Call.ExecutionStatus)
	require.Equal(t, "held", response.Call.SettlementStatus)
	require.Equal(t, "TOOL_MARKET_SETTLEMENT_PENDING", response.ErrorCode)
	require.Contains(t, string(response.Result), imageData)
	var user model.User
	require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
	charge := fixture.user.Quota - user.Quota
	require.Positive(t, charge, "normal model billing remains committed even when its delivery completion write fails")
	recovered, err := model.RecoverToolMarketCalls(context.Background())
	require.NoError(t, err)
	require.Zero(t, recovered, "a billing-pending result cannot be settled by the recovery sweep")
	require.ErrorIs(t, model.FinishToolMarketCall(response.Call.ID, true), model.ErrToolMarketConflict)
	status, err := GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, response.Call.ID)
	require.NoError(t, err)
	require.Equal(t, response.Result, status.Result)
	require.NoError(t, fixture.db.Model(&model.ToolMarketCall{}).Where("id = ?", response.Call.ID).Update("resolve_by", common.GetTimestamp()-1).Error)
	status, err = GetToolMarketExecutionResponseWithBuiltins(in.UserID, in.ClientID, response.Call.ID)
	require.NoError(t, err)
	require.Equal(t, "unknown", status.Call.ExecutionStatus, "expiry cannot call an unfinished model settlement a success")
	require.Equal(t, "released", status.Call.SettlementStatus)
	require.Equal(t, response.Result, status.Result)
	require.NoError(t, fixture.db.Callback().Update().Remove(callback))
	require.NoError(t, model.CompleteToolMarketBuiltinDrawingBilling(response.Call.ID, true), "billing completion can arrive after market deadline release")
	replay, err := ExecuteToolMarketWithBuiltins(context.Background(), in, pending.RequestState, acceptMarketDrawing())
	require.NoError(t, err)
	require.Equal(t, response.Result, replay.Result)
	require.EqualValues(t, 1, fixture.upstream.Load())
	require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
	require.Equal(t, charge, fixture.user.Quota-user.Quota)
}

func TestToolMarketBuiltinDrawingRejectsUnusableProviderResultsBeforeBilling(t *testing.T) {
	var expanded bytes.Buffer
	encoder := json.NewEncoder(&expanded)
	encoder.SetEscapeHTML(false)
	require.NoError(t, encoder.Encode(map[string]any{"data": []any{map[string]any{"url": "https://example.test/image.png"}}, "metadata": strings.Repeat("<", 6<<20)}))
	for name, payload := range map[string]string{"empty": `{}`, "usage-only": `{"data":[],"usage":{"total_tokens":100}}`, "invalid-base64": `{"data":[{"b64_json":"bm90IGFuIGltYWdl"}]}`, "unsupported-url": `{"data":[{"url":"javascript:alert(1)"}]}`, "oversized-retained-envelope": expanded.String()} {
		t.Run(name, func(t *testing.T) {
			fixture, in, pending := setupMarketDrawingFailureTest(t, json.RawMessage(payload))
			response, err := ExecuteToolMarketWithBuiltins(context.Background(), in, pending.RequestState, acceptMarketDrawing())
			require.NoError(t, err)
			require.NotEqual(t, "succeeded", response.Call.ExecutionStatus)
			require.Equal(t, "failed", response.Call.ExecutionStatus)
			var failure mcp.CallToolResult
			require.NoError(t, json.Unmarshal(response.Result, &failure))
			require.True(t, failure.IsError)
			require.EqualValues(t, 1, fixture.upstream.Load())
			require.Eventually(t, func() bool {
				var user model.User
				var token model.Token
				return fixture.db.First(&user, fixture.user.Id).Error == nil && fixture.db.First(&token, fixture.token.Id).Error == nil && user.Quota == fixture.user.Quota && token.RemainQuota == fixture.token.RemainQuota
			}, 3*time.Second, 10*time.Millisecond)
			var logs int64
			require.NoError(t, fixture.db.Model(&model.Log{}).Count(&logs).Error)
			require.Zero(t, logs)
		})
	}
}

func TestToolMarketBuiltinDrawingModelSettlementFailureRetainsImageAndPendingState(t *testing.T) {
	// The actual third image needs a post-generation debit beyond the two-image
	// prepayment. Fail that debit without changing the normal billing/refund code.
	fixture, in, pending := setupMarketDrawingFailureTest(t, json.RawMessage(`{"data":[{"url":"https://example.test/one.png"},{"url":"https://example.test/two.png"},{"url":"https://example.test/three.png"}]}`))
	var prepared atomic.Bool
	createCallback, updateCallback := "market-drawing-stage-created", "market-drawing-fail-model-settlement"
	require.NoError(t, fixture.db.Callback().Create().After("gorm:create").Register(createCallback, func(tx *gorm.DB) {
		if row, ok := tx.Statement.Dest.(*model.ToolMarketResult); ok && row.BuiltinBillingPending {
			prepared.Store(true)
		}
	}))
	require.NoError(t, fixture.db.Callback().Update().Before("gorm:update").Register(updateCallback, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*model.User); ok && prepared.Load() {
			if fields, ok := tx.Statement.Dest.(map[string]any); ok {
				if _, debit := fields["quota"]; debit {
					tx.AddError(errors.New("injected normal model settlement failure"))
				}
			}
		}
	}))
	t.Cleanup(func() {
		_ = fixture.db.Callback().Create().Remove(createCallback)
		_ = fixture.db.Callback().Update().Remove(updateCallback)
	})
	response, err := ExecuteToolMarketWithBuiltins(context.Background(), in, pending.RequestState, acceptMarketDrawing())
	require.NoError(t, err)
	require.True(t, prepared.Load())
	require.Equal(t, "unknown", response.Call.ExecutionStatus)
	require.Equal(t, "held", response.Call.SettlementStatus)
	require.Equal(t, "TOOL_MARKET_SETTLEMENT_PENDING", response.ErrorCode)
	require.Contains(t, string(response.Result), "three.png")
	var stored model.ToolMarketResult
	require.NoError(t, fixture.db.First(&stored, "call_id = ?", response.Call.ID).Error)
	require.True(t, stored.BuiltinBillingPending)
	recovered, err := model.RecoverToolMarketCalls(context.Background())
	require.NoError(t, err)
	require.Zero(t, recovered)
	var logs []model.Log
	require.NoError(t, fixture.db.Find(&logs).Error)
	require.Len(t, logs, 1)
	require.Contains(t, logs[0].Content, "费用结算未完成")
	replay, err := ExecuteToolMarketWithBuiltins(context.Background(), in, pending.RequestState, acceptMarketDrawing())
	require.NoError(t, err)
	require.Equal(t, response.Result, replay.Result)
	require.EqualValues(t, 1, fixture.upstream.Load())
}
