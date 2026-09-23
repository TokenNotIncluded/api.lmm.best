package router

import (
	"fmt"
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// Native OAuth and the subproject OIDC issuer have separate discovery paths.
// Existing Pi/DSH/CLI clients retain their original endpoints and scope policy.
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
	if err := mountSubprojectOIDC(router); err != nil {
		return fmt.Errorf("initialize subproject OIDC: %w", err)
	}
	return nil
}

// Disabled endpoints return 404 instead of accidentally serving the SPA.
func MountOAuthServerRoutes(router *gin.Engine, integration *service.OAuthIntegration) {
	h := controller.NewOAuthHTTP(integration)
	discovery := h.Guard(120, "metadata")
	browser := h.Guard(30, "browser")
	tokens := h.Guard(60, "token")
	resources := h.Guard(120, "resource")
	activity := h.Guard(30, "activity")
	router.GET("/.well-known/oauth-authorization-server", discovery, h.Metadata)
	router.GET("/.well-known/oauth-protected-resource/api/oauth2", discovery, h.ResourceMetadata)
	router.GET("/api/oauth2/authorize", browser, h.Authorize)
	router.POST("/api/user/auth/oauth2/continue", browser, h.Continue)
	router.POST("/api/user/auth/oauth2/consent", browser, h.Consent)
	router.POST("/api/oauth2/token", tokens, h.Token)
	router.POST("/api/oauth2/revoke", tokens, h.Revoke)
	router.GET("/api/oauth2/catalog", resources, h.Catalog)
	router.GET("/api/oauth2/balance", resources, h.Balance)
	router.GET("/api/oauth2/usage/activity", activity, h.Activity)
}
