package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/marketprovider"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopspring/decimal"
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
	previous, previousErr := common.CreditsPerUSD()
	legacy, _ := common.LegacyPricingQuotaPerUnit()
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	t.Cleanup(func() {
		if previousErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(previous, legacy))
		}
	})
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
			runID := "01HXYZ1234567890ABCDEF"
			schemaKey := "schema"
			wrongRun := false
			var polls atomic.Int32
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
					value = map[string]any{"provider": "surf", "endpoint": "/search/web", "price": map[string]any{"type": unit, "amount": map[string]any{"value": json.Number(price), "currency": "USD"}}, schemaKey: map[string]any{"input": operationSchema}}
				}
				if schemaKey == "inputSchema" {
					value[schemaKey] = operationSchema
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
				return &mcp.CallToolResult{StructuredContent: map[string]any{"runId": runID, "provider": "surf", "endpoint": "/search/web", "status": state, "providerResponse": map[string]any{"httpStatus": providerStatus}, "output": "fixture", "caller": "private-merchant-identity"}}, nil
			})
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
			remoteHTTP := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-merchant-key" {
					http.Error(w, "bad credential", 401)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/v1/runs/") {
					polls.Add(1)
					if r.Method != "GET" || r.URL.Path != "/v1/runs/"+runID {
						http.Error(w, "wrong request", 400)
						return
					}
					id := runID
					if wrongRun {
						id = "another-run"
					}
					state := "COMPLETED"
					if pending {
						state = "RUNNING"
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"runId": id, "provider": "surf", "endpoint": "/search/web", "status": state, "output": "fixture", "providerResponse": map[string]any{"httpStatus": providerStatus}, "caller": "private-merchant-identity"})
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
			require.NotContains(t, string(response.Result), "private-merchant-identity")
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
				schemaKey = "inputSchema" // documented API shape, not only the MCP variant
				providerStatus = 200
				pending = true
				request.RequestKey = "pending-job"
				response, err = remote.execute(ctx, request)
				require.NoError(t, err)
				require.Equal(t, "running", response.Call.ExecutionStatus)
				require.Equal(t, "held", response.Call.SettlementStatus)
				require.Equal(t, runID, response.Call.ProviderRunID)
				public, err := json.Marshal(response)
				require.NoError(t, err)
				require.NotContains(t, string(public), runID)
				// A fresh adapter, with no in-memory operation state, recovers it.
				restored := &ToolMarketRemote{client: remote.client, slots: make(chan struct{}, 2)}
				_, err = restored.recoverProviderCalls(ctx)
				require.NoError(t, err)
				require.EqualValues(t, 1, polls.Load())
				_, err = restored.recoverProviderCalls(ctx)
				require.NoError(t, err)
				require.EqualValues(t, 1, polls.Load(), "lease suppresses duplicate polls")
				require.NoError(t, db.Model(&model.ToolMarketCall{}).Where("id = ?", response.Call.ID).Update("provider_next_poll_at", 0).Error)
				pending, wrongRun = false, true
				_, err = restored.recoverProviderCalls(ctx)
				require.Error(t, err)
				current, err := model.GetToolMarketCall(users[0].Id, "client", response.Call.ID)
				require.NoError(t, err)
				require.Equal(t, "held", current.SettlementStatus, "unrelated run cannot settle this call")
				require.Error(t, model.BindToolMarketProviderRun(current.ID, "another-run"))
				require.NoError(t, db.Model(&model.ToolMarketCall{}).Where("id = ?", current.ID).Update("provider_next_poll_at", 0).Error)
				wrongRun = false
				before := executions.Load()
				_, err = restored.recoverProviderCalls(ctx)
				require.NoError(t, err)
				final, err := GetToolMarketExecutionResponse(users[0].Id, "client", current.ID)
				require.NoError(t, err)
				require.Equal(t, "settled", final.Call.SettlementStatus)
				require.Equal(t, "succeeded", final.Call.ExecutionStatus)
				require.NotContains(t, string(final.Result), "private-merchant-identity")
				_, err = restored.recoverProviderCalls(ctx)
				require.NoError(t, err)
				require.Equal(t, before, executions.Load(), "recovery never repeats execution")
				replay, err = remote.execute(ctx, request)
				require.NoError(t, err)
				require.Equal(t, final.Call.ID, replay.Call.ID)
				var transfers int64
				require.NoError(t, db.Model(&model.ToolMarketTransfer{}).Where("call_id = ?", current.ID).Count(&transfers).Error)
				require.EqualValues(t, 2, transfers, "one author transfer and one platform transfer")
				// A completed provider error must release, not charge, the hold.
				pending, runID = true, "FAILED-RUN"
				request.RequestKey = "failed-run"
				failed, err := remote.execute(ctx, request)
				require.NoError(t, err)
				pending, providerStatus = false, 500
				_, err = restored.recoverProviderCalls(ctx)
				require.NoError(t, err)
				failed, err = GetToolMarketExecutionResponse(users[0].Id, "client", failed.Call.ID)
				require.NoError(t, err)
				require.Equal(t, "released", failed.Call.SettlementStatus)
				require.Equal(t, "failed", failed.Call.ExecutionStatus)
				providerStatus = 200
				// Expiry frees the hold without dispatching again or charging late.
				pending = true
				runID = "EXPIRED-RUN"
				request.RequestKey = "expired-run"
				expiring, err := remote.execute(ctx, request)
				require.NoError(t, err)
				require.NoError(t, db.Model(&model.ToolMarketCall{}).Where("id = ?", expiring.Call.ID).Update("resolve_by", common.GetTimestamp()-1).Error)
				_, err = model.RecoverToolMarketCalls(ctx)
				require.NoError(t, err)
				expired, err := GetToolMarketExecutionResponse(users[0].Id, "client", expiring.Call.ID)
				require.NoError(t, err)
				require.Equal(t, "released", expired.Call.SettlementStatus)
				require.Equal(t, "unknown", expired.Call.ExecutionStatus)
				_, err = model.GetToolMarketCall(users[1].Id, "client", expired.Call.ID)
				require.Error(t, err, "another user cannot retrieve this run")
			}
		})
	}
}
