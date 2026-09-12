package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

const oauthRelayGroupKey = "oauth_resource_group"

// Detection precedes every legacy credential rewrite. A signalled OAuth request
// never falls back to API-key auth, including when the feature is disabled.
func isOAuthResourceAttempt(c *gin.Context) bool {
	if len(c.Request.Header.Values(service.OAuthGroupHeader)) != 0 {
		return true
	}
	for _, name := range []string{"Authorization", "x-api-key", "x-goog-api-key", "mj-api-secret", "Sec-WebSocket-Protocol"} {
		for _, value := range c.Request.Header.Values(name) {
			if strings.Contains(strings.ToLower(value), "lmm_") || strings.Contains(value, model.OAuthBillingKeyPrefix) {
				return true
			}
		}
	}
	query := c.Request.URL.Query()
	if _, exists := query["access_token"]; exists {
		return true
	}
	for _, name := range []string{"key", "api_key", "token"} {
		for _, value := range query[name] {
			if strings.Contains(value, "lmm_") || strings.Contains(value, model.OAuthBillingKeyPrefix) {
				return true
			}
		}
	}
	return false
}

func oauthResourceFailure(c *gin.Context, status int, code string) {
	service.OAuthNoStore(c.Writer)
	c.Header("WWW-Authenticate", `Bearer error="`+code+`"`)
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": "authentication_error", "code": code, "message": "OAuth request is not authorized"}})
}

func authenticateOAuthResource(c *gin.Context) {
	integration := service.CurrentOAuthIntegration()
	if integration == nil || !integration.OAuthRequestTransport(c.Request) {
		oauthResourceFailure(c, http.StatusUnauthorized, "invalid_token")
		return
	}
	if c.Request.Method != http.MethodPost || service.OAuthAPIForPath(c.Request.URL.Path) == "" || c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery || service.OAuthAlternateCredentials(c.Request) || len(c.Request.Header.Values("Content-Type")) != 1 {
		oauthResourceFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	contentType, _, err := mime.ParseMediaType(c.Request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		oauthResourceFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	tokenValue, err := oauthserver.BearerFromRequest(c.Request)
	ids := c.Request.Header.Values(service.OAuthGroupHeader)
	if err != nil || len(ids) != 1 {
		oauthResourceFailure(c, http.StatusUnauthorized, "invalid_token")
		return
	}
	group, err := service.OAuthGroupFromID(ids[0])
	if err != nil {
		oauthResourceFailure(c, http.StatusBadRequest, "invalid_request")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	grant, user, err := integration.ValidateResource(ctx, tokenValue, service.OAuthInvokeScope, service.OAuthGroupScope(group))
	if err != nil || !slices.Contains(integration.GrantedGroups(user, grant), group) {
		oauthResourceFailure(c, http.StatusForbidden, "insufficient_scope")
		return
	}
	token, err := model.EnsureOAuthBillingToken(ctx, integration.DB, grant.FamilyID, user.Id, group, service.OAuthGroupScope(group))
	if err != nil {
		oauthResourceFailure(c, http.StatusUnauthorized, "invalid_token")
		return
	}
	c.Set("id", user.Id)
	c.Set("username", user.Username)
	c.Set("role", user.Role)
	user.ToBaseUser().WriteContext(c)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
	if err := SetupContextForToken(c, token); err != nil {
		oauthResourceFailure(c, http.StatusUnauthorized, "invalid_token")
		return
	}
	c.Set(oauthRelayGroupKey, group)
	c.Set("oauth_grant_id", grant.FamilyID)
	c.Next()
}

// ValidateOAuthRelayModel runs after bounded body admission, before any channel
// routing. It rejects ambiguous JSON model/group fields and rechecks the live
// grant, user, group AND enabled channel capability on the writer database.
func ValidateOAuthRelayModel(c *gin.Context, routedModel string) bool {
	group, exists := c.Get(oauthRelayGroupKey)
	if !exists {
		return true
	}
	integration := service.CurrentOAuthIntegration()
	if integration == nil {
		oauthResourceFailure(c, 401, "invalid_token")
		return false
	}
	var body oauthModelBody
	if common.UnmarshalBodyReusable(c, &body) != nil || body.Model != routedModel {
		oauthResourceFailure(c, 400, "invalid_request")
		return false
	}
	access, err := oauthserver.BearerFromRequest(c.Request)
	if err != nil {
		oauthResourceFailure(c, 401, "invalid_token")
		return false
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	grant, user, err := integration.ValidateResource(ctx, access, service.OAuthInvokeScope, service.OAuthGroupScope(group.(string)))
	if err != nil || integration.ValidateModel(ctx, user, grant, group.(string), body.Model, c.Request.URL.Path) != nil {
		oauthResourceFailure(c, 403, "insufficient_scope")
		return false
	}
	// Consumed routing metadata must never become an upstream authorization input.
	c.Request.Header.Del(service.OAuthGroupHeader)
	return true
}

type oauthModelBody struct{ Model string }

func (b *oauthModelBody) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return errors.New("expected JSON object")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return errors.New("duplicate JSON field")
		}
		seen[name] = true
		// encoding/json binds struct fields case-insensitively, while routing's
		// gjson lookup is case-sensitive. Do not let Model/GROUP aliases change
		// the authorized envelope later in the relay pipeline.
		lower := strings.ToLower(name)
		switch lower {
		case "model", "group", "access_token", "api_key", "key", "authorization":
			if name != lower {
				return errors.New("noncanonical routing or credential field")
			}
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		switch name {
		case "model":
			if err := json.Unmarshal(raw, &b.Model); err != nil {
				return err
			}
		case "group", "access_token", "api_key", "key", "authorization":
			return errors.New("alternate routing or credential field")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	if b.Model == "" || len(b.Model) > 512 {
		return errors.New("missing model")
	}
	return nil
}
