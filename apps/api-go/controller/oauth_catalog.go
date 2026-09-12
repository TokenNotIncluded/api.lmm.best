package controller

import (
	"net/http"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func (h *OAuthHTTP) resource(c *gin.Context, scope string) (oauthserver.Grant, *model.User, bool) {
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery || service.OAuthAlternateCredentials(c.Request) || len(c.Request.Header.Values(service.OAuthGroupHeader)) != 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return oauthserver.Grant{}, nil, false
	}
	raw, err := oauthserver.BearerFromRequest(c.Request)
	if err != nil {
		oauthProtocolFailure(c, err)
		return oauthserver.Grant{}, nil, false
	}
	grant, user, err := h.Integration.ValidateResource(c.Request.Context(), raw, scope)
	if err != nil {
		c.Header("WWW-Authenticate", `Bearer error="invalid_token", resource_metadata="`+h.Integration.Issuer+`/.well-known/oauth-protected-resource/api/oauth2"`)
		oauthProtocolFailure(c, err)
		return oauthserver.Grant{}, nil, false
	}
	return grant, user, true
}

func (h *OAuthHTTP) Catalog(c *gin.Context) {
	grant, user, ok := h.resource(c, service.OAuthCatalogScope)
	if !ok {
		return
	}
	catalog, err := h.Integration.Catalog(c.Request.Context(), user, grant)
	if err != nil {
		oauthProtocolFailure(c, err)
		return
	}
	c.JSON(200, catalog)
}

func (h *OAuthHTTP) Balance(c *gin.Context) {
	_, user, ok := h.resource(c, service.OAuthBalanceScope)
	if !ok {
		return
	}
	var balance *float64
	if common.QuotaPerUnit > 0 {
		value := float64(user.Quota) / common.QuotaPerUnit
		balance = &value
	}
	c.JSON(200, gin.H{"schema_version": 1, "currency": "platform_credit", "balance": balance, "quota": user.Quota, "quota_per_unit": common.QuotaPerUnit, "updated_at": time.Now().Unix(), "authorization_limit": nil})
}
