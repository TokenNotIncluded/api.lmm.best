package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Reuse the existing isolated SQLite fixture and the real main executor,
// credential store, publication, grants, reservations and settlement. This is
// not PostgreSQL/cache or inbound OAuth acceptance. Do not run in parallel:
// marketRemoteTestDB temporarily replaces package-level database handles.
type bi09MarketFixture struct {
	db      *gorm.DB
	ctx     context.Context
	remote  *ToolMarketRemote
	fault   *bi09LostResponseTransport
	server  *mcp.Server
	tool    *mcp.Tool
	users   []model.User
	input   model.ToolMarketReserveInput
	service string
	calls   atomic.Int32
}

func newBI09MarketFixture(t *testing.T) *bi09MarketFixture {
	t.Helper()
	t.Setenv("TOOL_MARKET_ENCRYPTION_KEY", "bi09-isolated-test-encryption-key-not-a-real-secret")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	f := &bi09MarketFixture{db: marketRemoteTestDB(t), ctx: ctx}
	f.users = []model.User{
		{Username: "bi09-buyer", AffCode: "bi09-buyer", Role: 1, Status: 1, Quota: 1000},
		{Username: "bi09-author", AffCode: "bi09-author", Role: 1, Status: 1},
		{Username: "bi09-root", AffCode: "bi09-root", Role: 100, Status: 1},
		{Username: "bi09-other", AffCode: "bi09-other", Role: 1, Status: 1, Quota: 1000},
	}
	for i := range f.users {
		require.NoError(t, f.db.Create(&f.users[i]).Error)
	}
	require.NoError(t, model.SetToolMarketConfig(f.users[2].Id, model.ToolMarketConfig{Enabled: true, FeeBPS: 1000, RecipientID: f.users[2].Id}))
	f.server = mcp.NewServer(&mcp.Implementation{Name: "bi09-fixture", Version: "1"}, nil)
	f.tool = &mcp.Tool{Name: "bi09_write", Description: "Complete a fixture side effect", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}, "required": []string{"q"}, "additionalProperties": false,
	}}
	f.server.AddTool(f.tool, f.execute)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return f.server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	const secret = "bi09-provider-fixture-secret"
	httpServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer "+secret || req.Header.Get("Cookie") != "" || req.Header.Get("X-API-Key") != "" {
			http.Error(w, "fixture credential rejected", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, req)
	}))
	t.Cleanup(httpServer.Close)
	target, err := url.Parse(httpServer.URL)
	require.NoError(t, err)
	f.fault = &bi09LostResponseTransport{base: marketTestTransport{base: httpServer.Client().Transport, target: target}}
	f.remote = &ToolMarketRemote{client: &http.Client{Transport: marketResponseTransport{base: f.fault}}, slots: make(chan struct{}, 32)}
	const endpoint = "https://example.com/mcp"
	credential := &model.ToolMarketResolvedCredential{Mode: "bearer", Secret: secret}
	tools, err := f.remote.inspectAuthenticated(ctx, endpoint, credential)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	tools[0].PriceQuota = 100
	product, err := model.SaveToolMarketDraft(f.users[1].Id, "", model.ToolMarketDraftInput{Name: "BI09", ExecutionType: "remote", Visibility: "public", Endpoint: endpoint, Tools: tools})
	require.NoError(t, err)
	f.service = product.ID
	require.NoError(t, model.ConfigureToolMarketCredential(f.users[1].Id, product.ID, product.DraftVersionID, "bearer", secret, ""))
	require.NoError(t, f.remote.validate(ctx, f.users[1].Id, product.ID, false))
	require.NoError(t, model.SubmitToolMarketDraft(f.users[1].Id, product.ID, product.DraftVersionID))
	require.NoError(t, model.ReviewToolMarketVersion(f.users[2].Id, product.ID, product.DraftVersionID, true, "fixture only"))
	detail, err := model.GetToolMarketDetail(f.users[0].Id, product.ID, false)
	require.NoError(t, err)
	local := detail.Tools[0]
	require.NoError(t, model.SetToolMarketInstallation(f.users[0].Id, "bi09-client", local.ToolID, local.VersionID, true))
	grant, err := model.CreateToolMarketGrant(f.users[0].Id, model.ToolMarketGrant{ClientID: "bi09-client", ToolID: local.ToolID, VersionID: local.VersionID, MaxCalls: 10, MaxPriceQuota: 100, MaxTotalQuota: 1000, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	f.input = model.ToolMarketReserveInput{UserID: f.users[0].Id, ClientID: "bi09-client", ToolID: local.ToolID, VersionID: local.VersionID, GrantID: grant.ID, RequestKey: "bi09-request", Arguments: json.RawMessage(`{"q":"original"}`)}
	require.NoError(t, model.SetToolMarketBudget(f.users[0].Id, "account", "", 1000))
	require.NoError(t, model.SetToolMarketBudget(f.users[0].Id, "client", f.input.ClientID, 1000))
	require.NoError(t, model.SetToolMarketBudget(f.users[0].Id, "tool", f.input.ToolID, 1000))
	var stored model.ToolMarketCredential
	require.NoError(t, f.db.First(&stored, "version_id = ?", local.VersionID).Error)
	require.Equal(t, f.users[1].Id, stored.OwnerID)
	require.Equal(t, product.ID, stored.ServiceID)
	require.NotContains(t, stored.Ciphertext, secret)
	_, err = model.ResolveToolMarketCredential(f.users[0].Id, product.ID, local.VersionID, endpoint)
	require.Error(t, err, "buyer must not resolve the author's credential")
	require.Zero(t, f.calls.Load(), "discovery, review and installation must not execute the tool")
	return f
}

func (f *bi09MarketFixture) execute(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	f.calls.Add(1)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "bi09-effect-complete"}}}, nil
}

// Check persisted amounts, not only response status or HTTP success. The same
// allowance must be reflected in every configured budget and the exact grant.
func (f *bi09MarketFixture) money(t *testing.T, held, charged int) {
	t.Helper()
	want := []int{f.users[0].Quota - held - charged, charged * 9 / 10, charged / 10, 1000}
	for i, user := range f.users {
		var stored model.User
		require.NoError(t, f.db.First(&stored, user.Id).Error)
		require.Equal(t, want[i], stored.Quota, "wallet %d", i)
	}
	var grant model.ToolMarketGrant
	require.NoError(t, f.db.First(&grant, "id = ?", f.input.GrantID).Error)
	require.Equal(t, held, grant.ReservedQuota)
	require.Equal(t, charged, grant.SpentQuota)
	require.Equal(t, held/100, grant.ReservedCalls)
	require.Equal(t, charged/100, grant.SuccessfulCalls)
	var budgets []model.ToolMarketBudget
	require.NoError(t, f.db.Where("user_id = ?", f.input.UserID).Find(&budgets).Error)
	require.Len(t, budgets, 3)
	for _, budget := range budgets {
		require.Equal(t, held, budget.ReservedQuota, budget.Scope)
		require.Equal(t, charged, budget.SpentQuota, budget.Scope)
	}
	var transfers []model.ToolMarketTransfer
	require.NoError(t, f.db.Order("kind").Find(&transfers).Error)
	if charged == 0 {
		require.Empty(t, transfers)
	} else {
		require.Len(t, transfers, 2)
		for i, transfer := range transfers {
			require.Equal(t, f.input.UserID, transfer.FromUserID)
			require.Equal(t, f.users[i+1].Id, transfer.ToUserID)
			require.Equal(t, []string{"author", "platform"}[i], transfer.Kind)
			require.Equal(t, want[i+1], transfer.Quota)
			require.Equal(t, transfer.CallID+":"+transfer.Kind, transfer.ID)
		}
	}
	t.Logf("money: held=%d charged=%d author=%d platform=%d transfers=%d upstream_calls=%d", held, charged, want[1], want[2], len(transfers), f.calls.Load())
}

func (f *bi09MarketFixture) oneCall(t *testing.T, id, execution, settlement string, results int64) {
	t.Helper()
	var calls []model.ToolMarketCall
	require.NoError(t, f.db.Find(&calls).Error)
	require.Len(t, calls, 1)
	call := calls[0]
	require.Equal(t, id, call.ID)
	require.Equal(t, f.input.UserID, call.UserID)
	require.Equal(t, f.input.ClientID, call.ClientID)
	require.Equal(t, f.input.GrantID, call.GrantID)
	require.Equal(t, f.input.VersionID, call.VersionID)
	require.Equal(t, f.input.ToolID, call.ToolID)
	require.Equal(t, f.service, call.ServiceID)
	require.Equal(t, f.users[1].Id, call.OwnerID)
	require.Equal(t, f.users[2].Id, call.RecipientID)
	require.Equal(t, execution, call.ExecutionStatus)
	require.Equal(t, settlement, call.SettlementStatus)
	require.Equal(t, 100, call.PriceQuota)
	require.Equal(t, 1000, call.FeeBPS)
	var outcomes []model.ToolMarketResult
	require.NoError(t, f.db.Find(&outcomes).Error)
	require.Equal(t, results, int64(len(outcomes)))
	for _, outcome := range outcomes {
		require.Equal(t, id, outcome.CallID)
		require.Equal(t, f.input.UserID, outcome.UserID)
		require.True(t, outcome.Success)
		require.NotContains(t, string(outcome.Data), "bi09-provider-fixture-secret")
	}
	var transfers []model.ToolMarketTransfer
	require.NoError(t, f.db.Find(&transfers).Error)
	for _, transfer := range transfers {
		require.Equal(t, id, transfer.CallID)
	}
}

func TestToolMarketBI09CompletedResponseLostIsNotReexecuted(t *testing.T) {
	f := newBI09MarketFixture(t)
	f.fault.enabled.Store(true)
	response, err := f.remote.execute(f.ctx, f.input)
	require.NoError(t, err)
	require.NotNil(t, response)
	require.NotNil(t, response.Call)
	require.Equal(t, int32(1), f.fault.dropped.Load(), "fault must occur after a successful upstream result")
	id := response.Call.ID
	f.oneCall(t, id, "unknown", "held", 0)
	f.money(t, 100, 0)
	for range 3 {
		replay, err := f.remote.execute(f.ctx, f.input)
		require.NoError(t, err)
		require.Equal(t, id, replay.Call.ID)
	}
	processed, err := model.RecoverToolMarketCalls(f.ctx)
	require.NoError(t, err)
	require.Zero(t, processed, "an unexpired unknown call has no outcome to settle")
	require.Equal(t, int32(1), f.calls.Load())
	require.Equal(t, int32(1), f.fault.dropped.Load())
	// Exercise the existing expiry policy without sleeping or inventing a
	// failed upstream operation. Released money is not permission to replay.
	require.NoError(t, f.db.Model(&model.ToolMarketCall{}).Where("id = ?", id).Update("resolve_by", common.GetTimestamp()-1).Error)
	processed, err = model.RecoverToolMarketCalls(f.ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	_, err = f.remote.execute(f.ctx, f.input)
	require.NoError(t, err)
	f.oneCall(t, id, "unknown", "released", 0)
	f.money(t, 0, 0)
	require.Equal(t, int32(1), f.calls.Load())
}

func TestToolMarketBI09SettlementFailureRetriesOnlySettlement(t *testing.T) {
	f := newBI09MarketFixture(t)
	injected := errors.New("BI09 fixture rejects the platform transfer")
	const callback = "bi09:fail-platform-transfer"
	var fail atomic.Bool
	var faults atomic.Int32
	fail.Store(true)
	require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		transfer, ok := tx.Statement.Dest.(*model.ToolMarketTransfer)
		if ok && transfer.Kind == "platform" && fail.Load() {
			faults.Add(1)
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { _ = f.db.Callback().Create().Remove(callback) })
	response, err := f.remote.execute(f.ctx, f.input)
	require.NoError(t, err)
	require.NotNil(t, response)
	require.NotNil(t, response.Call)
	require.Equal(t, "TOOL_MARKET_SETTLEMENT_PENDING", response.ErrorCode)
	require.Contains(t, string(response.Result), "bi09-effect-complete")
	require.Positive(t, faults.Load(), "a passing test must actually inject a settlement failure")
	id := response.Call.ID
	f.oneCall(t, id, "running", "held", 1)
	f.money(t, 100, 0) // The earlier author credit must also roll back.
	_, err = f.remote.execute(f.ctx, f.input)
	require.NoError(t, err)
	require.Equal(t, int32(1), f.calls.Load())
	processed, err := model.RecoverToolMarketCalls(f.ctx)
	require.ErrorIs(t, err, injected)
	require.Zero(t, processed)
	f.money(t, 100, 0)
	fail.Store(false)
	processed, err = model.RecoverToolMarketCalls(f.ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	for range 3 {
		_, err = model.RecoverToolMarketCalls(f.ctx)
		require.NoError(t, err)
		replay, err := f.remote.execute(f.ctx, f.input)
		require.NoError(t, err)
		require.Equal(t, id, replay.Call.ID)
	}
	f.oneCall(t, id, "succeeded", "settled", 1)
	f.money(t, 0, 100)
	require.Equal(t, int32(1), f.calls.Load())
}

// Hold all callers at read-only discovery. They must all observe no prior
// record before any can reserve, rather than merely replaying an already
// completed call. Cancellation releases the gate if a participant fails.
type bi09DiscoveryGate struct {
	base    http.RoundTripper
	arrived atomic.Int32
	target  int32
	release chan struct{}
}

func (g *bi09DiscoveryGate) RoundTrip(req *http.Request) (*http.Response, error) {
	method, err := bi09RPCMethod(req)
	if err != nil {
		return nil, err
	}
	if method == "tools/list" {
		if g.arrived.Add(1) == g.target {
			close(g.release)
		}
		select {
		case <-g.release:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	return g.base.RoundTrip(req)
}

func TestToolMarketBI09ConcurrentReplayKeepsOneExecutionAndCharge(t *testing.T) {
	f := newBI09MarketFixture(t)
	const workers = 8
	gate := &bi09DiscoveryGate{base: f.fault, target: workers, release: make(chan struct{})}
	f.remote.client.Transport = marketResponseTransport{base: gate}
	type outcome struct {
		response *ToolMarketExecutionResponse
		err      error
	}
	results := make(chan outcome, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			response, err := f.remote.execute(f.ctx, f.input)
			results <- outcome{response, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	id := ""
	for result := range results {
		require.NoError(t, result.err)
		require.NotNil(t, result.response)
		require.NotNil(t, result.response.Call)
		if id == "" {
			id = result.response.Call.ID
		}
		require.Equal(t, id, result.response.Call.ID)
	}
	require.Equal(t, int32(workers), gate.arrived.Load())
	require.Equal(t, int32(1), f.calls.Load())
	f.oneCall(t, id, "succeeded", "settled", 1)
	f.money(t, 0, 100)
	changed := f.input
	changed.Arguments = json.RawMessage(`{"q":"different-side-effect"}`)
	_, err := f.remote.execute(f.ctx, changed)
	require.ErrorIs(t, err, model.ErrToolMarketConflict)
	for _, identity := range []struct {
		user   int
		client string
	}{{f.users[3].Id, f.input.ClientID}, {f.input.UserID, "other-installation"}} {
		_, _, err = model.GetToolMarketResult(identity.user, identity.client, id)
		require.Error(t, err, "another user or client must not read retained delivery")
	}
	f.money(t, 0, 100)
	require.Equal(t, int32(1), f.calls.Load())
}

func TestToolMarketBI09DenialPrecedesSideEffectsAndReservation(t *testing.T) {
	for _, mode := range []string{"arguments", "budget", "balance", "revoked", "expired-grant", "uninstalled", "other-user", "other-client", "other-grant", "other-version", "upstream-drift"} {
		t.Run(mode, func(t *testing.T) {
			f := newBI09MarketFixture(t)
			input := f.input
			switch mode {
			case "arguments":
				input.Arguments = json.RawMessage(`{"q":7}`)
			case "budget":
				require.NoError(t, model.SetToolMarketBudget(input.UserID, "account", "", 99))
			case "balance":
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					return model.ApplyWalletQuotaDelta(tx, input.UserID, -901)
				}))
				f.users[0].Quota = 99
			case "expired-grant":
				require.NoError(t, f.db.Model(&model.ToolMarketGrant{}).Where("id = ?", input.GrantID).Update("expires_at", common.GetTimestamp()-1).Error)
			case "other-grant":
				grant, err := model.CreateToolMarketGrant(f.users[3].Id, model.ToolMarketGrant{ClientID: input.ClientID, ToolID: input.ToolID, VersionID: input.VersionID, MaxCalls: 10, MaxPriceQuota: 100, MaxTotalQuota: 1000, ExpiresAt: common.GetTimestamp() + 3600})
				require.NoError(t, err)
				input.GrantID = grant.ID
			case "revoked":
				require.NoError(t, model.RevokeToolMarketGrant(input.UserID, input.GrantID))
			case "uninstalled":
				require.NoError(t, model.SetToolMarketInstallation(input.UserID, input.ClientID, input.ToolID, input.VersionID, false))
			case "other-user":
				input.UserID = f.users[3].Id
			case "other-client":
				input.ClientID = "not-installed"
			case "other-version":
				input.VersionID = "not-authorized-version"
			case "upstream-drift":
				f.tool.Description = "changed after publication"
				f.server.AddTool(f.tool, f.execute)
			}
			_, err := f.remote.execute(f.ctx, input)
			require.Error(t, err)
			switch mode {
			case "arguments":
				require.ErrorIs(t, err, ErrMarketRemoteInput)
			case "budget":
				require.ErrorIs(t, err, model.ErrToolMarketBudget)
			case "balance":
				require.ErrorIs(t, err, model.ErrToolMarketBalance)
			case "upstream-drift":
				require.ErrorIs(t, err, ErrMarketRemoteChanged)
			}
			require.Zero(t, f.calls.Load())
			var count int64
			require.NoError(t, f.db.Model(&model.ToolMarketCall{}).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, f.db.Model(&model.ToolMarketResult{}).Count(&count).Error)
			require.Zero(t, count)
			f.money(t, 0, 0)
		})
	}
}
