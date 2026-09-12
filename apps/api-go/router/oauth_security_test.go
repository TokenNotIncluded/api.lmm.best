package router

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauthserver"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func oauthRelayHeaders(access, group string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + access, service.OAuthGroupHeader: service.OAuthGroupID(group), "Content-Type": "application/json"}
}

func TestOAuthHTTPBrowserSessionInvalidation(t *testing.T) {
	for _, change := range []string{"same-account-new-session", "session-version", "auth-version", "logout", "expired", "duplicate-cookie", "removed-group"} {
		t.Run(change, func(t *testing.T) {
			h := setupOAuthHTTP(t)
			binding, csrf := h.continueConsent(t)
			cookies := []*http.Cookie{binding, {Name: service.RefreshCookieName, Value: h.login.RefreshToken}}
			session := h.db.Model(&model.UserSession{}).Where("sid = ?", h.login.Session.SID)
			switch change {
			case "same-account-new-session":
				login, err := service.CreateLoginSession(h.user.Id, "password", "192.0.2.1", "new-browser-session")
				require.NoError(t, err)
				cookies[1].Value = login.RefreshToken
			case "session-version":
				require.NoError(t, session.UpdateColumn("version", 2).Error)
			case "auth-version":
				require.NoError(t, h.db.Model(&model.User{}).Where("id = ?", h.user.Id).UpdateColumn("auth_version", 2).Error)
			case "logout":
				require.NoError(t, service.RevokeByRefreshToken(h.login.RefreshToken, "", "logout"))
			case "expired":
				require.NoError(t, session.UpdateColumn("expires_at", time.Now().Unix()-1).Error)
			case "duplicate-cookie":
				cookies = append(cookies, &http.Cookie{Name: service.RefreshCookieName, Value: h.otherLogin.RefreshToken})
			case "removed-group":
				require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
			}
			response := h.request("POST", "/api/user/auth/oauth2/consent", url.Values{"csrf": {csrf}, "decision": {"allow"}}.Encode(), map[string]string{"Origin": oauthTestIssuer, "Content-Type": "application/x-www-form-urlencoded"}, cookies...)
			require.Equal(t, 400, response.Code, response.Body.String())
			var grants int64
			require.NoError(t, h.db.Model(&model.OAuthServerGrant{}).Count(&grants).Error)
			require.Zero(t, grants)
		})
	}
}

func TestOAuthHTTPConsentDenyAndReplay(t *testing.T) {
	h := setupOAuthHTTP(t)
	binding, csrf := h.continueConsent(t)
	form := url.Values{"csrf": {csrf}, "decision": {"deny"}}.Encode()
	headers := map[string]string{"Origin": oauthTestIssuer, "Content-Type": "application/x-www-form-urlencoded"}
	cookie := &http.Cookie{Name: service.RefreshCookieName, Value: h.login.RefreshToken}
	response := h.request("POST", "/api/user/auth/oauth2/consent", form, headers, binding, cookie)
	require.Equal(t, 200, response.Code, response.Body.String())
	match := oauthRedirectFixture.FindStringSubmatch(response.Body.String())
	require.Len(t, match, 2)
	target, err := url.Parse(html.UnescapeString(match[1]))
	require.NoError(t, err)
	require.Equal(t, "access_denied", target.Query().Get("error"))
	require.Equal(t, oauthTestIssuer, target.Query().Get("iss"))
	require.Empty(t, target.Query().Get("code"))
	require.Equal(t, 400, h.request("POST", "/api/user/auth/oauth2/consent", form, headers, binding, cookie).Code)
	var count int64
	require.NoError(t, h.db.Model(&model.OAuthServerGrant{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestOAuthHTTPConsentRejectsOriginAndCookieAmbiguity(t *testing.T) {
	for _, kind := range []string{"cross-site", "duplicate-origin", "duplicate-binding", "duplicate-csrf", "authorization"} {
		t.Run(kind, func(t *testing.T) {
			h := setupOAuthHTTP(t)
			binding, csrf := h.continueConsent(t)
			body := url.Values{"csrf": {csrf}, "decision": {"allow"}}.Encode()
			if kind == "duplicate-csrf" {
				body += "&csrf=" + csrf
			}
			request := httptest.NewRequest("POST", oauthTestIssuer+"/api/user/auth/oauth2/consent", strings.NewReader(body))
			request.Header.Set("Origin", oauthTestIssuer)
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.AddCookie(binding)
			request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: h.login.RefreshToken})
			switch kind {
			case "cross-site":
				request.Header.Set("Sec-Fetch-Site", "cross-site")
			case "duplicate-origin":
				request.Header.Add("Origin", oauthTestIssuer)
			case "duplicate-binding":
				request.AddCookie(binding)
			case "authorization":
				request.Header.Set("Authorization", "Bearer "+h.login.AccessToken)
			}
			response := httptest.NewRecorder()
			h.engine.ServeHTTP(response, request)
			require.Equal(t, 400, response.Code, response.Body.String())
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}

func TestOAuthHTTPRefreshNarrowingAndReplay(t *testing.T) {
	h := setupOAuthHTTP(t)
	credentials, _ := h.approve(t)
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {service.OAuthPiClientID}, "refresh_token": {credentials.RefreshToken}, "resource": {h.integration.Resource}, "scope": {service.OAuthCatalogScope + " " + service.OAuthBalanceScope + " " + service.OAuthInvokeScope + " " + service.OAuthGroupScope("default")}}
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	response := h.request("POST", "/api/oauth2/token", form.Encode(), headers)
	require.Equal(t, 200, response.Code, response.Body.String())
	var narrowed oauthserver.TokenResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &narrowed))
	require.NotEqual(t, credentials.RefreshToken, narrowed.RefreshToken)
	require.NotContains(t, narrowed.Scope, service.OAuthGroupScope("vip"))
	auth := map[string]string{"Authorization": "Bearer " + narrowed.AccessToken}
	response = h.request("GET", "/api/oauth2/catalog", "", auth)
	require.Equal(t, 200, response.Code, response.Body.String())
	var catalog service.OAuthCatalog
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &catalog))
	require.Len(t, catalog.Groups, 1)
	require.Equal(t, "default", catalog.Groups[0].Name)
	require.NotEqual(t, 200, h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, oauthRelayHeaders(narrowed.AccessToken, "vip")).Code)
	form.Set("refresh_token", narrowed.RefreshToken)
	form.Set("scope", form.Get("scope")+" "+service.OAuthGroupScope("future"))
	require.NotEqual(t, 200, h.request("POST", "/api/oauth2/token", form.Encode(), headers).Code)
	require.Equal(t, 200, h.request("GET", "/api/oauth2/balance", "", auth).Code)
	form.Del("scope")
	form.Set("refresh_token", credentials.RefreshToken)
	require.NotEqual(t, 200, h.request("POST", "/api/oauth2/token", form.Encode(), headers).Code)
	require.NotEqual(t, 200, h.request("GET", "/api/oauth2/balance", "", auth).Code)
}

func TestOAuthHTTPNoFallbackToOrdinaryCredentials(t *testing.T) {
	h := setupOAuthHTTP(t)
	ordinary := model.Token{UserId: h.user.Id, Key: "ordinary_test_key", Name: "ordinary", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, h.db.Create(&ordinary).Error)
	h.engine.GET("/mixed-auth", middleware.TokenOrUserAuth(), func(c *gin.Context) { c.Status(200) })
	h.engine.GET("/usage-auth", middleware.QuotaQueryAuth(), func(c *gin.Context) { c.Status(200) })
	for _, path := range []string{"/read-only", "/mixed-auth", "/usage-auth"} {
		require.Equal(t, 200, h.request("GET", path, "", map[string]string{"Authorization": "Bearer " + ordinary.Key}).Code, path)
	}
	for _, headers := range []map[string]string{
		{"Authorization": "Bearer " + ordinary.Key, service.OAuthGroupHeader: service.OAuthGroupID("default")},
		{"Authorization": "Bearer " + h.login.AccessToken, service.OAuthGroupHeader: service.OAuthGroupID("default")},
		{"Authorization": "Bearer lmm_at_invalid", "x-api-key": ordinary.Key},
		{"Authorization": "Bearer " + ordinary.Key, "x-api-key": "lmm_at_invalid"},
		{"Authorization": "Bearer " + ordinary.Key, "mj-api-secret": "lmm_rt_invalid"},
	} {
		for _, path := range []string{"/read-only", "/mixed-auth", "/usage-auth"} {
			response := h.request("GET", path, "", headers)
			require.NotEqual(t, 200, response.Code, path)
			require.NotContains(t, response.Body.String(), ordinary.Key)
		}
	}
	_, err := service.ConfigureOAuthIntegration(nil, service.OAuthServerConfig{})
	require.NoError(t, err)
	require.NotEqual(t, 200, h.request("GET", "/mixed-auth?access_token=garbage", "", map[string]string{"Authorization": "Bearer " + h.login.AccessToken}).Code)
	require.NotEqual(t, 200, h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, oauthRelayHeaders("lmm_at_invalid", "default")).Code)
	require.Equal(t, 200, h.request("GET", "/read-only", "", map[string]string{"Authorization": "Bearer " + ordinary.Key}).Code)
}

func TestOAuthHTTPManagedKeysHiddenFromOrdinaryOperations(t *testing.T) {
	h := setupOAuthHTTP(t)
	self := h.engine.Group("/test/tokens", func(c *gin.Context) { c.Set("id", h.user.Id) })
	self.GET("", controller.GetAllTokens)
	self.GET("/:id/key", controller.GetTokenKey)
	self.DELETE("/:id", controller.DeleteToken)
	h.engine.GET("/managed-usage", middleware.QuotaQueryAuth(), func(c *gin.Context) { c.Status(200) })
	credentials, _ := h.approve(t)
	require.Equal(t, 200, h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, oauthRelayHeaders(credentials.AccessToken, "default")).Code)
	var managed model.Token
	require.NoError(t, h.db.Where("oauth_managed = ?", true).First(&managed).Error)
	ordinary := model.Token{UserId: h.user.Id, Key: "ordinary_isolation_key", Name: "ordinary", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, h.db.Create(&ordinary).Error)
	count, err := model.CountUserTokens(h.user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	for _, key := range []string{managed.Key, "sk-" + managed.Key} {
		found, total, err := model.SearchUserTokens(h.user.Id, "", key, 0, 10)
		require.NoError(t, err)
		require.Zero(t, total)
		require.Empty(t, found)
	}
	keys, err := model.GetTokenKeysByIds([]int{managed.Id, ordinary.Id}, h.user.Id)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Equal(t, ordinary.Key, keys[0].Key)
	managed.Status = common.TokenStatusDisabled
	require.NoError(t, managed.SelectUpdate())
	require.NoError(t, managed.Delete())
	var retained model.Token
	require.NoError(t, h.db.First(&retained, managed.Id).Error)
	require.Equal(t, common.TokenStatusEnabled, retained.Status)
	for _, path := range []string{"/test/tokens", "/test/tokens/" + strconv.Itoa(managed.Id) + "/key"} {
		response := h.request("GET", path, "", nil)
		require.NotContains(t, response.Body.String(), managed.Key)
		require.NotContains(t, response.Body.String(), "OAuth / LMM for Pi")
	}
	require.Contains(t, h.request("GET", "/test/tokens/"+strconv.Itoa(ordinary.Id)+"/key", "", nil).Body.String(), ordinary.Key)
	response := h.request("DELETE", "/test/tokens/"+strconv.Itoa(managed.Id), "", nil)
	require.Contains(t, response.Body.String(), `"success":false`)
	require.NoError(t, h.db.First(&retained, managed.Id).Error)
	for _, path := range []string{"/read-only", "/managed-usage", "/v1/chat/completions"} {
		method := "GET"
		if strings.HasPrefix(path, "/v1/") {
			method = "POST"
		}
		for _, raw := range []string{managed.Key, "sk-" + managed.Key} {
			require.NotEqual(t, 200, h.request(method, path, `{"model":"gpt-4o"}`, map[string]string{"Authorization": "Bearer " + raw, "Content-Type": "application/json"}).Code)
		}
	}
}

func TestOAuthHTTPRefundAndInsufficientFunds(t *testing.T) {
	h := setupOAuthHTTP(t)
	credentials, _ := h.approve(t)
	headers := oauthRelayHeaders(credentials.AccessToken, "default")
	headers["X-Test-Refund"] = "yes"
	response := h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, headers)
	require.Equal(t, 200, response.Code, response.Body.String())
	var wallet model.User
	var managed model.Token
	require.Eventually(t, func() bool {
		return h.db.First(&wallet, h.user.Id).Error == nil && wallet.Quota == 10000 &&
			h.db.Where("oauth_managed = ?", true).First(&managed).Error == nil && managed.UsedQuota == 0
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, 10000, wallet.Quota)
	require.Zero(t, managed.UsedQuota)
	require.Zero(t, managed.RemainQuota)
	require.NoError(t, h.db.Model(&model.User{}).Where("id = ?", h.user.Id).UpdateColumn("quota", 100).Error)
	response = h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, headers)
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	require.NoError(t, h.db.First(&wallet, h.user.Id).Error)
	require.NoError(t, h.db.First(&managed, managed.Id).Error)
	require.Equal(t, 100, wallet.Quota)
	require.Zero(t, managed.UsedQuota)
}

func TestOAuthHTTPRevocationBeforeModelRouting(t *testing.T) {
	h := setupOAuthHTTP(t)
	credentials, _ := h.approve(t)
	h.engine.POST("/v1/responses", middleware.TokenAuth(), func(c *gin.Context) {
		raw := url.Values{"client_id": {service.OAuthPiClientID}, "token": {credentials.RefreshToken}}.Encode()
		require.NoError(t, h.integration.Core.Revoke(c.Request.Context(), raw))
		require.False(t, middleware.ValidateOAuthRelayModel(c, "gpt-4o"))
	})
	response := h.request("POST", "/v1/responses", `{"model":"gpt-4o"}`, oauthRelayHeaders(credentials.AccessToken, "default"))
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	var wallet model.User
	require.NoError(t, h.db.First(&wallet, h.user.Id).Error)
	require.Equal(t, 10000, wallet.Quota)
}

func TestOAuthHTTPRejectsAmbiguousResourceHeaders(t *testing.T) {
	h := setupOAuthHTTP(t)
	credentials, _ := h.approve(t)
	for _, name := range []string{"Authorization", service.OAuthGroupHeader, "Content-Type"} {
		request := httptest.NewRequest("POST", oauthTestIssuer+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
		for key, value := range oauthRelayHeaders(credentials.AccessToken, "default") {
			request.Header.Set(key, value)
		}
		request.Header.Add(name, request.Header.Get(name))
		response := httptest.NewRecorder()
		h.engine.ServeHTTP(response, request)
		require.NotEqual(t, 200, response.Code, name)
	}
	for _, extra := range []string{"x-api-key", "x-goog-api-key", "mj-api-secret", "Cookie", "Sec-WebSocket-Protocol"} {
		headers := oauthRelayHeaders(credentials.AccessToken, "default")
		headers[extra] = "ordinary-credential"
		require.Equal(t, 400, h.request("POST", "/v1/chat/completions", `{"model":"gpt-4o"}`, headers).Code, extra)
	}
}
