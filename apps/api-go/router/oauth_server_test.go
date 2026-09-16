package router

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const oauthTestIssuer = "https://oauth.example.com"

type oauthHTTPTest struct {
	db          *gorm.DB
	engine      *gin.Engine
	integration *service.OAuthIntegration
	user        model.User
	login       *service.AuthBundle
	otherLogin  *service.AuthBundle
	verifier    string
	query       string
}

func setupOAuthHTTP(t *testing.T) *oauthHTTPTest {
	t.Helper()
	oldDB, oldLogDB, oldRedis, oldSecret, oldType := model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret, common.MainDatabaseType()
	oldGroups, oldRatios := setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "oauth.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Log{}))
	model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret = db, db, false, "isolated-oauth-http-session-test"
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2,"future":3}`))
	t.Cleanup(func() {
		_, _ = service.ConfigureOAuthIntegration(nil, service.OAuthServerConfig{})
		model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret = oldDB, oldLogDB, oldRedis, oldSecret
		common.SetMainDatabaseType(oldType)
		_ = setting.UpdateUserUsableGroupsByJSONString(oldGroups)
		_ = ratio_setting.UpdateGroupRatioByJSONString(oldRatios)
		_ = sqlDB.Close()
	})
	user := model.User{Username: "oauth-user", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", Quota: 10000, AuthVersion: 1, AffCode: "oauth-one"}
	other := model.User{Username: "other-user", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", Quota: 999999, AuthVersion: 1, AffCode: "oauth-two"}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&other).Error)
	login, err := service.CreateLoginSession(user.Id, "password", "192.0.2.1", "oauth-test")
	require.NoError(t, err)
	otherLogin, err := service.CreateLoginSession(other.Id, "password", "192.0.2.1", "oauth-test")
	require.NoError(t, err)
	integration, err := service.ConfigureOAuthIntegration(db, service.OAuthServerConfig{Enabled: true, Issuer: oauthTestIssuer, Groups: []string{"default", "vip", "future"}})
	require.NoError(t, err)
	channel := model.Channel{Id: 300, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "offline-fixture", Key: "not-a-network-key", Models: "gpt-4o", Group: "default,vip,future"}
	require.NoError(t, db.Create(&channel).Error)
	for _, group := range []string{"default", "vip", "future"} {
		require.NoError(t, db.Create(&model.Ability{Group: group, Model: "gpt-4o", ChannelId: channel.Id, Enabled: true}).Error)
	}
	engine := gin.New()
	engine.Use(middleware.BodyStorageCleanup())
	MountOAuthServerRoutes(engine, integration)
	// Exercise production OAuth authentication/model boundary and actual billing,
	// but never contact an upstream model or make a paid network call.
	engine.POST("/v1/chat/completions", middleware.TokenAuth(), func(c *gin.Context) {
		var body struct {
			Model string `json:"model"`
		}
		if common.UnmarshalBodyReusable(c, &body) != nil || !middleware.ValidateOAuthRelayModel(c, body.Model) {
			return
		}
		info := &relaycommon.RelayInfo{UserId: c.GetInt("id"), TokenId: c.GetInt("token_id"), TokenKey: c.GetString("token_key"), TokenUnlimited: c.GetBool("token_unlimited_quota"), OriginModelName: body.Model, UsingGroup: common.GetContextKeyString(c, constant.ContextKeyUsingGroup)}
		info.UserSetting.BillingPreference = "wallet_only"
		var before model.User
		var beforeToken model.Token
		require.NoError(t, db.First(&before, info.UserId).Error)
		require.NoError(t, db.First(&beforeToken, info.TokenId).Error)
		billing, apiErr := service.NewBillingSession(c, info, 200)
		if apiErr != nil {
			c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.Error()})
			return
		}
		var reserved model.User
		var reservedToken model.Token
		require.NoError(t, db.First(&reserved, info.UserId).Error)
		require.NoError(t, db.First(&reservedToken, info.TokenId).Error)
		require.Equal(t, before.Quota-200, reserved.Quota, "wallet must be reserved before upstream work")
		require.Equal(t, beforeToken.UsedQuota+200, reservedToken.UsedQuota, "real TokenId must carry the same reservation")
		require.Equal(t, 200, billing.GetPreConsumedQuota())
		if c.GetHeader("X-Test-Refund") == "yes" {
			billing.Refund(c)
			billing.Refund(c) // Retry must not double-credit a failed request.
		} else {
			if err := billing.Settle(80); err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
		}
		c.JSON(200, gin.H{"id": info.UserId, "token_id": info.TokenId, "group": info.UsingGroup, "cross_group_retry": common.GetContextKeyBool(c, constant.ContextKeyTokenCrossGroupRetry)})
	})
	engine.GET("/read-only", middleware.TokenAuthReadOnly(), func(c *gin.Context) { c.Status(200) })
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	initialScopes := append([]string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthUsageScope, service.OAuthInvokeScope}, service.OAuthBuiltinMCPScopes()...)
	query := url.Values{"client_id": {service.OAuthPiClientID}, "response_type": {"code"}, "redirect_uri": {"http://127.0.0.1:35679/oauth/lmm/callback"}, "resource": {integration.Resource}, "scope": {strings.Join(initialScopes, " ")}, "state": {strings.Repeat("s", 32)}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}.Encode()
	return &oauthHTTPTest{db: db, engine: engine, integration: integration, user: user, login: login, otherLogin: otherLogin, verifier: verifier, query: query}
}

func (h *oauthHTTPTest) request(method, path, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, oauthTestIssuer+path, strings.NewReader(body))
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	h.engine.ServeHTTP(recorder, request)
	return recorder
}

var oauthCSRFFixture = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
var oauthRedirectFixture = regexp.MustCompile(`<a[^>]*href="(http://127\.0\.0\.1:[^"]+)"`)

func oauthFormState(t *testing.T, response *httptest.ResponseRecorder) (*http.Cookie, string) {
	t.Helper()
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "strict-origin", response.Header().Get("Referrer-Policy"), "native form POSTs must preserve Origin without leaking authorization queries")
	match := oauthCSRFFixture.FindStringSubmatch(response.Body.String())
	require.Len(t, match, 2)
	var cookie *http.Cookie
	for _, item := range response.Result().Cookies() {
		if item.Name == "__Host-lmm-oauth-flow" && item.MaxAge > 0 {
			cookie = item
		}
	}
	require.NotNil(t, cookie)
	require.True(t, cookie.HttpOnly)
	require.True(t, cookie.Secure)
	require.Equal(t, "/", cookie.Path)
	return cookie, html.UnescapeString(match[1])
}

func (h *oauthHTTPTest) begin(t *testing.T) (*http.Cookie, string) {
	return oauthFormState(t, h.request("GET", "/api/oauth2/authorize?"+h.query, "", nil))
}

func (h *oauthHTTPTest) continueConsent(t *testing.T) (*http.Cookie, string) {
	cookie, csrf := h.begin(t)
	response := h.request("POST", "/api/user/auth/oauth2/continue", url.Values{"csrf": {csrf}}.Encode(), map[string]string{"Origin": oauthTestIssuer, "Content-Type": "application/x-www-form-urlencoded"}, cookie, &http.Cookie{Name: service.RefreshCookieName, Value: h.login.RefreshToken})
	bound, secret := oauthFormState(t, response)
	require.NotEqual(t, cookie.Value, bound.Value, "login must rotate binding")
	require.Contains(t, response.Body.String(), "oauth-user")
	require.Contains(t, response.Body.String(), "vip")
	require.NotContains(t, response.Body.String(), "<li>future</li>")
	return bound, secret
}

func (h *oauthHTTPTest) approve(t *testing.T) (oauthserver.TokenResponse, string) {
	cookie, csrf := h.continueConsent(t)
	response := h.request("POST", "/api/user/auth/oauth2/consent", url.Values{"csrf": {csrf}, "decision": {"allow"}}.Encode(), map[string]string{"Origin": oauthTestIssuer, "Content-Type": "application/x-www-form-urlencoded"}, cookie, &http.Cookie{Name: service.RefreshCookieName, Value: h.login.RefreshToken})
	require.Equal(t, 200, response.Code, response.Body.String())
	match := oauthRedirectFixture.FindStringSubmatch(response.Body.String())
	require.Len(t, match, 2, response.Body.String())
	target, err := url.Parse(html.UnescapeString(match[1]))
	require.NoError(t, err)
	require.Equal(t, oauthTestIssuer, target.Query().Get("iss"))
	require.Equal(t, strings.Repeat("s", 32), target.Query().Get("state"))
	code := target.Query().Get("code")
	require.NotEmpty(t, code)
	exchange := url.Values{"client_id": {service.OAuthPiClientID}, "grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"http://127.0.0.1:35679/oauth/lmm/callback"}, "resource": {h.integration.Resource}, "code_verifier": {h.verifier}}.Encode()
	token := h.request("POST", "/api/oauth2/token", exchange, map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	require.Equal(t, 200, token.Code, token.Body.String())
	var result oauthserver.TokenResponse
	require.NoError(t, json.Unmarshal(token.Body.Bytes(), &result))
	require.NotEmpty(t, result.AccessToken)
	return result, exchange
}

func TestOAuthHTTPDiscoveryAndDisabled(t *testing.T) {
	h := setupOAuthHTTP(t)
	response := h.request("GET", "/.well-known/oauth-authorization-server", "", nil)
	require.Equal(t, 200, response.Code)
	var metadata oauthserver.Metadata
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &metadata))
	require.Equal(t, oauthTestIssuer+"/api/oauth2/authorize", metadata.AuthorizationEndpoint)
	require.Equal(t, []string{"S256"}, metadata.CodeChallengeMethodsSupported)
	require.Len(t, metadata.ScopesSupported, 6)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	disabled := gin.New()
	MountOAuthServerRoutes(disabled, nil)
	request := httptest.NewRequest("GET", oauthTestIssuer+"/.well-known/oauth-authorization-server", nil)
	recorder := httptest.NewRecorder()
	disabled.ServeHTTP(recorder, request)
	require.Equal(t, 404, recorder.Code)
	request = httptest.NewRequest("GET", "https://attacker.example/.well-known/oauth-authorization-server", nil)
	recorder = httptest.NewRecorder()
	h.engine.ServeHTTP(recorder, request)
	require.Equal(t, 400, recorder.Code)
	require.Equal(t, 200, h.request("GET", "/.well-known/oauth-protected-resource/api/oauth2", "", nil).Code)
}

func TestOAuthHTTPRegistersDshAsAnIndependentNativeClient(t *testing.T) {
	h := setupOAuthHTTP(t)
	dshQuery, err := url.ParseQuery(h.query)
	require.NoError(t, err)
	dshQuery.Set("client_id", service.OAuthDshClientID)
	response := h.request("GET", "/api/oauth2/authorize?"+dshQuery.Encode(), "", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), service.OAuthDshClientName)
	require.Contains(t, response.Body.String(), "Authorize DSH")
	require.NotContains(t, response.Body.String(), service.OAuthPiClientName)
}

func TestOAuthHTTPAuthorizationCSRFAndIdentitySwitch(t *testing.T) {
	h := setupOAuthHTTP(t)
	require.Equal(t, 400, h.request("GET", "/api/oauth2/authorize?"+h.query+"&client_id=lmm-pi", "", nil).Code)
	cookie, csrf := h.begin(t)
	for _, origin := range []string{"", "null", "https://evil.example"} {
		response := h.request("POST", "/api/user/auth/oauth2/continue", url.Values{"csrf": {csrf}}.Encode(), map[string]string{"Origin": origin, "Content-Type": "application/x-www-form-urlencoded"}, cookie, &http.Cookie{Name: service.RefreshCookieName, Value: h.login.RefreshToken})
		require.Equal(t, 400, response.Code)
	}
	for _, body := range []string{"csrf=" + csrf + "&csrf=" + csrf, "csrf=" + csrf + "&user_id=2", "csrf=wrong"} {
		response := h.request("POST", "/api/user/auth/oauth2/continue", body, map[string]string{"Origin": oauthTestIssuer, "Content-Type": "application/x-www-form-urlencoded"}, cookie, &http.Cookie{Name: service.RefreshCookieName, Value: h.login.RefreshToken})
		require.Equal(t, 400, response.Code)
	}
	cookie, csrf = h.continueConsent(t)
	response := h.request("POST", "/api/user/auth/oauth2/consent", url.Values{"csrf": {csrf}, "decision": {"allow"}}.Encode(), map[string]string{"Origin": oauthTestIssuer, "Content-Type": "application/x-www-form-urlencoded"}, cookie, &http.Cookie{Name: service.RefreshCookieName, Value: h.otherLogin.RefreshToken})
	require.Equal(t, 400, response.Code)
	var count int64
	require.NoError(t, h.db.Model(&model.OAuthServerGrant{}).Count(&count).Error)
	require.Zero(t, count)
	require.Contains(t, response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")
	require.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
}

func TestOAuthHTTPBrowserFlowSurvivesHandlerReplacement(t *testing.T) {
	h := setupOAuthHTTP(t)
	preflightCookie, csrf := h.begin(t)
	// A second handler models a different reverse-proxy worker/process. The
	// authorization row and one-time CSRF state must be shared through storage.
	second := gin.New()
	second.Use(middleware.BodyStorageCleanup())
	MountOAuthServerRoutes(second, h.integration)
	request := func(engine *gin.Engine, method, path, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, oauthTestIssuer+path, strings.NewReader(body))
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		result := httptest.NewRecorder()
		engine.ServeHTTP(result, req)
		return result
	}
	response := request(second, "POST", "/api/user/auth/oauth2/continue", url.Values{"csrf": {csrf}}.Encode(), map[string]string{"Origin": oauthTestIssuer, "Content-Type": "application/x-www-form-urlencoded"}, preflightCookie, &http.Cookie{Name: service.RefreshCookieName, Value: h.login.RefreshToken})
	consentCookie, consentCSRF := oauthFormState(t, response)
	response = request(h.engine, "POST", "/api/user/auth/oauth2/consent", url.Values{"csrf": {consentCSRF}, "decision": {"allow"}}.Encode(), map[string]string{"Origin": oauthTestIssuer, "Content-Type": "application/x-www-form-urlencoded"}, consentCookie, &http.Cookie{Name: service.RefreshCookieName, Value: h.login.RefreshToken})
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Authorization complete")
}

func TestOAuthHTTPResourceBillingIsolationAndRevocation(t *testing.T) {
	h := setupOAuthHTTP(t)
	credentials, _ := h.approve(t)
	for _, scope := range service.OAuthBuiltinMCPScopes() {
		require.Contains(t, credentials.Scope, scope)
	}
	require.Contains(t, credentials.Scope, service.OAuthGroupScope("vip"))
	require.NotContains(t, credentials.Scope, service.OAuthGroupScope("future"))
	auth := map[string]string{"Authorization": "Bearer " + credentials.AccessToken}
	response := h.request("GET", "/api/oauth2/catalog", "", auth)
	require.Equal(t, 200, response.Code, response.Body.String())
	var catalog service.OAuthCatalog
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &catalog))
	require.Len(t, catalog.Groups, 2)
	require.Len(t, catalog.Models, 2)
	require.NotContains(t, response.Body.String(), model.OAuthBillingKeyPrefix)
	response = h.request("GET", "/api/oauth2/balance", "", auth)
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"quota":10000`)
	require.Equal(t, 400, h.request("GET", "/api/oauth2/balance?user_id=2", "", auth).Code)
	relayHeaders := map[string]string{"Authorization": "Bearer " + credentials.AccessToken, service.OAuthGroupHeader: service.OAuthGroupID("default"), "Content-Type": "application/json"}
	response = h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o","messages":[]}`, relayHeaders)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"cross_group_retry":false`)
	var wallet model.User
	require.NoError(t, h.db.First(&wallet, h.user.Id).Error)
	require.Equal(t, 9920, wallet.Quota)
	var managed model.Token
	require.NoError(t, h.db.Where("oauth_managed = ?", true).First(&managed).Error)
	require.Equal(t, 80, managed.UsedQuota)
	require.Empty(t, managed.GetFullKey())
	require.Empty(t, managed.GetMaskedKey())
	_, err := model.ValidateUserToken(managed.Key)
	require.Error(t, err)
	_, err = model.GetTokenByIds(managed.Id, h.user.Id)
	require.Error(t, err)
	tokens, err := model.GetAllUserTokens(h.user.Id, 0, 100)
	require.NoError(t, err)
	require.Empty(t, tokens)
	keys, err := model.GetTokenKeysByIds([]int{managed.Id}, h.user.Id)
	require.NoError(t, err)
	require.Empty(t, keys)
	count, err := model.BatchDeleteTokens([]int{managed.Id}, h.user.Id)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Error(t, model.DeleteTokenById(managed.Id, h.user.Id))
	managed.Group = "vip"
	require.NoError(t, managed.Update())
	require.NoError(t, h.db.First(&managed, managed.Id).Error)
	require.Equal(t, "default", managed.Group)
	require.Equal(t, 401, h.request("GET", "/read-only", "", map[string]string{"Authorization": "Bearer " + managed.Key}).Code)
	relayHeaders["X-Test-Refund"] = "yes"
	response = h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, relayHeaders)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Eventually(t, func() bool {
		if h.db.First(&wallet, h.user.Id).Error != nil || wallet.Quota != 9920 {
			return false
		}
		return h.db.First(&managed, managed.Id).Error == nil && managed.UsedQuota == 80
	}, 2*time.Second, 10*time.Millisecond)
	var bindings int64
	require.NoError(t, h.db.Model(&model.OAuthBillingBinding{}).Count(&bindings).Error)
	require.EqualValues(t, 1, bindings)
	revoke := url.Values{"client_id": {service.OAuthPiClientID}, "token": {credentials.RefreshToken}, "token_type_hint": {"refresh_token"}}.Encode()
	response = h.request("POST", "/api/oauth2/revoke", revoke, map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotEqual(t, 200, h.request("GET", "/api/oauth2/catalog", "", auth).Code)
	require.NotEqual(t, 200, h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, relayHeaders).Code)
}

func TestOAuthHTTPRejectsAmbiguityAndLiveGroupChanges(t *testing.T) {
	h := setupOAuthHTTP(t)
	credentials, exchange := h.approve(t)
	require.Equal(t, 400, h.request("POST", "/api/oauth2/token", exchange+"&client_id=lmm-pi", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}).Code)
	headers := map[string]string{"Authorization": "Bearer " + credentials.AccessToken, service.OAuthGroupHeader: service.OAuthGroupID("default"), "Content-Type": "application/json"}
	for _, body := range []string{`{"model":"gpt-4o","model":"other"}`, `{"model":"gpt-4o","Model":"other"}`, `{"model":"gpt-4o","GROUP":"vip"}`, `{"model":"gpt-4o","group":"vip"}`, `{"model":"gpt-4o","api_key":"other"}`, `{"model":"unknown-model"}`} {
		require.NotEqual(t, 200, h.request("POST", "/v1/chat/completions", body, headers).Code)
	}
	headers["x-api-key"] = "ordinary-key"
	require.Equal(t, 400, h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, headers).Code)
	delete(headers, "x-api-key")
	headers[service.OAuthGroupHeader] = service.OAuthGroupID("future")
	require.NotEqual(t, 200, h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, headers).Code)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","future":"Future"}`))
	auth := map[string]string{"Authorization": "Bearer " + credentials.AccessToken}
	response := h.request("GET", "/api/oauth2/catalog", "", auth)
	require.Equal(t, 200, response.Code)
	var catalog service.OAuthCatalog
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &catalog))
	require.Len(t, catalog.Groups, 1)
	require.Equal(t, "default", catalog.Groups[0].Name)
	headers[service.OAuthGroupHeader] = service.OAuthGroupID("vip")
	require.NotEqual(t, 200, h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, headers).Code)
	require.NoError(t, h.db.Model(&model.User{}).Where("id = ?", h.user.Id).Update("status", common.UserStatusDisabled).Error)
	require.NotEqual(t, 200, h.request("GET", "/api/oauth2/balance", "", auth).Code)
}

func TestOAuthHTTPActivityIsScopedAndAggregated(t *testing.T) {
	h := setupOAuthHTTP(t)
	credentials, _ := h.approve(t)
	const start int64 = 1704067200 // 2024-01-01T00:00:00Z
	const end int64 = start + 3*24*60*60
	require.NoError(t, h.db.Create(&[]model.Log{
		{UserId: h.user.Id, CreatedAt: start + 60, Type: model.LogTypeConsume, PromptTokens: 10, CompletionTokens: 5, Quota: 100},
		{UserId: h.user.Id, CreatedAt: start + 2*24*60*60 + 60, Type: model.LogTypeConsume, PromptTokens: 20, CompletionTokens: 7, Quota: 200},
		{UserId: h.user.Id, CreatedAt: start + 2*24*60*60 + 120, Type: model.LogTypeSystem, PromptTokens: 99, CompletionTokens: 99, Quota: 999},
		{UserId: h.user.Id + 1, CreatedAt: start + 60, Type: model.LogTypeConsume, PromptTokens: 999, CompletionTokens: 999, Quota: 9999},
	}).Error)
	auth := map[string]string{"Authorization": "Bearer " + credentials.AccessToken}
	response := h.request("GET", "/api/oauth2/usage/activity?from=1704067200&to=1704326400", "", auth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var payload struct {
		Timezone string                   `json:"timezone"`
		Days     []model.UsageActivityDay `json:"days"`
		Totals   struct {
			Requests         int64 `json:"requests"`
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
			Quota            int64 `json:"quota"`
		} `json:"totals"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Equal(t, "UTC", payload.Timezone)
	require.Len(t, payload.Days, 2)
	require.Equal(t, "2024-01-01", payload.Days[0].Date)
	require.EqualValues(t, 1, payload.Days[0].Requests)
	require.Equal(t, "2024-01-03", payload.Days[1].Date)
	require.EqualValues(t, 1, payload.Days[1].Requests)
	require.EqualValues(t, 2, payload.Totals.Requests)
	require.EqualValues(t, 30, payload.Totals.PromptTokens)
	require.EqualValues(t, 12, payload.Totals.CompletionTokens)
	require.EqualValues(t, 42, payload.Totals.TotalTokens)
	require.EqualValues(t, 300, payload.Totals.Quota)

	// Query parameters are intentionally narrow and duplicate values are
	// rejected rather than silently selecting one of them.
	require.Equal(t, http.StatusBadRequest, h.request("GET", "/api/oauth2/usage/activity?from=1704067200&from=1704067201&to=1704326400", "", auth).Code)
	require.Equal(t, http.StatusBadRequest, h.request("GET", "/api/oauth2/usage/activity?from=1704067200&to=1704326400&user_id=2", "", auth).Code)
}

func TestOAuthHTTPActivityRequiresUsageScope(t *testing.T) {
	h := setupOAuthHTTP(t)
	credentials, _ := h.approve(t)
	form := url.Values{
		"grant_type": {"refresh_token"}, "client_id": {service.OAuthPiClientID},
		"refresh_token": {credentials.RefreshToken}, "resource": {h.integration.Resource},
		"scope": {service.OAuthCatalogScope + " " + service.OAuthBalanceScope + " " + service.OAuthInvokeScope + " " + service.OAuthGroupScope("default")},
	}
	response := h.request("POST", "/api/oauth2/token", form.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var narrowed oauthserver.TokenResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &narrowed))
	auth := map[string]string{"Authorization": "Bearer " + narrowed.AccessToken}
	require.Equal(t, http.StatusUnauthorized, h.request("GET", "/api/oauth2/usage/activity", "", auth).Code)
}
