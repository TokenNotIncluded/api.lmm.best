package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupToolMarketBuiltinControllerTest(t *testing.T) (*gorm.DB, model.User) {
	t.Helper()
	db, user, _ := setupOpenSourceBountyMCPControllerTest(t)
	require.NoError(t, db.AutoMigrate(&model.ToolMarketService{}, &model.ToolMarketVersion{}, &model.ToolMarketTool{}, &model.ToolMarketToolVersion{}, &model.ToolMarketAccess{}, &model.ToolMarketInstallation{}, &model.ToolMarketGrant{}, &model.ToolMarketEvent{}, &model.ToolMarketToken{}, &model.ToolMarketCall{}, &model.ToolMarketResult{}, &model.ToolMarketConfig{}, &model.ToolMarketBudget{}, &model.ToolMarketTransfer{}, &model.ToolMarketBuiltinContinuation{}))
	require.NoError(t, EnsureToolMarketBuiltinCatalog(context.Background()))
	return db, user
}

func builtinControllerTool(t *testing.T, userID int, key, name string) model.ToolMarketToolVersion {
	t.Helper()
	detail, err := model.GetToolMarketDetail(userID, model.ToolMarketBuiltinServiceID(key), false)
	require.NoError(t, err)
	require.Equal(t, 0, detail.Service.OwnerID)
	require.Equal(t, "builtin", detail.Version.ExecutionType)
	for _, tool := range detail.Tools {
		if tool.Name == name {
			require.Zero(t, tool.PriceQuota)
			return tool
		}
	}
	t.Fatalf("built-in tool %s not found", name)
	return model.ToolMarketToolVersion{}
}

func builtinControllerGrant(t *testing.T, userID int, client string, tool model.ToolMarketToolVersion) *model.ToolMarketGrant {
	t.Helper()
	require.NoError(t, model.SetToolMarketInstallation(userID, client, tool.ToolID, tool.VersionID, true))
	grant, err := model.CreateToolMarketGrant(userID, model.ToolMarketGrant{ClientID: client, ToolID: tool.ToolID, VersionID: tool.VersionID, MaxCalls: 3, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	return grant
}

func TestToolMarketBuiltinBountyConfirmationPreservesFundingAndIdempotency(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	tool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.publish")
	grant := builtinControllerGrant(t, user.Id, "agent", tool)
	project, err := model.CreateOpenSourceBountyDraft(user.Id, model.OpenSourceBountyDraftInput{RepositoryUrl: "https://github.com/example/market-builtin", Title: "Fix a reproducible defect", Description: "A genuine defect with concrete impact", Rules: "Provide reproduction, focused source changes and validation", RewardQuota: 333, RewardSlots: 3})
	require.NoError(t, err)
	in := model.ToolMarketReserveInput{UserID: user.Id, ClientID: "agent", ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID, RequestKey: "publish-once", Arguments: json.RawMessage(fmt.Sprintf(`{"project_id":%d}`, project.Id))}
	first, err := ExecuteToolMarketWithBuiltins(context.Background(), in, "", nil)
	require.NoError(t, err)
	require.Equal(t, "awaiting_confirmation", first.Call.ExecutionStatus)
	require.Equal(t, "held", first.Call.SettlementStatus)
	require.Zero(t, first.Call.PriceQuota)
	var pending mcp.CallToolResult
	require.NoError(t, json.Unmarshal(first.Result, &pending))
	require.True(t, pending.NeedsInput())
	var balance model.User
	require.NoError(t, db.First(&balance, user.Id).Error)
	require.Equal(t, 10000, balance.Quota)
	var current model.ToolMarketGrant
	require.NoError(t, db.First(&current, "id = ?", grant.ID).Error)
	require.Zero(t, current.SuccessfulCalls)
	require.Equal(t, 1, current.ReservedCalls)
	replayed, err := ExecuteToolMarketWithBuiltins(context.Background(), in, "", nil)
	require.NoError(t, err)
	require.JSONEq(t, string(first.Result), string(replayed.Result))
	_, err = ExecuteToolMarketWithBuiltins(context.Background(), in, "other-state", mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}})
	require.Error(t, err)
	confirmed := mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}}
	second, err := ExecuteToolMarketWithBuiltins(context.Background(), in, pending.RequestState, confirmed)
	require.NoError(t, err)
	require.Equal(t, "succeeded", second.Call.ExecutionStatus)
	require.Equal(t, "settled", second.Call.SettlementStatus)
	require.NoError(t, db.First(&balance, user.Id).Error)
	require.Equal(t, 9001, balance.Quota, "free tool execution retains the bounty's real funding debit")
	require.NoError(t, db.First(&current, "id = ?", grant.ID).Error)
	require.Equal(t, 1, current.SuccessfulCalls)
	require.Zero(t, current.ReservedCalls)
	var transfers int64
	require.NoError(t, db.Model(&model.ToolMarketTransfer{}).Count(&transfers).Error)
	require.Zero(t, transfers, "built-in calls must not create market fees or author income")
	third, err := ExecuteToolMarketWithBuiltins(context.Background(), in, pending.RequestState, confirmed)
	require.NoError(t, err)
	require.JSONEq(t, string(second.Result), string(third.Result))
	require.NoError(t, db.First(&balance, user.Id).Error)
	require.Equal(t, 9001, balance.Quota, "confirmed request replay must not fund twice")
}

func TestToolMarketBuiltinRequiresNewExactGrantAndValidArguments(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	tool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.get")
	in := model.ToolMarketReserveInput{UserID: user.Id, ClientID: "agent", ToolID: tool.ToolID, VersionID: tool.VersionID, RequestKey: "read", Arguments: json.RawMessage(`{"project_id":1}`)}
	_, err := ExecuteToolMarketWithBuiltins(context.Background(), in, "", nil)
	require.Error(t, err, "market invoke scope alone is not a tool grant")
	require.NoError(t, model.SetToolMarketInstallation(user.Id, "agent", tool.ToolID, tool.VersionID, true))
	_, err = ExecuteToolMarketWithBuiltins(context.Background(), in, "", nil)
	require.Error(t, err, "loading never authorizes execution")
	grant := builtinControllerGrant(t, user.Id, "agent", tool)
	in.GrantID = grant.ID
	in.Arguments = json.RawMessage(`{"project_id":"not-an-integer"}`)
	_, err = ExecuteToolMarketWithBuiltins(context.Background(), in, "", nil)
	require.ErrorIs(t, err, service.ErrMarketRemoteInput)
	var count int64
	require.NoError(t, db.Model(&model.ToolMarketCall{}).Count(&count).Error)
	require.Zero(t, count, "invalid arguments are rejected before creating a durable request")
}

func TestToolMarketBuiltinMCPListsOnlyGrantedVersionsAndPreservesNativeResults(t *testing.T) {
	_, user := setupToolMarketBuiltinControllerTest(t)
	token, _, err := model.CreateToolMarketToken(user.Id, "market-agent", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	server := httptest.NewServer(NewToolMarketMCPHandler())
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "builtin-test", Version: "1"}, &mcp.ClientOptions{MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}})
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: openSourceBountyBearerTransport{token: token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	defer session.Close()
	list, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, list.Tools, 3)
	tool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.list")
	builtinControllerGrant(t, user.Id, "market-agent", tool)
	list, err = session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, list.Tools, 4)
	name := "market_tool_" + strings.ReplaceAll(tool.ToolID, "-", "")
	var descriptor *mcp.Tool
	for _, item := range list.Tools {
		if item.Name == name {
			descriptor = item
		}
	}
	require.NotNil(t, descriptor)
	require.NotNil(t, descriptor.OutputSchema)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"request_id": "read-board", "arguments": map[string]any{}}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotNil(t, result.StructuredContent)
	require.Contains(t, result.Meta, "lmm/market")
	require.Contains(t, result.StructuredContent.(map[string]any), "message", "native bounty output must not become a billing wrapper")
	require.NotContains(t, result.StructuredContent.(map[string]any), "call")
	// Exercise the public market endpoint's actual protocol continuation, not
	// merely the internal executor. The same client/request must echo state.
	publishTool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.publish")
	builtinControllerGrant(t, user.Id, "market-agent", publishTool)
	project, err := model.CreateOpenSourceBountyDraft(user.Id, model.OpenSourceBountyDraftInput{RepositoryUrl: "https://github.com/example/market-protocol", Title: "Fix protocol confirmation", Description: "A reproducible real protocol defect", Rules: "Provide reproduction and focused tests", RewardQuota: 250, RewardSlots: 2})
	require.NoError(t, err)
	publishName := "market_tool_" + strings.ReplaceAll(publishTool.ToolID, "-", "")
	publishArgs := map[string]any{"request_id": "publish-via-mcp", "arguments": map[string]any{"project_id": project.Id}}
	pending, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: publishName, Arguments: publishArgs})
	require.NoError(t, err)
	require.False(t, pending.IsError)
	require.True(t, pending.NeedsInput())
	require.NotEmpty(t, pending.RequestState)
	require.Contains(t, pending.Meta, "lmm/market")
	completed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: publishName, Arguments: publishArgs, RequestState: pending.RequestState, InputResponses: mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}}})
	require.NoError(t, err)
	require.False(t, completed.IsError)
	require.False(t, completed.NeedsInput())
	require.Contains(t, completed.StructuredContent.(map[string]any), "remaining_quota")
}

func TestToolMarketMCPExecutionOutputRetainsContentAndConfirmation(t *testing.T) {
	native := &mcp.CallToolResult{Meta: mcp.Meta{"provider": "original", "lmm/market": "untrusted"}, Content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/png", Data: []byte("image")}, &mcp.AudioContent{MIMEType: "audio/wav", Data: []byte("audio")}}, StructuredContent: map[string]any{"answer": 42}, RequestState: "state", InputRequests: mcp.InputRequestMap{"confirmation": &mcp.ElicitParams{Mode: "form", Message: "Confirm"}}}
	data, err := json.Marshal(native)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(data, &wire))
	wire["resultType"] = "input_required"
	data, err = json.Marshal(wire)
	require.NoError(t, err)
	response := &service.ToolMarketExecutionResponse{Call: &model.ToolMarketCall{ID: "call", ExecutionStatus: "awaiting_confirmation", SettlementStatus: "held"}, Result: data}
	result, err := marketMCPExecutionOutput(response, nil)
	require.NoError(t, err)
	require.IsType(t, &mcp.ImageContent{}, result.Content[0])
	require.IsType(t, &mcp.AudioContent{}, result.Content[1])
	require.Equal(t, "state", result.RequestState)
	require.True(t, result.NeedsInput())
	require.Equal(t, "original", result.Meta["provider"])
	require.IsType(t, map[string]any{}, result.Meta["lmm/market"])
	require.Equal(t, float64(42), result.StructuredContent.(map[string]any)["answer"])
}
