package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestToolMarketMetaMCPStrictActionInputsAndExactCreditBoundary(t *testing.T) {
	expiry := common.GetTimestamp() + 60
	valid := fmt.Sprintf(`{"action":"authorize","tool_id":"tool","version_id":"version","max_price_quota":9007199254740991,"max_total_quota":9007199254740991,"max_calls":1,"expires_at":%d}`, expiry)
	input, err := decodeToolMarketMetaInput(json.RawMessage(valid))
	require.NoError(t, err)
	require.EqualValues(t, common.MaxWalletQuota, input.MaxTotalQuota)
	for _, raw := range []string{
		`{"action":"status","client_id":"other"}`,
		`{"action":"status","user_id":5}`,
		`{"action":"status","Action":"search"}`,
		`{"action":"status","action":"search"}`,
		`{"action":"status","query":"not-a-status-parameter"}`,
		`{"action":"calls","limit":null}`,
		`{"action":"set_client_budget","limit_quota":-1}`,
		`{"action":"set_client_budget","limit_quota":9007199254740992}`,
		`{"action":"set_client_budget","limit_quota":9007199254740991.1}`,
		`{"action":"set_client_budget","limit_quota":1e1}`,
		`{"action":"set_client_budget"}`,
		`{"action":"set_tool_budget","tool_id":"x","version_id":"v","limit_quota":0}`,
		`{"action":"calls","offset":-1}`,
		`{"action":"calls","limit":101}`,
		`{"action":"calls","limit":0}`,
		`{"action":"authorize","tool_id":"x","version_id":"v"}`,
		`{"action":"load","tool_id":"x"}`,
		`{"action":"status"} {"action":"search"}`,
		`null`,
	} {
		_, err := decodeToolMarketMetaInput(json.RawMessage(raw))
		require.ErrorIs(t, err, model.ErrToolMarketInput, raw)
	}
	input, err = decodeToolMarketMetaInput(json.RawMessage(`{"action":"calls"}`))
	require.NoError(t, err)
	require.Equal(t, 20, input.Limit)
	input, err = decodeToolMarketMetaInput(json.RawMessage(`{"action":"set_client_budget","limit_quota":0}`))
	require.NoError(t, err)
	require.Zero(t, input.LimitQuota)
}

func TestToolMarketMetaOwnerInputRejectsAliasesDuplicatesAndImplicitUnlimited(t *testing.T) {
	for _, raw := range []string{
		`{"enabled":true,"max_total_quota":0}`,
		`{"enabled":false,"max_total_quota":0,"expires_at":0}`,
		`{"enabled":true,"max_total_quota":9007199254740991}`,
	} {
		_, err := decodeToolMarketMetaDelegationInput([]byte(raw))
		require.NoError(t, err, raw)
	}
	for _, raw := range []string{
		`{"enabled":true,"max_total_quota":-1}`,
		`{"enabled":true,"max_total_quota":9007199254740991.1}`,
		`{"enabled":true,"max_total_quota":9007199254740992}`,
		`{"enabled":true,"max_total_quota":null}`,
		`{"enabled":true,"max_total_quota":0,"max_total_quota":100}`,
		`{"Enabled":true,"max_total_quota":0}`,
		`{"enabled":true,"max_total_quota":0,"client_id":"other"}`,
		`{"enabled":true,"max_total_quota":0,"unlimited":true}`,
		`{"enabled":true}`,
		`{"enabled":false,"max_total_quota":100}`,
	} {
		_, err := decodeToolMarketMetaDelegationInput([]byte(raw))
		require.ErrorIs(t, err, model.ErrToolMarketInput, raw)
	}
}

func TestToolMarketMetaDefaultFreeToolExistsForReadOnlyConnections(t *testing.T) {
	db, user, _ := setupOpenSourceBountyMCPControllerTest(t)
	require.NoError(t, db.AutoMigrate(&model.ToolMarketToken{}, &model.ToolMarketEvent{}, &model.ToolMarketCall{}, &model.ToolMarketBudget{}))
	raw, _, err := model.CreateToolMarketToken(user.Id, "read-only-meta", false, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	server := httptest.NewServer(NewToolMarketMCPHandler())
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "meta-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: openSourceBountyBearerTransport{token: raw}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	defer session.Close()
	list, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	metas := 0
	for _, tool := range list.Tools {
		if tool.Name == "metamcp" {
			metas++
			require.NotNil(t, tool.Meta["lmm/pricing"])
		}
	}
	require.Equal(t, 1, metas)
	status, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "metamcp", Arguments: map[string]any{"action": "status"}})
	require.NoError(t, err)
	require.False(t, status.IsError)
	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	var wire struct {
		StructuredContent struct {
			Free bool `json:"free"`
		} `json:"structuredContent"`
	}
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.True(t, wire.StructuredContent.Free)
	require.NotContains(t, string(encoded), raw)
	require.NotContains(t, string(encoded), "credential_id")
	denied, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "metamcp", Arguments: map[string]any{"action": "load", "tool_id": "tool", "version_id": "version"}})
	require.NoError(t, err)
	require.True(t, denied.IsError)
	encoded, err = json.Marshal(denied)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "once in the connection settings")
	var calls int64
	require.NoError(t, db.Model(&model.ToolMarketCall{}).Count(&calls).Error)
	require.Zero(t, calls, "the built-in meta handler never enters the merchant billing path")
}
