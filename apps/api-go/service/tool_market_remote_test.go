package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/glebarez/sqlite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type marketTestTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (t marketTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = t.target.Scheme
	u.Host = t.target.Host
	clone.URL = &u
	clone.Host = t.target.Host
	return t.base.RoundTrip(clone)
}

func marketRemoteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB, oldRedis := model.DB, common.RedisEnabled
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, oldLog)
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.ToolMarketService{}, &model.ToolMarketVersion{}, &model.ToolMarketTool{}, &model.ToolMarketToolVersion{}, &model.ToolMarketAccess{}, &model.ToolMarketFavorite{}, &model.ToolMarketInstallation{}, &model.ToolMarketGrant{}, &model.ToolMarketBudget{}, &model.ToolMarketCall{}, &model.ToolMarketTransfer{}, &model.ToolMarketEvent{}, &model.ToolMarketConfig{}, &model.ToolMarketResult{}, &model.ToolMarketToken{}))
	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		common.SetDatabaseTypes(oldMain, oldLog)
		_ = pool.Close()
	})
	return db
}

func TestToolMarketRemoteLifecycleAndBusinessResults(t *testing.T) {
	for _, mode := range []string{"success", "error", "schema", "unknown", "pending"} {
		t.Run(mode, func(t *testing.T) {
			db := marketRemoteTestDB(t)
			users := []model.User{{Username: "buyer", AffCode: "buyer", Role: 1, Status: 1, Quota: 1000}, {Username: "author", AffCode: "author", Role: 1, Status: 1}, {Username: "root", AffCode: "root", Role: 100, Status: 1}}
			for i := range users {
				require.NoError(t, db.Create(&users[i]).Error)
			}
			require.NoError(t, model.SetToolMarketConfig(users[2].Id, model.ToolMarketConfig{Enabled: true, FeeBPS: 1000, RecipientID: users[2].Id}))
			var calls atomic.Int32
			server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
			tool := &mcp.Tool{Name: "lookup", Description: "Lookup public text", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}, "required": []string{"q"}}, OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}, "required": []string{"answer"}}}
			server.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.Add(1)
				switch mode {
				case "error":
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Business operation failed"}}}, nil
				case "schema":
					return &mcp.CallToolResult{StructuredContent: map[string]any{"answer": 123}}, nil
				case "unknown":
					return nil, errors.New("simulated transport or protocol failure")
				case "pending":
					return &mcp.CallToolResult{StructuredContent: map[string]any{"answer": "later", "status": "pending"}}, nil
				default:
					return &mcp.CallToolResult{StructuredContent: map[string]any{"answer": "delivered"}}, nil
				}
			})
			httpServer := httptest.NewTLSServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
			defer httpServer.Close()
			target, err := url.Parse(httpServer.URL)
			require.NoError(t, err)
			remote := &ToolMarketRemote{client: &http.Client{Transport: marketResponseTransport{base: marketTestTransport{base: httpServer.Client().Transport, target: target}}}, slots: make(chan struct{}, 2)}
			ctx := context.Background()
			tools, err := remote.inspect(ctx, "https://example.com/mcp")
			require.NoError(t, err)
			require.Len(t, tools, 1)
			require.Zero(t, calls.Load())
			tools[0].PriceQuota = 100
			product, err := model.SaveToolMarketDraft(users[1].Id, "", model.ToolMarketDraftInput{Name: "Fixture", ExecutionType: "remote", Visibility: "public", Endpoint: "https://example.com/mcp", Tools: tools})
			require.NoError(t, err)
			require.NoError(t, remote.validate(ctx, users[1].Id, product.ID, false))
			require.Zero(t, calls.Load())
			require.NoError(t, model.SubmitToolMarketDraft(users[1].Id, product.ID, product.DraftVersionID))
			require.NoError(t, model.ReviewToolMarketVersion(users[2].Id, product.ID, product.DraftVersionID, true, "reviewed"))
			detail, err := model.GetToolMarketDetail(users[0].Id, product.ID, false)
			require.NoError(t, err)
			local := detail.Tools[0]
			require.NoError(t, model.SetToolMarketInstallation(users[0].Id, "client", local.ToolID, local.VersionID, true))
			grant, err := model.CreateToolMarketGrant(users[0].Id, model.ToolMarketGrant{ClientID: "client", ToolID: local.ToolID, VersionID: local.VersionID, MaxCalls: 10, MaxPriceQuota: 100, MaxTotalQuota: 1000, ExpiresAt: common.GetTimestamp() + 3600})
			require.NoError(t, err)
			input := model.ToolMarketReserveInput{UserID: users[0].Id, ClientID: "client", ToolID: local.ToolID, VersionID: local.VersionID, GrantID: grant.ID, RequestKey: "one", Arguments: json.RawMessage(`{"q":"hello"}`)}
			bad := input
			bad.Arguments = json.RawMessage(`{"wrong":true}`)
			_, err = remote.execute(ctx, bad)
			require.ErrorIs(t, err, ErrMarketRemoteInput)
			require.Zero(t, calls.Load())
			response, err := remote.execute(ctx, input)
			require.NoError(t, err)
			require.Equal(t, int32(1), calls.Load())
			replayed, err := remote.execute(ctx, input)
			require.NoError(t, err)
			require.Equal(t, response.Call.ID, replayed.Call.ID)
			require.Equal(t, int32(1), calls.Load())
			var buyer model.User
			require.NoError(t, db.First(&buyer, users[0].Id).Error)
			switch mode {
			case "success":
				require.Equal(t, "settled", response.Call.SettlementStatus)
				require.Contains(t, string(response.Result), "delivered")
				require.Equal(t, 900, buyer.Quota)
				var author, root model.User
				require.NoError(t, db.First(&author, users[1].Id).Error)
				require.NoError(t, db.First(&root, users[2].Id).Error)
				require.Equal(t, 90, author.Quota)
				require.Equal(t, 10, root.Quota)
				_, _, err = model.GetToolMarketResult(users[1].Id, "", response.Call.ID)
				require.Error(t, err)
				_, _, err = model.GetToolMarketResult(users[0].Id, "another-client", response.Call.ID)
				require.Error(t, err)
			case "unknown", "pending":
				require.Equal(t, "unknown", response.Call.ExecutionStatus)
				require.Equal(t, "held", response.Call.SettlementStatus)
				require.Equal(t, 900, buyer.Quota)
				require.NoError(t, db.Model(&model.ToolMarketCall{}).Where("id = ?", response.Call.ID).Update("resolve_by", common.GetTimestamp()-1).Error)
				_, err = model.RecoverToolMarketCalls(ctx)
				require.NoError(t, err)
				require.NoError(t, db.First(&buyer, users[0].Id).Error)
				require.Equal(t, 1000, buyer.Quota)
				require.Equal(t, int32(1), calls.Load())
			default:
				require.Equal(t, "failed", response.Call.ExecutionStatus)
				require.Equal(t, "released", response.Call.SettlementStatus)
				require.Equal(t, 1000, buyer.Quota)
			}
			// A modified definition cannot become executable through a stale review.
			tool.Description = "Changed upstream"
			server.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.Add(1)
				return nil, nil
			})
			input.RequestKey = "two"
			_, err = remote.execute(ctx, input)
			require.ErrorIs(t, err, ErrMarketRemoteChanged)
			require.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestToolMarketRemoteNetworkAndSchemaBoundaries(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.1.1", "198.19.1.2", "192.0.2.1", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "64:ff9b::7f00:1", "2002:7f00:1::"} {
		require.False(t, marketPublicIP(netip.MustParseAddr(address)), address)
	}
	require.True(t, marketPublicIP(netip.MustParseAddr("8.8.8.8")))
	for _, endpoint := range []string{"http://example.com/mcp", "https://127.0.0.1/mcp", "https://u:p@example.com/mcp", "https://example.com:8080/mcp", "https://example.com/mcp?token=secret", "https://example.com/mcp#fragment"} {
		_, err := marketRemoteURL(endpoint)
		require.Error(t, err, endpoint)
	}
	for _, schema := range []string{`{"type":"object","$ref":"https://example.com/schema"}`, `{"type":"object","$id":"https://example.com/"}`, `{"type":"object","$ref":"#"}`, `{"type":"object","$schema":"unknown"}`} {
		_, err := marketSchema([]byte(schema))
		require.Error(t, err, schema)
	}
	_, err := marketSchema([]byte(`{"type":"object","properties":{"q":{"$ref":"#/$defs/q"}},"$defs":{"q":{"type":"string"}}}`))
	require.NoError(t, err)
	client := newMarketRemoteHTTPClient()
	require.Error(t, client.CheckRedirect(&http.Request{}, nil))
	body := &marketLimitedBody{ReadCloser: io.NopCloser(strings.NewReader("12345")), remaining: 4}
	_, err = io.ReadAll(body)
	require.ErrorIs(t, err, ErrMarketRemoteResult)
}
