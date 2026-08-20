package middleware

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/service/authz"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const authIdentityContextKey = "auth_identity"
const dashboardCredentialContextKey = "dashboard_credential"
const consoleActivationContextKey = "console_activation_granted"

type dashboardCredentialKind int

type dashboardCredentialResult struct {
	user           *model.UserBase
	identity       service.AuthIdentity
	credentialKind dashboardCredentialKind
	err            error
}

const (
	dashboardCredentialUnmatched dashboardCredentialKind = iota
	dashboardCredentialInternal
	dashboardCredentialPAT
)

func validUserInfo(username string, role int) bool {
	// check username is empty
	if strings.TrimSpace(username) == "" {
		return false
	}
	if !common.IsValidateRole(role) {
		return false
	}
	return true
}

func authHelper(c *gin.Context, minRole int) {
	user, identity, useAccessToken, err := authenticateDashboardRequest(c)
	if err != nil {
		writeDashboardAuthError(c, err)
		return
	}
	if user.Status != common.UserStatusEnabled {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "code": "AUTH_USER_DISABLED", "message": common.TranslateMessage(c, i18n.MsgAuthUserBanned)})
		return
	}
	if user.Role < minRole {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "code": "AUTH_INSUFFICIENT_PRIVILEGE", "message": common.TranslateMessage(c, i18n.MsgAuthInsufficientPrivilege)})
		return
	}
	if !validUserInfo(user.Username, user.Role) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "code": "AUTH_USER_INVALID", "message": common.TranslateMessage(c, i18n.MsgAuthUserInfoInvalid)})
		return
	}
	setDashboardAuthContext(c, user, identity, useAccessToken)

	// 管理/root 写操作审计兜底：内聚在鉴权链路里，保证任何经过 AdminAuth/RootAuth
	// 的写接口都会自动留痕（无需在路由上单独挂审计中间件，避免漏挂）。
	// handler 内手动埋点者会设置 ContextKeyAuditLogged，finishAdminAudit 据此跳过。
	var auditWriter *auditResponseWriter
	if minRole >= common.RoleAdminUser {
		auditWriter = beginAdminAudit(c)
	}

	c.Next()

	finishAdminAudit(c, auditWriter)
}

func TryUserAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		user, identity, credentialKind, err := classifyDashboardCredential(c)
		if err != nil {
			writeDashboardAuthError(c, err)
			return
		}
		if credentialKind != dashboardCredentialUnmatched {
			setDashboardAuthContext(c, user, identity, credentialKind == dashboardCredentialPAT)
		}
		c.Next()
	}
}

func UserAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, common.RoleCommonUser)
	}
}

func AdminAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, common.RoleAdminUser)
	}
}

func RootAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		authHelper(c, common.RoleRootUser)
	}
}

// GetAuthIdentity returns a dashboard session identity. PAT-authenticated
// requests intentionally have no SessionID and cannot manage browser sessions.
func GetAuthIdentity(c *gin.Context) (service.AuthIdentity, bool) {
	value, ok := c.Get(authIdentityContextKey)
	if !ok {
		return service.AuthIdentity{}, false
	}
	identity, ok := value.(service.AuthIdentity)
	return identity, ok
}

// GetSessionAuthIdentity returns only identities backed by a live dashboard
// session. PAT-authenticated requests intentionally fail this check.
func GetSessionAuthIdentity(c *gin.Context) (service.AuthIdentity, bool) {
	identity, ok := GetAuthIdentity(c)
	if !ok {
		identity = service.AuthIdentity{
			UserID:          c.GetInt("id"),
			SessionID:       c.GetString("session_id"),
			UserAuthVersion: c.GetInt64("auth_version"),
			SessionVersion:  c.GetInt64("session_version"),
		}
	}
	if identity.UserID <= 0 || identity.SessionID == "" || identity.UserAuthVersion <= 0 || identity.SessionVersion <= 0 {
		return service.AuthIdentity{}, false
	}
	return identity, true
}

func authenticateDashboardRequest(c *gin.Context) (*model.UserBase, service.AuthIdentity, bool, error) {
	user, identity, credentialKind, err := classifyDashboardCredential(c)
	if err != nil {
		return nil, service.AuthIdentity{}, credentialKind == dashboardCredentialPAT, err
	}
	if credentialKind == dashboardCredentialUnmatched {
		return nil, service.AuthIdentity{}, false, service.ErrAuthTokenInvalid
	}
	return user, identity, credentialKind == dashboardCredentialPAT, nil
}

func classifyDashboardCredential(c *gin.Context) (*model.UserBase, service.AuthIdentity, dashboardCredentialKind, error) {
	if cached, ok := c.Get(dashboardCredentialContextKey); ok {
		result := cached.(dashboardCredentialResult)
		return result.user, result.identity, result.credentialKind, result.err
	}
	user, identity, credentialKind, err := classifyDashboardCredentialUncached(c)
	c.Set(dashboardCredentialContextKey, dashboardCredentialResult{
		user:           user,
		identity:       identity,
		credentialKind: credentialKind,
		err:            err,
	})
	return user, identity, credentialKind, err
}

func classifyDashboardCredentialUncached(c *gin.Context) (*model.UserBase, service.AuthIdentity, dashboardCredentialKind, error) {
	raw, ok := authorizationToken(c.GetHeader("Authorization"))
	if !ok {
		return nil, service.AuthIdentity{}, dashboardCredentialUnmatched, nil
	}
	identity, internal, err := service.ParseDashboardAccessToken(raw)
	if internal {
		if err != nil {
			return nil, service.AuthIdentity{}, dashboardCredentialInternal, err
		}
		_, user, err := service.ValidateLoginSession(identity)
		if err != nil {
			return nil, service.AuthIdentity{}, dashboardCredentialInternal, err
		}
		return user, identity, dashboardCredentialInternal, nil
	}
	patUser, err := model.ValidateAccessToken(raw)
	if err != nil {
		return nil, service.AuthIdentity{}, dashboardCredentialPAT, err
	}
	if patUser == nil || patUser.Id <= 0 {
		return nil, service.AuthIdentity{}, dashboardCredentialUnmatched, nil
	}
	user, err := model.GetUserCache(patUser.Id)
	if err != nil {
		return nil, service.AuthIdentity{}, dashboardCredentialPAT, err
	}
	return user, service.AuthIdentity{UserID: user.Id, UserAuthVersion: user.AuthVersion}, dashboardCredentialPAT, nil
}

// ConsoleAccessGate keeps developer surfaces unavailable until the account has
// earned the current trust-level access boundary. It reuses the authentication
// result in UserAuth so requests do not pay for a second session lookup.
func ConsoleAccessGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, _, credentialKind, err := classifyDashboardCredential(c)
		if err != nil || credentialKind == dashboardCredentialUnmatched || user == nil {
			if consoleDiscoveryRoute(c.Request.Method, c.Request.URL.Path) {
				abortRelayAsNotFound(c)
				return
			}
			c.Next()
			return
		}
		activated, trustErr := trustLevelAllowsDeveloperAccess(user)
		if trustErr != nil {
			common.SysLog(fmt.Sprintf("failed to calculate console trust level for user %d: %s", user.Id, trustErr.Error()))
			activated = false
		}
		c.Set(consoleActivationContextKey, activated)
		if activated || preActivationRouteAllowed(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}
		abortRelayAsNotFound(c)
	}
}

func trustLevelAllowsDeveloperAccess(user *model.UserBase) (bool, error) {
	access, err := model.GetDeveloperAccessStateForUserBase(user)
	if err != nil {
		return false, err
	}
	return access.Granted, nil
}

// consoleDiscoveryRoute hides relay-console inventory from unauthenticated and
// invalid dashboard credentials. Valid API keys continue to use the dedicated
// relay routes, whose token middleware applies the same generic 404 policy.
func consoleDiscoveryRoute(method string, path string) bool {
	path = strings.TrimSuffix(path, "/")
	if publicSubscriptionCallbackRoute(method, path) {
		return false
	}
	for _, prefix := range []string{
		"/api/assistant/pricing",
		"/api/channel",
		"/api/custom-oauth-provider",
		"/api/data",
		"/api/deployments",
		"/api/group",
		"/api/log",
		"/api/mj",
		"/api/models",
		"/api/open-source-bounties/mcp-token",
		"/api/option",
		"/api/performance",
		"/api/perf-metrics",
		"/api/prefill_group",
		"/api/pricing",
		"/api/rankings",
		"/api/ratio_config",
		"/api/ratio_sync",
		"/api/redemption",
		"/api/status/test",
		"/api/subscription",
		"/api/system-info",
		"/api/system-task",
		"/api/task",
		"/api/token",
		"/api/usage",
		"/api/user/groups",
		"/api/user/models",
		"/api/user/self/groups",
		"/api/vendors",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// ConsoleActivationGranted reports whether the current dashboard request has
// passed the trust-level developer access boundary. Anonymous and invalid
// credentials deliberately return false.
func ConsoleActivationGranted(c *gin.Context) bool {
	value, ok := c.Get(consoleActivationContextKey)
	activated, ok := value.(bool)
	return ok && activated
}

// AuthenticatedDashboardUser returns the valid dashboard identity already
// classified by ConsoleAccessGate. Callers can use it on public-but-optional-
// auth surfaces without accepting an unverified user id from request data.
func AuthenticatedDashboardUser(c *gin.Context) (*model.UserBase, bool) {
	user, _, credentialKind, err := classifyDashboardCredential(c)
	if err != nil || credentialKind == dashboardCredentialUnmatched || user == nil {
		return nil, false
	}
	return user, true
}

func preActivationRouteAllowed(method string, path string) bool {
	path = strings.TrimSuffix(path, "/")
	if publicSubscriptionCallbackRoute(method, path) {
		return true
	}

	if path == "/api/open-source-bounties" {
		return method == http.MethodGet
	}
	if strings.HasPrefix(path, "/api/open-source-bounties/projects/") {
		projectPath := strings.TrimPrefix(path, "/api/open-source-bounties/projects/")
		segments := strings.Split(projectPath, "/")
		return len(segments) == 1 && segments[0] != "" && method == http.MethodGet
	}
	switch path {
	case "/api/setup", "/api/status", "/api/notice", "/api/user-agreement", "/api/privacy-policy", "/api/about", "/api/home_page_content", "/api/security/policy", "/api/security/stats":
		return method == http.MethodGet
	case "/api/verification", "/api/reset_password":
		return method == http.MethodGet
	case "/api/user/register", "/api/user/login", "/api/user/login/2fa", "/api/user/reset", "/api/user/auth/refresh", "/api/user/auth/logout":
		return method == http.MethodPost
	case "/api/verify":
		return method == http.MethodPost
	case "/api/data/self", "/api/data/flow/self":
		// These endpoints are authenticated, user-scoped reads. Keep them
		// available to an authenticated account before console activation;
		// UserAuth still enforces the identity boundary and the admin-wide
		// data routes remain behind ConsoleAccessGate.
		return method == http.MethodGet
	case "/api/user/self":
		return method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete
	case "/api/user/self/onboarding/todo":
		return method == http.MethodGet
	case "/api/user/passkey":
		return method == http.MethodGet || method == http.MethodDelete
	case "/api/user/sessions", "/api/user/oauth/bindings", "/api/user/2fa/status":
		return method == http.MethodGet
	case "/api/user/developer-access/request":
		return method == http.MethodGet || method == http.MethodPost
	case "/api/user/account-action-requests", "/api/user/account-action-requests/appeal":
		return method == http.MethodGet || method == http.MethodPost
	case "/api/release-notes/latest":
		return method == http.MethodGet
	case "/api/user/sessions/revoke-others", "/api/user/passkey/register/begin", "/api/user/passkey/register/finish", "/api/user/passkey/verify/begin", "/api/user/passkey/verify/finish", "/api/user/2fa/setup", "/api/user/2fa/enable", "/api/user/2fa/disable", "/api/user/2fa/backup_codes":
		return method == http.MethodPost
	case "/api/user/setting":
		return method == http.MethodPut
	}

	if strings.HasPrefix(path, "/api/user/sessions/") || strings.HasPrefix(path, "/api/user/oauth/bindings/") || strings.HasPrefix(path, "/api/user/bindings/") {
		return method == http.MethodDelete
	}
	if strings.HasPrefix(path, "/api/release-notes/") && strings.HasSuffix(path, "/read") {
		return method == http.MethodPost
	}
	return false
}

func publicSubscriptionCallbackRoute(method string, path string) bool {
	switch path {
	case "/api/subscription/epay/notify", "/api/subscription/epay/return":
		return method == http.MethodGet || method == http.MethodPost
	default:
		return false
	}
}

func authorizationToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", false
	}
	parts := strings.Fields(header)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		header = parts[1]
	} else if len(parts) != 1 {
		return "", false
	}
	return header, header != ""
}

func setDashboardAuthContext(c *gin.Context, user *model.UserBase, identity service.AuthIdentity, useAccessToken bool) {
	c.Header("Auth-Version", "864b7076dbcd0a3c01b5520316720ebf")
	c.Set("username", user.Username)
	c.Set("role", user.Role)
	c.Set("id", user.Id)
	c.Set("group", user.Group)
	c.Set("user_group", user.Group)
	c.Set("use_access_token", useAccessToken)
	c.Set("session_id", identity.SessionID)
	c.Set("auth_version", identity.UserAuthVersion)
	c.Set("session_version", identity.SessionVersion)
	c.Set(authIdentityContextKey, identity)
	user.WriteContext(c)
}

func writeDashboardAuthError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrAuthTokenExpired) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "code": "AUTH_TOKEN_EXPIRED", "message": common.TranslateMessage(c, i18n.MsgAuthNotLoggedIn)})
		return
	}
	if errors.Is(err, service.ErrLoginSessionRevoked) || errors.Is(err, gorm.ErrRecordNotFound) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "code": "AUTH_SESSION_REVOKED", "message": common.TranslateMessage(c, i18n.MsgAuthNotLoggedIn)})
		return
	}
	if errors.Is(err, service.ErrAuthTokenInvalid) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "code": "AUTH_UNAUTHORIZED", "message": common.TranslateMessage(c, i18n.MsgAuthAccessTokenInvalid)})
		return
	}
	common.SysLog("dashboard authentication error: " + err.Error())
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"success": false, "code": "AUTH_INTERNAL_ERROR", "message": common.TranslateMessage(c, i18n.MsgDatabaseError)})
}

func RequirePermission(permission authz.Permission) func(c *gin.Context) {
	return func(c *gin.Context) {
		role := c.GetInt("role")
		userID := c.GetInt("id")
		if authz.Can(userID, role, permission) {
			c.Next()
			return
		}
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": common.TranslateMessage(c, i18n.MsgAuthInsufficientPrivilege),
		})
		c.Abort()
	}
}

func WssAuth(c *gin.Context) {

}

// TokenOrUserAuth allows either session-based user auth or API token auth.
// Used for endpoints that need to be accessible from both the dashboard and API clients.
func TokenOrUserAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		raw, ok := authorizationToken(c.GetHeader("Authorization"))
		if ok {
			identity, internal, err := service.ParseDashboardAccessToken(raw)
			if !internal {
				TokenAuth()(c)
				return
			}
			if err != nil {
				writeDashboardAuthError(c, err)
				return
			}
			_, user, err := service.ValidateLoginSession(identity)
			if err != nil {
				writeDashboardAuthError(c, err)
				return
			}
			setDashboardAuthContext(c, user, identity, false)
			c.Next()
			return
		}
		// Opaque credentials are relay API keys here, never dashboard PATs.
		TokenAuth()(c)
	}
}

// TokenAuthReadOnly 宽松版本的令牌认证中间件，用于只读查询接口。
// 只验证令牌 key 是否存在，不检查令牌状态、过期时间和额度。
// 即使令牌已过期、已耗尽或已禁用，也允许访问。
// 仍然检查用户是否被封禁。
func TokenAuthReadOnly() func(c *gin.Context) {
	return func(c *gin.Context) {
		key := c.Request.Header.Get("Authorization")
		if key == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": common.TranslateMessage(c, i18n.MsgTokenNotProvided),
			})
			c.Abort()
			return
		}
		if strings.HasPrefix(key, "Bearer ") || strings.HasPrefix(key, "bearer ") {
			key = strings.TrimSpace(key[7:])
		}
		key = strings.TrimPrefix(key, "sk-")
		parts := strings.Split(key, "-")
		key = parts[0]

		token, err := model.GetTokenByKey(key, false)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusUnauthorized, gin.H{
					"success": false,
					"message": common.TranslateMessage(c, i18n.MsgTokenInvalid),
				})
			} else {
				common.SysLog("TokenAuthReadOnly GetTokenByKey database error: " + err.Error())
				c.JSON(http.StatusInternalServerError, gin.H{
					"success": false,
					"message": common.TranslateMessage(c, i18n.MsgDatabaseError),
				})
			}
			c.Abort()
			return
		}

		// TokenAuthReadOnly must keep allowing other token states to query read-only
		// data, such as token usage logs; only explicitly disabled tokens are denied.
		if token.Status == common.TokenStatusDisabled {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": common.TranslateMessage(c, i18n.MsgTokenStatusUnavailable),
			})
			c.Abort()
			return
		}

		userCache, err := model.GetUserCache(token.UserId)
		if err != nil {
			common.SysLog(fmt.Sprintf("TokenAuthReadOnly GetUserCache error for user %d: %v", token.UserId, err))
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": common.TranslateMessage(c, i18n.MsgDatabaseError),
			})
			c.Abort()
			return
		}
		if userCache.Status != common.UserStatusEnabled {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": common.TranslateMessage(c, i18n.MsgAuthUserBanned),
			})
			c.Abort()
			return
		}
		if granted, trustErr := trustLevelAllowsDeveloperAccess(userCache); trustErr != nil || !granted {
			abortRelayAsNotFound(c)
			return
		}

		c.Set("id", token.UserId)
		c.Set("token_id", token.Id)
		c.Set("token_key", token.Key)
		c.Next()
	}
}

func TokenAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		if failure := authenticateRelayToken(c); failure != nil {
			writeRelayTokenAuthFailure(c, failure)
			return
		}
		c.Next()
	}
}

type relayTokenAuthFailure struct {
	err     error
	status  int
	message string
	code    types.ErrorCode
	conceal bool
}

func newRelayTokenAuthFailure(err error, status int, message string, code types.ErrorCode) *relayTokenAuthFailure {
	return &relayTokenAuthFailure{err: err, status: status, message: message, code: code}
}

// RevalidateTokenAuth reruns the complete relay-token policy without writing an
// HTTP response. Long-lived transports call it for every logical request.
func RevalidateTokenAuth(c *gin.Context) *types.NewAPIError {
	failure := authenticateRelayToken(c)
	if failure == nil {
		return nil
	}
	message := failure.message
	if failure.conceal {
		message = "token authentication failed"
	}
	return types.NewErrorWithStatusCode(errors.New(message), failure.code, failure.status, types.ErrOptionWithSkipRetry())
}

func authenticateRelayToken(c *gin.Context) *relayTokenAuthFailure {
	prepareRelayTokenCredential(c)
	key, parts := relayTokenCredential(c)
	token, err := model.ValidateUserToken(key)
	if err != nil {
		if errors.Is(err, model.ErrDatabase) {
			common.SysLog("TokenAuth ValidateUserToken database error: " + err.Error())
			return newRelayTokenAuthFailure(err, http.StatusInternalServerError,
				common.TranslateMessage(c, i18n.MsgDatabaseError), "")
		}
		failure := newRelayTokenAuthFailure(err, http.StatusUnauthorized, "token authentication failed", types.ErrorCodeAccessDenied)
		failure.conceal = true
		return failure
	}

	allowIps := token.GetIpLimits()
	if len(allowIps) > 0 {
		clientIp := c.ClientIP()
		logger.LogDebug(c, "Token has IP restrictions, checking client IP %s", clientIp)
		ip := net.ParseIP(clientIp)
		if ip == nil {
			return newRelayTokenAuthFailure(errors.New("invalid client IP"), http.StatusForbidden, "无法解析客户端 IP 地址", "")
		}
		if !common.IsIpInCIDRList(ip, allowIps) {
			return newRelayTokenAuthFailure(errors.New("client IP is not allowed by token"), http.StatusForbidden, "您的 IP 不在令牌允许访问的列表中", types.ErrorCodeAccessDenied)
		}
		logger.LogDebug(c, "Client IP %s passed the token IP restrictions check", clientIp)
	}

	userCache, err := model.GetUserCache(token.UserId)
	if err != nil {
		common.SysLog(fmt.Sprintf("TokenAuth GetUserCache error for user %d: %v", token.UserId, err))
		return newRelayTokenAuthFailure(err, http.StatusInternalServerError,
			common.TranslateMessage(c, i18n.MsgDatabaseError), "")
	}
	if userCache.Status != common.UserStatusEnabled {
		return newRelayTokenAuthFailure(errors.New("user is disabled"), http.StatusForbidden,
			common.TranslateMessage(c, i18n.MsgAuthUserBanned), "")
	}
	if granted, trustErr := trustLevelAllowsDeveloperAccess(userCache); trustErr != nil || !granted {
		return newRelayTokenAuthFailure(errors.New("account trust level is insufficient"), http.StatusNotFound, "Not Found", types.ErrorCodeAccessDenied)
	}

	userGroup := userCache.Group
	tokenGroup := token.Group
	if tokenGroup != "" {
		if _, ok := service.GetUserUsableGroups(userGroup)[tokenGroup]; !ok {
			return newRelayTokenAuthFailure(errors.New("token group is not available to user"), http.StatusForbidden,
				fmt.Sprintf("无权访问 %s 分组", tokenGroup), "")
		}
		if !ratio_setting.ContainsGroupRatio(tokenGroup) && tokenGroup != "auto" {
			return newRelayTokenAuthFailure(errors.New("token group is deprecated"), http.StatusForbidden,
				fmt.Sprintf("分组 %s 已被弃用", tokenGroup), "")
		}
		userGroup = tokenGroup
	}

	if err := SetupContextForToken(c, token, parts...); err != nil {
		return newRelayTokenAuthFailure(err, http.StatusForbidden, "普通用户不支持指定渠道", "")
	}
	userCache.WriteContext(c)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, userGroup)
	return nil
}

func prepareRelayTokenCredential(c *gin.Context) {
	applyWebSocketSubprotocolAuthorization(c.Request.Header)
	if strings.Contains(c.Request.URL.Path, "/v1/messages") || strings.Contains(c.Request.URL.Path, "/v1/models") {
		if anthropicKey := c.Request.Header.Get("x-api-key"); anthropicKey != "" {
			c.Request.Header.Set("Authorization", "Bearer "+anthropicKey)
		}
	}
	if c.Request.URL.Path == "/v1/models" ||
		strings.HasPrefix(c.Request.URL.Path, "/v1beta/models") ||
		strings.HasPrefix(c.Request.URL.Path, "/v1beta/openai/models") ||
		strings.HasPrefix(c.Request.URL.Path, "/v1/models/") {
		if skKey := c.Query("key"); skKey != "" {
			c.Request.Header.Set("Authorization", "Bearer "+skKey)
		}
		if xGoogKey := c.Request.Header.Get("x-goog-api-key"); xGoogKey != "" {
			c.Request.Header.Set("Authorization", "Bearer "+xGoogKey)
		}
	}
}

func relayTokenCredential(c *gin.Context) (string, []string) {
	key := c.Request.Header.Get("Authorization")
	if strings.HasPrefix(key, "Bearer ") || strings.HasPrefix(key, "bearer ") {
		key = strings.TrimSpace(key[7:])
	}
	if key == "" || key == "midjourney-proxy" {
		key = c.Request.Header.Get("mj-api-secret")
		if strings.HasPrefix(key, "Bearer ") || strings.HasPrefix(key, "bearer ") {
			key = strings.TrimSpace(key[7:])
		}
	}
	key = strings.TrimPrefix(key, "sk-")
	parts := strings.Split(key, "-")
	return parts[0], parts
}

func writeRelayTokenAuthFailure(c *gin.Context, failure *relayTokenAuthFailure) {
	if failure.conceal {
		abortRelayAsNotFound(c)
		return
	}
	if failure.err != nil && failure.message == "普通用户不支持指定渠道" {
		c.Header("specific_channel_version", "701e3ae1dc3f7975556d354e0675168d004891c8")
	}
	if failure.code != "" {
		abortWithOpenAiMessage(c, failure.status, failure.message, failure.code)
		return
	}
	abortWithOpenAiMessage(c, failure.status, failure.message)
}

func applyWebSocketSubprotocolAuthorization(header http.Header) bool {
	key, ok := apiKeyFromWebSocketSubprotocol(header.Get("Sec-WebSocket-Protocol"))
	if !ok {
		return false
	}
	header.Set("Authorization", "Bearer "+key)
	return true
}

func apiKeyFromWebSocketSubprotocol(protocols string) (string, bool) {
	const insecureAPIKeyPrefix = "openai-insecure-api-key."
	for _, part := range strings.Split(protocols, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, insecureAPIKeyPrefix) {
			key := strings.TrimPrefix(part, insecureAPIKeyPrefix)
			return key, key != ""
		}
	}
	return "", false
}

func abortRelayAsNotFound(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "Not Found"})
}

func SetupContextForToken(c *gin.Context, token *model.Token, parts ...string) error {
	if token == nil {
		return fmt.Errorf("token is nil")
	}
	c.Set("id", token.UserId)
	c.Set("token_id", token.Id)
	c.Set("token_key", token.Key)
	c.Set("token_name", token.Name)
	c.Set("token_unlimited_quota", token.UnlimitedQuota)
	if !token.UnlimitedQuota {
		c.Set("token_quota", token.RemainQuota)
	}
	if token.ModelLimitsEnabled {
		c.Set("token_model_limit_enabled", true)
		c.Set("token_model_limit", token.GetModelLimitsMap())
	} else {
		c.Set("token_model_limit_enabled", false)
	}
	common.SetContextKey(c, constant.ContextKeyTokenGroup, token.Group)
	common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, token.CrossGroupRetry)
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "")
	if token.AutoGroups != "" {
		autoGroups, err := token.GetAutoGroups()
		if err != nil {
			common.SysError(fmt.Sprintf("failed to parse auto groups for token %d: %v", token.Id, err))
			autoGroups = []string{}
			common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, autoGroups)
		} else if len(autoGroups) > 0 {
			common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, autoGroups)
		}
	}
	if len(parts) > 1 {
		if model.IsAdmin(token.UserId) {
			c.Set("specific_channel_id", parts[1])
		} else {
			return fmt.Errorf("普通用户不支持指定渠道")
		}
	}
	return nil
}
