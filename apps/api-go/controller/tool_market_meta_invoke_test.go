package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// Each client discovers tools once, before the test changes any authorization.
// All later invocation and confirmation requests must work without list refresh.
func toolMarketMetaInvokeClient(t *testing.T, userID int, clientID string, invoke bool) func(*mcp.CallToolParams) *mcp.CallToolResult {
	t.Helper()
	token, _, err := model.CreateToolMarketToken(userID, clientID, invoke, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	server := httptest.NewServer(NewToolMarketMCPHandler())
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "meta-invoke-test", Version: "1"}, &mcp.ClientOptions{MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}})
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: openSourceBountyBearerTransport{token: token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	list, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	var meta *mcp.Tool
	for _, tool := range list.Tools {
		if tool.Name == "metamcp" {
			meta = tool
		}
	}
	require.NotNil(t, meta)
	schema, err := json.Marshal(meta.InputSchema)
	require.NoError(t, err)
	require.Contains(t, string(schema), `"invoke"`)
	require.Contains(t, meta.Description, "target tool's pricing")
	return func(params *mcp.CallToolParams) *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(context.Background(), params)
		require.NoError(t, err)
		require.NotNil(t, result)
		return result
	}
}

func toolMarketMetaInvokeParams(tool model.ToolMarketToolVersion, requestID string, arguments map[string]any) *mcp.CallToolParams {
	return &mcp.CallToolParams{Name: "metamcp", Arguments: map[string]any{
		"action": "invoke", "tool_id": tool.ToolID, "version_id": tool.VersionID,
		"request_id": requestID, "arguments": arguments,
	}}
}

func TestToolMarketMetaInvokeRejectsReadOnlyConnectionBeforeExecution(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	call := toolMarketMetaInvokeClient(t, user.Id, "meta-read-only", false)
	tool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.list")
	builtinControllerGrant(t, user.Id, "meta-read-only", tool)
	result := call(toolMarketMetaInvokeParams(tool, "denied", map[string]any{}))
	require.True(t, result.IsError, "an owner grant does not widen the token's invoke permission")
	status := call(&mcp.CallToolParams{Name: "metamcp", Arguments: map[string]any{"action": "status"}})
	require.False(t, status.IsError)
	view := status.StructuredContent.(map[string]any)
	require.Equal(t, true, view["free"])
	require.Equal(t, true, view["invoke_supported"])
	require.Equal(t, true, view["invoke_uses_target_pricing"])
	require.Equal(t, false, view["invoke_requires_tools_list_refresh"])
	require.Equal(t, false, view["can_invoke"])
	var calls int64
	require.NoError(t, db.Model(&model.ToolMarketCall{}).Count(&calls).Error)
	require.Zero(t, calls)
}

func TestToolMarketMetaInvokeUsesExactGrantsAndSharedRequestIdentity(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	const clientID = "meta-invoke-agent"
	call := toolMarketMetaInvokeClient(t, user.Id, clientID, true)
	tool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.list")
	params := toolMarketMetaInvokeParams(tool, "read-once", map[string]any{})
	require.True(t, call(params).IsError, "invoke must not create an installation or grant")
	var installations, grants int64
	require.NoError(t, db.Model(&model.ToolMarketInstallation{}).Count(&installations).Error)
	require.NoError(t, db.Model(&model.ToolMarketGrant{}).Count(&grants).Error)
	require.Zero(t, installations)
	require.Zero(t, grants)
	require.NoError(t, model.SetToolMarketInstallation(user.Id, clientID, tool.ToolID, tool.VersionID, true))
	require.True(t, call(params).IsError, "loading alone is not authorization")
	require.NoError(t, db.Model(&model.ToolMarketGrant{}).Count(&grants).Error)
	require.Zero(t, grants)
	grant, err := model.CreateToolMarketGrant(user.Id, model.ToolMarketGrant{ClientID: clientID, ToolID: tool.ToolID, VersionID: tool.VersionID, MaxCalls: 3, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	first := call(params)
	require.False(t, first.IsError, "an invoke-only token may use an owner grant without AI management delegation")
	require.Contains(t, first.Meta, "lmm/market")
	require.Contains(t, first.StructuredContent.(map[string]any), "message")
	require.NotContains(t, first.StructuredContent.(map[string]any), "call", "return native content, not a billing wrapper")

	replay := call(&mcp.CallToolParams{Name: "market_tool_" + strings.ReplaceAll(tool.ToolID, "-", ""), Arguments: map[string]any{"request_id": "read-once", "arguments": map[string]any{}}})
	require.False(t, replay.IsError)
	firstData, err := json.Marshal(first.StructuredContent)
	require.NoError(t, err)
	replayData, err := json.Marshal(replay.StructuredContent)
	require.NoError(t, err)
	require.JSONEq(t, string(firstData), string(replayData))
	require.True(t, call(toolMarketMetaInvokeParams(tool, "read-once", map[string]any{"changed": true})).IsError, "request identity cannot be reused for different arguments")

	otherClient := toolMarketMetaInvokeClient(t, user.Id, "meta-other-client", true)
	require.True(t, otherClient(params).IsError, "another client cannot use or replay this grant")
	other := model.User{Username: "meta-other-account", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: 10000}
	require.NoError(t, db.Create(&other).Error)
	otherUser := toolMarketMetaInvokeClient(t, other.Id, clientID, true)
	require.True(t, otherUser(params).IsError, "the same client name does not cross account boundaries")

	wrongVersion := tool
	wrongVersion.VersionID = "unapproved-version"
	require.True(t, call(toolMarketMetaInvokeParams(wrongVersion, "wrong-version", map[string]any{})).IsError)
	require.NoError(t, model.SetToolMarketInstallation(user.Id, clientID, tool.ToolID, tool.VersionID, false))
	require.True(t, call(toolMarketMetaInvokeParams(tool, "unloaded", map[string]any{})).IsError)
	require.NoError(t, model.SetToolMarketInstallation(user.Id, clientID, tool.ToolID, tool.VersionID, true))
	require.NoError(t, db.Model(grant).Update("revoked_at", common.GetTimestamp()).Error)
	require.True(t, call(toolMarketMetaInvokeParams(tool, "revoked", map[string]any{})).IsError)
	// Restore the fixture before each independent denial, so an exhausted or
	// revoked grant cannot mask a missing expiry or call-limit check.
	require.NoError(t, db.Model(grant).Update("revoked_at", 0).Error)
	require.NoError(t, db.Model(grant).Update("expires_at", common.GetTimestamp()-1).Error)
	require.True(t, call(toolMarketMetaInvokeParams(tool, "expired", map[string]any{})).IsError)
	require.NoError(t, db.Model(grant).Updates(map[string]any{"expires_at": common.GetTimestamp() + 3600, "max_calls": 1}).Error)
	require.True(t, call(toolMarketMetaInvokeParams(tool, "over-call-limit", map[string]any{})).IsError)
	var current model.ToolMarketGrant
	require.NoError(t, db.First(&current, "id = ?", grant.ID).Error)
	require.Equal(t, 1, current.SuccessfulCalls)
	require.Zero(t, current.ReservedCalls)
	var calls int64
	require.NoError(t, db.Model(&model.ToolMarketCall{}).Count(&calls).Error)
	require.EqualValues(t, 1, calls, "both routes share one durable business request")
}

func TestToolMarketMetaInvokeValidatesTargetSchemaBeforeReservation(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	call := toolMarketMetaInvokeClient(t, user.Id, "meta-invalid-schema", true)
	tool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.get")
	builtinControllerGrant(t, user.Id, "meta-invalid-schema", tool)
	result := call(toolMarketMetaInvokeParams(tool, "invalid-project", map[string]any{"project_id": "not-an-integer"}))
	require.True(t, result.IsError)
	var calls int64
	require.NoError(t, db.Model(&model.ToolMarketCall{}).Count(&calls).Error)
	require.Zero(t, calls)
}

func TestToolMarketMetaInvokePreservesConfirmationAndFundingAcrossRoutes(t *testing.T) {
	for _, route := range []string{"metamcp", "market_tool"} {
		t.Run(route, func(t *testing.T) {
			db, user := setupToolMarketBuiltinControllerTest(t)
			const clientID = "meta-confirmation-agent"
			call := toolMarketMetaInvokeClient(t, user.Id, clientID, true)
			tool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.publish")
			grant := builtinControllerGrant(t, user.Id, clientID, tool)
			project, err := model.CreateOpenSourceBountyDraft(user.Id, model.OpenSourceBountyDraftInput{RepositoryUrl: "https://github.com/example/meta-confirmation", Title: "Fix a reproducible defect", Description: "A concrete defect with a focused reproduction", Rules: "Provide a source fix and regression tests", RewardQuota: 250, RewardSlots: 2})
			require.NoError(t, err)
			params := toolMarketMetaInvokeParams(tool, "publish-once", map[string]any{"project_id": project.Id})
			pending := call(params)
			require.False(t, pending.IsError)
			require.True(t, pending.NeedsInput())
			require.NotEmpty(t, pending.RequestState)
			require.Contains(t, pending.Meta, "lmm/market")
			replayed := call(params)
			require.True(t, replayed.NeedsInput())
			require.Equal(t, pending.RequestState, replayed.RequestState)
			var balance model.User
			require.NoError(t, db.First(&balance, user.Id).Error)
			require.Equal(t, 10000, balance.Quota, "invoking a confirmation tool must not fund it")

			confirmed := mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}}
			wrongState := *params
			wrongState.RequestState, wrongState.InputResponses = "wrong-state", confirmed
			require.True(t, call(&wrongState).IsError)
			wrongRequest := toolMarketMetaInvokeParams(tool, "different-request", map[string]any{"project_id": project.Id})
			wrongRequest.RequestState, wrongRequest.InputResponses = pending.RequestState, confirmed
			require.True(t, call(wrongRequest).IsError)
			wrongArguments := toolMarketMetaInvokeParams(tool, "publish-once", map[string]any{"project_id": project.Id + 1})
			wrongArguments.RequestState, wrongArguments.InputResponses = pending.RequestState, confirmed
			require.True(t, call(wrongArguments).IsError)

			native := &mcp.CallToolParams{Name: "market_tool_" + strings.ReplaceAll(tool.ToolID, "-", ""), Arguments: map[string]any{"request_id": "publish-once", "arguments": map[string]any{"project_id": project.Id}}, RequestState: pending.RequestState, InputResponses: confirmed}
			params.RequestState, params.InputResponses = pending.RequestState, confirmed
			completion, retry := native, params
			if route == "metamcp" {
				completion, retry = params, native
			}
			completed := call(completion)
			require.False(t, completed.IsError)
			require.False(t, completed.NeedsInput())
			require.Contains(t, completed.StructuredContent.(map[string]any), "remaining_quota")
			finalReplay := call(retry)
			require.False(t, finalReplay.IsError)
			require.False(t, finalReplay.NeedsInput())
			require.NoError(t, db.First(&balance, user.Id).Error)
			require.Equal(t, 9500, balance.Quota, "cross-route continuation and replay must fund exactly once")
			var current model.ToolMarketGrant
			require.NoError(t, db.First(&current, "id = ?", grant.ID).Error)
			require.Equal(t, 1, current.SuccessfulCalls)
			require.Zero(t, current.ReservedCalls)
			var calls, transfers int64
			require.NoError(t, db.Model(&model.ToolMarketCall{}).Count(&calls).Error)
			require.EqualValues(t, 1, calls)
			require.NoError(t, db.Model(&model.ToolMarketTransfer{}).Count(&transfers).Error)
			require.Zero(t, transfers, "the meta entry adds no marketplace charge to a built-in operation")
		})
	}
}

func TestToolMarketMetaManagementRejectsInvocationContinuation(t *testing.T) {
	_, user := setupToolMarketBuiltinControllerTest(t)
	call := toolMarketMetaInvokeClient(t, user.Id, "meta-management-state", true)
	result := call(&mcp.CallToolParams{Name: "metamcp", Arguments: map[string]any{"action": "status"}, RequestState: "not-for-management", InputResponses: mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}}})
	require.True(t, result.IsError, "management must not silently consume a tool continuation")
}
