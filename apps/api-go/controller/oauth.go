package controller

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauth"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const oauthAuthFlowTTL = 10 * time.Minute

const oauthStateCookiePrefix = "lmm_oauth_state_"

// oauthStateCookieName keeps concurrent provider logins independent while
// avoiding user-controlled characters in the cookie name.
func oauthStateCookieName(provider string) string {
	digest := sha256.Sum256([]byte(provider))
	return oauthStateCookiePrefix + hex.EncodeToString(digest[:8])
}

func setOAuthStateCookie(c *gin.Context, provider, state string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     oauthStateCookieName(provider),
		Value:    state,
		Path:     "/",
		MaxAge:   int(oauthAuthFlowTTL / time.Second),
		HttpOnly: true,
		// OAuth state is a bearer CSRF token. It must never be sent over
		// cleartext HTTP, even when other development cookies are relaxed.
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearOAuthStateCookie(c *gin.Context, provider string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     oauthStateCookieName(provider),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func oauthStateCookieMatches(c *gin.Context, provider, state string) bool {
	cookie, err := c.Request.Cookie(oauthStateCookieName(provider))
	if err != nil || cookie.Value == "" || state == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) == 1
}

type oauthStateRequest struct {
	Provider      string `json:"provider"`
	Intent        string `json:"intent"`
	Aff           string `json:"aff,omitempty"`
	AcceptedLegal bool   `json:"accepted_legal"`
}

type oauthFlowPayload struct {
	AffiliateCode string `json:"affiliate_code,omitempty"`
	AcceptedLegal bool   `json:"accepted_legal,omitempty"`
}

// providerParams returns map with Provider key for i18n templates
func providerParams(name string) map[string]any {
	return map[string]any{"Provider": name}
}

func applyLinuxDOPaymentRestriction(provider oauth.Provider, oauthUser *oauth.OAuthUser, user *model.User) error {
	if _, ok := provider.(*oauth.LinuxDOProvider); !ok || oauthUser == nil || user == nil {
		return nil
	}
	score, ok := oauthUser.Extra["gamification_score"].(float64)
	if !ok || score < 0 {
		return nil
	}
	if err := model.UpdateLinuxDOGamificationScore(user.Id, score); err != nil {
		return err
	}
	user.LinuxDOGamificationScore = score
	user.LinuxDOScoreUpdatedAt = time.Now().Unix()
	if score > model.LinuxDOGamificationScorePaymentThreshold {
		user.PaymentRestrictionFlags |= model.PaymentRestrictionLinuxDOHighScore
	} else {
		user.PaymentRestrictionFlags &^= model.PaymentRestrictionLinuxDOHighScore
	}
	return nil
}

// GenerateOAuthCode generates a state code for OAuth CSRF protection
func GenerateOAuthCode(c *gin.Context) {
	var request oauthStateRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	request.Provider = strings.TrimSpace(request.Provider)
	request.Intent = strings.TrimSpace(request.Intent)
	request.Aff = strings.TrimSpace(request.Aff)
	if oauth.GetProvider(request.Provider) == nil ||
		(request.Intent != model.AuthFlowIntentLogin && request.Intent != model.AuthFlowIntentBind) ||
		len(request.Aff) > 32 ||
		(request.Intent == model.AuthFlowIntentBind && request.Aff != "") {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	userID := 0
	sessionID := ""
	if request.Intent == model.AuthFlowIntentBind {
		identity, ok := middleware.GetSessionAuthIdentity(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "绑定操作需要登录"})
			return
		}
		userID = identity.UserID
		sessionID = identity.SessionID
	}
	payload, err := common.Marshal(oauthFlowPayload{
		AffiliateCode: request.Aff,
		AcceptedLegal: request.Intent == model.AuthFlowIntentLogin && request.AcceptedLegal,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	expiresAt := time.Now().Add(oauthAuthFlowTTL)
	state, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   model.AuthFlowPurposeOAuth,
		Provider:  request.Provider,
		Intent:    request.Intent,
		UserId:    userID,
		SessionId: sessionID,
		Payload:   string(payload),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	setOAuthStateCookie(c, request.Provider, state)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"flow_token": state,
			"expires_at": expiresAt.Unix(),
		},
	})
}

// HandleOAuth handles OAuth callback for all standard OAuth providers
func HandleOAuth(c *gin.Context) {
	providerName := c.Param("provider")
	provider := oauth.GetProvider(providerName)
	if provider == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgOAuthUnknownProvider),
		})
		return
	}

	// 1. Validate state (CSRF protection)
	state := c.Query("state")
	pendingFlow, err := model.GetAuthFlow(state, model.AuthFlowMatch{
		Purpose:  model.AuthFlowPurposeOAuth,
		Provider: providerName,
	})
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgOAuthStateInvalid),
		})
		return
	}
	// Both login and binding callbacks must remain bound to the browser that
	// started the flow. Bind flows also carry a server-side session reference,
	// but that reference alone is a bearer capability if the state leaks through
	// a provider redirect, history, or referrer.
	if !oauthStateCookieMatches(c, providerName, state) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgOAuthStateInvalid),
		})
		return
	}

	consumeMatch := model.AuthFlowMatch{
		Purpose:  model.AuthFlowPurposeOAuth,
		Provider: providerName,
		Intent:   pendingFlow.Intent,
	}
	// 2. Bind flows are bound to the live dashboard Session that created them.
	if pendingFlow.Intent == model.AuthFlowIntentBind {
		identity, ok := middleware.GetSessionAuthIdentity(c)
		if ok {
			if identity.UserID != pendingFlow.UserId || identity.SessionID != pendingFlow.SessionId {
				c.JSON(http.StatusForbidden, gin.H{
					"success": false,
					"message": i18n.T(c, i18n.MsgOAuthStateInvalid),
				})
				return
			}
		} else if strings.TrimSpace(c.GetHeader("Authorization")) != "" {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": i18n.T(c, i18n.MsgOAuthStateInvalid),
			})
			return
		} else {
			identity, err = service.ValidateSessionReference(pendingFlow.UserId, pendingFlow.SessionId)
			if err != nil {
				c.JSON(http.StatusForbidden, gin.H{
					"success": false,
					"message": i18n.T(c, i18n.MsgOAuthStateInvalid),
				})
				return
			}
		}
		consumeMatch.UserId = identity.UserID
		consumeMatch.SessionId = identity.SessionID
	} else if pendingFlow.Intent != model.AuthFlowIntentLogin {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	// 3. Check if provider is enabled
	if !provider.IsEnabled() {
		common.ApiErrorI18n(c, i18n.MsgOAuthNotEnabled, providerParams(provider.GetName()))
		return
	}

	// 4. Handle error from provider
	errorCode := c.Query("error")
	if errorCode != "" {
		if _, err := model.ConsumeAuthFlow(state, consumeMatch); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": i18n.T(c, i18n.MsgOAuthStateInvalid)})
			return
		}
		clearOAuthStateCookie(c, providerName)
		// OAuth error_description is provider-controlled query data. Do not echo
		// it to the browser: a provider response (or a crafted callback) could
		// otherwise expose internal details through the public callback endpoint.
		common.SysLog(fmt.Sprintf("[OAuth] provider callback rejected: provider=%q", providerName))
		common.ApiErrorI18n(c, i18n.MsgOAuthConnectFailed, providerParams(provider.GetName()))
		return
	}
	if pendingFlow.Intent == model.AuthFlowIntentBind {
		handleOAuthBind(c, provider, pendingFlow, state)
		return
	}

	// 5. Exchange code for token
	code := c.Query("code")
	token, err := provider.ExchangeToken(c.Request.Context(), code, c)
	if err != nil {
		handleOAuthError(c, err)
		return
	}

	// 6. Get user info
	oauthUser, err := provider.GetUserInfo(c.Request.Context(), token)
	if err != nil {
		handleOAuthError(c, err)
		return
	}
	flow, err := model.ConsumeAuthFlow(state, consumeMatch)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": i18n.T(c, i18n.MsgOAuthStateInvalid)})
		return
	}
	clearOAuthStateCookie(c, providerName)

	// 7. Find or create user
	var payload oauthFlowPayload
	if err := common.UnmarshalJsonStr(flow.Payload, &payload); err != nil {
		common.ApiError(c, err)
		return
	}
	user, err := findOrCreateOAuthUser(c, provider, oauthUser, payload.AffiliateCode, payload.AcceptedLegal)
	if err != nil {
		var gateErr *registrationGateError
		if errors.As(err, &gateErr) {
			writeRegistrationGateError(c, gateErr)
			return
		}
		if errors.Is(err, model.ErrEmailAlreadyTaken) {
			common.ApiErrorI18n(c, i18n.MsgUserEmailAlreadyTaken)
			return
		}
		switch err.(type) {
		case *OAuthUserDeletedError:
			common.ApiErrorI18n(c, i18n.MsgOAuthUserDeleted)
		case *OAuthRegistrationDisabledError:
			common.ApiErrorI18n(c, i18n.MsgUserRegisterDisabled)
		case *OAuthEmailAlreadyTakenError:
			common.ApiErrorI18n(c, i18n.MsgUserEmailAlreadyTaken)
		case *OAuthEmailVerificationRequiredError:
			common.ApiErrorI18n(c, i18n.MsgOAuthEmailVerificationRequired)
		default:
			common.ApiError(c, err)
		}
		return
	}
	if err := applyLinuxDOPaymentRestriction(provider, oauthUser, user); err != nil {
		common.ApiError(c, err)
		return
	}

	// 8. Check user status
	if user.Status != common.UserStatusEnabled {
		common.ApiErrorI18n(c, i18n.MsgOAuthUserBanned)
		return
	}

	// 9. Setup login
	setupLogin(user, c)
}

// handleOAuthBind handles binding OAuth account to existing user
func handleOAuthBind(c *gin.Context, provider oauth.Provider, pendingFlow *model.AuthFlow, flowToken string) {
	// Exchange code for token
	code := c.Query("code")
	token, err := provider.ExchangeToken(c.Request.Context(), code, c)
	if err != nil {
		handleOAuthError(c, err)
		return
	}

	// Get user info
	oauthUser, err := provider.GetUserInfo(c.Request.Context(), token)
	if err != nil {
		handleOAuthError(c, err)
		return
	}

	// Check if this OAuth account is already bound (check both new ID and legacy ID)
	if provider.IsUserIDTaken(oauthUser.ProviderUserID) {
		common.ApiErrorI18n(c, i18n.MsgOAuthAlreadyBound, providerParams(provider.GetName()))
		return
	}
	// Also check legacy ID to prevent duplicate bindings during migration period
	if legacyID, ok := oauthUser.Extra["legacy_id"].(string); ok && legacyID != "" {
		if provider.IsUserIDTaken(legacyID) {
			common.ApiErrorI18n(c, i18n.MsgOAuthAlreadyBound, providerParams(provider.GetName()))
			return
		}
	}

	if _, err := model.ConsumeAuthFlow(flowToken, model.AuthFlowMatch{
		Purpose:   model.AuthFlowPurposeOAuth,
		Provider:  pendingFlow.Provider,
		Intent:    model.AuthFlowIntentBind,
		UserId:    pendingFlow.UserId,
		SessionId: pendingFlow.SessionId,
	}); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": i18n.T(c, i18n.MsgOAuthStateInvalid)})
		return
	}

	userId := pendingFlow.UserId

	// Handle binding based on provider type
	if genericProvider, ok := provider.(*oauth.GenericOAuthProvider); ok {
		// Custom provider: use user_oauth_bindings table
		err = model.UpdateUserOAuthBinding(userId, genericProvider.GetProviderId(), oauthUser.ProviderUserID)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	} else {
		// Built-in provider: update only the binding column. Do not write the
		// full user snapshot, which may contain stale role/status/group values.
		column := provider.ProviderUserIDColumn()
		if column == "" {
			err = errors.New("oauth provider does not expose a bind column")
		} else {
			err = model.UpdateUserBindColumn(userId, column, oauthUser.ProviderUserID)
		}
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if err := applyLinuxDOPaymentRestriction(provider, oauthUser, &model.User{Id: userId}); err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccessI18n(c, i18n.MsgOAuthBindSuccess, gin.H{
		"action": "bind",
	})
}

// findOrCreateOAuthUser finds existing user or creates new user
func findOrCreateOAuthUser(c *gin.Context, provider oauth.Provider, oauthUser *oauth.OAuthUser, affiliateCode string, acceptedLegal bool) (*model.User, error) {
	user := &model.User{}

	// Check if user already exists with new ID
	if provider.IsUserIDTaken(oauthUser.ProviderUserID) {
		err := provider.FillUserByProviderID(user, oauthUser.ProviderUserID)
		if err != nil {
			return nil, err
		}
		// Check if user has been deleted
		if user.Id == 0 {
			return nil, &OAuthUserDeletedError{}
		}
		return user, nil
	}

	// Try to find user with legacy ID (for GitHub migration from login to numeric ID)
	if legacyID, ok := oauthUser.Extra["legacy_id"].(string); ok && legacyID != "" {
		if provider.IsUserIDTaken(legacyID) {
			err := provider.FillUserByProviderID(user, legacyID)
			if err != nil {
				return nil, err
			}
			if user.Id != 0 {
				// Found user with legacy ID, migrate to new ID
				common.SysLog(fmt.Sprintf("[OAuth] Migrating user %d from legacy_id=%s to new_id=%s",
					user.Id, legacyID, oauthUser.ProviderUserID))
				if err := user.UpdateGitHubId(oauthUser.ProviderUserID); err != nil {
					common.SysError(fmt.Sprintf("[OAuth] Failed to migrate user %d: %s", user.Id, err.Error()))
					// Continue with login even if migration fails
				}
				return user, nil
			}
		}
	}

	// User doesn't exist, create new user if registration is enabled
	if !common.RegisterEnabled {
		return nil, &OAuthRegistrationDisabledError{}
	}
	if !common.OAuthRegisterEnabled {
		return nil, &OAuthRegistrationDisabledError{}
	}
	if common.IsRegistrationMethodDisabled(oauthRegistrationMethod(provider)) {
		return nil, &OAuthRegistrationDisabledError{}
	}
	if err := publicRegistrationGateFailure(acceptedLegal); err != nil {
		return nil, err
	}
	if common.EmailVerificationEnabled && (oauthUser == nil || !oauthUser.EmailVerified) {
		return nil, &OAuthEmailVerificationRequiredError{}
	}

	// Set up new user
	user.Username = provider.GetProviderPrefix() + strconv.Itoa(model.GetMaxUserId()+1)

	if oauthUser.Username != "" {
		if exists, err := model.CheckUserExistOrDeleted(oauthUser.Username, ""); err == nil && !exists {
			// 防止索引退化
			if len(oauthUser.Username) <= model.UserNameMaxLength {
				user.Username = oauthUser.Username
			}
		}
	}

	if oauthUser.DisplayName != "" {
		user.DisplayName = oauthUser.DisplayName
	} else if oauthUser.Username != "" {
		user.DisplayName = oauthUser.Username
	} else {
		user.DisplayName = provider.GetName() + " User"
	}
	if oauthUser.Email != "" {
		user.Email = model.NormalizeEmail(oauthUser.Email)
		if err := model.EnsureEmailAvailable(user.Email, 0); err != nil {
			if errors.Is(err, model.ErrEmailAlreadyTaken) {
				return nil, &OAuthEmailAlreadyTakenError{}
			}
			return nil, err
		}
	}
	user.Role = common.RoleCommonUser
	user.Status = common.UserStatusEnabled

	// Handle affiliate code
	inviterId := 0
	if affiliateCode != "" {
		inviterId, _ = model.GetUserIdByAffCode(affiliateCode)
	}

	// Use transaction to ensure user creation and OAuth binding are atomic
	if genericProvider, ok := provider.(*oauth.GenericOAuthProvider); ok {
		// Custom provider: create user and binding in a transaction
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			// Create user
			if err := user.InsertWithTx(tx, inviterId); err != nil {
				return err
			}

			// Create OAuth binding
			binding := &model.UserOAuthBinding{
				UserId:         user.Id,
				ProviderId:     genericProvider.GetProviderId(),
				ProviderUserId: oauthUser.ProviderUserID,
			}
			if err := model.CreateUserOAuthBindingWithTx(tx, binding); err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		// Perform post-transaction tasks (logs, sidebar config, inviter rewards)
		user.FinalizeOAuthUserCreation(inviterId)
	} else {
		// Built-in provider: create user and update provider ID in a transaction
		err := model.DB.Transaction(func(tx *gorm.DB) error {
			// Create user
			if err := user.InsertWithTx(tx, inviterId); err != nil {
				return err
			}

			// Set the provider user ID on the user model and update
			provider.SetProviderUserID(user, oauthUser.ProviderUserID)
			if err := tx.Model(user).Updates(map[string]interface{}{
				"github_id":   user.GitHubId,
				"discord_id":  user.DiscordId,
				"oidc_id":     user.OidcId,
				"linux_do_id": user.LinuxDOId,
				"wechat_id":   user.WeChatId,
				"telegram_id": user.TelegramId,
			}).Error; err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		// Perform post-transaction tasks
		user.FinalizeOAuthUserCreation(inviterId)
	}

	return user, nil
}

func oauthRegistrationMethod(provider oauth.Provider) string {
	switch typed := provider.(type) {
	case *oauth.GitHubProvider:
		return "github"
	case *oauth.DiscordProvider:
		return "discord"
	case *oauth.OIDCProvider:
		return "oidc"
	case *oauth.LinuxDOProvider:
		return "linuxdo"
	case *oauth.GenericOAuthProvider:
		return "custom:" + typed.GetConfig().Slug
	default:
		return strings.ToLower(strings.TrimSpace(provider.GetName()))
	}
}

// Error types for OAuth
type OAuthUserDeletedError struct{}

func (e *OAuthUserDeletedError) Error() string {
	return "user has been deleted"
}

type OAuthRegistrationDisabledError struct{}

func (e *OAuthRegistrationDisabledError) Error() string {
	return "registration is disabled"
}

type OAuthEmailAlreadyTakenError struct{}

func (e *OAuthEmailAlreadyTakenError) Error() string {
	return "email is already in use"
}

// OAuthEmailVerificationRequiredError is returned when strict email
// verification is enabled but the provider did not attest the address.
// Existing OAuth identities are returned before this check, so enabling the
// setting never locks existing users out of login.
type OAuthEmailVerificationRequiredError struct{}

func (e *OAuthEmailVerificationRequiredError) Error() string {
	return "oauth provider did not provide a verified email address"
}

// handleOAuthError handles OAuth errors and returns translated message
func handleOAuthError(c *gin.Context, err error) {
	switch e := err.(type) {
	case *oauth.OAuthError:
		if e.Params != nil {
			common.ApiErrorI18n(c, e.MsgKey, e.Params)
		} else {
			common.ApiErrorI18n(c, e.MsgKey)
		}
	case *oauth.AccessDeniedError:
		common.ApiErrorMsg(c, e.Message)
	case *oauth.TrustLevelError:
		common.ApiErrorI18n(c, i18n.MsgOAuthTrustLevelLow)
	default:
		// Provider/network errors can contain response bodies, URLs, or other
		// implementation details. Keep the diagnostic server-side and return a
		// stable public message instead of reflecting the upstream error.
		if err != nil {
			common.SysLog("OAuth provider request failed: " + err.Error())
		}
		common.ApiErrorI18n(c, i18n.MsgOAuthConnectFailed)
	}
}
