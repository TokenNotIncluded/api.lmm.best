package controller

import (
	"net/http"
	"strconv"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

func (h *OAuthHTTP) resource(c *gin.Context, scope string) (oauthserver.Grant, *model.User, bool) {
	return h.resourceQuery(c, scope, false)
}

func (h *OAuthHTTP) resourceQuery(c *gin.Context, scope string, allowQuery bool) (oauthserver.Grant, *model.User, bool) {
	if (!allowQuery && c.Request.URL.RawQuery != "") || c.Request.URL.ForceQuery || service.OAuthAlternateCredentials(c.Request) || len(c.Request.Header.Values(service.OAuthGroupHeader)) != 0 {
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

func (h *OAuthHTTP) Activity(c *gin.Context) {
	query := c.Request.URL.Query()
	for key := range query {
		if key != "from" && key != "to" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}
	}
	defaultEnd := time.Now().Unix()
	parse := func(name string, fallback int64) (int64, bool) {
		values, present := query[name]
		if !present {
			return fallback, true
		}
		if len(values) != 1 || values[0] == "" {
			return 0, false
		}
		parsed, err := strconv.ParseInt(values[0], 10, 64)
		return parsed, err == nil
	}
	end, ok := parse("to", defaultEnd)
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	start, ok := parse("from", end-365*24*60*60)
	if !ok || start < 0 || end <= start || end-start > 366*24*60*60 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	_, user, valid := h.resourceQuery(c, service.OAuthUsageScope, true)
	if !valid {
		return
	}
	activity, err := model.GetUserUsageActivity(user.Id, start, end)
	if err != nil {
		oauthProtocolFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"schema_version":  1,
		"start_timestamp": activity.StartTimestamp,
		"end_timestamp":   activity.EndTimestamp,
		"timezone":        activity.Timezone,
		"days":            activity.Days,
		"totals": gin.H{
			"requests":          activity.Requests,
			"prompt_tokens":     activity.PromptTokens,
			"completion_tokens": activity.CompletionTokens,
			"total_tokens":      activity.PromptTokens + activity.CompletionTokens,
			"quota":             activity.Quota,
		},
	})
}
