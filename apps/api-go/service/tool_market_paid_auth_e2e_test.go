package service_test

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
	"github.com/LIghtJUNction/api.lmm.best/internal/toolmarketfixture"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestToolMarketPaidAuthenticatedUploadCredentialBindingAndRotation(t *testing.T) {
	for _, mode := range []string{"bearer", "api_key"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TOOL_MARKET_ENCRYPTION_KEY", "a7fc9d15b268034eadf527609e41b38c")
			harness := newPaidMarketHarness(t)
			const secret = "test-service-secret-5b268c1ad7fe39"
			var executions atomic.Int32
			var leakedLMMCredential atomic.Bool
			var rejectBusinessCalls atomic.Bool
			options := toolmarketfixture.Options{Bearer: secret, OnCall: func(string) { executions.Add(1) }}
			fixtureHandler := toolmarketfixture.Handler(toolmarketfixture.NewServer(options), options)
			remote := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if strings.Contains(request.Header.Get("Authorization"), "lmm_") || request.Header.Get("Cookie") != "" || request.Header.Get("Proxy-Authorization") != "" {
					leakedLMMCredential.Store(true)
				}
				if mode == "api_key" {
					if request.Header.Get("X-API-Key") != secret || request.Header.Get("Authorization") != "" {
						http.Error(writer, "test service API key required", http.StatusUnauthorized)
						return
					}
					request = request.Clone(request.Context())
					request.Header = request.Header.Clone()
					request.Header.Set("Authorization", "Bearer "+secret)
				}
				if rejectBusinessCalls.Load() && request.Method == http.MethodPost {
					body, err := io.ReadAll(request.Body)
					if err != nil {
						http.Error(writer, "fixture read error", http.StatusBadRequest)
						return
					}
					request.Body = io.NopCloser(bytes.NewReader(body))
					var message struct {
						Method string `json:"method"`
					}
					if json.Unmarshal(body, &message) == nil && message.Method == "tools/call" {
						http.Error(writer, "deliberate provider refusal; private test credential="+secret, http.StatusUnauthorized)
						return
					}
				}
				fixtureHandler.ServeHTTP(writer, request)
			}))
			t.Cleanup(remote.Close)
			target, err := url.Parse(remote.URL)
			require.NoError(t, err)
			restore := service.ReplaceToolMarketRemoteTransportForTest(paidMarketTLSTransport{base: remote.Client().Transport, target: target})
			t.Cleanup(restore)
			endpoint := "https://paid-fixture.example.com/mcp"
			status, body := harness.request("", http.MethodPost, "/api/tool-market/inspect", map[string]any{"endpoint": endpoint})
			require.Equal(t, http.StatusUnauthorized, status)
			status, body = harness.request("author", http.MethodPost, "/api/tool-market/inspect", map[string]any{"endpoint": endpoint})
			require.Equal(t, http.StatusUnprocessableEntity, status, string(body))
			status, body = harness.request("author", http.MethodPost, "/api/tool-market/inspect", map[string]any{"endpoint": endpoint, "authentication": map[string]any{"mode": mode, "secret": "wrong-test-service-credential"}})
			require.Equal(t, http.StatusUnprocessableEntity, status, string(body))
			var inputs []model.ToolMarketToolInput
			harness.ok("author", http.MethodPost, "/api/tool-market/inspect", map[string]any{"endpoint": endpoint, "authentication": map[string]any{"mode": mode, "secret": secret}}, &inputs)
			require.Len(t, inputs, 9)
			require.Zero(t, executions.Load())
			var stored int64
			require.NoError(t, harness.db.Model(&model.ToolMarketCredential{}).Count(&stored).Error)
			require.Zero(t, stored, "temporary inspect credentials must never be saved")
			for i := range inputs {
				inputs[i].PriceQuota = 100
				inputs[i].Permissions = []string{"network", "read"}
			}
			input := model.ToolMarketDraftInput{Name: "Authenticated paid MCP TEST", Description: "Isolated fixture only", ExecutionType: "remote", Visibility: "shared", AllowedUsers: []int{harness.users["buyer"].Id}, Endpoint: endpoint, Tools: inputs}
			var product model.ToolMarketService
			harness.ok("author", http.MethodPost, "/api/tool-market/services", input, &product)
			path := "/api/tool-market/services/" + product.ID
			credentialPath := path + "/credentials"
			credentialInput := map[string]any{"version_id": product.DraftVersionID, "mode": mode, "secret": secret}
			for _, user := range []string{"buyer", "outsider", "root"} {
				status, body = harness.request(user, http.MethodPut, credentialPath, credentialInput)
				require.Equal(t, http.StatusForbidden, status, "%s: %s", user, body)
				require.NotContains(t, string(body), secret)
				status, body = harness.request(user, http.MethodGet, credentialPath+"?version_id="+product.DraftVersionID, nil)
				require.Equal(t, http.StatusForbidden, status, "%s: %s", user, body)
			}
			status, body = harness.request("author", http.MethodPut, credentialPath, map[string]any{"version_id": "foreign-version", "mode": mode, "secret": secret})
			require.NotEqual(t, http.StatusOK, status)
			var metadata model.ToolMarketCredentialMetadata
			harness.ok("author", http.MethodPut, credentialPath, credentialInput, &metadata)
			require.True(t, metadata.Configured)
			require.Equal(t, mode, metadata.Mode)
			harness.ok("author", http.MethodGet, credentialPath+"?version_id="+product.DraftVersionID, nil, &metadata)
			_, body = harness.request("author", http.MethodGet, credentialPath+"?version_id="+product.DraftVersionID, nil)
			require.NotContains(t, string(body), secret)
			require.NotContains(t, string(body), "ciphertext")
			var row model.ToolMarketCredential
			require.NoError(t, harness.db.First(&row, "version_id = ?", product.DraftVersionID).Error)
			require.NotEmpty(t, row.Ciphertext)
			require.NotContains(t, row.Ciphertext, secret)
			data, err := json.Marshal(row)
			require.NoError(t, err)
			require.JSONEq(t, `{}`, string(data), "even accidental model serialization must not expose secrets")
			// Credential changes clear validation. An earlier successful check
			// cannot approve a later wrong secret under the same draft version.
			harness.ok("author", http.MethodPost, path+"/validate", nil, nil)
			harness.ok("author", http.MethodPut, credentialPath, map[string]any{"version_id": product.DraftVersionID, "mode": mode, "secret": "wrong-test-service-credential"}, nil)
			status, body = harness.request("author", http.MethodPost, path+"/validate", nil)
			require.Equal(t, http.StatusUnprocessableEntity, status, string(body))
			var staleVersion model.ToolMarketVersion
			require.NoError(t, harness.db.First(&staleVersion, "id = ?", product.DraftVersionID).Error)
			require.Empty(t, staleVersion.ValidationDigest, "changed credentials invalidate earlier approval evidence")
			var staleTools []model.ToolMarketToolVersion
			require.NoError(t, harness.db.Where("version_id = ?", product.DraftVersionID).Find(&staleTools).Error)
			for _, tool := range staleTools {
				require.Empty(t, tool.RemoteDigest)
			}
			harness.ok("author", http.MethodPut, credentialPath, credentialInput, nil)
			var rediscovered []model.ToolMarketToolInput
			harness.ok("author", http.MethodPost, "/api/tool-market/inspect", map[string]any{"endpoint": endpoint, "service_id": product.ID, "version_id": product.DraftVersionID}, &rediscovered)
			require.Len(t, rediscovered, 9)
			status, _ = harness.request("buyer", http.MethodPost, "/api/tool-market/inspect", map[string]any{"endpoint": endpoint, "service_id": product.ID, "version_id": product.DraftVersionID})
			require.NotEqual(t, http.StatusOK, status)
			status, _ = harness.request("author", http.MethodPost, "/api/tool-market/inspect", map[string]any{"endpoint": "https://paid-fixture.example.com/other-path", "service_id": product.ID, "version_id": product.DraftVersionID})
			require.NotEqual(t, http.StatusOK, status, "saved credentials bind the exact endpoint")
			harness.ok("author", http.MethodPost, path+"/validate", nil, nil)
			harness.ok("author", http.MethodPost, path+"/submit", map[string]any{"version_id": product.DraftVersionID}, nil)
			status, _ = harness.request("author", http.MethodPut, credentialPath, credentialInput)
			require.Equal(t, http.StatusConflict, status, "submitted credentials are immutable")
			harness.ok("root", http.MethodPost, path+"/review", map[string]any{"version_id": product.DraftVersionID, "approve": true, "note": "authenticated isolated TLS MCP verified"}, nil)
			require.Zero(t, executions.Load())
			var detail model.ToolMarketDetail
			harness.ok("buyer", http.MethodGet, path, nil, &detail)
			var tool model.ToolMarketToolVersion
			for _, item := range detail.Tools {
				if item.Name == "fixture_echo" {
					tool = item
				}
			}
			require.NotEmpty(t, tool.ToolID)
			for _, client := range []string{model.ToolMarketWebClient, "authenticated-paid-client"} {
				harness.ok("buyer", http.MethodPut, "/api/tool-market/installations", map[string]any{"client_id": client, "tool_id": tool.ToolID, "version_id": tool.VersionID, "loaded": true}, nil)
				harness.ok("buyer", http.MethodPost, "/api/tool-market/grants", model.ToolMarketGrant{ClientID: client, ToolID: tool.ToolID, VersionID: tool.VersionID, MaxPriceQuota: 100, MaxTotalQuota: 200, MaxCalls: 2, ExpiresAt: common.GetTimestamp() + 3600}, nil)
			}
			var execution service.ToolMarketExecutionResponse
			harness.ok("buyer", http.MethodPost, "/api/tool-market/invoke", map[string]any{"tool_id": tool.ToolID, "version_id": tool.VersionID, "request_id": "authenticated-web-call", "arguments": map[string]any{"text": "HTTPS authenticated result"}}, &execution)
			require.Equal(t, "settled", execution.Call.SettlementStatus)
			require.Equal(t, 1900, harness.balance("buyer"))
			var result mcp.CallToolResult
			require.NoError(t, json.Unmarshal(execution.Result, &result))
			require.Equal(t, "HTTPS authenticated result", result.StructuredContent.(map[string]any)["text"])
			var token struct {
				Token string `json:"token"`
			}
			harness.ok("buyer", http.MethodPost, "/api/tool-market/tokens", map[string]any{"client_id": "authenticated-paid-client", "can_invoke": true, "can_manage": true, "expires_at": common.GetTimestamp() + 3600}, &token)
			session := harness.connect(token.Token)
			resultPtr, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: paidMarketToolName(tool), Arguments: map[string]any{"request_id": "authenticated-mcp-call", "arguments": map[string]any{"text": "native authenticated MCP"}}})
			require.NoError(t, err)
			require.False(t, resultPtr.IsError)
			require.Equal(t, "native authenticated MCP", resultPtr.StructuredContent.(map[string]any)["text"])
			require.Equal(t, 1800, harness.balance("buyer"))
			require.Equal(t, 180, harness.balance("author"))
			require.Equal(t, 20, harness.balance("root"))
			require.Equal(t, int32(2), executions.Load())
			require.False(t, leakedLMMCredential.Load())
			// Provider authentication can disappear between tools/list and the
			// business call. A definite 401 immediately releases its hold and
			// keeps the provider's raw error body and key out of the result.
			rejectBusinessCalls.Store(true)
			var refused service.ToolMarketExecutionResponse
			harness.ok("buyer", http.MethodPost, "/api/tool-market/invoke", map[string]any{"tool_id": tool.ToolID, "version_id": tool.VersionID, "request_id": "provider-revoked-auth", "arguments": map[string]any{"text": "must not charge"}}, &refused)
			require.Equal(t, "failed", refused.Call.ExecutionStatus)
			require.Equal(t, "released", refused.Call.SettlementStatus)
			require.NotContains(t, string(refused.Result), secret)
			require.Equal(t, 1800, harness.balance("buyer"))
			require.Equal(t, 180, harness.balance("author"))
			require.Equal(t, 20, harness.balance("root"))
			require.Equal(t, int32(2), executions.Load())
			rejectBusinessCalls.Store(false)
			// Copy into an explicitly saved new version re-encrypts the secret,
			// but cannot change any installed/granted live version in place.
			var next model.ToolMarketService
			harness.ok("author", http.MethodPut, path+"/draft", input, &next)
			require.NotEqual(t, product.DraftVersionID, next.DraftVersionID)
			status, _ = harness.request("author", http.MethodPut, credentialPath, credentialInput)
			require.Equal(t, http.StatusConflict, status, "published credentials cannot be updated through a new draft")
			harness.ok("author", http.MethodPut, credentialPath, map[string]any{"version_id": next.DraftVersionID, "mode": mode, "copy_from_version_id": product.DraftVersionID}, nil)
			row = model.ToolMarketCredential{}
			require.NoError(t, harness.db.First(&row, "version_id = ?", next.DraftVersionID).Error)
			require.NotContains(t, row.Ciphertext, secret)
			harness.ok("author", http.MethodPost, path+"/validate", nil, nil)
			var current model.ToolMarketDetail
			harness.ok("buyer", http.MethodGet, path, nil, &current)
			require.Equal(t, tool.VersionID, current.Version.ID)
			var events []model.ToolMarketEvent
			require.NoError(t, harness.db.Find(&events).Error)
			data, err = json.Marshal(events)
			require.NoError(t, err)
			require.NotContains(t, string(data), secret)
			require.NotContains(t, string(data), harness.tokens["author"])
		})
	}
}
