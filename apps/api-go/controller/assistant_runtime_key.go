package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
)

// EnsureAssistantRuntimeKey lets the assistant's billing owner make the
// internal credential visible in key management before the next chat request.
// No key material is returned to the browser.
func EnsureAssistantRuntimeKey(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if c.GetBool("use_access_token") || strings.TrimSpace(c.GetString("session_id")) == "" {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_SESSION_REQUIRED", errors.New("a browser login session is required"))
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil || body == nil || len(body) != 0 {
		writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_INVALID_REQUEST", errors.New("an empty JSON object is required"))
		return
	}
	billingUser, err := loadAssistantBillingUser()
	if err != nil || billingUser == nil {
		writeAssistantError(c, http.StatusServiceUnavailable, "ASSISTANT_BILLING_ACCOUNT_UNAVAILABLE", errors.New("assistant billing account is unavailable"))
		return
	}
	if billingUser.Id != c.GetInt("id") {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_BILLING_OWNER_REQUIRED", errors.New("this super administrator does not own assistant billing"))
		return
	}
	settings := setting.GetAssistantSettings()
	if !settings.Enabled {
		writeAssistantError(c, http.StatusConflict, "ASSISTANT_DISABLED", errors.New("the AI assistant is disabled"))
		return
	}
	group, _, err := assistantConfiguredRouteResolver(settings)
	if err != nil {
		writeAssistantError(c, http.StatusServiceUnavailable, "ASSISTANT_ROUTING_GROUP_UNAVAILABLE", errors.New("assistant model route is unavailable"))
		return
	}
	token, created, err := ensureAssistantRuntimeToken(billingUser.Id, group)
	if err != nil {
		writeAssistantError(c, http.StatusServiceUnavailable, "ASSISTANT_RUNTIME_KEY_UNAVAILABLE", errors.New("assistant runtime key is unavailable"))
		return
	}
	common.ApiSuccess(c, gin.H{"id": token.Id, "name": token.Name, "group": token.Group, "created": created})
}
