package service_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/toolmarketfixture"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

func TestToolMarketAdministratorOwnReviewHTTP(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		name := "sqlite"
		if postgres {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			for roleName, role := range map[string]int{"admin": common.RoleAdminUser, "root": common.RoleRootUser} {
				t.Run(roleName, func(t *testing.T) {
					h := newPaidMarketHarness(t, postgres)
					require.NoError(t, h.db.Model(&model.User{}).Where("id = ?", h.users["author"].Id).Update("role", role).Error)
					var calls atomic.Int32
					options := toolmarketfixture.Options{OnCall: func(string) { calls.Add(1) }}
					remote := httptest.NewTLSServer(toolmarketfixture.Handler(toolmarketfixture.NewServer(options), options))
					t.Cleanup(remote.Close)
					target, err := url.Parse(remote.URL)
					require.NoError(t, err)
					t.Cleanup(service.ReplaceToolMarketRemoteTransportForTest(paidMarketTLSTransport{base: remote.Client().Transport, target: target}))
					const endpoint = "https://paid-fixture.example.com/mcp"
					var tools []model.ToolMarketToolInput
					h.ok("author", http.MethodPost, "/api/tool-market/inspect", map[string]any{"endpoint": endpoint}, &tools)
					require.NotEmpty(t, tools)
					var product model.ToolMarketService
					h.ok("author", http.MethodPost, "/api/tool-market/services", model.ToolMarketDraftInput{Name: "Own reviewed MCP", ExecutionType: "remote", Visibility: "public", Endpoint: endpoint, Tools: tools[:1]}, &product)
					path := "/api/tool-market/services/" + product.ID
					status, _ := h.request("buyer", http.MethodGet, path+"/draft", nil)
					require.Equal(t, http.StatusNotFound, status, "another publisher's draft stays private")
					h.ok("author", http.MethodPost, path+"/validate", nil, nil)
					h.ok("author", http.MethodPost, path+"/submit", map[string]any{"version_id": product.DraftVersionID}, nil)
					input := map[string]any{"version_id": product.DraftVersionID, "approve": true, "note": "Trusted validator checked my exact version"}
					status, _ = h.request("buyer", http.MethodPost, path+"/review", input)
					require.Equal(t, http.StatusForbidden, status)
					var review model.ToolMarketDetail
					h.ok("author", http.MethodGet, path+"/review", nil, &review)
					require.Equal(t, product.ID, review.Service.ID)
					require.Equal(t, product.DraftVersionID, review.Version.ID)
					require.Equal(t, "pending", review.Version.Status)
					h.ok("author", http.MethodPost, path+"/review", input, nil)
					require.Zero(t, calls.Load(), "review only discovers definitions; no business tool executes")
					var published model.ToolMarketDetail
					h.ok("buyer", http.MethodGet, path, nil, &published)
					require.Equal(t, product.DraftVersionID, published.Version.ID)
					require.Equal(t, "published", published.Version.Status)
					require.Equal(t, h.users["author"].Id, published.Version.ReviewedBy)
					require.Empty(t, published.Service.DraftVersionID)
				})
			}
		})
	}
}
