package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/toolmarketfixture"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestToolMarketPaidPostgresBuyerIsolationAndConcurrentLedger(t *testing.T) {
	harness := newPaidMarketHarness(t, true)
	for _, user := range []string{"buyer", "outsider"} {
		require.NoError(t, harness.db.Model(&model.User{}).Where("id = ?", harness.users[user].Id).Update("quota", 1000).Error)
		harness.ok(user, http.MethodPut, "/api/tool-market/budgets", map[string]any{"scope": "account", "scope_id": "", "limit_quota": 100}, nil)
	}
	var executions atomic.Int32
	options := toolmarketfixture.Options{OnCall: func(string) { executions.Add(1) }}
	remote := httptest.NewTLSServer(toolmarketfixture.Handler(toolmarketfixture.NewServer(options), options))
	t.Cleanup(remote.Close)
	target, err := url.Parse(remote.URL)
	require.NoError(t, err)
	restore := service.ReplaceToolMarketRemoteTransportForTest(paidMarketTLSTransport{base: remote.Client().Transport, target: target})
	t.Cleanup(restore)
	const endpoint = "https://paid-fixture.example.com/mcp"
	var inputs []model.ToolMarketToolInput
	harness.ok("author", http.MethodPost, "/api/tool-market/inspect", map[string]any{"endpoint": endpoint}, &inputs)
	var selected model.ToolMarketToolInput
	for _, input := range inputs {
		if input.Name == "fixture_slow_echo" {
			selected = input
		}
	}
	require.NotEmpty(t, selected.Name)
	selected.PriceQuota, selected.Permissions = 100, []string{"network", "read"}
	var product model.ToolMarketService
	harness.ok("author", http.MethodPost, "/api/tool-market/services", model.ToolMarketDraftInput{Name: "PostgreSQL paid concurrency TEST", ExecutionType: "remote", Visibility: "shared", AllowedUsers: []int{harness.users["buyer"].Id, harness.users["outsider"].Id}, Endpoint: endpoint, Tools: []model.ToolMarketToolInput{selected}}, &product)
	path := "/api/tool-market/services/" + product.ID
	harness.ok("author", http.MethodPost, path+"/validate", nil, nil)
	harness.ok("author", http.MethodPost, path+"/submit", map[string]any{"version_id": product.DraftVersionID}, nil)
	harness.ok("root", http.MethodPost, path+"/review", map[string]any{"version_id": product.DraftVersionID, "approve": true, "note": "isolated PostgreSQL concurrency test"}, nil)
	var detail model.ToolMarketDetail
	harness.ok("buyer", http.MethodGet, path, nil, &detail)
	require.Len(t, detail.Tools, 1)
	tool := detail.Tools[0]
	sessions := map[string]*mcp.ClientSession{}
	for _, user := range []string{"buyer", "outsider"} {
		const client = "same-client-name"
		harness.ok(user, http.MethodPut, "/api/tool-market/installations", map[string]any{"client_id": client, "tool_id": tool.ToolID, "version_id": tool.VersionID, "loaded": true}, nil)
		harness.ok(user, http.MethodPost, "/api/tool-market/grants", model.ToolMarketGrant{ClientID: client, ToolID: tool.ToolID, VersionID: tool.VersionID, MaxPriceQuota: 100, MaxTotalQuota: 100, MaxCalls: 1, ExpiresAt: common.GetTimestamp() + 3600}, nil)
		var token struct {
			Token string `json:"token"`
		}
		harness.ok(user, http.MethodPost, "/api/tool-market/tokens", map[string]any{"client_id": client, "can_invoke": true, "can_manage": false, "expires_at": common.GetTimestamp() + 3600}, &token)
		sessions[user] = harness.connect(token.Token)
	}
	var group sync.WaitGroup
	failures := make(chan error, 16)
	start := make(chan struct{})
	for _, user := range []string{"buyer", "outsider"} {
		for i := 0; i < 8; i++ {
			group.Add(1)
			go func(session *mcp.ClientSession) {
				defer group.Done()
				<-start
				_, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: paidMarketToolName(tool), Arguments: map[string]any{"request_id": "identical-key-for-two-users", "arguments": map[string]any{"text": "user-scoped replay", "delay_ms": 200}}})
				failures <- err
			}(sessions[user])
		}
	}
	close(start)
	group.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.Equal(t, int32(2), executions.Load(), "same request ID belongs independently to each authenticated buyer")
	require.Equal(t, 900, harness.balance("buyer"))
	require.Equal(t, 900, harness.balance("outsider"))
	require.Equal(t, 180, harness.balance("author"))
	require.Equal(t, 20, harness.balance("root"))
	var calls []model.ToolMarketCall
	require.NoError(t, harness.db.Order("user_id").Find(&calls).Error)
	require.Len(t, calls, 2)
	require.NotEqual(t, calls[0].ID, calls[1].ID)
	for _, call := range calls {
		require.Equal(t, "succeeded", call.ExecutionStatus)
		require.Equal(t, "settled", call.SettlementStatus)
	}
	var transferCount, badLedgerCount int64
	require.NoError(t, harness.db.Model(&model.ToolMarketTransfer{}).Count(&transferCount).Error)
	require.EqualValues(t, 4, transferCount)
	require.NoError(t, harness.db.Raw("SELECT COUNT(*) FROM (SELECT call_id FROM tool_market_transfers GROUP BY call_id HAVING COUNT(*) <> 2 OR SUM(quota) <> 100) AS invalid_ledger").Scan(&badLedgerCount).Error)
	require.Zero(t, badLedgerCount, "ledger transfers must commit once and sum to the held quota")
	for _, user := range []string{"buyer", "outsider"} {
		var budget model.ToolMarketBudget
		require.NoError(t, harness.db.Where("user_id = ? AND scope = ?", harness.users[user].Id, "account").First(&budget).Error)
		require.Equal(t, 100, budget.SpentQuota)
		require.Zero(t, budget.ReservedQuota)
		var grant model.ToolMarketGrant
		require.NoError(t, harness.db.Where("user_id = ?", harness.users[user].Id).First(&grant).Error)
		require.Equal(t, 1, grant.SuccessfulCalls)
		require.Zero(t, grant.ReservedCalls)
		require.Zero(t, grant.ReservedQuota)
	}
}
