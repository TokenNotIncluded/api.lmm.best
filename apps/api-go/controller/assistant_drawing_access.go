package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// Preserve the existing Credit gate. These are legacy pricing units, not USD.
const drawingWebMinimumLegacyUnits = 10

// Only the private MCP relay engine sets this key. No client header, query or
// tool argument is accepted as proof of an MCP origin.
type drawingMCPRelayIdentityKey struct{}
type drawingMCPAPIKeyIDKey struct{}
type drawingMCPRelayIdentity struct {
	UserID int
}

type drawingWebAccess struct {
	MinimumBalanceUSD    *float64 `json:"minimum_balance_usd"`
	MinimumBalanceCredit *int     `json:"minimum_balance_credit"`
	BalanceUSD           *float64 `json:"balance_usd"`
	BalanceCredit        *int     `json:"balance_credit"`
	Allowed              bool     `json:"allowed"`
}

var drawingUserQuota = model.GetUserQuota

func drawingWebAccessForUser(userID int) drawingWebAccess {
	access := drawingWebAccess{}
	if _, err := common.LegacyPricingUnitsPerUSD(); err != nil {
		return access
	}
	legacyUnit, err := common.LegacyPricingQuotaPerUnit()
	if err != nil {
		return access
	}
	minimumQuota, err := common.WalletQuotaFromDecimalStrict(legacyUnit.Mul(decimal.NewFromInt(drawingWebMinimumLegacyUnits)).Ceil())
	if err != nil {
		return access
	}
	minimum, _, err := assistantFiatProjection(int64(minimumQuota))
	if err != nil {
		return access
	}
	access.MinimumBalanceUSD, access.MinimumBalanceCredit = &minimum, &minimumQuota
	if userID <= 0 {
		return access
	}
	// Wallet reservations commit to the DB before invalidating Redis. A stale
	// cache must not authorize browser generation or hide a completed recharge.
	quota, err := drawingUserQuota(userID, true)
	if err != nil {
		return access
	}
	balance, _, err := assistantFiatProjection(int64(quota))
	if err != nil {
		return access
	}
	access.BalanceUSD = &balance
	access.BalanceCredit = &quota
	access.Allowed = quota >= minimumQuota
	return access
}

func requireDrawingWebBalance(c *gin.Context, userID int) bool {
	if identity, ok := c.Request.Context().Value(drawingMCPRelayIdentityKey{}).(drawingMCPRelayIdentity); ok && identity.UserID == userID && userID > 0 {
		return true
	}
	access := drawingWebAccessForUser(userID)
	if access.Allowed {
		common.SetContextKey(c, constant.ContextKeyWebDrawingMinimumQuota, common.GetTrustQuota())
		return true
	}
	status := http.StatusForbidden
	code := "WEB_DRAWING_MINIMUM_BALANCE"
	message := "Web drawing requires the minimum available wallet balance. API key creation and drawing via API or MCP remain available and are billed normally."
	if access.MinimumBalanceCredit != nil {
		message = fmt.Sprintf("Web drawing requires a starting available wallet balance of at least %d Credits. API key creation and drawing via API or MCP remain available and are billed normally.", *access.MinimumBalanceCredit)
	}
	if access.BalanceUSD == nil {
		status = http.StatusServiceUnavailable
		code = "WEB_DRAWING_BALANCE_UNAVAILABLE"
		message = "The available wallet balance or currency units could not be checked. API key creation and drawing via API or MCP remain available and are billed normally."
	}
	c.AbortWithStatusJSON(status, gin.H{
		"error":              gin.H{"message": message, "type": "access_denied", "code": code},
		"drawing_web_access": access,
	})
	return false
}

func writeDrawingTokenError(c *gin.Context, err error, group string) {
	status, code, message := http.StatusServiceUnavailable, "DRAWING_KEY_UNAVAILABLE", "The drawing API key could not be prepared. Please try again later."
	switch {
	case errors.Is(err, model.ErrDrawingTokenAccessDenied):
		status, code, message = http.StatusForbidden, "DRAWING_DEVELOPER_ACCESS_REQUIRED", err.Error()
	case errors.Is(err, model.ErrDrawingTokenGroupUnavailable):
		status, code, message = http.StatusForbidden, "DRAWING_GROUP_UNAVAILABLE", err.Error()
	case errors.Is(err, model.ErrDrawingTokenRequired):
		status, code, message = http.StatusForbidden, "DRAWING_KEY_REQUIRED", err.Error()
	case errors.Is(err, model.ErrDrawingTokenLimit):
		status, code, message = http.StatusForbidden, "DRAWING_KEY_LIMIT_REACHED", err.Error()
	case errors.Is(err, model.ErrDrawingTokenWarningRequired):
		status, code, message = http.StatusForbidden, "GROUP_WARNING_CONFIRMATION_REQUIRED", err.Error()
	}
	response := gin.H{
		"success": false, "code": code, "message": message,
		"error": gin.H{"message": message, "type": "access_denied", "code": code},
	}
	if errors.Is(err, model.ErrDrawingTokenWarningRequired) {
		if warning, ok := ratio_setting.GetGroupWarning(group); ok {
			response["warning"] = warning
			response["required_confirmations"] = warning.Confirmations
		}
	}
	c.AbortWithStatusJSON(status, response)
}

func drawingRelayHeaders(headers http.Header) http.Header {
	headers = headers.Clone()
	for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "X-Api-Key", "X-Goog-Api-Key", "Mj-Api-Secret", "Sec-WebSocket-Protocol"} {
		headers.Del(name)
	}
	return headers
}

func prepareDrawingTokenContext(c *gin.Context, userID int, group string, selected ...int) (*model.Token, bool, bool) {
	var token *model.Token
	var created bool
	var err error
	if len(selected) > 0 && selected[0] > 0 {
		token, err = model.ResolveDrawingTokenByID(userID, selected[0], group)
	} else {
		token, created, err = model.ResolveDrawingToken(userID, group, service.IsUserSelectableGroup)
	}
	if err != nil {
		writeDrawingTokenError(c, err, group)
		return nil, false, false
	}
	// Reuse the complete API-key policy, without TokenAuth's c.Next side effects.
	// Keep client-IP/proxy headers for normal IP policy, but strip all other
	// credentials both during authentication and before relay header snapshots.
	headers := drawingRelayHeaders(c.Request.Header)
	c.Request.Header = headers.Clone()
	c.Request.Header.Set("Authorization", "Bearer "+token.Key)
	apiErr := middleware.RevalidateTokenAuth(c)
	c.Request.Header = headers
	if apiErr != nil {
		c.AbortWithStatusJSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return nil, false, false
	}
	// A concurrent edit/cache inconsistency must never switch the authenticated
	// account, key or group between resolution and the authoritative validator.
	if common.GetContextKeyInt(c, constant.ContextKeyUserId) != userID ||
		common.GetContextKeyInt(c, constant.ContextKeyTokenId) != token.Id ||
		common.GetContextKeyString(c, constant.ContextKeyTokenGroup) != group ||
		common.GetContextKeyString(c, constant.ContextKeyUsingGroup) != group {
		writeDrawingTokenError(c, model.ErrDrawingTokenGroupUnavailable, group)
		return nil, false, false
	}
	common.SetContextKey(c, constant.ContextKeyDrawingRealToken, true)
	return token, created, true
}

// PreparePlaygroundImageAuth must run before Distribute and model rate limits.
// Generic chat playground requests deliberately do not enter this middleware.
func PreparePlaygroundImageAuth(c *gin.Context) {
	user, group, ok := playgroundImageUserAndGroup(c)
	if !ok || !requireDrawingWebBalance(c, c.GetInt("id")) {
		c.Abort()
		return
	}
	selectedKeyID, _ := c.Request.Context().Value(drawingMCPAPIKeyIDKey{}).(int)
	if _, _, ok := prepareDrawingTokenContext(c, user.Id, group, selectedKeyID); !ok {
		return
	}
	c.Next()
}

// EnsureAssistantDrawingKey returns metadata only. Existing key management is
// the sole reveal surface; neither generation nor MCP responses return secrets.
func EnsureAssistantDrawingKey(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if c.GetBool("use_access_token") || strings.TrimSpace(c.GetString("session_id")) == "" {
		writeAssistantError(c, http.StatusForbidden, "ASSISTANT_INTERACTIVE_SESSION_REQUIRED", errors.New("drawing key creation requires an interactive browser session"))
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil || body == nil || len(body) != 0 {
		writeAssistantError(c, http.StatusBadRequest, "ASSISTANT_INVALID_REQUEST", errors.New("drawing key requests require an empty JSON object"))
		return
	}
	token, created, ok := prepareDrawingTokenContext(c, c.GetInt("id"), model.DrawingTokenGroup)
	if !ok {
		return
	}
	common.ApiSuccess(c, gin.H{"id": token.Id, "name": token.Name, "group": token.Group, "created": created})
}
