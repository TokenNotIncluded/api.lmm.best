package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupDashboardCreditUnitTest(t *testing.T) {
	t.Helper()
	setupDashboardAuthMiddlewareTest(t)
	gin.SetMode(gin.TestMode)
	require.NoError(t, model.DB.AutoMigrate(&model.WalletTransfer{}, &model.Log{}))
	previousLogDB := model.LOG_DB
	model.LOG_DB = model.DB
	t.Cleanup(func() { model.LOG_DB = previousLogDB })
}

func creditUnitRequest(router http.Handler, method, path, token, origin, fetchSite, unit string, quota int) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(fmt.Sprintf(`{"quota":%d}`, quota)))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if fetchSite != "" {
		request.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	if unit != "" {
		request.Header.Set("X-LMM-Credit-Unit", unit)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func creditUnitWalletHandler(t *testing.T, calls *int) gin.HandlerFunc {
	return func(c *gin.Context) {
		(*calls)++
		var body struct {
			Quota int `json:"quota"`
		}
		require.NoError(t, c.ShouldBindJSON(&body))
		_, err := model.CreateWalletTransfer(c.GetInt("id"), body.Quota, "credit-unit-test-request")
		require.NoError(t, err)
		c.Status(http.StatusNoContent)
	}
}

func TestDashboardBrowserCreditUnitGuardsWalletAndAdminWrites(t *testing.T) {
	const initialQuota = 10_000_000
	for _, test := range []struct {
		name, method, origin, fetchSite, unit string
		admin                                 bool
		quota, status                         int
	}{
		{name: "old cached CREDIT wallet", method: http.MethodPost, origin: "https://api.lmm.best", quota: 3_359_744, status: http.StatusConflict},
		{name: "old cached USD wallet", method: http.MethodPost, fetchSite: "same-origin", quota: 3_359_744, status: http.StatusConflict},
		{name: "wrong unit", method: http.MethodPost, origin: "https://api.lmm.best", unit: "3359744", quota: 100_000, status: http.StatusConflict},
		{name: "noncanonical unit spelling", method: http.MethodPost, origin: "https://api.lmm.best", unit: "500000.0", quota: 100_000, status: http.StatusConflict},
		{name: "new browser wallet", method: http.MethodPost, origin: "https://api.lmm.best", fetchSite: "same-origin", unit: "500000", quota: 100_000, status: http.StatusNoContent},
		{name: "direct raw PAT wallet", method: http.MethodPost, quota: 3_359_744, status: http.StatusNoContent},
		{name: "old cached admin PUT", method: http.MethodPut, origin: "https://api.lmm.best", admin: true, quota: 3_359_744, status: http.StatusConflict},
		{name: "old cached admin PATCH", method: http.MethodPatch, fetchSite: "same-origin", admin: true, quota: 3_359_744, status: http.StatusConflict},
		{name: "old cached admin DELETE", method: http.MethodDelete, origin: "https://api.lmm.best", admin: true, quota: 3_359_744, status: http.StatusConflict},
		{name: "new browser admin", method: http.MethodPut, origin: "https://api.lmm.best", unit: "500000", admin: true, quota: 100_000, status: http.StatusNoContent},
		{name: "direct raw PAT admin", method: http.MethodPut, admin: true, quota: 3_359_744, status: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			setupDashboardCreditUnitTest(t)
			user := createMiddlewarePATUser(t, "credit-unit-user", "credit-unit-pat")
			updates := map[string]any{"quota": initialQuota}
			if test.admin {
				updates["role"] = common.RoleAdminUser
			}
			require.NoError(t, model.DB.Model(user).Updates(updates).Error)
			calls := 0
			router := gin.New()
			path := "/api/wallet-transfer"
			if test.admin {
				path = "/api/user/"
				router.Handle(test.method, path, AdminAuth(), func(c *gin.Context) {
					calls++
					var body struct {
						Quota int `json:"quota"`
					}
					require.NoError(t, c.ShouldBindJSON(&body))
					require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", body.Quota).Error)
					c.Status(http.StatusNoContent)
				})
			} else {
				router.POST(path, UserAuth(), creditUnitWalletHandler(t, &calls))
			}
			response := creditUnitRequest(router, test.method, path, "credit-unit-pat", test.origin, test.fetchSite, test.unit, test.quota)
			assert.Equal(t, test.status, response.Code)
			var current model.User
			require.NoError(t, model.DB.First(&current, user.Id).Error)
			var transfers, logs int64
			require.NoError(t, model.DB.Model(&model.WalletTransfer{}).Count(&transfers).Error)
			require.NoError(t, model.DB.Model(&model.Log{}).Count(&logs).Error)
			if test.status == http.StatusConflict {
				assert.Contains(t, response.Body.String(), `"code":"CREDIT_UNIT_REFRESH_REQUIRED"`)
				assert.Contains(t, response.Body.String(), "请刷新页面")
				assert.Zero(t, calls)
				assert.Equal(t, initialQuota, current.Quota)
				assert.Zero(t, transfers)
				assert.Zero(t, logs)
			} else {
				assert.Equal(t, 1, calls)
				if test.admin {
					assert.Equal(t, test.quota, current.Quota)
					assert.EqualValues(t, 1, logs)
				} else {
					assert.Equal(t, initialQuota-test.quota, current.Quota)
					assert.EqualValues(t, 1, transfers)
				}
			}
			assert.Empty(t, response.Header().Values("Set-Cookie"))
			assert.Equal(t, "credit-unit-pat", *current.AccessToken)
		})
	}
}

func TestDashboardBrowserCreditUnitPreservesSessionAndRefresh(t *testing.T) {
	setupDashboardCreditUnitTest(t)
	user := createMiddlewarePATUser(t, "credit-unit-session-user", "unrelated-pat")
	require.NoError(t, model.DB.Model(user).Update("quota", 1_000_000).Error)
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "credit-unit-test")
	require.NoError(t, err)
	var before model.UserSession
	require.NoError(t, model.DB.First(&before, "sid = ?", bundle.Session.SID).Error)
	calls := 0
	router := gin.New()
	router.POST("/api/wallet-transfer", UserAuth(), creditUnitWalletHandler(t, &calls))
	// The production refresh route is public and does not use authHelper.
	var refreshed *service.AuthBundle
	router.POST("/api/user/auth/refresh", func(c *gin.Context) {
		refreshed, _, err = service.RefreshLoginSession(bundle.RefreshToken, bundle.Session.SID, "127.0.0.1", "credit-unit-test")
		require.NoError(t, err)
		c.Status(http.StatusNoContent)
	})
	response := creditUnitRequest(router, http.MethodPost, "/api/wallet-transfer", bundle.AccessToken, "https://api.lmm.best", "", "", 3_359_744)
	require.Equal(t, http.StatusConflict, response.Code)
	assert.Zero(t, calls)
	assert.Empty(t, response.Header().Values("Set-Cookie"))
	var after model.UserSession
	require.NoError(t, model.DB.First(&after, "sid = ?", bundle.Session.SID).Error)
	assert.Equal(t, before, after)
	response = creditUnitRequest(router, http.MethodPost, "/api/user/auth/refresh", "", "https://api.lmm.best", "same-origin", "", 0)
	require.Equal(t, http.StatusNoContent, response.Code)
	require.NotNil(t, refreshed)
	assert.Equal(t, bundle.Session.SID, refreshed.Session.SID)
	response = creditUnitRequest(router, http.MethodPost, "/api/wallet-transfer", refreshed.AccessToken, "https://api.lmm.best", "same-origin", "500000", 100_000)
	require.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, 1, calls)
	var current model.User
	require.NoError(t, model.DB.First(&current, user.Id).Error)
	assert.Equal(t, 900_000, current.Quota)
}

func TestDashboardBrowserCreditUnitKeepsAuthenticationFailures(t *testing.T) {
	for _, test := range []struct {
		name, token                 string
		admin, disabled, invalidJWT bool
		status                      int
	}{
		{name: "missing auth", status: http.StatusUnauthorized},
		{name: "invalid PAT", token: "invalid-pat", status: http.StatusUnauthorized},
		{name: "invalid internal JWT", invalidJWT: true, status: http.StatusUnauthorized},
		{name: "disabled account", token: "credit-unit-common-pat", disabled: true, status: http.StatusUnauthorized},
		{name: "insufficient role", token: "credit-unit-common-pat", admin: true, status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			setupDashboardCreditUnitTest(t)
			user := createMiddlewarePATUser(t, "credit-unit-common", "credit-unit-common-pat")
			if test.disabled {
				require.NoError(t, model.DB.Model(user).Update("status", common.UserStatusDisabled).Error)
			}
			token := test.token
			if test.invalidJWT {
				validToken, _, err := service.IssueAccessToken(service.AuthIdentity{UserID: user.Id, SessionID: "credit-unit-invalid-session", UserAuthVersion: user.AuthVersion, SessionVersion: 1})
				require.NoError(t, err)
				token = tamperDashboardToken(validToken)
			}
			calls := 0
			auth := UserAuth()
			if test.admin {
				auth = AdminAuth()
			}
			router := gin.New()
			router.POST("/api/protected", auth, func(c *gin.Context) { calls++; c.Status(http.StatusNoContent) })
			response := creditUnitRequest(router, http.MethodPost, "/api/protected", token, "https://api.lmm.best", "", "", 0)
			assert.Equal(t, test.status, response.Code)
			assert.NotContains(t, response.Body.String(), "CREDIT_UNIT_REFRESH_REQUIRED")
			assert.Zero(t, calls)
		})
	}
}

func TestDashboardBrowserCreditUnitLeavesReadsAndPublicRoutes(t *testing.T) {
	setupDashboardCreditUnitTest(t)
	createMiddlewarePATUser(t, "credit-unit-exemption-user", "credit-unit-exemption-pat")
	calls := 0
	handler := func(c *gin.Context) { calls++; c.Status(http.StatusNoContent) }
	router := gin.New()
	router.GET("/api/protected", UserAuth(), handler)
	router.HEAD("/api/protected", UserAuth(), handler)
	router.POST("/outside-api", UserAuth(), handler)
	// These routes mirror the production router's public/optional auth chains.
	for _, path := range []string{"/api/stripe/webhook", "/api/creem/webhook", "/api/waffo/webhook", "/api/waffo-pancake/webhook/production", "/api/user/epay/notify", "/api/subscription/epay/notify", "/api/user/login"} {
		router.POST(path, handler)
	}
	router.POST("/api/oauth/state", TryUserAuth(), handler)
	for _, test := range []struct{ method, path, token string }{
		{http.MethodGet, "/api/protected", "credit-unit-exemption-pat"},
		{http.MethodHead, "/api/protected", "credit-unit-exemption-pat"},
		{http.MethodPost, "/outside-api", "credit-unit-exemption-pat"},
		{http.MethodPost, "/api/stripe/webhook", ""},
		{http.MethodPost, "/api/creem/webhook", ""},
		{http.MethodPost, "/api/waffo/webhook", ""},
		{http.MethodPost, "/api/waffo-pancake/webhook/production", ""},
		{http.MethodPost, "/api/user/epay/notify", ""},
		{http.MethodPost, "/api/subscription/epay/notify", ""},
		{http.MethodPost, "/api/user/login", ""},
		{http.MethodPost, "/api/oauth/state", "credit-unit-exemption-pat"},
	} {
		response := creditUnitRequest(router, test.method, test.path, test.token, "https://api.lmm.best", "same-origin", "", 0)
		assert.Equal(t, http.StatusNoContent, response.Code, test.path)
	}
	assert.Equal(t, 11, calls)
}
