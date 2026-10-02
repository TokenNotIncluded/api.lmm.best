package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketBuiltinRESTWalletContinuationRetainsOriginalGrant(t *testing.T) {
	db, user, tool := setupBuiltinWalletGrantContinuation(t)
	original := builtinControllerGrant(t, user.Id, model.ToolMarketWebClient, tool)
	engine := gin.New()
	// Exercise the real HTTP body and controller; session authentication's
	// resulting account identity is supplied by this isolated test middleware.
	engine.Use(func(c *gin.Context) { c.Set("id", user.Id); c.Next() })
	engine.POST("/api/tool-market/invoke", InvokeToolMarketWithBuiltins)
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	body := map[string]any{
		"tool_id": tool.ToolID, "version_id": tool.VersionID, "request_id": "rest-pending-wallet",
		"arguments": map[string]any{"quota": 300},
	}
	status, first := postBuiltinGrantREST(t, server, body)
	require.Equal(t, http.StatusOK, status)
	require.True(t, first.Success)
	require.NotNil(t, first.Data)
	require.Equal(t, "awaiting_confirmation", first.Data.Call.ExecutionStatus)
	require.Equal(t, original.ID, first.Data.Call.GrantID)
	var pending mcp.CallToolResult
	require.NoError(t, json.Unmarshal(first.Data.Result, &pending))
	require.True(t, pending.NeedsInput())
	require.NotEmpty(t, pending.RequestState)
	assertBuiltinGrantWalletState(t, db, user.Id, 0, 10_000)

	later := laterBuiltinWalletGrant(t, db, user.Id, model.ToolMarketWebClient, tool)
	body["request_state"] = pending.RequestState
	body["input_responses"] = acceptedBuiltinWalletGrantConfirmation()
	body["grant_id"] = later.ID
	status, rejected := postBuiltinGrantREST(t, server, body)
	require.Equal(t, http.StatusConflict, status, "an explicit different grant cannot rebind the same durable request")
	require.False(t, rejected.Success)
	require.Equal(t, "TOOL_MARKET_CONFLICT", rejected.Code)
	var stillPending model.ToolMarketCall
	require.NoError(t, db.First(&stillPending, "id = ?", first.Data.Call.ID).Error)
	require.Equal(t, "awaiting_confirmation", stillPending.ExecutionStatus)
	require.Equal(t, original.ID, stillPending.GrantID)
	assertBuiltinGrantWalletState(t, db, user.Id, 0, 10_000)

	delete(body, "grant_id")
	status, completed := postBuiltinGrantREST(t, server, body)
	require.Equal(t, http.StatusOK, status)
	require.True(t, completed.Success)
	require.NotNil(t, completed.Data)
	require.Equal(t, "succeeded", completed.Data.Call.ExecutionStatus)
	require.Equal(t, first.Data.Call.ID, completed.Data.Call.ID)
	require.Equal(t, original.ID, completed.Data.Call.GrantID, "omitted grant must retain the original confirmation's authorization")
	assertBuiltinGrantWalletState(t, db, user.Id, 1, 9_700)
	assertBuiltinWalletGrantAccounting(t, db, original.ID, later.ID)
	status, replay := postBuiltinGrantREST(t, server, body)
	require.Equal(t, http.StatusOK, status)
	require.True(t, replay.Success)
	require.Equal(t, completed.Data.Call.ID, replay.Data.Call.ID)
	require.JSONEq(t, string(completed.Data.Result), string(replay.Data.Result))
	assertBuiltinGrantWalletState(t, db, user.Id, 1, 9_700)
	delete(body, "request_state")
	delete(body, "input_responses")
	body["request_id"] = "rest-new-wallet-request"
	status, fresh := postBuiltinGrantREST(t, server, body)
	require.Equal(t, http.StatusOK, status)
	require.True(t, fresh.Success)
	require.Equal(t, "awaiting_confirmation", fresh.Data.Call.ExecutionStatus)
	require.Equal(t, later.ID, fresh.Data.Call.GrantID, "only new requests select the current default grant")
	assertBuiltinGrantWalletState(t, db, user.Id, 1, 9_700)
}

func TestToolMarketBuiltinMCPWalletContinuationRetainsOriginalGrant(t *testing.T) {
	db, user, tool := setupBuiltinWalletGrantContinuation(t)
	const clientID = "wallet-grant-agent"
	original := builtinControllerGrant(t, user.Id, clientID, tool)
	token, _, err := model.CreateToolMarketToken(user.Id, clientID, true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	server := httptest.NewServer(NewToolMarketMCPHandler())
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "wallet-grant-continuation-test", Version: "1"}, &mcp.ClientOptions{MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}})
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: server.URL, HTTPClient: &http.Client{Transport: openSourceBountyBearerTransport{token: token}}, DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	name := "market_tool_" + strings.ReplaceAll(tool.ToolID, "-", "")
	args := map[string]any{"request_id": "mcp-pending-wallet", "arguments": map[string]any{"quota": 300}}
	pending, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, pending.IsError)
	require.True(t, pending.NeedsInput())
	require.NotEmpty(t, pending.RequestState)
	first := builtinGrantCallFromMCP(t, pending)
	require.Equal(t, original.ID, first.GrantID)
	require.Equal(t, "awaiting_confirmation", first.ExecutionStatus)
	assertBuiltinGrantWalletState(t, db, user.Id, 0, 10_000)

	later := laterBuiltinWalletGrant(t, db, user.Id, clientID, tool)
	// Every HTTP MCP request constructs the current installed tool set. The
	// newly selected descriptor must not substitute its grant for this call.
	params := &mcp.CallToolParams{Name: name, Arguments: args, RequestState: pending.RequestState, InputResponses: acceptedBuiltinWalletGrantConfirmation()}
	completed, err := session.CallTool(context.Background(), params)
	require.NoError(t, err)
	require.False(t, completed.IsError)
	require.False(t, completed.NeedsInput())
	call := builtinGrantCallFromMCP(t, completed)
	require.Equal(t, first.ID, call.ID)
	require.Equal(t, "succeeded", call.ExecutionStatus)
	require.Equal(t, "settled", call.SettlementStatus)
	require.Equal(t, original.ID, call.GrantID)
	assertBuiltinGrantWalletState(t, db, user.Id, 1, 9_700)
	assertBuiltinWalletGrantAccounting(t, db, original.ID, later.ID)
	replay, err := session.CallTool(context.Background(), params)
	require.NoError(t, err)
	require.False(t, replay.IsError)
	require.Equal(t, call.ID, builtinGrantCallFromMCP(t, replay).ID)
	require.Equal(t, completed.StructuredContent, replay.StructuredContent)
	assertBuiltinGrantWalletState(t, db, user.Id, 1, 9_700)
	args["request_id"] = "mcp-new-wallet-request"
	fresh, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, fresh.IsError)
	require.True(t, fresh.NeedsInput())
	require.Equal(t, later.ID, builtinGrantCallFromMCP(t, fresh).GrantID, "only new requests select the current default grant")
	assertBuiltinGrantWalletState(t, db, user.Id, 1, 9_700)
}

func setupBuiltinWalletGrantContinuation(t *testing.T) (*gorm.DB, model.User, model.ToolMarketToolVersion) {
	t.Helper()
	db, user := setupToolMarketBuiltinControllerTest(t)
	require.NoError(t, db.AutoMigrate(&model.WalletTransfer{}))
	previousOrigin := system_setting.ServerAddress
	system_setting.ServerAddress = "https://console.example.test/"
	t.Cleanup(func() { system_setting.ServerAddress = previousOrigin })
	return db, user, builtinControllerTool(t, user.Id, "wallet", "wallet.transfer.create")
}

func laterBuiltinWalletGrant(t *testing.T, db *gorm.DB, userID int, clientID string, tool model.ToolMarketToolVersion) *model.ToolMarketGrant {
	t.Helper()
	grant := builtinControllerGrant(t, userID, clientID, tool)
	require.NoError(t, db.Model(&model.ToolMarketGrant{}).Where("id = ?", grant.ID).Update("created_at", common.GetTimestamp()+1).Error)
	current, err := model.GetToolMarketExecution(userID, clientID, tool.ToolID, tool.VersionID, "")
	require.NoError(t, err)
	require.Equal(t, grant.ID, current.Grant.ID, "the test must reproduce a genuinely different current grant")
	return grant
}

func acceptedBuiltinWalletGrantConfirmation() mcp.InputResponseMap {
	return mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}}
}

type builtinGrantRESTResponse struct {
	Success bool                                 `json:"success"`
	Code    string                               `json:"code"`
	Data    *service.ToolMarketExecutionResponse `json:"data"`
}

func postBuiltinGrantREST(t *testing.T, server *httptest.Server, body any) (int, builtinGrantRESTResponse) {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	response, err := server.Client().Post(server.URL+"/api/tool-market/invoke", "application/json", bytes.NewReader(data))
	require.NoError(t, err)
	defer response.Body.Close()
	var result builtinGrantRESTResponse
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	return response.StatusCode, result
}

func builtinGrantCallFromMCP(t *testing.T, result *mcp.CallToolResult) model.ToolMarketCall {
	t.Helper()
	metadata, ok := result.Meta["lmm/market"].(map[string]any)
	require.True(t, ok)
	data, err := json.Marshal(metadata["call"])
	require.NoError(t, err)
	var call model.ToolMarketCall
	require.NoError(t, json.Unmarshal(data, &call))
	require.NotEmpty(t, call.ID)
	return call
}

func assertBuiltinGrantWalletState(t *testing.T, db *gorm.DB, userID int, count, quota int) {
	t.Helper()
	var transfers int64
	require.NoError(t, db.Model(&model.WalletTransfer{}).Where("sender_id = ?", userID).Count(&transfers).Error)
	require.EqualValues(t, count, transfers)
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	require.Equal(t, quota, user.Quota)
}

func assertBuiltinWalletGrantAccounting(t *testing.T, db *gorm.DB, originalID, laterID string) {
	t.Helper()
	var original, later model.ToolMarketGrant
	require.NoError(t, db.First(&original, "id = ?", originalID).Error)
	require.NoError(t, db.First(&later, "id = ?", laterID).Error)
	require.Equal(t, 1, original.SuccessfulCalls)
	require.Zero(t, original.ReservedCalls)
	require.Zero(t, original.SpentQuota)
	require.Zero(t, later.SuccessfulCalls)
	require.Zero(t, later.ReservedCalls)
}
