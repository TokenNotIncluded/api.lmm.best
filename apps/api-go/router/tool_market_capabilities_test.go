package router

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the public config handler and its actual route registrations using
// the existing in-memory fixture. No backend version string or DB capability
// field determines whether these endpoints are available.
func TestToolMarketCapabilitiesMatchRegisteredHandlers(t *testing.T) {
	engine, _, _, _ := toolMarketTestRouter(t)
	response := toolMarketHTTPRequest(engine, http.MethodGet, "/api/tool-market/config", "", "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Capabilities map[string]bool `json:"capabilities"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Len(t, body.Data.Capabilities, 3)
	// Inspect the normal central router as well, so omitting a feature router
	// cannot leave a capability advertised by an otherwise valid config handler.
	publishedRouter := gin.New()
	SetApiRouter(publishedRouter)
	routes := map[string]string{}
	for _, route := range publishedRouter.Routes() {
		routes[route.Method+" "+route.Path] = route.Handler
	}
	for capability, handlers := range map[string]map[string]string{
		"service_deletion":      {"DELETE /api/tool-market/services/:id": "DeleteToolMarketService"},
		"client_record_cleanup": {"DELETE /api/tool-market/tokens/:id/record": "RemoveToolMarketTokenRecord", "DELETE /api/tool-market/grants/:id/record": "RemoveToolMarketGrantRecord", "POST /api/tool-market/clients/remove": "RemoveToolMarketClient"},
		"meta_delegation":       {"GET /api/tool-market/meta-delegations/oauth-clients": "ListToolMarketMetaOAuthClients", "GET /api/tool-market/meta-delegations/:kind/:id": "GetToolMarketMetaDelegation", "PUT /api/tool-market/meta-delegations/:kind/:id": "SetToolMarketMetaDelegation"},
	} {
		require.True(t, body.Data.Capabilities[capability], capability)
		for path, handler := range handlers {
			require.Contains(t, routes[path], "."+handler, path)
		}
	}
	require.Contains(t, routes["POST /api/tool-market/tokens"], ".CreateToolMarketToken")
	require.Contains(t, routes["DELETE /api/tool-market/tokens/:id"], ".RevokeToolMarketToken")
}
