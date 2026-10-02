package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type toolMarketDrawingIPRelayObservation struct {
	status   int
	clientIP string
}

func toolMarketDrawingIPRouter(t *testing.T, clientID string) (*gin.Engine, string, model.ToolMarketToolVersion, *[]toolMarketDrawingIPRelayObservation) {
	t.Helper()
	oldDB, oldSQLitePath, oldMaster := model.DB, common.SQLitePath, common.IsMasterNode
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldDrawing, oldMemory := common.DrawingEnabled, common.MemoryCacheEnabled
	oldCritical, oldGlobal := common.CriticalRateLimitEnable, common.GlobalApiRateLimitEnable
	oldGroups, oldRatios := setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		model.DB, common.SQLitePath, common.IsMasterNode = oldDB, oldSQLitePath, oldMaster
		common.SetDatabaseTypes(oldMain, oldLog)
		common.DrawingEnabled, common.MemoryCacheEnabled = oldDrawing, oldMemory
		common.CriticalRateLimitEnable, common.GlobalApiRateLimitEnable = oldCritical, oldGlobal
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatios))
	})
	// Initialize the model's SQL column quoting without migrating a real DB.
	t.Setenv("SQL_DSN", "local")
	common.SQLitePath, common.IsMasterNode = t.TempDir()+"/init.db", false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, model.InitDB())
	pool, err := model.DB.DB()
	require.NoError(t, err)
	require.NoError(t, pool.Close())
	model.DB = oldDB
	common.DrawingEnabled, common.MemoryCacheEnabled = true, false
	common.CriticalRateLimitEnable, common.GlobalApiRateLimitEnable = false, false
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","image-2":"Images"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"image-2":1}`))

	engine, db, bearer, user := toolMarketTestRouter(t)
	require.NoError(t, db.AutoMigrate(
		&model.Token{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{},
		&model.ToolMarketToken{}, &model.ToolMarketResult{}, &model.ToolMarketBuiltinContinuation{},
		&model.OpenSourceBountyMCPConfirmation{}, &model.OpenSourceBountyMCPOperation{},
	))
	level := model.TrustLevelMinUser + 1
	user.Group, user.TrustLevelOverride, user.Quota = "default", &level, int(10*common.QuotaPerUnit)
	require.NoError(t, db.Save(&user).Error)
	allowedIP := "203.0.113.10"
	key := model.Token{UserId: user.Id, Key: strings.Repeat("a", 48), Name: "Existing restricted drawing key",
		Group: "image-2", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, AllowIps: &allowedIP}
	require.NoError(t, db.Create(&key).Error)
	channel := model.Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Name: "IP policy fixture", Key: "fixture-provider-key", Models: "gpt-image-1", Group: "image-2"}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(nil))
	model.InvalidatePricingCache()
	require.Eventually(t, func() bool {
		for _, pricing := range model.GetPricing() {
			if pricing.ModelName == "gpt-image-1" {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, controller.EnsureToolMarketBuiltinCatalog(context.Background()))
	detail, err := model.GetToolMarketDetail(user.Id, model.ToolMarketBuiltinServiceID("drawing"), false)
	require.NoError(t, err)
	var tool model.ToolMarketToolVersion
	for _, row := range detail.Tools {
		if row.Name == "drawing.generate" {
			tool = row
		}
	}
	require.NotEmpty(t, tool.ToolID)
	require.NoError(t, model.SetToolMarketInstallation(user.Id, clientID, tool.ToolID, tool.VersionID, true))
	_, err = model.CreateToolMarketGrant(user.Id, model.ToolMarketGrant{ClientID: clientID, ToolID: tool.ToolID,
		VersionID: tool.VersionID, MaxCalls: 20, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	if clientID != model.ToolMarketWebClient {
		bearer, _, err = model.CreateToolMarketToken(user.Id, clientID, true, false, common.GetTimestamp()+3600)
		require.NoError(t, err)
	}
	SetToolMarketMCPRouter(engine)

	// Keep the real drawing key resolution/authentication and its IP restriction.
	// The final handler substitutes only the provider response; billing is tested
	// separately by the drawing parity suite.
	relay := gin.New()
	require.NoError(t, relay.SetTrustedProxies(nil))
	observations := &[]toolMarketDrawingIPRelayObservation{}
	relay.Use(func(c *gin.Context) {
		c.Set("id", user.Id)
		c.Next()
		*observations = append(*observations, toolMarketDrawingIPRelayObservation{status: c.Writer.Status(), clientIP: c.ClientIP()})
	})
	relay.POST("/pg/images/generations", controller.PreparePlaygroundImageAuth, func(c *gin.Context) {
		assert.Equal(t, key.Id, common.GetContextKeyInt(c, constant.ContextKeyTokenId))
		assert.Equal(t, allowedIP, c.ClientIP())
		c.JSON(http.StatusOK, gin.H{"created": 1, "data": []gin.H{{"b64_json": "aXA="}}})
	})
	controller.SetToolMarketBuiltinDrawingRelay(relay)
	t.Cleanup(func() { controller.NewDrawingMCPHandler() })
	return engine, bearer, tool, observations
}

func TestToolMarketDrawingRoutesPreserveTrustedClientIPAndKeyRestrictions(t *testing.T) {
	for _, endpoint := range []struct {
		name, path, clientID string
	}{
		{"web", "/api/tool-market/invoke", model.ToolMarketWebClient},
		{"mcp", "/mcp/market", "drawing-ip-agent"},
		{"mcp_slash", "/mcp/market/", "drawing-ip-agent"},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			engine, bearer, tool, observations := toolMarketDrawingIPRouter(t, endpoint.clientID)
			for _, scenario := range []struct {
				name, trustedProxies, peer, forwarded, expectedIP string
				allowed                                           bool
			}{
				{"allowed_peer", "none", "203.0.113.10:1234", "", "203.0.113.10", true},
				{"denied_peer_with_forged_headers", "none", "198.51.100.20:1234", "203.0.113.10", "198.51.100.20", false},
				{"allowed_client_behind_trusted_proxy", "192.0.2.2", "192.0.2.2:1234", "203.0.113.10", "203.0.113.10", true},
				{"denied_client_behind_trusted_proxy", "192.0.2.2", "192.0.2.2:1234", "198.51.100.20", "198.51.100.20", false},
				{"untrusted_proxy_with_forged_headers", "192.0.2.2", "198.51.100.20:1234", "203.0.113.10", "198.51.100.20", false},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					t.Setenv("TRUSTED_PROXIES", scenario.trustedProxies)
					require.NoError(t, middleware.ConfigureTrustedProxies(engine))
					invoke := func(requestState string) mcp.CallToolResult {
						arguments := map[string]any{"prompt": "verify the existing drawing key IP policy", "group": "image-2", "model": "gpt-image-1", "n": 1}
						var body any
						if endpoint.clientID == model.ToolMarketWebClient {
							input := map[string]any{"tool_id": tool.ToolID, "version_id": tool.VersionID, "request_id": scenario.name, "arguments": arguments}
							if requestState != "" {
								input["request_state"] = requestState
								input["input_responses"] = mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}}
							}
							body = input
						} else {
							params := &mcp.CallToolParams{Name: "market_tool_" + strings.ReplaceAll(tool.ToolID, "-", ""), Arguments: map[string]any{"request_id": scenario.name, "arguments": arguments}, RequestState: requestState}
							params.Meta = mcp.Meta{
								mcp.MetaKeyProtocolVersion:    "2026-07-28",
								mcp.MetaKeyClientInfo:         map[string]any{"name": "drawing-ip-test", "version": "1"},
								mcp.MetaKeyClientCapabilities: map[string]any{},
							}
							if requestState != "" {
								params.InputResponses = mcp.InputResponseMap{"confirmation": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirmed": true}}}
							}
							body = map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params}
						}
						data, err := json.Marshal(body)
						require.NoError(t, err)
						request := httptest.NewRequest(http.MethodPost, endpoint.path, strings.NewReader(string(data)))
						request.RemoteAddr = scenario.peer
						request.Header.Set("Authorization", "Bearer "+bearer)
						request.Header.Set("Content-Type", "application/json")
						request.Header.Set("Accept", "application/json, text/event-stream")
						request.Header.Set("MCP-Protocol-Version", "2026-07-28")
						request.Header.Set("MCP-Method", "tools/call")
						request.Header.Set("MCP-Name", "market_tool_"+strings.ReplaceAll(tool.ToolID, "-", ""))
						request.Header.Set("X-Forwarded-For", scenario.forwarded)
						request.Header.Set("X-Real-IP", scenario.forwarded)
						response := httptest.NewRecorder()
						engine.ServeHTTP(response, request)
						require.Equal(t, http.StatusOK, response.Code, response.Body.String())
						var result mcp.CallToolResult
						if endpoint.clientID == model.ToolMarketWebClient {
							var envelope struct {
								Success bool `json:"success"`
								Data    struct {
									Result json.RawMessage `json:"result"`
								} `json:"data"`
							}
							require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
							require.True(t, envelope.Success, response.Body.String())
							require.NoError(t, json.Unmarshal(envelope.Data.Result, &result))
						} else {
							var envelope struct {
								Result mcp.CallToolResult `json:"result"`
								Error  json.RawMessage    `json:"error"`
							}
							require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
							require.Empty(t, envelope.Error, response.Body.String())
							result = envelope.Result
						}
						return result
					}
					before := len(*observations)
					pending := invoke("")
					require.False(t, pending.IsError)
					require.True(t, pending.NeedsInput())
					require.NotEmpty(t, pending.RequestState)
					require.Len(t, *observations, before, "confirmation must not reach the image relay")
					completed := invoke(pending.RequestState)
					require.Len(t, *observations, before+1)
					require.Equal(t, scenario.expectedIP, (*observations)[before].clientIP)
					require.Equal(t, !scenario.allowed, completed.IsError)
					if scenario.allowed {
						require.Equal(t, http.StatusOK, (*observations)[before].status)
						require.NotNil(t, completed.StructuredContent)
					} else {
						require.Equal(t, http.StatusForbidden, (*observations)[before].status, "the existing key must reject the actual denied client IP")
					}
				})
			}
		})
	}
}
