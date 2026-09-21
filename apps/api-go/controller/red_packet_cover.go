package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

func writeRedPacketCoverTokenError(c *gin.Context, err error, group string) {
	status, code, message := http.StatusServiceUnavailable, "RED_PACKET_COVER_KEY_UNAVAILABLE", "The red packet cover API key could not be prepared. Please try again later."
	switch {
	case errors.Is(err, model.ErrRedPacketTokenAccessDenied):
		status, code, message = http.StatusForbidden, "RED_PACKET_COVER_DEVELOPER_ACCESS_REQUIRED", err.Error()
	case errors.Is(err, model.ErrRedPacketTokenGroupUnavailable):
		status, code, message = http.StatusForbidden, "RED_PACKET_COVER_GROUP_UNAVAILABLE", err.Error()
	case errors.Is(err, model.ErrRedPacketTokenLimit):
		status, code, message = http.StatusForbidden, "RED_PACKET_COVER_KEY_LIMIT_REACHED", err.Error()
	case errors.Is(err, model.ErrRedPacketTokenWarningRequired):
		status, code, message = http.StatusForbidden, "GROUP_WARNING_CONFIRMATION_REQUIRED", err.Error()
	}
	response := gin.H{
		"success": false, "code": code, "message": message,
		"error": gin.H{"message": message, "type": "access_denied", "code": code},
	}
	if errors.Is(err, model.ErrRedPacketTokenWarningRequired) {
		if warning, ok := ratio_setting.GetGroupWarning(group); ok {
			response["warning"] = warning
			response["required_confirmations"] = warning.Confirmations
		}
	}
	c.AbortWithStatusJSON(status, response)
}

func prepareRedPacketCoverTokenContext(c *gin.Context, userID int, group string) (*model.Token, bool, bool) {
	token, created, err := model.ResolveRedPacketCoverToken(userID, group, service.IsUserSelectableGroup)
	if err != nil {
		writeRedPacketCoverTokenError(c, err, group)
		return nil, false, false
	}
	// Reuse the complete API-key policy
	headers := c.Request.Header.Clone()
	for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "X-Api-Key", "X-Goog-Api-Key", "Mj-Api-Secret", "Sec-WebSocket-Protocol"} {
		headers.Del(name)
	}
	c.Request.Header = headers.Clone()
	c.Request.Header.Set("Authorization", "Bearer "+token.Key)
	apiErr := middleware.RevalidateTokenAuth(c)
	c.Request.Header = headers
	if apiErr != nil {
		c.AbortWithStatusJSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return nil, false, false
	}
	// Verify authenticated account, key, and group match
	if common.GetContextKeyInt(c, constant.ContextKeyUserId) != userID ||
		common.GetContextKeyInt(c, constant.ContextKeyTokenId) != token.Id ||
		common.GetContextKeyString(c, constant.ContextKeyTokenGroup) != group ||
		common.GetContextKeyString(c, constant.ContextKeyUsingGroup) != group {
		writeRedPacketCoverTokenError(c, model.ErrRedPacketTokenGroupUnavailable, group)
		return nil, false, false
	}
	return token, created, true
}

// PrepareRedPacketCoverAuth creates or reuses an auto-managed key for red packet cover generation.
// Unlike PreparePlaygroundImageAuth, this does not enforce a minimum balance requirement,
// but still requires developer access (l1+).
func PrepareRedPacketCoverAuth(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Unauthorized",
			"error":   gin.H{"message": "Authentication required", "type": "unauthorized", "code": "UNAUTHORIZED"},
		})
		return
	}
	group := strings.TrimSpace(c.Query("group"))
	if group == "" {
		group = "image-2" // default group
	}
	if _, _, ok := prepareRedPacketCoverTokenContext(c, userID, group); !ok {
		return
	}
	c.Next()
}

// EnsureRedPacketCoverKey returns metadata for the red packet cover key.
// Creates the key if it doesn't exist yet.
func EnsureRedPacketCoverKey(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if c.GetBool("use_access_token") || strings.TrimSpace(c.GetString("session_id")) == "" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Red packet cover key creation requires an interactive browser session",
			"error":   gin.H{"message": "Red packet cover key creation requires an interactive browser session", "type": "forbidden", "code": "INTERACTIVE_SESSION_REQUIRED"},
		})
		return
	}
	var body struct {
		Group string `json:"group"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
			"error":   gin.H{"message": "Invalid request body", "type": "invalid_request", "code": "INVALID_REQUEST"},
		})
		return
	}
	group := strings.TrimSpace(body.Group)
	if group == "" {
		group = "image-2"
	}
	token, created, ok := prepareRedPacketCoverTokenContext(c, c.GetInt("id"), group)
	if !ok {
		return
	}
	common.ApiSuccess(c, gin.H{"id": token.Id, "name": token.Name, "group": token.Group, "created": created})
}
