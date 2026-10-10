package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// PiRemoteAuth accepts the browser's normal user identity or the explicitly
// consented Pi remote scope. It never turns an OAuth token into dashboard access,
// creates a billing token, or depends on the currently selected model or quota.
func PiRemoteAuth() gin.HandlerFunc {
	browserAuth := UserAuth()
	return func(c *gin.Context) {
		if !isOAuthResourceAttempt(c) {
			browserAuth(c)
			return
		}
		integration := service.CurrentOAuthIntegration()
		if integration == nil || !integration.OAuthRequestTransport(c.Request) {
			oauthResourceFailure(c, http.StatusUnauthorized, "invalid_token")
			return
		}
		if service.OAuthAlternateCredentials(c.Request) || len(c.Request.Header.Values(service.OAuthGroupHeader)) != 0 {
			oauthResourceFailure(c, http.StatusBadRequest, "invalid_request")
			return
		}
		access, err := oauthserver.BearerFromRequest(c.Request)
		if err != nil {
			oauthResourceFailure(c, http.StatusUnauthorized, "invalid_token")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		grant, user, err := integration.ValidateResource(ctx, access, service.OAuthRemoteControlScope)
		if err != nil || grant.ClientID != service.OAuthPiClientID {
			oauthResourceFailure(c, http.StatusForbidden, "insufficient_scope")
			return
		}
		service.OAuthNoStore(c.Writer)
		c.Set("id", user.Id)
		c.Set("username", user.Username)
		c.Set("role", user.Role)
		user.ToBaseUser().WriteContext(c)
		c.Next()
	}
}
