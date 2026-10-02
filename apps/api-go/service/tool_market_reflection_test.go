package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestToolMarketRemoteCredentialReflectionNeverPublishedOrPersisted(t *testing.T) {
	for _, mode := range []string{"bearer", "api_key"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TOOL_MARKET_ENCRYPTION_KEY", "reflection-fixture-encryption-key-strong-32-bytes")
			db := marketRemoteTestDB(t)
			users := []model.User{{Username: "reflection-buyer", AffCode: "reflect-buyer", Role: 1, Status: 1, Quota: 1000}, {Username: "reflection-author", AffCode: "reflect-author", Role: 1, Status: 1}, {Username: "reflection-root", AffCode: "reflect-root", Role: 100, Status: 1}}
			for i := range users {
				require.NoError(t, db.Create(&users[i]).Error)
			}
			require.NoError(t, model.SetToolMarketConfig(users[2].Id, model.ToolMarketConfig{Enabled: true, RecipientID: users[2].Id}))
			const secret = "fixture-reflected-credential-9K2"
			credential := &model.ToolMarketResolvedCredential{Mode: mode, Secret: secret}
			var calls atomic.Int32
			var resultCase atomic.Int32
			server := mcp.NewServer(&mcp.Implementation{Name: "reflection-fixture", Version: "1"}, nil)
			callHandler := func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.Add(1)
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "fixture-result"}}}
				switch resultCase.Load() {
				case 1:
					result.Content = []mcp.Content{&mcp.TextContent{Text: "authorization=" + secret}}
				case 2:
					result.StructuredContent = map[string]any{"nested": []any{map[string]any{"credential": secret}}}
				case 3:
					result.Meta = mcp.Meta{"provider": base64.RawURLEncoding.EncodeToString([]byte(secret))}
				case 4:
					var escaped strings.Builder
					for _, r := range secret {
						fmt.Fprintf(&escaped, `\u%04x`, r)
					}
					result.Content = []mcp.Content{&mcp.TextContent{Text: "credential=" + escaped.String()}}
				case 5:
					result.Content = []mcp.Content{&mcp.ImageContent{MIMEType: "image/png", Data: []byte("x" + secret + "suffix")}}
				case 6:
					result.Content = []mcp.Content{&mcp.AudioContent{MIMEType: "audio/wav", Data: []byte("xy" + secret + "suffix")}}
				case 7:
					result.IsError = true
					result.Content = []mcp.Content{&mcp.TextContent{Text: `{"error":{"credential":"` + secret + `"}}`}}
				}
				return result, nil
			}
			safeDefinition := func() *mcp.Tool {
				return &mcp.Tool{Name: "reflect_lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}
			}
			server.AddTool(safeDefinition(), callHandler)
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: mode == "bearer"})
			httpServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				received := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
				if mode == "api_key" {
					received = req.Header.Get("X-API-Key")
				}
				if received != secret {
					http.Error(w, "fixture authentication rejected", http.StatusUnauthorized)
					return
				}
				handler.ServeHTTP(w, req)
			}))
			defer httpServer.Close()
			target, err := url.Parse(httpServer.URL)
			require.NoError(t, err)
			remote := &ToolMarketRemote{client: &http.Client{Transport: marketResponseTransport{base: marketTestTransport{base: httpServer.Client().Transport, target: target}}}, slots: make(chan struct{}, 2)}
			ctx, endpoint := context.Background(), "https://example.com/mcp"
			tools, err := remote.inspectAuthenticated(ctx, endpoint, credential)
			require.NoError(t, err)
			tools[0].PriceQuota = 100
			product, err := model.SaveToolMarketDraft(users[1].Id, "", model.ToolMarketDraftInput{Name: "Credential reflection", ExecutionType: "remote", Visibility: "public", Endpoint: endpoint, Tools: tools})
			require.NoError(t, err)
			require.NoError(t, model.ConfigureToolMarketCredential(users[1].Id, product.ID, product.DraftVersionID, mode, secret, ""))
			// None of these received-credential reflections may be returned from
			// inspect or attached to a validation record for later publication.
			for _, field := range []string{"name", "description", "input_schema", "output_schema", "meta"} {
				definition := safeDefinition()
				switch field {
				case "name":
					definition.Name = secret
				case "description":
					definition.Description = "remote credential: " + secret
				case "input_schema":
					definition.InputSchema = map[string]any{"type": "object", "description": base64.StdEncoding.EncodeToString([]byte(secret))}
				case "output_schema":
					definition.OutputSchema = map[string]any{"type": "object", "description": secret}
				case "meta":
					definition.Meta = mcp.Meta{"credential": secret}
				}
				server.AddTool(definition, callHandler)
				rows, err := remote.inspectAuthenticated(ctx, endpoint, credential)
				require.ErrorIs(t, err, ErrMarketRemoteSchema, field)
				require.Nil(t, rows)
				require.NotContains(t, err.Error(), secret)
				require.ErrorIs(t, remote.validate(ctx, users[1].Id, product.ID, false), ErrMarketRemoteSchema, field)
				draft, err := model.GetToolMarketDetail(users[1].Id, product.ID, true)
				require.NoError(t, err)
				require.False(t, draft.Validated)
				server.RemoveTools(definition.Name)
				server.AddTool(safeDefinition(), callHandler)
			}
			require.Zero(t, calls.Load())
			require.NoError(t, remote.validate(ctx, users[1].Id, product.ID, false))
			require.NoError(t, model.SubmitToolMarketDraft(users[1].Id, product.ID, product.DraftVersionID))
			require.NoError(t, model.ReviewToolMarketVersion(users[2].Id, product.ID, product.DraftVersionID, true, "safe reflection fixture"))
			detail, err := model.GetToolMarketDetail(users[0].Id, product.ID, false)
			require.NoError(t, err)
			tool := detail.Tools[0]
			require.NoError(t, model.SetToolMarketInstallation(users[0].Id, "reflection-client", tool.ToolID, tool.VersionID, true))
			grant, err := model.CreateToolMarketGrant(users[0].Id, model.ToolMarketGrant{ClientID: "reflection-client", ToolID: tool.ToolID, VersionID: tool.VersionID, MaxCalls: 7, MaxPriceQuota: 100, MaxTotalQuota: 700, ExpiresAt: common.GetTimestamp() + 3600})
			require.NoError(t, err)
			reflectedLiveDefinition := safeDefinition()
			reflectedLiveDefinition.Meta = mcp.Meta{"credential": secret}
			server.AddTool(reflectedLiveDefinition, callHandler)
			_, err = remote.execute(ctx, model.ToolMarketReserveInput{UserID: users[0].Id, ClientID: "reflection-client", ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID, RequestKey: "live-definition-reflection", Arguments: json.RawMessage(`{}`)})
			require.ErrorIs(t, err, ErrMarketRemoteSchema)
			require.Zero(t, calls.Load(), "reflected live metadata is blocked before reserve or business execution")
			server.AddTool(safeDefinition(), callHandler)
			for index := int32(1); index <= 7; index++ {
				resultCase.Store(index)
				input := model.ToolMarketReserveInput{UserID: users[0].Id, ClientID: "reflection-client", ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID, RequestKey: fmt.Sprintf("reflection-%d", index), Arguments: json.RawMessage(`{}`)}
				response, err := remote.execute(ctx, input)
				require.NoError(t, err)
				require.Equal(t, "failed", response.Call.ExecutionStatus)
				require.Equal(t, "released", response.Call.SettlementStatus)
				require.Equal(t, "TOOL_MARKET_UNSAFE_RESULT", response.ErrorCode)
				require.JSONEq(t, `{"isError":true,"content":[{"type":"text","text":"The remote MCP service returned an unsafe result."}]}`, string(response.Result))
				var persisted model.ToolMarketResult
				require.NoError(t, db.First(&persisted, "call_id = ?", response.Call.ID).Error)
				require.False(t, persisted.Success)
				require.Equal(t, string(response.Result), persisted.Data, "only the fixed safe package is durable")
				// A repeated request returns the fixed durable result, and never
				// retries a provider operation with a possibly committed side effect.
				replayed, err := remote.execute(ctx, input)
				require.NoError(t, err)
				require.Equal(t, response.Call.ID, replayed.Call.ID)
				require.Equal(t, int32(index), calls.Load())
				var buyer model.User
				require.NoError(t, db.First(&buyer, users[0].Id).Error)
				require.Equal(t, 1000, buyer.Quota)
			}
		})
	}
}
