package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/toolmarketfixture"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/router"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The only test seam replaces the final public-host network hop. All controller,
// dashboard authentication, market token, MCP, schema and billing code is real.
// The TLS client trusts only httptest's certificate; no SSL/SSRF exception exists
// in a production build. These tests never access an external paid service.
type paidMarketTLSTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (transport paidMarketTLSTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.Host != "paid-fixture.example.com" || request.URL.Path != "/mcp" {
		return nil, fmt.Errorf("unexpected fixture destination")
	}
	clone := request.Clone(request.Context())
	target := *request.URL
	target.Scheme, target.Host = transport.target.Scheme, transport.target.Host
	clone.URL, clone.Host = &target, transport.target.Host
	return transport.base.RoundTrip(clone)
}

type paidMarketBearerTransport struct {
	base  http.RoundTripper
	token string
}

func (transport paidMarketBearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(clone)
}

type paidMarketHarness struct {
	t      *testing.T
	db     *gorm.DB
	server *httptest.Server
	users  map[string]model.User
	tokens map[string]string
}

func newPaidMarketHarness(t *testing.T, usePostgres ...bool) *paidMarketHarness {
	t.Helper()
	postgresEnabled := len(usePostgres) > 0 && usePostgres[0]
	if postgresEnabled && (strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN")) == "" || os.Getenv("TEST_POSTGRES_ISOLATED_SCHEMA") != "1") {
		t.Skip("set TEST_POSTGRES_DSN and TEST_POSTGRES_ISOLATED_SCHEMA=1 for isolated multi-connection PostgreSQL qualification")
	}
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis := common.RedisEnabled
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousGlobal, previousCritical, previousSecret := common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.SessionSecret
	previousGin := gin.Mode()
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedis
		common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.SessionSecret = previousGlobal, previousCritical, previousSecret
		common.SetDatabaseTypes(previousMain, previousLog)
		gin.SetMode(previousGin)
	})
	common.RedisEnabled = false
	common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable = false, false
	common.SessionSecret = "isolated-market-paid-http-session-test"
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	gin.SetMode(gin.TestMode)
	var db *gorm.DB
	var err error
	if postgresEnabled {
		common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
		db = openPaidMarketPostgres(t)
	} else {
		db, err = gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
	}
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	if postgresEnabled {
		pool.SetMaxOpenConns(16)
	} else {
		pool.SetMaxOpenConns(1)
	}
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Log{}, &model.Option{}, &model.ModerationJob{}, &model.ToolMarketService{}, &model.ToolMarketVersion{}, &model.ToolMarketTool{}, &model.ToolMarketToolVersion{}, &model.ToolMarketAccess{}, &model.ToolMarketFavorite{}, &model.ToolMarketInstallation{}, &model.ToolMarketGrant{}, &model.ToolMarketBudget{}, &model.ToolMarketCall{}, &model.ToolMarketTransfer{}, &model.ToolMarketEvent{}, &model.ToolMarketConfig{}, &model.ToolMarketResult{}, &model.ToolMarketToken{}, &model.ToolMarketCredential{}))
	harness := &paidMarketHarness{t: t, db: db, users: map[string]model.User{}, tokens: map[string]string{}}
	for _, name := range []string{"buyer", "author", "outsider", "root"} {
		role, quota := common.RoleCommonUser, 0
		if name == "root" {
			role = common.RoleRootUser
		}
		if name == "buyer" {
			quota = 2000
		}
		user := model.User{Username: "paid-test-" + name, AffCode: "paid-test-" + name, Group: "default", Role: role, Status: common.UserStatusEnabled, Quota: quota, AuthVersion: 1}
		require.NoError(t, db.Create(&user).Error)
		bundle, err := service.CreateLoginSession(user.Id, "password", "192.0.2.111", "market-paid-e2e")
		require.NoError(t, err)
		harness.users[name], harness.tokens[name] = user, bundle.AccessToken
	}
	engine := gin.New()
	router.SetApiRouter(engine)
	router.SetToolMarketMCPRouter(engine)
	harness.server = httptest.NewServer(engine)
	t.Cleanup(func() {
		harness.server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, middleware.WaitAdminAudits(ctx))
	})
	harness.ok("root", http.MethodPut, "/api/tool-market/config", model.ToolMarketConfig{Enabled: true, FeeBPS: 1000, RecipientID: harness.users["root"].Id}, nil)
	harness.ok("buyer", http.MethodPut, "/api/tool-market/budgets", map[string]any{"scope": "account", "scope_id": "", "limit_quota": 600}, nil)
	return harness
}

func openPaidMarketPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	base, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	basePool, err := base.DB()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, basePool.PingContext(ctx))
	schema := fmt.Sprintf("lmm_market_paid_%d_%d", os.Getpid(), time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	require.NoError(t, base.WithContext(ctx).Exec("CREATE SCHEMA "+quoted).Error)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, base.WithContext(ctx).Exec("DROP SCHEMA IF EXISTS "+quoted+" CASCADE").Error)
		require.NoError(t, basePool.Close())
	})
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		dsn = parsed.String()
	} else {
		dsn += " search_path=" + schema
	}
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	return db
}

func (harness *paidMarketHarness) request(user, method, path string, input any) (int, []byte) {
	harness.t.Helper()
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		require.NoError(harness.t, err)
	}
	request, err := http.NewRequest(method, harness.server.URL+path, bytes.NewReader(body))
	require.NoError(harness.t, err)
	request.Header.Set("Content-Type", "application/json")
	if user != "" {
		request.Header.Set("Authorization", "Bearer "+harness.tokens[user])
	}
	response, err := harness.server.Client().Do(request)
	require.NoError(harness.t, err)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	require.NoError(harness.t, err)
	return response.StatusCode, data
}

func (harness *paidMarketHarness) ok(user, method, path string, input, output any) {
	harness.t.Helper()
	status, data := harness.request(user, method, path, input)
	require.Equal(harness.t, http.StatusOK, status, "%s %s: %s", method, path, data)
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	require.NoError(harness.t, json.Unmarshal(data, &envelope), string(data))
	require.True(harness.t, envelope.Success, string(data))
	if output != nil {
		require.NoError(harness.t, json.Unmarshal(envelope.Data, output), string(data))
	}
}

func (harness *paidMarketHarness) balance(user string) int {
	harness.t.Helper()
	var record model.User
	require.NoError(harness.t, harness.db.First(&record, harness.users[user].Id).Error)
	return record.Quota
}

func (harness *paidMarketHarness) connect(token string) *mcp.ClientSession {
	harness.t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "market-paid-client", Version: "1"}, &mcp.ClientOptions{MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}})
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: harness.server.URL + "/mcp/market", HTTPClient: &http.Client{Transport: paidMarketBearerTransport{base: harness.server.Client().Transport, token: token}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(harness.t, err)
	harness.t.Cleanup(func() { _ = session.Close() })
	return session
}

func paidMarketToolName(tool model.ToolMarketToolVersion) string {
	return "market_tool_" + strings.ReplaceAll(tool.ToolID, "-", "")
}

func TestToolMarketPaidHTTPMCPUploadAndSettlement(t *testing.T) {
	runPaidMarketHTTPMCPUploadAndSettlement(t, false)
}

func TestToolMarketPaidHTTPMCPUploadAndSettlementPostgres(t *testing.T) {
	runPaidMarketHTTPMCPUploadAndSettlement(t, true)
}

func runPaidMarketHTTPMCPUploadAndSettlement(t *testing.T, postgres bool) {
	harness := newPaidMarketHarness(t, postgres)
	var executions atomic.Int32
	options := toolmarketfixture.Options{OnCall: func(string) { executions.Add(1) }}
	upstream := toolmarketfixture.NewServer(options)
	remote := httptest.NewTLSServer(toolmarketfixture.Handler(upstream, options))
	t.Cleanup(remote.Close)
	target, err := url.Parse(remote.URL)
	require.NoError(t, err)
	restore := service.ReplaceToolMarketRemoteTransportForTest(paidMarketTLSTransport{base: remote.Client().Transport, target: target})
	t.Cleanup(restore)
	endpoint := "https://paid-fixture.example.com/mcp"
	var inputs []model.ToolMarketToolInput
	harness.ok("author", http.MethodPost, "/api/tool-market/inspect", map[string]any{"endpoint": endpoint}, &inputs)
	require.Len(t, inputs, 9)
	for i := range inputs {
		inputs[i].PriceQuota = 100
		inputs[i].Permissions = []string{"network", "read"}
	}
	var product model.ToolMarketService
	harness.ok("author", http.MethodPost, "/api/tool-market/services", model.ToolMarketDraftInput{Name: "Paid MCP TEST fixture", Description: "Isolated fixed-price test; no real models or payments.", ExecutionType: "remote", Visibility: "shared", AllowedUsers: []int{harness.users["buyer"].Id}, Endpoint: endpoint, Tools: inputs}, &product)
	path := "/api/tool-market/services/" + product.ID
	harness.ok("author", http.MethodPost, path+"/validate", nil, nil)
	harness.ok("author", http.MethodPost, path+"/submit", map[string]any{"version_id": product.DraftVersionID}, nil)
	harness.ok("root", http.MethodPost, path+"/review", map[string]any{"version_id": product.DraftVersionID, "approve": true, "note": "isolated TLS MCP fixture verified"}, nil)
	require.Zero(t, executions.Load(), "inspect/validate/review must not execute business tools")
	var detail model.ToolMarketDetail
	harness.ok("buyer", http.MethodGet, path, nil, &detail)
	status, _ := harness.request("outsider", http.MethodGet, path, nil)
	require.Equal(t, http.StatusNotFound, status, "unshared users cannot discover this paid fixture")
	status, _ = harness.request("", http.MethodGet, path, nil)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = harness.request("outsider", http.MethodGet, path+"/draft", nil)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = harness.request("buyer", http.MethodPost, path+"/review", map[string]any{"approve": true, "version_id": product.DraftVersionID})
	require.Equal(t, http.StatusForbidden, status, "a buyer cannot self-approve a paid upload")
	tools := map[string]model.ToolMarketToolVersion{}
	for _, tool := range detail.Tools {
		tools[tool.Name] = tool
	}
	var token struct {
		Token  string                `json:"token"`
		Record model.ToolMarketToken `json:"record"`
	}
	harness.ok("buyer", http.MethodPost, "/api/tool-market/tokens", map[string]any{"client_id": "paid-test-client", "can_invoke": true, "can_manage": true, "expires_at": common.GetTimestamp() + 3600}, &token)
	session := harness.connect(token.Token)
	list, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	managementNames := []string{"metamcp", "lmm_market_search", "lmm_market_details", "lmm_market_call_status", "lmm_market_load"}
	assertListedTools := func(list *mcp.ListToolsResult, grantedNames ...string) {
		t.Helper()
		expected := append([]string(nil), managementNames...)
		for _, name := range grantedNames {
			expected = append(expected, paidMarketToolName(tools[name]))
		}
		var names []string
		for _, descriptor := range list.Tools {
			names = append(names, descriptor.Name)
			if descriptor.Name == "metamcp" {
				require.Contains(t, descriptor.Description, "Management operations are free")
			}
		}
		require.ElementsMatch(t, expected, names, "only default free management and this client's exact granted tools may be exposed")
	}
	assertListedTools(list)
	metaStatus, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "metamcp", Arguments: map[string]any{"action": "status"}})
	require.NoError(t, err)
	require.False(t, metaStatus.IsError)
	encodedStatus, err := json.Marshal(metaStatus.StructuredContent)
	require.NoError(t, err)
	var freeStatus struct {
		Free bool `json:"free"`
	}
	require.NoError(t, json.Unmarshal(encodedStatus, &freeStatus))
	require.True(t, freeStatus.Free, "default MetaMCP reports a free management operation")
	require.Equal(t, 2000, harness.balance("buyer"), "default management never charges the buyer")
	require.Zero(t, executions.Load(), "default management does not execute a paid remote tool")
	grants := map[string]model.ToolMarketGrant{}
	for _, name := range []string{"fixture_echo", "fixture_image", "fixture_fail", "fixture_invalid_output", "fixture_empty", "fixture_pending", "fixture_unknown", "fixture_slow_echo"} {
		tool := tools[name]
		harness.ok("buyer", http.MethodPut, "/api/tool-market/installations", map[string]any{"client_id": token.Record.ClientID, "tool_id": tool.ToolID, "version_id": tool.VersionID, "loaded": true}, nil)
		if name == "fixture_echo" {
			list, err = session.ListTools(context.Background(), nil)
			require.NoError(t, err)
			assertListedTools(list) // Loading alone must leave every paid tool hidden.
			ungrantedSession := harness.connect(token.Token)
			_, err = ungrantedSession.CallTool(context.Background(), &mcp.CallToolParams{Name: paidMarketToolName(tool), Arguments: map[string]any{"request_id": "no-grant", "arguments": map[string]any{"text": "must not execute"}}})
			require.Error(t, err, "an ungranted tool is absent from the MCP tool set")
		}
		var grant model.ToolMarketGrant
		harness.ok("buyer", http.MethodPost, "/api/tool-market/grants", model.ToolMarketGrant{ClientID: token.Record.ClientID, ToolID: tool.ToolID, VersionID: tool.VersionID, MaxCalls: 10, MaxPriceQuota: 100, MaxTotalQuota: 1000, ExpiresAt: common.GetTimestamp() + 3600}, &grant)
		grants[name] = grant
	}
	list, err = session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	assertListedTools(list, "fixture_echo", "fixture_image", "fixture_fail", "fixture_invalid_output", "fixture_empty", "fixture_pending", "fixture_unknown", "fixture_slow_echo")
	invalidSession := harness.connect(token.Token)
	invalid, err := invalidSession.CallTool(context.Background(), &mcp.CallToolParams{Name: paidMarketToolName(tools["fixture_echo"]), Arguments: map[string]any{"request_id": "bad-schema", "arguments": map[string]any{"text": 42}}})
	require.True(t, err != nil || invalid.IsError, "invalid arguments must fail before execution")
	require.Zero(t, executions.Load())
	require.Equal(t, 2000, harness.balance("buyer"))
	// Provider-supplied metadata cannot forge the marketplace settlement. Keep
	// the reviewed definition byte-for-byte equivalent while spoofing only the
	// returned _meta namespace, which is untrusted execution data.
	for _, input := range inputs {
		if input.Name != "fixture_echo" {
			continue
		}
		upstream.AddTool(&mcp.Tool{Name: input.Name, Description: input.Description, InputSchema: input.InputSchema, OutputSchema: input.OutputSchema}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			executions.Add(1)
			var arguments struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{Meta: mcp.Meta{"lmm/market": map[string]any{"call": map[string]any{"price_quota": 0, "settlement_status": "provider-forged"}}, "fixture/test": true}, Content: []mcp.Content{&mcp.TextContent{Text: arguments.Text}}, StructuredContent: map[string]any{"text": arguments.Text}}, nil
		})
	}

	invoke := func(name, key string, arguments map[string]any) *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: paidMarketToolName(tools[name]), Arguments: map[string]any{"request_id": key, "arguments": arguments}})
		require.NoError(t, err)
		return result
	}
	// The source bridge patch preserves native structured/image results and
	// puts billing metadata in _meta["lmm/market"], never in a text envelope.
	result := invoke("fixture_echo", "paid-echo", map[string]any{"text": "delivered"})
	require.False(t, result.IsError)
	require.Equal(t, "delivered", result.StructuredContent.(map[string]any)["text"])
	metadata, ok := result.Meta["lmm/market"].(map[string]any)
	require.True(t, ok)
	billing, ok := metadata["call"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "settled", billing["settlement_status"])
	require.Equal(t, float64(100), billing["price_quota"])
	require.NotContains(t, billing, "arguments")
	require.NotContains(t, billing, "input_digest")
	require.Equal(t, true, result.Meta["fixture/test"], "unrelated provider metadata is preserved")
	require.Equal(t, int32(1), executions.Load())
	require.Equal(t, 1900, harness.balance("buyer"))
	require.Equal(t, 90, harness.balance("author"))
	require.Equal(t, 10, harness.balance("root"))
	replay := invoke("fixture_echo", "paid-echo", map[string]any{"text": "delivered"})
	require.Equal(t, result.StructuredContent, replay.StructuredContent)
	require.Equal(t, int32(1), executions.Load())
	conflict := invoke("fixture_echo", "paid-echo", map[string]any{"text": "changed arguments"})
	require.True(t, conflict.IsError)
	require.Equal(t, int32(1), executions.Load())

	image := invoke("fixture_image", "paid-image", map[string]any{})
	require.False(t, image.IsError)
	require.Len(t, image.Content, 1)
	pixel, ok := image.Content[0].(*mcp.ImageContent)
	require.True(t, ok, "native MCP image must reach the buyer unchanged")
	require.Equal(t, "image/png", pixel.MIMEType)
	require.NotEmpty(t, pixel.Data)
	require.Equal(t, 1800, harness.balance("buyer"))
	failed := invoke("fixture_fail", "paid-failure", map[string]any{})
	require.True(t, failed.IsError)
	require.Equal(t, 1800, harness.balance("buyer"), "business errors must not charge")
	for _, name := range []string{"fixture_invalid_output", "fixture_empty"} {
		invalid := invoke(name, "paid-"+name, map[string]any{})
		require.True(t, invalid.IsError)
		require.Equal(t, 1800, harness.balance("buyer"), "invalid/empty final results must not charge")
	}

	for _, name := range []string{"fixture_pending", "fixture_unknown"} {
		before := executions.Load()
		invoke(name, "paid-"+name, map[string]any{})
		var call model.ToolMarketCall
		require.NoError(t, harness.db.Where("tool_id = ?", tools[name].ToolID).First(&call).Error)
		require.Equal(t, "unknown", call.ExecutionStatus)
		require.Equal(t, "held", call.SettlementStatus)
		require.Equal(t, 1700, harness.balance("buyer"))
		invoke(name, "paid-"+name, map[string]any{})
		require.Equal(t, before+1, executions.Load(), "unknown calls are never automatically re-executed")
		require.NoError(t, harness.db.Model(&model.ToolMarketCall{}).Where("id = ?", call.ID).Update("resolve_by", common.GetTimestamp()-1).Error)
		processed, err := model.RecoverToolMarketCalls(context.Background())
		require.NoError(t, err)
		require.Equal(t, 1, processed)
		require.Equal(t, 1800, harness.balance("buyer"))
	}

	// A real network call remains in flight while multiple clients replay it.
	// The database's request key, not a test-side lock, grants execution once.
	var wait sync.WaitGroup
	results := make(chan error, 8)
	before := executions.Load()
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: paidMarketToolName(tools["fixture_slow_echo"]), Arguments: map[string]any{"request_id": "concurrent-replay", "arguments": map[string]any{"text": "once", "delay_ms": 150}}})
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.Equal(t, before+1, executions.Load())
	require.Equal(t, 1700, harness.balance("buyer"))
	require.Equal(t, 270, harness.balance("author"))
	require.Equal(t, 30, harness.balance("root"))
	var count int64
	require.NoError(t, harness.db.Model(&model.ToolMarketTransfer{}).Count(&count).Error)
	require.EqualValues(t, 6, count, "each successful call has exactly one author and one fee transfer")
	var budget model.ToolMarketBudget
	require.NoError(t, harness.db.Where("user_id = ? AND scope = ?", harness.users["buyer"].Id, "account").First(&budget).Error)
	require.Equal(t, 300, budget.SpentQuota)
	require.Zero(t, budget.ReservedQuota)
	var grant model.ToolMarketGrant
	require.NoError(t, harness.db.First(&grant, "id = ?", grants["fixture_echo"].ID).Error)
	require.Equal(t, 1, grant.SuccessfulCalls)
	require.Zero(t, grant.ReservedCalls)

	// Different request IDs race for a one-call grant, then for the remainder
	// of the account budget. PostgreSQL uses sixteen connections here, so row
	// locking and atomic settlement are tested instead of SQLite serialization.
	adder := tools["fixture_add"]
	harness.ok("buyer", http.MethodPut, "/api/tool-market/installations", map[string]any{"client_id": token.Record.ClientID, "tool_id": adder.ToolID, "version_id": adder.VersionID, "loaded": true}, nil)
	harness.ok("buyer", http.MethodPost, "/api/tool-market/grants", model.ToolMarketGrant{ClientID: token.Record.ClientID, ToolID: adder.ToolID, VersionID: adder.VersionID, MaxCalls: 1, MaxPriceQuota: 100, MaxTotalQuota: 100, ExpiresAt: common.GetTimestamp() + 3600}, nil)
	raceDistinct := func(name string, n int, prefix string, arguments map[string]any) int {
		t.Helper()
		var successes atomic.Int32
		var group sync.WaitGroup
		failures := make(chan error, n)
		for i := 0; i < n; i++ {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: paidMarketToolName(tools[name]), Arguments: map[string]any{"request_id": fmt.Sprintf("%s-%d", prefix, index), "arguments": arguments}})
				if err == nil && !result.IsError {
					successes.Add(1)
				}
				failures <- err
			}(i)
		}
		group.Wait()
		close(failures)
		for err := range failures {
			require.NoError(t, err)
		}
		return int(successes.Load())
	}
	before = executions.Load()
	require.Equal(t, 1, raceDistinct("fixture_add", 8, "grant-cap", map[string]any{"a": 20, "b": 22}))
	require.Equal(t, before+1, executions.Load())
	require.Equal(t, 1600, harness.balance("buyer"))
	before = executions.Load()
	require.Equal(t, 2, raceDistinct("fixture_slow_echo", 8, "budget-cap", map[string]any{"text": "budget-limited", "delay_ms": 30}))
	require.Equal(t, before+2, executions.Load())
	require.Equal(t, 1400, harness.balance("buyer"))
	require.Equal(t, 540, harness.balance("author"))
	require.Equal(t, 60, harness.balance("root"))
	budget = model.ToolMarketBudget{}
	require.NoError(t, harness.db.Where("user_id = ? AND scope = ?", harness.users["buyer"].Id, "account").First(&budget).Error)
	require.Equal(t, 600, budget.SpentQuota)
	require.Zero(t, budget.ReservedQuota)
	require.NoError(t, harness.db.Model(&model.ToolMarketTransfer{}).Count(&count).Error)
	require.EqualValues(t, 12, count)
	var mismatchedLedgers int64
	require.NoError(t, harness.db.Raw("SELECT COUNT(*) FROM (SELECT call_id FROM tool_market_transfers GROUP BY call_id HAVING COUNT(*) <> 2 OR SUM(quota) <> 100) AS invalid_ledger").Scan(&mismatchedLedgers).Error)
	require.Zero(t, mismatchedLedgers)

	// Another user/client cannot reuse this caller's grant or read its results.
	var call model.ToolMarketCall
	require.NoError(t, harness.db.Where("tool_id = ?", tools["fixture_echo"].ToolID).First(&call).Error)
	status, _ = harness.request("author", http.MethodGet, "/api/tool-market/calls/"+call.ID+"/result", nil)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = harness.request("outsider", http.MethodPost, "/api/tool-market/invoke", map[string]any{"tool_id": tools["fixture_echo"].ToolID, "version_id": tools["fixture_echo"].VersionID, "grant_id": grants["fixture_echo"].ID, "request_id": "stolen-grant", "arguments": map[string]any{"text": "blocked"}, "user_id": harness.users["buyer"].Id, "client_id": token.Record.ClientID})
	require.NotEqual(t, http.StatusOK, status)
	require.Equal(t, 1400, harness.balance("buyer"))
	var otherToken struct {
		Token  string                `json:"token"`
		Record model.ToolMarketToken `json:"record"`
	}
	harness.ok("buyer", http.MethodPost, "/api/tool-market/tokens", map[string]any{"client_id": "another-client", "can_invoke": true, "can_manage": true, "expires_at": common.GetTimestamp() + 3600}, &otherToken)
	otherSession := harness.connect(otherToken.Token)
	list, err = otherSession.ListTools(context.Background(), nil)
	require.NoError(t, err)
	assertListedTools(list) // Another client must not inherit the buyer's paid grants.
	foreign, err := otherSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "lmm_market_call_status", Arguments: map[string]any{"call_id": call.ID}})
	require.NoError(t, err)
	require.True(t, foreign.IsError)
	require.NoError(t, harness.db.Model(&model.ToolMarketToken{}).Where("id = ?", otherToken.Record.ID).Update("expires_at", common.GetTimestamp()-1).Error)
	_, err = otherSession.ListTools(context.Background(), nil)
	require.Error(t, err, "expired MCP credential must fail on the existing session")

	// A changed remote description invalidates the reviewed fingerprint before
	// any new hold or business call, without modifying published snapshots.
	upstream.AddTool(&mcp.Tool{Name: "fixture_echo", Description: "Changed after review", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		executions.Add(1)
		return nil, nil
	})
	before = executions.Load()
	drift := invoke("fixture_echo", "definition-drift", map[string]any{"text": "blocked"})
	require.True(t, drift.IsError)
	require.Equal(t, before, executions.Load())
	require.Equal(t, 1400, harness.balance("buyer"))
	require.NoError(t, harness.db.Model(&model.ToolMarketGrant{}).Where("id = ?", grants["fixture_image"].ID).Update("expires_at", common.GetTimestamp()-1).Error)
	list, err = session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	for _, tool := range list.Tools {
		require.NotEqual(t, paidMarketToolName(tools["fixture_image"]), tool.Name, "expired grants remove executable tools")
	}

	harness.ok("buyer", http.MethodDelete, "/api/tool-market/grants/"+grants["fixture_echo"].ID, nil, nil)
	list, err = session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	for _, tool := range list.Tools {
		require.NotEqual(t, paidMarketToolName(tools["fixture_echo"]), tool.Name)
	}
	revokedSession := harness.connect(token.Token)
	_, err = revokedSession.CallTool(context.Background(), &mcp.CallToolParams{Name: paidMarketToolName(tools["fixture_echo"]), Arguments: map[string]any{"request_id": "after-revoke", "arguments": map[string]any{"text": "blocked"}}})
	require.Error(t, err)
	harness.ok("buyer", http.MethodDelete, "/api/tool-market/tokens/"+token.Record.ID, nil, nil)
	_, err = session.ListTools(context.Background(), nil)
	require.Error(t, err, "revoked MCP credentials must fail on the existing session")
	var allCalls []model.ToolMarketCall
	require.NoError(t, harness.db.Find(&allCalls).Error)
	require.Len(t, allCalls, 11)
	for _, call := range allCalls {
		require.NotEqual(t, "held", call.SettlementStatus)
	}
}
