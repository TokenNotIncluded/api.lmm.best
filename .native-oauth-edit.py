from pathlib import Path
r=Path('apps/api-go')
def e(path,old,new):
 p=r/path;s=p.read_text();assert old in s,(path,old[:80]);p.write_text(s.replace(old,new,1))
e('controller/oauth_catalog.go','\t"math"','\t"math"\n\t"slices"')
e('controller/oauth_catalog.go','func (h *OAuthHTTP) Balance(c *gin.Context) {','''func (h *OAuthHTTP) OpenAIModels(c *gin.Context) {
    grant, user, ok := h.resource(c, service.OAuthCatalogScope)
    if !ok { return }
    catalog, err := h.Integration.Catalog(c.Request.Context(), user, grant)
    if err != nil { oauthProtocolFailure(c, err); return }
    data := make([]gin.H, 0, len(catalog.Models))
    for _, entry := range catalog.Models {
        if slices.Contains(entry.APIs, "openai-completions") {
            data = append(data, gin.H{"id": entry.ID, "object": "model", "name": entry.Name, "owned_by": "lmm"})
        }
    }
    c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}

func (h *OAuthHTTP) Balance(c *gin.Context) {''')
e('router/oauth_server.go','\trouter.GET("/api/oauth2/catalog", resources, h.Catalog)','\trouter.GET("/api/oauth2/catalog", resources, h.Catalog)\n\trouter.GET(service.OAuthOpenAIBasePath+"/models", resources, h.OpenAIModels)')
e('middleware/oauth_resource.go','type oauthModelBody struct{ Model string }','type oauthModelBody struct {\n Model string\n maxModelBytes int\n}')
e('middleware/oauth_resource.go','\tif b.Model == "" || len(b.Model) > 512 {','\tlimit := b.maxModelBytes\n\tif limit == 0 { limit = 512 }\n\tif b.Model == "" || len(b.Model) > limit {')
e('router/relay-router.go','"github.com/LIghtJUNction/api.lmm.best/relaykit/types"','"github.com/LIghtJUNction/api.lmm.best/relaykit/types"\n "github.com/LIghtJUNction/api.lmm.best/service"')
e('router/relay-router.go','\t// https://platform.openai.com/docs/api-reference/introduction','''    nativeOAuth := router.Group(service.OAuthOpenAIBasePath)
    nativeOAuth.Use(middleware.RouteTag("relay"), middleware.SystemPerformanceCheck(), middleware.OAuthOpenAIAuth,
        largeRequestAdmission, middleware.OAuthOpenAIRequest, middleware.ModelRequestRateLimit(), middleware.Distribute())
    nativeOAuth.POST("/chat/completions", func(c *gin.Context) {
        controller.Relay(c, types.RelayFormatOpenAI)
    })
    // https://platform.openai.com/docs/api-reference/introduction''')
e('router/oauth_server_test.go','\tengine.POST("/v1/chat/completions", middleware.TokenAuth(), func(c *gin.Context) {','\trelay := func(c *gin.Context) {')
e('router/oauth_server_test.go','\t})\n\tengine.GET("/read-only", middleware.TokenAuthReadOnly(),','\t}\n\tengine.POST("/v1/chat/completions", middleware.TokenAuth(), relay)\n\tengine.POST(service.OAuthOpenAIBasePath+"/chat/completions", middleware.OAuthOpenAIAuth, middleware.RelayRequestAdmission(), middleware.OAuthOpenAIRequest, relay)\n\tengine.GET("/read-only", middleware.TokenAuthReadOnly(),')
p=r/'service/oauth_contract.md';p.write_text(p.read_text()+'''
## Native OpenAI-compatible clients

`GET /api/oauth2/openai/v1/models` returns a standard OpenAI collection from the
same authorized Catalog v1, filtered to Chat Completions models. IDs retain the
exact `lmm:<group-id>:<model-id>` binding; names display the group and model.
Unknown prices and capability limits are omitted, not fabricated as zero.

`POST /api/oauth2/openai/v1/chat/completions` accepts one of those exact IDs with
the same OAuth bearer. It rejects caller group headers, plain upstream names,
ambiguous JSON, alternate credentials, query credentials and unauthorized groups.
Authentication precedes shared large-request admission. The admitted envelope is
translated internally to the existing `/v1/chat/completions` relay; the original
OAuth group/model checks and billing session remain authoritative. This is not
an HTTP proxy, a second token store or a new grant type. Streaming and cancellation
use the normal relay. Existing Pi/DSH and API-key endpoints are unchanged.

This permits a fixed declarative Codewhale provider: install once, authorize
inside the host, then choose a group-bound model. No companion login, manifest
export, shared token file or per-user plugin generation is needed. The native
Codewhale client keeps its registered four initial scopes; no MCP permission is
added. Deployment and real browser/inference/billing acceptance remain required.
''')
