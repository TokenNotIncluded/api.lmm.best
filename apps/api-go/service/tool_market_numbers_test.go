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
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestToolMarketRemoteExactNumbersAndSchemaThroughJSONAndSSE(t *testing.T) {
	for _, jsonResponse := range []bool{true, false} {
		name := "json"
		if !jsonResponse {
			name = "sse"
		}
		t.Run(name, func(t *testing.T) {
			db := marketRemoteTestDB(t)
			users := []model.User{{Username: "number-buyer", AffCode: "number-buyer", Role: 1, Status: 1, Quota: 1000}, {Username: "number-author", AffCode: "number-author", Role: 1, Status: 1}, {Username: "number-root", AffCode: "number-root", Role: 100, Status: 1}}
			for i := range users {
				require.NoError(t, db.Create(&users[i]).Error)
			}
			require.NoError(t, model.SetToolMarketConfig(users[2].Id, model.ToolMarketConfig{Enabled: true, RecipientID: users[2].Id}))
			const integer = "9007199254740993"
			const decimal = "0.10000000000000000000000000000000000001"
			inputSchema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":` + integer + `,"maximum":` + integer + `,"const":` + integer + `},"amount":{"type":"number","minimum":` + decimal + `,"maximum":` + decimal + `,"const":` + decimal + `}},"required":["id","amount"],"additionalProperties":false}`)
			outputSchema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","const":` + integer + `},"amount":{"type":"number","const":` + decimal + `}},"required":["id","amount"]}`)
			var calls atomic.Int32
			actualNumbers := make(chan [2]string, 1)
			server := mcp.NewServer(&mcp.Implementation{Name: "exact-number-fixture", Version: "1"}, nil)
			definition := &mcp.Tool{Name: "exact", InputSchema: inputSchema, OutputSchema: outputSchema}
			handler := func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.Add(1)
				var arguments struct {
					ID     json.Number `json:"id"`
					Amount json.Number `json:"amount"`
				}
				if err := json.Unmarshal(req.Params.Arguments, &arguments); err != nil {
					return nil, err
				}
				actualNumbers <- [2]string{arguments.ID.String(), arguments.Amount.String()}
				return &mcp.CallToolResult{Meta: mcp.Meta{"exact-id": json.Number(integer)}, Content: []mcp.Content{&mcp.TextContent{Text: "exact numbers received"}}, StructuredContent: json.RawMessage(`{"id":` + integer + `,"amount":` + decimal + `}`)}, nil
			}
			server.AddTool(definition, handler)
			httpServer := httptest.NewTLSServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: jsonResponse}))
			defer httpServer.Close()
			target, err := url.Parse(httpServer.URL)
			require.NoError(t, err)
			remote := &ToolMarketRemote{client: &http.Client{Transport: marketResponseTransport{base: marketTestTransport{base: httpServer.Client().Transport, target: target}}}, slots: make(chan struct{}, 2)}
			ctx := context.Background()
			tools, err := remote.inspect(ctx, "https://example.com/mcp")
			require.NoError(t, err)
			require.Contains(t, string(tools[0].InputSchema), integer, "SDK float decoding must not rewrite provider schema limits")
			require.Contains(t, string(tools[0].InputSchema), decimal)
			tools[0].PriceQuota = 100
			product, err := model.SaveToolMarketDraft(users[1].Id, "", model.ToolMarketDraftInput{Name: "Exact numbers", ExecutionType: "remote", Visibility: "public", Endpoint: "https://example.com/mcp", Tools: tools})
			require.NoError(t, err)
			require.NoError(t, remote.validate(ctx, users[1].Id, product.ID, false))
			require.NoError(t, model.SubmitToolMarketDraft(users[1].Id, product.ID, product.DraftVersionID))
			require.NoError(t, model.ReviewToolMarketVersion(users[2].Id, product.ID, product.DraftVersionID, true, "exact numeric fixture"))
			detail, err := model.GetToolMarketDetail(users[0].Id, product.ID, false)
			require.NoError(t, err)
			tool := detail.Tools[0]
			require.NoError(t, model.SetToolMarketInstallation(users[0].Id, "number-client", tool.ToolID, tool.VersionID, true))
			grant, err := model.CreateToolMarketGrant(users[0].Id, model.ToolMarketGrant{ClientID: "number-client", ToolID: tool.ToolID, VersionID: tool.VersionID, MaxCalls: 2, MaxPriceQuota: 100, MaxTotalQuota: 200, ExpiresAt: common.GetTimestamp() + 3600})
			require.NoError(t, err)
			input := model.ToolMarketReserveInput{UserID: users[0].Id, ClientID: "number-client", ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID, RequestKey: "number-one"}
			for _, arguments := range []string{
				`{"id":9007199254740992,"amount":` + decimal + `}`,
				`{"id":9007199254740994,"amount":` + decimal + `}`,
				`{"id":9007199254740993.1,"amount":` + decimal + `}`,
				`{"id":` + integer + `,"amount":0.1}`,
				`{"id":` + integer + `,"amount":0.10000000000000000000000000000000000002}`,
				`{"id":1e999999999,"amount":` + decimal + `}`,
				`{"id":` + strings.Repeat("9", 257) + `,"amount":` + decimal + `}`,
			} {
				input.Arguments = json.RawMessage(arguments)
				_, err = remote.execute(ctx, input)
				require.ErrorIs(t, err, ErrMarketRemoteInput, arguments)
			}
			require.Zero(t, calls.Load(), "invalid typed/bound/const numbers fail before dispatch or billing")
			input.Arguments = json.RawMessage(`{"id":` + integer + `,"amount":` + decimal + `}`)
			response, err := remote.execute(ctx, input)
			require.NoError(t, err)
			require.Equal(t, "settled", response.Call.SettlementStatus)
			actual := <-actualNumbers
			require.Equal(t, integer, actual[0])
			require.Equal(t, decimal, actual[1])
			var exactResult map[string]any
			require.NoError(t, marketDecodeExactJSON(response.Result, &exactResult))
			require.Equal(t, json.Number(integer), exactResult["structuredContent"].(map[string]any)["id"])
			require.Equal(t, json.Number(decimal), exactResult["structuredContent"].(map[string]any)["amount"])
			require.Equal(t, json.Number(integer), exactResult["_meta"].(map[string]any)["exact-id"])
			_, err = remote.execute(ctx, input)
			require.NoError(t, err)
			require.Equal(t, int32(1), calls.Load())
			// Adjacent constants used to collapse to the same SDK float64. A
			// provider changing just one such constraint must fail drift checks.
			definition.InputSchema = json.RawMessage(strings.ReplaceAll(string(inputSchema), integer, "9007199254740992"))
			server.AddTool(definition, handler)
			input.RequestKey = "number-drift"
			_, err = remote.execute(ctx, input)
			require.ErrorIs(t, err, ErrMarketRemoteChanged)
			require.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestToolMarketExactSchemaDraftsAndReferenceBoundary(t *testing.T) {
	for _, draft := range []string{"", "http://json-schema.org/draft-07/schema#", "https://json-schema.org/draft-07/schema#", "https://json-schema.org/draft/2020-12/schema"} {
		schema := json.RawMessage(`{"$schema":"` + draft + `","type":"object","properties":{"id":{"$ref":"#/$defs/id"}},"$defs":{"id":{"type":"integer","minimum":9007199254740993}},"required":["id"]}`)
		_, err := ValidateToolMarketArguments(schema, json.RawMessage(`{"id":9007199254740993}`))
		require.NoError(t, err, draft)
		_, err = ValidateToolMarketArguments(schema, json.RawMessage(`{"id":9007199254740992}`))
		require.ErrorIs(t, err, ErrMarketRemoteInput, draft)
	}
	for _, schema := range []string{`{"type":"object","$ref":"https://example.com/schema"}`, `{"type":"object","$dynamicRef":"#/x"}`, `{"type":"object","$id":"https://example.com/schema"}`, `{"type":"object","properties":{"id":{"minimum":1e9999999}}}`} {
		_, err := ValidateToolMarketArguments(json.RawMessage(schema), json.RawMessage(`{}`))
		require.ErrorIs(t, err, ErrMarketRemoteSchema)
	}
}
