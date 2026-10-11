package router

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/humangrant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
)

func SetHumanGrantRouter(router *gin.Engine) error {
	store, err := service.NewHumanGrantStore(model.DB)
	if err != nil {
		return fmt.Errorf("initialize HumanGrant: %w", err)
	}
	raw := strings.TrimSpace(os.Getenv("LMM_HUMANGRANT_ORIGIN"))
	if raw == "" {
		raw = strings.TrimSpace(system_setting.ServerAddress)
	}
	local := os.Getenv("LMM_HUMANGRANT_ALLOW_LOCAL_HTTP") == "true"
	// Keep unrelated human login available when the site has not configured its
	// public HTTPS address yet. HG reports ready:false and fails closed with 503.
	origin, _ := humangrant.Origin(raw, local)
	engine := &humangrant.Engine{Store: store, Origin: origin, Local: local}
	MountHumanGrantRoutes(router, engine)
	return nil
}
func MountHumanGrantRoutes(router *gin.Engine, handler http.Handler) {
	serve := func(c *gin.Context) {
		// The generic Gin recovery logger dumps request URLs. This dedicated
		// recovery path never logs private authorization links or headers.
		defer func() {
			if recover() != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "The request could not be completed.", "retry_after_seconds": 0})
			}
		}()
		controller := http.NewResponseController(c.Writer)
		_ = controller.SetReadDeadline(time.Now().Add(15 * time.Second))
		_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
		defer controller.SetReadDeadline(time.Time{})
		defer controller.SetWriteDeadline(time.Time{})
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		handler.ServeHTTP(c.Writer, c.Request.WithContext(ctx))
		c.Abort()
	}
	routes := map[string][]string{
		"GET":  {"/humangrant", "/humangrant/config", "/humangrant/client.js", "/humangrant/ui.js", "/humangrant/style.css", "/human/grants", "/human/:link", "/invite/:token", "/v1/requests/:id", "/v1/requests/:id/status.md", "/v1/session", "/v1/accounts/:id/profile", humangrant.BrowserPrefix + "/settings", humangrant.BrowserPrefix + "/grants"},
		"POST": {"/v1/requests", "/v1/requests/:id/claim", "/v1/requests/:id/cancel", "/v1/requests/:id/email", humangrant.BrowserPrefix + "/human/:link/begin", humangrant.BrowserPrefix + "/human/:link/verify", humangrant.BrowserPrefix + "/human/:link/approve", humangrant.BrowserPrefix + "/human/:link/reject", humangrant.BrowserPrefix + "/grants/:id/revoke", "/invite/:token/reject", "/invite/:token/block"},
		"PUT":  {humangrant.BrowserPrefix + "/settings", "/v1/accounts/:id/profile"},
	}
	for method, paths := range routes {
		for _, path := range paths {
			router.Handle(method, path, serve)
		}
	}
}
