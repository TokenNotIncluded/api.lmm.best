package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func TestToolMarketCredentialsReachOnlyTheirApprovedEndpoint(t *testing.T) {
	for _, mode := range []string{"bearer", "api_key"} {
		t.Run(mode, func(t *testing.T) {
			var contacted atomic.Int32
			base := marketCredentialTransportFunc(func(req *http.Request) (*http.Response, error) {
				contacted.Add(1)
				require.Empty(t, req.Header.Get("Cookie"))
				require.Empty(t, req.Header.Get("Proxy-Authorization"))
				if mode == "bearer" {
					require.Equal(t, "Bearer remote-secret", req.Header.Get("Authorization"))
					require.Empty(t, req.Header.Get("X-API-Key"))
				} else {
					require.Empty(t, req.Header.Get("Authorization"))
					require.Equal(t, "remote-secret", req.Header.Get("X-API-Key"))
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
			})
			transport := marketResponseTransport{base: base, endpoint: "https://example.com/mcp", credential: &model.ToolMarketResolvedCredential{Mode: mode, Secret: "remote-secret"}}
			for _, endpoint := range []string{"https://other.example/mcp", "https://example.com/other", "https://example.com/mcp?token=secret", "https://example.com/mcp?", "http://example.com/mcp", "https://127.0.0.1/mcp"} {
				req, err := http.NewRequest(http.MethodPost, endpoint, nil)
				require.NoError(t, err)
				_, err = transport.RoundTrip(req)
				require.ErrorIs(t, err, ErrMarketRemoteNetwork)
			}
			proxy, err := http.NewRequest(http.MethodPost, transport.endpoint, nil)
			require.NoError(t, err)
			proxy.Header.Set("Authorization", "Bearer lmm_at_private")
			proxy.Header.Set("Cookie", "session=private")
			proxy.Header.Set("Proxy-Authorization", "private")
			proxy.Header.Set("X-API-Key", "untrusted")
			response, err := transport.RoundTrip(proxy)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, int32(1), contacted.Load())
			require.Equal(t, "Bearer lmm_at_private", proxy.Header.Get("Authorization"), "the original request is never mutated")
		})
	}
}

type marketCredentialTransportFunc func(*http.Request) (*http.Response, error)

func (f marketCredentialTransportFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestToolMarketAuthenticatedRemoteLifecycle(t *testing.T) {
	for _, mode := range []string{"bearer", "api_key"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TOOL_MARKET_ENCRYPTION_KEY", "tool-market-fixture-encryption-key-strong-32-bytes")
			db := marketRemoteTestDB(t)
			users := []model.User{{Username: "credential-buyer", AffCode: "cred-buyer", Role: 1, Status: 1, Quota: 1000}, {Username: "credential-author", AffCode: "cred-author", Role: 1, Status: 1}, {Username: "credential-root", AffCode: "cred-root", Role: 100, Status: 1}}
			for i := range users {
				require.NoError(t, db.Create(&users[i]).Error)
			}
			require.NoError(t, model.SetToolMarketConfig(users[2].Id, model.ToolMarketConfig{Enabled: true, FeeBPS: 1000, RecipientID: users[2].Id}))
			var calls atomic.Int32
			server := mcp.NewServer(&mcp.Implementation{Name: "authenticated-fixture", Version: "1"}, nil)
			server.AddTool(&mcp.Tool{Name: "authenticated_lookup", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.Add(1)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "authenticated-result"}}}, nil
			})
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
			var rotate atomic.Bool
			var rotateCredential func()
			httpServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				secret := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
				if mode == "api_key" {
					secret = req.Header.Get("X-API-Key")
				}
				if secret != "remote-fixture-secret" {
					http.Error(w, "do not expose this upstream error or credential", http.StatusUnauthorized)
					return
				}
				if rotate.Load() {
					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					req.Body = io.NopCloser(bytes.NewReader(body))
					if bytes.Contains(body, []byte(`"tools/list"`)) && rotate.Swap(false) {
						rotateCredential()
					}
				}
				handler.ServeHTTP(w, req)
			}))
			defer httpServer.Close()
			target, err := url.Parse(httpServer.URL)
			require.NoError(t, err)
			remote := &ToolMarketRemote{client: &http.Client{Transport: marketResponseTransport{base: marketTestTransport{base: httpServer.Client().Transport, target: target}}}, slots: make(chan struct{}, 2)}
			ctx := context.Background()
			endpoint := "https://example.com/mcp"
			credential := &model.ToolMarketResolvedCredential{Mode: mode, Secret: "remote-fixture-secret"}
			_, err = remote.inspect(ctx, endpoint)
			require.ErrorIs(t, err, ErrMarketRemoteAuth)
			_, err = remote.inspectAuthenticated(ctx, endpoint, &model.ToolMarketResolvedCredential{Mode: mode, Secret: "wrong-secret"})
			require.ErrorIs(t, err, ErrMarketRemoteAuth)
			tools, err := remote.inspectAuthenticated(ctx, endpoint, credential)
			require.NoError(t, err)
			require.Zero(t, calls.Load(), "discovery never executes a business tool")
			tools[0].PriceQuota = 100
			product, err := model.SaveToolMarketDraft(users[1].Id, "", model.ToolMarketDraftInput{Name: "Authenticated", ExecutionType: "remote", Visibility: "public", Endpoint: endpoint, Tools: tools})
			require.NoError(t, err)
			require.NoError(t, model.ConfigureToolMarketCredential(users[1].Id, product.ID, product.DraftVersionID, mode, credential.Secret, ""))
			// Rotating during tools/list must not let the older credential's
			// validation overwrite the new credential's invalidation.
			rotateCredential = func() {
				if err := model.ConfigureToolMarketCredential(users[1].Id, product.ID, product.DraftVersionID, mode, credential.Secret, ""); err != nil {
					t.Errorf("rotate fixture credential: %v", err)
				}
			}
			rotate.Store(true)
			require.ErrorIs(t, remote.validate(ctx, users[1].Id, product.ID, false), model.ErrToolMarketConflict)
			draft, err := model.GetToolMarketDetail(users[1].Id, product.ID, true)
			require.NoError(t, err)
			require.False(t, draft.Validated)
			require.NoError(t, remote.validate(ctx, users[1].Id, product.ID, false))
			require.NoError(t, model.SubmitToolMarketDraft(users[1].Id, product.ID, product.DraftVersionID))
			require.NoError(t, remote.validate(ctx, users[2].Id, product.ID, true))
			require.NoError(t, model.ReviewToolMarketVersion(users[2].Id, product.ID, product.DraftVersionID, true, "authenticated fixture"))
			detail, err := model.GetToolMarketDetail(users[0].Id, product.ID, false)
			require.NoError(t, err)
			tool := detail.Tools[0]
			require.NoError(t, model.SetToolMarketInstallation(users[0].Id, "credential-client", tool.ToolID, tool.VersionID, true))
			grant, err := model.CreateToolMarketGrant(users[0].Id, model.ToolMarketGrant{ClientID: "credential-client", ToolID: tool.ToolID, VersionID: tool.VersionID, MaxCalls: 2, MaxPriceQuota: 100, MaxTotalQuota: 200, ExpiresAt: common.GetTimestamp() + 3600})
			require.NoError(t, err)
			input := model.ToolMarketReserveInput{UserID: users[0].Id, ClientID: "credential-client", ToolID: tool.ToolID, VersionID: tool.VersionID, GrantID: grant.ID, RequestKey: "authenticated-one", Arguments: json.RawMessage(`{}`)}
			response, err := remote.execute(ctx, input)
			require.NoError(t, err)
			require.Equal(t, "settled", response.Call.SettlementStatus)
			require.Contains(t, string(response.Result), "authenticated-result")
			replayed, err := remote.execute(ctx, input)
			require.NoError(t, err)
			require.Equal(t, response.Call.ID, replayed.Call.ID)
			require.Equal(t, int32(1), calls.Load())
			var buyer model.User
			require.NoError(t, db.First(&buyer, users[0].Id).Error)
			require.Equal(t, 900, buyer.Quota)
			require.ErrorIs(t, model.ConfigureToolMarketCredential(users[1].Id, product.ID, tool.VersionID, "none", "", ""), model.ErrToolMarketConflict, "published identity cannot change under an old grant")
		})
	}
}
