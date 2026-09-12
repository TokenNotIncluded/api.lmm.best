package router

import (
	"fmt"

	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// SetOAuthServerRouter has no overlap with /api/oauth/:provider. All OAuth HTTP
// handlers have their own limits, deadlines and security headers, not the
// dashboard/JWT API group or anonymous model-relay authentication middleware.
func SetOAuthServerRouter(router *gin.Engine) error {
	config, err := service.OAuthServerConfigFromEnv()
	if err != nil {
		return fmt.Errorf("configure OAuth server: %w", err)
	}
	integration, err := service.ConfigureOAuthIntegration(model.DB, config)
	if err != nil {
		return fmt.Errorf("initialize OAuth server: %w", err)
	}
	MountOAuthServerRoutes(router, integration)
	return nil
}

// MountOAuthServerRoutes also mounts disabled 404s so a SPA fallback cannot
// accidentally pretend to be OAuth discovery. Explicit injection supports
// isolated HTTP integration tests; production always uses startup configuration.
func MountOAuthServerRoutes(router *gin.Engine, integration *service.OAuthIntegration) {
	h := controller.NewOAuthHTTP(integration)
	discovery := h.Guard(120, "metadata")
	browser := h.Guard(30, "browser")
	tokens := h.Guard(60, "token")
	resources := h.Guard(120, "resource")
	router.GET("/.well-known/oauth-authorization-server", discovery, h.Metadata)
	router.GET("/.well-known/oauth-protected-resource/api/oauth2", discovery, h.ResourceMetadata)
	router.GET("/api/oauth2/authorize", browser, h.Authorize)
	router.POST("/api/user/auth/oauth2/continue", browser, h.Continue)
	router.POST("/api/user/auth/oauth2/consent", browser, h.Consent)
	router.POST("/api/oauth2/token", tokens, h.Token)
	router.POST("/api/oauth2/revoke", tokens, h.Revoke)
	router.GET("/api/oauth2/catalog", resources, h.Catalog)
	router.GET("/api/oauth2/balance", resources, h.Balance)
}
