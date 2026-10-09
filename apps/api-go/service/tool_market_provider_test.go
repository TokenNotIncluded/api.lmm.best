package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/marketprovider"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestToolMarketProviderPresetFiltersAccountTools(t *testing.T) {
	tools := []model.ToolMarketToolInput{{Name: "execute_tool"}, {Name: "describe_tool"}, {Name: "find_tools"}, {Name: "agentkey_account"}, {Name: "delete_key"}}
	selected, err := ToolMarketPresetTools("agentkey", tools)
	require.NoError(t, err)
	require.Len(t, selected, 3)
	require.Equal(t, "agentkey", selected[0].ProviderPricing.Provider)
	require.Nil(t, selected[1].ProviderPricing)
	_, err = ToolMarketPresetTools("unknown", tools)
	require.Error(t, err)
	require.Error(t, marketProviderAccountBoundary("https://api.agentkey.app/v1/mcp", "describe_tool", map[string]any{"name": "agentkey_account"}))
}

func TestToolMarketProviderQuoteAndSettlement(t *testing.T) {
	for _, preset := range marketprovider.Presets() {
		t.Run(preset.ID, func(t *testing.T) {
			t.Setenv("TOOL_MARKET_ENCRYPTION_KEY", "provider-fixture-encryption-2026")
			db := marketRemoteTestDB(t)
			users := []model.User{{Username: "buyer", AffCode: "buyer", Role: 1, Status: 1, Quota: 10000}, {Username: "author", AffCode: "author", Role: 1, Status: 1}, {Username: "root", AffCode: "root", Role: 100, Status: 1}}
			for i := range users {
				require.NoError(t, db.Create(&users[i]).Error)
			}
			require.NoError(t, model.SetToolMarketConfig(users[2].Id, model.ToolMarketConfig{Enabled: true, FeeBPS: 1000, RecipientID: users[2].Id}))
			var executions, inspections atomic.Int32
			price := "0.0001"
			variable := false
			pending := false
			providerStatus := 200
			generic := map[string]any{"type": "object"}
			operationSchema := map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string"}}, "required": []string{"url"}}
			server := mcp.NewServer(&mcp.Implementation{Name: "provider-fixture", Version: "1"}, nil)
			server.AddTool(&mcp.Tool{Name: preset.InspectTool, InputSchema: generic}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				inspections.Add(1)
				var value map[string]any
				if preset.ID == "agentkey" {
					value = map[string]any{"name": "Firecrawl/scrape", "cost": map[string]any{"usd_per_call": json.Number(price), "credits_per_call": 17}, "params": operationSchema}
					if variable {
						delete(value["cost"].(map[string]any), "usd_per_call")
					}
				} else {
					unit := "PER_CALL"
					if variable {
						unit = "PER_RESULT"
					}
					value = map[string]any{"provider": "surf", "endpoint": "/search/web", "price": map[string]any{"type": unit, "amount": map[string]any{"value": json.Number(price), "currency": "USD"}}, "schema": map[string]any{"input": operationSchema}}
				}
				return &mcp.CallToolResult{StructuredContent: value}, nil
			})
			server.AddTool(&mcp.Tool{Name: preset.ExecuteTool, InputSchema: generic}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				executions.Add(1)
				if preset.ID == "agentkey" {
					return &mcp.CallToolResult{StructuredContent: map[string]any{"provider": "Firecrawl", "data": map[string]any{"answer": "fixture"}}}, nil
				}
				state := "COMPLETED"
				if pending {
					state = "RUNNING"
				}
				return &mcp.CallToolResult{StructuredContent: map[string]any{"status": state, "providerResponse": map[string]any{"httpStatus": providerStatus}, "output": "fixture"}}, nil
			})
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
			remoteHTTP := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-merchant-key" {
					http.Error(w, "bad credential", 401)
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer remoteHTTP.Close()
			target, err := url.Parse(remoteHTTP.URL)
			require.NoError(t, err)
			remote := &ToolMarketRemote{client: &http.Client{Transport: marketResponseTransport{base: marketTestTransport{base: remoteHTTP.Client().Transport, target: target}}}, slots: make(chan struct{}, 2)}
			ctx := context.Background()
			credential := &model.ToolMarketResolvedCredential{Mode: "bearer", Secret: "fixture-merchant-key"}
			inputs, err := remote.inspectAuthenticated(ctx, preset.Endpoint, credential)
			require.NoError(t, err)
			inputs, err = ToolMarketPresetTools(preset.ID, inputs)
			require.NoError(t, err)
			for i := range inputs {
				if inputs[i].ProviderPricing != nil {
					inputs[i].PriceQuota = 1000
				}
			}
			product, err := model.SaveToolMarketDraft(users[1].Id, "", model.ToolMarketDraftInput{Name: "Preset fixture", ExecutionType: "remote", Visibility: "public", Endpoint: preset.Endpoint, Tools: inputs})
			require.NoError(t, err)
			require.NoError(t, model.ConfigureToolMarketCredential(users[1].Id, product.ID, product.DraftVersionID, "bearer", credential.Secret, ""))
			require.NoError(t, remote.validate(ctx, users[1].Id, product.ID, false))
			require.NoError(t, model.SubmitToolMarketDraft(users[1].Id, product.ID, product.DraftVersionID))
			require.NoError(t, model.ReviewToolMarketVersion(users[2].Id, product.ID, product.DraftVersionID, true, "fixture review"))
			detail, err := model.GetToolMarketDetail(users[0].Id, product.ID, false)
			require.NoError(t, err)
			var local model.ToolMarketToolVersion
			for _, tool := range detail.Tools {
				if tool.Name == preset.ExecuteTool {
					local = tool
				}
			}
			require.NotNil(t, local.ProviderPricing, "immutable publication retains provider pricing")
			require.NoError(t, model.SetToolMarketInstallation(users[0].Id, "client", local.ToolID, local.VersionID, true))
			grant, err := model.CreateToolMarketGrant(users[0].Id, model.ToolMarketGrant{ClientID: "client", ToolID: local.ToolID, VersionID: local.VersionID, MaxCalls: 10, MaxPriceQuota: 1000, MaxTotalQuota: 10000, ExpiresAt: common.GetTimestamp() + 3600})
			require.NoError(t, err)
			args := json.RawMessage(`{"name":"Firecrawl/scrape","params":{"url":"https://example.com"}}`)
			if preset.ID == "monid" {
				args = json.RawMessage(`{"provider":"surf","endpoint":"/search/web","input":{"url":"https://example.com"}}`)
			}
			request := model.ToolMarketReserveInput{UserID: users[0].Id, ClientID: "client", ToolID: local.ToolID, VersionID: local.VersionID, GrantID: grant.ID, RequestKey: "quoted", Arguments: args}
			response, err := remote.execute(ctx, request)
			require.NoError(t, err)
			require.Equal(t, "settled", response.Call.SettlementStatus)
			units, err := common.LedgerQuotaPerUSD()
			require.NoError(t, err)
			expected, err := (marketprovider.Quote{Provider: preset.ID, Unit: "call", AmountUSD: price}).Quota("1.2", units.String())
			require.NoError(t, err)
			require.Equal(t, expected, response.Call.PriceQuota, "charge quote, not the 1000-quota ceiling")
			require.NotNil(t, response.Call.ProviderQuote)
			require.Equal(t, "1.2", response.Call.PriceMultiplier)
			replay, err := remote.execute(ctx, request)
			require.NoError(t, err)
			require.Equal(t, response.Call.ID, replay.Call.ID)
			require.Equal(t, int32(1), executions.Load())
			require.Equal(t, int32(1), inspections.Load(), "replay must not even re-inspect")
			price = "1"
			request.RequestKey = "price-over-ceiling"
			_, err = remote.execute(ctx, request)
			require.ErrorIs(t, err, model.ErrToolMarketBudget)
			require.Equal(t, int32(1), executions.Load(), "no paid dispatch above the cap")
			price, variable = "0.0001", true
			request.RequestKey = "unsupported-price"
			_, err = remote.execute(ctx, request)
			require.Error(t, err)
			require.Equal(t, int32(1), executions.Load(), "missing USD or per-result pricing must not dispatch")
			if preset.ID == "monid" {
				variable = false
				providerStatus = 404
				request.RequestKey = "business-404"
				response, err = remote.execute(ctx, request)
				require.NoError(t, err)
				require.Equal(t, "released", response.Call.SettlementStatus)
				pending = true
				request.RequestKey = "pending-job"
				response, err = remote.execute(ctx, request)
				require.NoError(t, err)
				require.Equal(t, "unknown", response.Call.ExecutionStatus)
				require.Equal(t, "held", response.Call.SettlementStatus)
			}
		})
	}
}
