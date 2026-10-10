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
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketMCPBridgePreservesExactStructuredNumbersAndMetadata(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"exact"}],"structuredContent":{"counter":9007199254740993,"fraction":0.12345678901234567890123456789},"_meta":{"counter":9007199254740993,"fraction":0.12345678901234567890123456789,"lmm/market":"spoofed"}}`)
	result, err := marketMCPExecutionOutput(&service.ToolMarketExecutionResponse{Call: &model.ToolMarketCall{ID: "call", ExecutionStatus: "succeeded", SettlementStatus: "settled"}, Result: raw}, nil)
	require.NoError(t, err)
	require.Equal(t, json.Number("9007199254740993"), result.Meta["counter"])
	require.Equal(t, json.Number("0.12345678901234567890123456789"), result.Meta["fraction"])
	require.IsType(t, map[string]any{}, result.Meta["lmm/market"])
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	var wire struct {
		StructuredContent json.RawMessage            `json:"structuredContent"`
		Meta              map[string]json.RawMessage `json:"_meta"`
	}
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Equal(t, `{"counter":9007199254740993,"fraction":0.12345678901234567890123456789}`, string(wire.StructuredContent))
	require.Equal(t, "9007199254740993", string(wire.Meta["counter"]))
	require.Equal(t, "0.12345678901234567890123456789", string(wire.Meta["fraction"]))
}

func TestToolMarketMCPBridgePreservesNativeContentMetadataNumbers(t *testing.T) {
	raw := json.RawMessage(`{"content":[
		{"type":"text","text":"exact","_meta":{"counter":9007199254740993,"nested":{"fraction":0.12345678901234567890123456789}}},
		{"type":"image","data":"aW1n","mimeType":"image/png","_meta":{"counter":9007199254740993}},
		{"type":"audio","data":"YXVkaW8=","mimeType":"audio/wav","_meta":{"counter":9007199254740993}},
		{"type":"resource_link","uri":"https://example.test/","name":"target","_meta":{"counter":9007199254740993}},
		{"type":"resource","resource":{"uri":"https://example.test/exact","text":"exact","_meta":{"counter":9007199254740993}},"_meta":{"counter":9007199254740993}}
	]}`)
	result, err := marketMCPExecutionOutput(&service.ToolMarketExecutionResponse{Call: &model.ToolMarketCall{ID: "call", ExecutionStatus: "succeeded", SettlementStatus: "settled"}, Result: raw}, nil)
	require.NoError(t, err)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	var wire struct {
		Content []struct {
			Meta     map[string]json.RawMessage `json:"_meta"`
			Resource *struct {
				Meta map[string]json.RawMessage `json:"_meta"`
			} `json:"resource"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Len(t, wire.Content, 5)
	for _, content := range wire.Content {
		require.Equal(t, "9007199254740993", string(content.Meta["counter"]))
	}
	var nested map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire.Content[0].Meta["nested"], &nested))
	require.Equal(t, "0.12345678901234567890123456789", string(nested["fraction"]))
	require.Equal(t, "9007199254740993", string(wire.Content[4].Resource.Meta["counter"]))
}

func TestToolMarketMCPSchemaWirePreservesExactNumericConstraints(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	entry, err := model.SaveToolMarketDraft(user.Id, "", model.ToolMarketDraftInput{
		Name: "Exact schema remote", ExecutionType: "remote", Visibility: "public", Endpoint: "https://example.com/mcp",
		Tools: []model.ToolMarketToolInput{{Name: "exact", Permissions: []string{"read"},
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"counter":{"type":"integer","minimum":9007199254740993}},"required":["counter"]}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"fraction":{"type":"number","maximum":0.12345678901234567890123456789}}}`),
		}},
	})
	require.NoError(t, err)
	require.NoError(t, model.SubmitToolMarketDraft(user.Id, entry.ID, entry.DraftVersionID))
	require.NoError(t, db.Model(&model.ToolMarketVersion{}).Where("id = ?", entry.DraftVersionID).Update("validation_digest", gorm.Expr("digest")).Error)
	var root model.User
	require.NoError(t, db.Where("role = ?", common.RoleRootUser).First(&root).Error)
	require.NoError(t, model.ReviewToolMarketVersion(root.Id, entry.ID, entry.DraftVersionID, true, "exact wire schema fixture"))
	detail, err := model.GetToolMarketDetail(user.Id, entry.ID, false)
	require.NoError(t, err)
	builtinControllerGrant(t, user.Id, "precise-client", detail.Tools[0])
	token, _, err := model.CreateToolMarketToken(user.Id, "precise-client", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	server := httptest.NewServer(NewToolMarketMCPHandler())
	t.Cleanup(server.Close)
	request, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var wire struct {
		Result struct {
			Tools []struct {
				Name         string          `json:"name"`
				InputSchema  json.RawMessage `json:"inputSchema"`
				OutputSchema json.RawMessage `json:"outputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&wire))
	name := "market_tool_" + strings.ReplaceAll(detail.Tools[0].ToolID, "-", "")
	for _, tool := range wire.Result.Tools {
		if tool.Name == name {
			require.Contains(t, string(tool.InputSchema), `"minimum":9007199254740993`)
			require.Contains(t, string(tool.OutputSchema), `"maximum":0.12345678901234567890123456789`)
			return
		}
	}
	t.Fatal("granted exact-schema tool missing from real MCP tools/list response")
}

func TestToolMarketBuiltinPreservesLargeBountyIDThroughRealHandler(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	project, err := model.CreateOpenSourceBountyDraft(user.Id, model.OpenSourceBountyDraftInput{
		RepositoryUrl: "https://github.com/example/exact-project", Title: "Fix integer precision", Description: "A reproducible integer precision defect", Rules: "Provide focused reproduction and validation", RewardQuota: 100, RewardSlots: 1,
	})
	require.NoError(t, err)
	const exactID = 9007199254740993
	require.NoError(t, db.Model(&model.OpenSourceBountyProject{}).Where("id = ?", project.Id).Update("id", exactID).Error)
	tool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.get")
	grant := builtinControllerGrant(t, user.Id, "precise-builtin", tool)
	response, err := ExecuteToolMarketWithBuiltins(context.Background(), model.ToolMarketReserveInput{
		UserID: user.Id, ClientID: "precise-builtin", ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID,
		RequestKey: "exact-project", Arguments: json.RawMessage(`{"project_id":9007199254740993}`),
	}, "", nil)
	require.NoError(t, err)
	require.Equal(t, "succeeded", response.Call.ExecutionStatus)
	var result struct {
		StructuredContent json.RawMessage `json:"structuredContent"`
	}
	require.NoError(t, json.Unmarshal(response.Result, &result))
	require.Contains(t, string(result.StructuredContent), `"id":9007199254740993`, "the real typed handler must receive and return the original ID")
	bridged, err := marketMCPExecutionOutput(response, nil)
	require.NoError(t, err)
	encoded, err := json.Marshal(bridged)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"id":9007199254740993`, "the stored result and final MCP response must retain the exact ID")
}

func TestToolMarketBuiltinRejectsLargeFractionalIntegerBeforeReservation(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	tool := builtinControllerTool(t, user.Id, "wallet", "wallet.transfer.create")
	grant := builtinControllerGrant(t, user.Id, "precise-builtin", tool)
	_, err := ExecuteToolMarketWithBuiltins(context.Background(), model.ToolMarketReserveInput{
		UserID: user.Id, ClientID: "precise-builtin", ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID,
		RequestKey: "fractional-quota", Arguments: json.RawMessage(`{"quota":9007199254740992.1}`),
	}, "", nil)
	require.ErrorIs(t, err, service.ErrMarketRemoteInput)
	var count int64
	require.NoError(t, db.Model(&model.ToolMarketCall{}).Count(&count).Error)
	require.Zero(t, count, "a fraction must not round into an allowed integer and create a pending call")
}
