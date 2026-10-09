package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketMetaSearchReturnsSummariesWithoutDetailQueries(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	properties := map[string]any{}
	for i := range 30 {
		properties[fmt.Sprintf("field_%d", i)] = map[string]any{"type": "string", "description": strings.Repeat("schema-only-marker ", 16)}
	}
	schema, err := json.Marshal(map[string]any{"type": "object", "properties": properties})
	require.NoError(t, err)
	service, err := model.SaveToolMarketDraft(user.Id, "", model.ToolMarketDraftInput{
		Name: "Meta summary fixture", Description: strings.Repeat("用途", 200), ExecutionType: "remote", Visibility: "public", Endpoint: "https://example.com/mcp",
		Tools: []model.ToolMarketToolInput{{Name: "lookup", InputSchema: schema, Permissions: []string{"read"}, PriceQuota: 123}},
	})
	require.NoError(t, err)
	require.NoError(t, model.SubmitToolMarketDraft(user.Id, service.ID, service.DraftVersionID))
	require.NoError(t, db.Model(&model.ToolMarketVersion{}).Where("id = ?", service.DraftVersionID).Update("validation_digest", gorm.Expr("digest")).Error)
	var root model.User
	require.NoError(t, db.Where("role = ?", common.RoleRootUser).First(&root).Error)
	require.NoError(t, model.ReviewToolMarketVersion(root.Id, service.ID, service.DraftVersionID, true, "fixture checked"))
	identity := marketMCPIdentity{userID: user.Id}
	input, err := decodeToolMarketMetaInput(json.RawMessage(`{"action":"search","query":"Meta summary fixture"}`))
	require.NoError(t, err)
	queryCount, rowCount := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("meta_discovery_queries", func(*gorm.DB) { queryCount++ }))
	require.NoError(t, db.Callback().Row().Before("gorm:row").Register("meta_discovery_rows", func(*gorm.DB) { rowCount++ }))
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove("meta_discovery_queries")
		_ = db.Callback().Row().Remove("meta_discovery_rows")
	})
	value, err := toolMarketMetaSearch(context.Background(), identity, input)
	require.NoError(t, err)
	require.Zero(t, queryCount, "search must not load service, version or tool details")
	require.Equal(t, 1, rowCount, "search must use one catalog statement, not one query per result")
	view := value.(map[string]any)
	items := view["items"].([]toolMarketMetaSearchItem)
	require.Len(t, items, 1)
	require.Equal(t, service.ID, items[0].ID)
	require.Equal(t, service.DraftVersionID, items[0].VersionID)
	require.Equal(t, "Meta summary fixture", items[0].Name)
	require.Equal(t, 240, utf8.RuneCountInString(items[0].Description))
	require.True(t, utf8.ValidString(items[0].Description))
	require.True(t, items[0].DescriptionTruncated)
	require.Equal(t, 123, items[0].MinPriceQuota)
	require.Equal(t, 123, items[0].MaxPriceQuota)
	require.Equal(t, 1, items[0].ToolCount)
	require.Equal(t, 5, view["limit"])
	require.Equal(t, "details", view["details_action"])
	result, err := marketMCPOutput(value, nil)
	require.NoError(t, err)
	compactJSON, err := json.Marshal(result)
	require.NoError(t, err)
	for _, excluded := range []string{"input_schema", "output_schema", "schema-only-marker", "owner_id", "endpoint"} {
		require.NotContains(t, string(compactJSON), excluded)
	}
	// The existing details action still returns the exact, complete schema.
	detailValue, err := executeToolMarketMeta(context.Background(), identity, &toolMarketMetaInput{Action: "details", ServiceID: service.ID})
	require.NoError(t, err)
	detail := detailValue.(*model.ToolMarketDetail)
	require.JSONEq(t, string(schema), detail.Tools[0].InputSchema)
	require.Equal(t, strings.Repeat("用途", 200), detail.Version.Description)
	rows, err := model.ListToolMarket(user.Id, input.Query, "", 0, 5)
	require.NoError(t, err)
	legacyItem := struct {
		model.ToolMarketListItem
		Tools []model.ToolMarketToolVersion `json:"tools"`
	}{rows[0], detail.Tools}
	legacy, err := marketMCPOutput(map[string]any{"items": []any{legacyItem}, "offset": 0, "limit": 5, "credits_per_usd": 500000}, nil)
	require.NoError(t, err)
	legacyJSON, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.Less(t, len(compactJSON), len(legacyJSON)/3, "large tool schemas must not grow discovery responses")
	t.Logf("search fixture wire bytes, including text and structured copies: %d -> %d", len(legacyJSON), len(compactJSON))

	// A zero base price must not erase the fact that a tool is metered.
	require.NoError(t, db.Model(&model.ToolMarketToolVersion{}).Where("version_id = ?", service.DraftVersionID).Updates(map[string]any{"price_quota": 0, "billing_mode": "input_tokens", "input_token_price_quota": 100, "max_input_tokens": 10}).Error)
	value, err = toolMarketMetaSearch(context.Background(), identity, input)
	require.NoError(t, err)
	items = value.(map[string]any)["items"].([]toolMarketMetaSearchItem)
	require.Zero(t, items[0].MinPriceQuota)
	require.Equal(t, 1, items[0].MeteredTools)
	require.Contains(t, value.(map[string]any)["pricing_note"], "metered")
	input.Offset = 1
	value, err = toolMarketMetaSearch(context.Background(), identity, input)
	require.NoError(t, err)
	emptyJSON, err := json.Marshal(value)
	require.NoError(t, err)
	require.Contains(t, string(emptyJSON), `"items":[]`)
	input.Offset = 0
	require.NoError(t, db.Model(&model.ToolMarketVersion{}).Where("id = ?", service.DraftVersionID).Update("visibility", "private").Error)
	value, err = toolMarketMetaSearch(context.Background(), marketMCPIdentity{userID: user.Id + 1000}, input)
	require.NoError(t, err)
	require.Empty(t, value.(map[string]any)["items"])
}

func toolMarketMetaModeSession(t *testing.T, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "meta-mode-test", Version: "1"}, &mcp.ClientOptions{MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}})
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: openSourceBountyBearerTransport{token: token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestToolMarketMetaCompactModeKeepsExecutionAndFullModeCompatibility(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	const clientID = "meta-mode-agent"
	token, _, err := model.CreateToolMarketToken(user.Id, clientID, true, true, common.GetTimestamp()+3600)
	require.NoError(t, err)
	tool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.list")
	builtinControllerGrant(t, user.Id, clientID, tool)
	server := httptest.NewServer(NewToolMarketMCPHandler())
	defer server.Close()
	for _, mode := range []string{"", "full", "compact"} {
		t.Run("mode="+mode, func(t *testing.T) {
			session := toolMarketMetaModeSession(t, server.URL+"?mode="+mode, token)
			list, err := session.ListTools(context.Background(), nil)
			require.NoError(t, err)
			names := make([]string, 0, len(list.Tools))
			for _, tool := range list.Tools {
				names = append(names, tool.Name)
			}
			if mode == "compact" {
				require.Equal(t, []string{"metamcp"}, names)
			} else {
				require.ElementsMatch(t, []string{"metamcp", "lmm_market_search", "lmm_market_details", "lmm_market_load", "lmm_market_call_status", "market_tool_" + strings.ReplaceAll(tool.ToolID, "-", "")}, names)
			}
			loaded, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "metamcp", Arguments: map[string]any{"action": "load", "tool_id": tool.ToolID, "version_id": tool.VersionID}})
			require.NoError(t, err)
			require.False(t, loaded.IsError)
			require.Equal(t, mode != "compact", loaded.StructuredContent.(map[string]any)["refresh_tools_list"])
			result, err := session.CallTool(context.Background(), toolMarketMetaInvokeParams(tool, "same-request-across-modes", map[string]any{}))
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Contains(t, result.Meta, "lmm/market")
			if mode != "compact" {
				replay, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "market_tool_" + strings.ReplaceAll(tool.ToolID, "-", ""), Arguments: map[string]any{"request_id": "same-request-across-modes", "arguments": map[string]any{}}})
				require.NoError(t, err)
				require.False(t, replay.IsError)
			}
		})
	}
	var calls int64
	require.NoError(t, db.Model(&model.ToolMarketCall{}).Count(&calls).Error)
	require.EqualValues(t, 1, calls, "switching discovery modes must not duplicate a business request")

	request := httptest.NewRequest(http.MethodPost, "/?mode=invalid", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	NewToolMarketMCPHandler().ServeHTTP(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code)
	request.Header.Del("Authorization")
	response = httptest.NewRecorder()
	NewToolMarketMCPHandler().ServeHTTP(response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code, "mode selection cannot bypass authentication")
}

func TestToolMarketMetaCompactModeDoesNotWidenReadOnlyPermission(t *testing.T) {
	db, user := setupToolMarketBuiltinControllerTest(t)
	const clientID = "meta-compact-read-only"
	token, _, err := model.CreateToolMarketToken(user.Id, clientID, false, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	tool := builtinControllerTool(t, user.Id, "open_source_bounties", "open_source_bounties.list")
	builtinControllerGrant(t, user.Id, clientID, tool)
	server := httptest.NewServer(NewToolMarketMCPHandler())
	defer server.Close()
	session := toolMarketMetaModeSession(t, server.URL+"?mode=compact", token)
	list, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, list.Tools, 1)
	require.Equal(t, "metamcp", list.Tools[0].Name)
	for _, params := range []*mcp.CallToolParams{
		toolMarketMetaInvokeParams(tool, "must-not-execute", map[string]any{}),
		{Name: "metamcp", Arguments: map[string]any{"action": "load", "tool_id": tool.ToolID, "version_id": tool.VersionID}},
	} {
		result, err := session.CallTool(context.Background(), params)
		require.NoError(t, err)
		require.True(t, result.IsError)
	}
	var calls int64
	require.NoError(t, db.Model(&model.ToolMarketCall{}).Count(&calls).Error)
	require.Zero(t, calls)
}
