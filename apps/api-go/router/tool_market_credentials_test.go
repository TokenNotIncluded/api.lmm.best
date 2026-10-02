package router

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestToolMarketCredentialRoutesKeepSecretsAndOwnershipPrivate(t *testing.T) {
	t.Setenv("TOOL_MARKET_ENCRYPTION_KEY", "tool-market-route-encryption-key-at-least-32-bytes")
	router, db, token, user := toolMarketTestRouter(t)
	require.NoError(t, db.AutoMigrate(&model.ToolMarketCredential{}))
	product, err := model.SaveToolMarketDraft(user.Id, "", model.ToolMarketDraftInput{Name: "Credential route", ExecutionType: "remote", Visibility: "private", Endpoint: "https://example.com/mcp", Tools: []model.ToolMarketToolInput{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
	require.NoError(t, err)
	path := "/api/tool-market/services/" + product.ID + "/credentials"
	body := `{"version_id":"` + product.DraftVersionID + `","mode":"bearer","secret":"fixture-remote-secret"}`
	response := toolMarketHTTPRequest(router, http.MethodPut, path, "", body)
	require.NotEqual(t, http.StatusOK, response.Code)
	response = toolMarketHTTPRequest(router, http.MethodPut, path, token, body)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"configured":true`)
	require.NotContains(t, response.Body.String(), "fixture-remote-secret")
	require.NotContains(t, response.Body.String(), "ciphertext")
	response = toolMarketHTTPRequest(router, http.MethodGet, path+"?version_id="+product.DraftVersionID, token, "")
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"mode":"bearer"`)
	require.NotContains(t, response.Body.String(), "fixture-remote-secret")
	otherToken := "other-market-owner-token"
	other := model.User{Username: "other-market-owner", AffCode: "other-market-owner", Role: 1, Status: 1, AccessToken: &otherToken}
	require.NoError(t, db.Create(&other).Error)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		queryPath := path
		if method == http.MethodGet {
			queryPath += "?version_id=" + product.DraftVersionID
		}
		response = toolMarketHTTPRequest(router, method, queryPath, otherToken, body)
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), "fixture-remote-secret")
	}
	// Auth fields are not a route to caller-controlled HTTP headers or JSON
	// payloads that silently retain only the last top-level object.
	for _, invalid := range []string{
		`{"version_id":"` + product.DraftVersionID + `","mode":"bearer","secret":"fixture","headers":{"Cookie":"private"}}`,
		body + ` {"mode":"none"}`,
		`{"version_id":"` + product.DraftVersionID + `","mode":"bearer","secret":"lmm_at_not_a_remote_key"}`,
	} {
		response = toolMarketHTTPRequest(router, http.MethodPut, path, token, invalid)
		require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	}
	response = toolMarketHTTPRequest(router, http.MethodPut, path, token, `{"version_id":"`+product.DraftVersionID+`","mode":"none"}`)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"configured":false`)
}

func TestToolMarketCredentialRoutesFailClosedAndRejectReferenceSpoofing(t *testing.T) {
	t.Setenv("TOOL_MARKET_ENCRYPTION_KEY", "")
	t.Setenv("CRYPTO_SECRET", "")
	router, db, token, user := toolMarketTestRouter(t)
	require.NoError(t, db.AutoMigrate(&model.ToolMarketCredential{}))
	product, err := model.SaveToolMarketDraft(user.Id, "", model.ToolMarketDraftInput{Name: "Unavailable credential", ExecutionType: "remote", Visibility: "private", Endpoint: "https://example.com/mcp", Tools: []model.ToolMarketToolInput{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
	require.NoError(t, err)
	path := "/api/tool-market/services/" + product.ID + "/credentials"
	response := toolMarketHTTPRequest(router, http.MethodPut, path, token, `{"version_id":"`+product.DraftVersionID+`","mode":"api_key","secret":"fixture-must-not-leak"}`)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Contains(t, response.Body.String(), "TOOL_MARKET_CREDENTIALS_UNAVAILABLE")
	require.NotContains(t, response.Body.String(), "fixture-must-not-leak")
	for _, body := range []string{
		`{"endpoint":"https://example.com/mcp","service_id":"` + product.ID + `"}`,
		`{"endpoint":"https://example.com/mcp","service_id":"` + product.ID + `","version_id":"` + product.DraftVersionID + `","authentication":{"mode":"none"}}`,
		`{"endpoint":"https://other.example/mcp","service_id":"` + product.ID + `","version_id":"` + product.DraftVersionID + `"}`,
		`{"endpoint":"https://example.com/mcp","authentication":{"mode":"api_key","secret":"fixture","header":"Authorization"}}`,
		`{"endpoint":"https://example.com/mcp","authentication":{"mode":"bearer","secret":"fixture\r\nCookie: private"}}`,
		`{"endpoint":"https://example.com/mcp?secret=fixture","authentication":{"mode":"none"}}`,
	} {
		response = toolMarketHTTPRequest(router, http.MethodPost, "/api/tool-market/inspect", token, body)
		require.NotEqual(t, http.StatusOK, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), "Cookie: private")
	}
	response = toolMarketHTTPRequest(router, http.MethodGet, "/api/tool-market/config", "", "")
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"builtin_enabled":true`)
	require.Contains(t, response.Body.String(), `"enabled":false`)
}

func TestToolMarketPrivateActivationHTTPRejectsPublicAndPaidWithoutRemoteRequest(t *testing.T) {
	for _, visibility := range []string{"public", "shared", "private"} {
		t.Run(visibility, func(t *testing.T) {
			router, _, token, user := toolMarketTestRouter(t)
			input := model.ToolMarketDraftInput{Name: "No bypass", ExecutionType: "remote", Visibility: visibility, Endpoint: "https://example.com/mcp", Tools: []model.ToolMarketToolInput{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`), PriceQuota: 1}}}
			if visibility == "shared" {
				input.AllowedUsers = []int{user.Id}
			}
			product, err := model.SaveToolMarketDraft(user.Id, "", input)
			require.NoError(t, err)
			response := toolMarketHTTPRequest(router, http.MethodPost, "/api/tool-market/services/"+product.ID+"/activate", token, `{"version_id":"`+product.DraftVersionID+`"}`)
			require.True(t, response.Code == http.StatusConflict || response.Code == http.StatusForbidden, response.Body.String())
			require.False(t, strings.Contains(response.Body.String(), "TOOL_MARKET_REMOTE"), "local policy rejection happens before any network validation")
		})
	}
}
