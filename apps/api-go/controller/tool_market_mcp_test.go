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
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToolMarketMCPUsesVerifiedClientAndRefreshesToolSet(t *testing.T) {
	db, user, _ := setupOpenSourceBountyMCPControllerTest(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.ModerationJob{}, &model.ToolMarketService{}, &model.ToolMarketVersion{}, &model.ToolMarketTool{}, &model.ToolMarketToolVersion{}, &model.ToolMarketAccess{}, &model.ToolMarketInstallation{}, &model.ToolMarketGrant{}, &model.ToolMarketEvent{}, &model.ToolMarketToken{}, &model.ToolMarketCall{}, &model.ToolMarketResult{}, &model.ToolMarketBudget{}, &model.ToolMarketTransfer{}))
	service, err := model.SaveToolMarketDraft(user.Id, "", model.ToolMarketDraftInput{Name: "MCP fixture", ExecutionType: "remote", Visibility: "public", Endpoint: "https://example.com/mcp", Tools: []model.ToolMarketToolInput{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"$ref":"#/$defs/q"}},"$defs":{"q":{"type":"string"}}}`), Permissions: []string{"read"}}}})
	require.NoError(t, err)
	require.NoError(t, model.SubmitToolMarketDraft(user.Id, service.ID, service.DraftVersionID))
	require.NoError(t, db.Model(&model.ToolMarketVersion{}).Where("id = ?", service.DraftVersionID).Update("validation_digest", gorm.Expr("digest")).Error)
	var root model.User
	require.NoError(t, db.Where("role = ?", 100).First(&root).Error)
	require.NoError(t, model.ReviewToolMarketVersion(root.Id, service.ID, service.DraftVersionID, true, "fixture checked"))
	detail, err := model.GetToolMarketDetail(user.Id, service.ID, false)
	require.NoError(t, err)
	tool := detail.Tools[0]
	token, record, err := model.CreateToolMarketToken(user.Id, "agent-a", true, true, common.GetTimestamp()+3600)
	require.NoError(t, err)
	server := httptest.NewServer(NewToolMarketMCPHandler())
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "market-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: openSourceBountyBearerTransport{token: token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	defer session.Close()
	list, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, list.Tools, 5)
	// The caller's attempt to spoof another client is ignored; verified identity wins.
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "lmm_market_load", Arguments: map[string]any{"tool_id": tool.ToolID, "version_id": tool.VersionID, "loaded": true, "client_id": "agent-b"}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	var installation model.ToolMarketInstallation
	require.NoError(t, db.First(&installation).Error)
	require.Equal(t, "agent-a", installation.ClientID)
	list, err = session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, list.Tools, 5, "loading must not auto-authorize execution")
	// The default Meta tool is already available before any loaded tools. Owner
	// consent with a real zero client cap lets AI authorize this free tool only.
	subject, err := model.ToolMarketMetaPersonalSubject(user.Id, record.ID)
	require.NoError(t, err)
	_, err = model.SetToolMarketMetaDelegation(subject, model.ToolMarketMetaDelegation{Enabled: true})
	require.NoError(t, err)
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "metamcp", Arguments: map[string]any{"action": "unload", "tool_id": tool.ToolID, "version_id": tool.VersionID}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "metamcp", Arguments: map[string]any{"action": "load", "tool_id": tool.ToolID, "version_id": tool.VersionID}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "metamcp", Arguments: map[string]any{"action": "authorize", "tool_id": tool.ToolID, "version_id": tool.VersionID, "max_price_quota": 0, "max_total_quota": 0, "max_calls": 1, "expires_at": common.GetTimestamp() + 60}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	for _, table := range []string{"tool_market_calls", "tool_market_transfers"} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.Zero(t, count, "default Meta load/authorize never charges")
	}
	list, err = session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, list.Tools, 6, "tools/list must refresh without reconnecting")
	var rawSchema []byte
	for _, item := range list.Tools {
		if strings.HasPrefix(item.Name, "market_tool_") {
			require.NotContains(t, item.Description, "Price:")
			require.NotContains(t, item.Description, "quota per")
			require.Contains(t, item.Meta, "lmm/pricing")
			rawSchema, err = json.Marshal(item.InputSchema)
			require.NoError(t, err)
		}
	}
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(rawSchema, &schema))
	resolved, err := schema.Resolve(nil)
	require.NoError(t, err)
	require.NoError(t, resolved.Validate(map[string]any{"request_id": "one", "arguments": map[string]any{"q": "valid"}}))
	require.Error(t, resolved.Validate(map[string]any{"request_id": "one", "arguments": map[string]any{"q": 42}}))
	require.NoError(t, model.SetToolMarketInstallation(user.Id, "agent-a", tool.ToolID, tool.VersionID, false))
	list, err = session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, list.Tools, 5)
	require.NoError(t, model.RevokeToolMarketToken(user.Id, record.ID))
	_, err = session.ListTools(context.Background(), nil)
	require.Error(t, err)
}

func TestToolMarketMCPScopesAndConsentAreExplicit(t *testing.T) {
	db, user, _ := setupOpenSourceBountyMCPControllerTest(t)
	require.NoError(t, db.AutoMigrate(&model.ToolMarketToken{}, &model.ToolMarketEvent{}))
	token, _, err := model.CreateToolMarketToken(user.Id, "read-only", false, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	identity, err := marketMCPAuthenticate(context.Background(), token)
	require.NoError(t, err)
	server, err := newToolMarketMCPServer(identity)
	require.NoError(t, err)
	require.NotNil(t, server)
	require.False(t, identity.invoke)
	require.False(t, identity.manage)
	labels := marketOAuthPermissionLabels("zh", []string{service.OAuthMarketDiscoverScope, service.OAuthMarketInvokeScope})
	require.Len(t, labels, 2)
	require.Contains(t, labels[1], "限额")
	require.Empty(t, marketOAuthPermissionLabels("en", []string{service.OAuthCatalogScope}))
	data, err := json.Marshal(recordSafeToken(t, token))
	require.NoError(t, err)
	require.NotContains(t, string(data), token)
	require.NotContains(t, string(data), "digest")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("auth_version", gorm.Expr("auth_version + 1")).Error)
	_, err = marketMCPAuthenticate(context.Background(), token)
	require.Error(t, err)
}

func recordSafeToken(t *testing.T, raw string) *model.ToolMarketToken {
	t.Helper()
	token, err := model.VerifyToolMarketToken(raw)
	require.NoError(t, err)
	return token
}
