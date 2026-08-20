package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreActivationRouteMatrixKeepsChallengesReadOnly(t *testing.T) {
	allowed := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/open-source-bounties"},
		{http.MethodGet, "/api/open-source-bounties/projects/7"},
		{http.MethodGet, "/api/user/developer-access/request"},
		{http.MethodPost, "/api/user/developer-access/request"},
		{http.MethodGet, "/api/security/policy"},
		{http.MethodGet, "/api/security/stats"},
		{http.MethodGet, "/api/release-notes/latest"},
		{http.MethodGet, "/api/user/self/onboarding/todo"},
		{http.MethodPost, "/api/release-notes/7/read"},
		{http.MethodPut, "/api/user/self"},
		{http.MethodPost, "/api/user/passkey/register/begin"},
		{http.MethodGet, "/api/subscription/epay/notify"},
		{http.MethodPost, "/api/subscription/epay/notify"},
		{http.MethodGet, "/api/subscription/epay/return"},
		{http.MethodPost, "/api/subscription/epay/return"},
	}
	for _, request := range allowed {
		assert.True(t, preActivationRouteAllowed(request.method, request.path), "%s %s", request.method, request.path)
	}

	denied := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/token"},
		{http.MethodGet, "/api/open-source-bounties/accepted"},
		{http.MethodGet, "/api/open-source-bounties/disputes/mine"},
		{http.MethodPost, "/api/open-source-bounties/projects/7/accept"},
		{http.MethodPost, "/api/open-source-bounties/projects/7/submit"},
		{http.MethodPost, "/api/open-source-bounties/challenges/9/withdraw"},
		{http.MethodPost, "/api/open-source-bounties/challenges/9/rate-owner"},
		{http.MethodPost, "/api/open-source-bounties/challenges/9/disputes"},
		{http.MethodGet, "/api/user/topup/info"},
		{http.MethodGet, "/api/user/topup/self"},
		{http.MethodPost, "/api/user/stripe/pay"},
		{http.MethodGet, "/api/user/aff"},
		{http.MethodGet, "/api/user/checkin"},
		{http.MethodPost, "/api/user/checkin"},
		{http.MethodGet, "/api/user/developer-access/unknown"},
		{http.MethodPatch, "/api/user/self/onboarding/todo"},
		{http.MethodGet, "/api/models"},
		{http.MethodGet, "/api/channel"},
		{http.MethodGet, "/api/pricing"},
		{http.MethodGet, "/api/subscription/plans"},
		{http.MethodPost, "/api/subscription/balance/pay"},
		{http.MethodGet, "/api/usage"},
		{http.MethodPost, "/api/subscription/admin/plans"},
		{http.MethodGet, "/api/open-source-bounties-probe"},
		{http.MethodGet, "/api/subscription-probe"},
		{http.MethodPost, "/api/open-source-bounties"},
		{http.MethodGet, "/api/open-source-bounties/mine"},
		{http.MethodGet, "/api/open-source-bounties/mcp-token"},
		{http.MethodPut, "/api/open-source-bounties/projects/7"},
		{http.MethodPost, "/api/open-source-bounties/projects/7/publish"},
		{http.MethodPost, "/api/open-source-bounties/challenges/9/approve"},
		{http.MethodPost, "/api/open-source-bounties/challenges/9/tip"},
	}
	for _, request := range denied {
		assert.False(t, preActivationRouteAllowed(request.method, request.path), "%s %s", request.method, request.path)
	}
}

func TestTrustLevelDeveloperAccessBoundary(t *testing.T) {
	levelZero := 0
	levelOne := 1
	invalidLevel := 99
	for _, test := range []struct {
		name    string
		user    *model.UserBase
		granted bool
	}{
		{name: "level zero", user: &model.UserBase{Role: common.RoleCommonUser, TrustLevelOverride: &levelZero}},
		{name: "level one", user: &model.UserBase{Role: common.RoleCommonUser, TrustLevelOverride: &levelOne}, granted: true},
		{name: "invalid ordinary override", user: &model.UserBase{Role: common.RoleCommonUser, TrustLevelOverride: &invalidLevel}},
		{name: "administrator", user: &model.UserBase{Role: common.RoleAdminUser}, granted: true},
		{name: "root", user: &model.UserBase{Role: common.RoleRootUser}, granted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			granted, err := trustLevelAllowsDeveloperAccess(test.user)
			require.NoError(t, err)
			assert.Equal(t, test.granted, granted)
		})
	}
}

func TestConsoleAccessGateHidesDiscoveryRoutesWithoutActivatedSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{
		"/api/assistant/pricing",
		"/api/channel",
		"/api/custom-oauth-provider",
		"/api/data",
		"/api/data/self",
		"/api/deployments",
		"/api/group",
		"/api/log/self",
		"/api/mj",
		"/api/models",
		"/api/open-source-bounties/mcp-token",
		"/api/option",
		"/api/performance/stats",
		"/api/perf-metrics/summary",
		"/api/prefill_group",
		"/api/pricing",
		"/api/rankings",
		"/api/ratio_config",
		"/api/ratio_sync/channels",
		"/api/redemption",
		"/api/status/test",
		"/api/subscription/plans",
		"/api/system-info/instances",
		"/api/system-task/list",
		"/api/task",
		"/api/token",
		"/api/usage",
		"/api/user/groups",
		"/api/user/models",
		"/api/user/self/groups",
		"/api/vendors/search",
	} {
		router := gin.New()
		router.Use(ConsoleAccessGate())
		router.GET(path, func(c *gin.Context) { c.Status(http.StatusNoContent) })

		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

		assert.Equal(t, http.StatusNotFound, response.Code, path)
		assert.JSONEq(t, `{"message":"Not Found"}`, response.Body.String(), path)
	}
}

func TestConsoleAccessGateAllowsAuthenticatedSelfUsageReadsBeforeActivation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	levelZero := 0
	user := &model.UserBase{
		Id:                 18,
		Role:               common.RoleCommonUser,
		TrustLevelOverride: &levelZero,
	}

	for _, path := range []string{"/api/data/self", "/api/data/flow/self"} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(dashboardCredentialContextKey, dashboardCredentialResult{
				user:           user,
				credentialKind: dashboardCredentialInternal,
			})
			c.Next()
		})
		router.Use(ConsoleAccessGate())
		router.GET(path, func(c *gin.Context) { c.Status(http.StatusNoContent) })

		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))

		assert.Equal(t, http.StatusNoContent, response.Code, path)
	}
}

func TestConsoleAccessGateKeepsPublicAccountAndBountyRoutesReachable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/status"},
		{http.MethodGet, "/api/security/policy"},
		{http.MethodGet, "/api/security/stats"},
		{http.MethodPost, "/api/user/login"},
		{http.MethodGet, "/api/open-source-bounties"},
		{http.MethodGet, "/api/subscription/epay/notify"},
		{http.MethodPost, "/api/subscription/epay/notify"},
		{http.MethodGet, "/api/subscription/epay/return"},
		{http.MethodPost, "/api/subscription/epay/return"},
		{http.MethodGet, "/api/user/self/onboarding/todo"},
	} {
		router := gin.New()
		router.Use(ConsoleAccessGate())
		router.Any(request.path, func(c *gin.Context) { c.Status(http.StatusNoContent) })

		response := httptest.NewRecorder()
		httpRequest := httptest.NewRequest(request.method, request.path, nil)
		router.ServeHTTP(response, httpRequest)

		assert.Equal(t, http.StatusNoContent, response.Code, "%s %s", request.method, request.path)
	}
}

func TestConsoleAccessGateReturnsTheGenericNotFoundForRestrictedRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, user := range []*model.UserBase{
		{Id: 7, Role: common.RoleCommonUser, ConsoleActivatedAt: 0},
		{Id: 8, Role: common.RoleCommonUser, ConsoleActivatedAt: 10},
		{Id: 9, Role: common.RoleAdminUser, ConsoleActivatedAt: 0},
	} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(dashboardCredentialContextKey, dashboardCredentialResult{
				user:           user,
				credentialKind: dashboardCredentialInternal,
			})
			c.Next()
		})
		router.Use(ConsoleAccessGate())
		router.GET("/api/models", func(c *gin.Context) { c.Status(http.StatusNoContent) })
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/models", nil))

		if user.Role < common.RoleAdminUser && user.ConsoleActivatedAt == 0 {
			assert.Equal(t, http.StatusNotFound, response.Code)
			assert.JSONEq(t, `{"message":"Not Found"}`, response.Body.String())
			continue
		}
		require.Equal(t, http.StatusNoContent, response.Code)
	}
}

func TestConsoleAccessGateAnnotatesActivationForStatusSurfaces(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name      string
		user      *model.UserBase
		activated bool
	}{
		{name: "unactivated", user: &model.UserBase{Id: 7, Role: common.RoleCommonUser}},
		{name: "approved timestamp activates", user: &model.UserBase{Id: 8, Role: common.RoleCommonUser, ConsoleActivatedAt: 10}, activated: true},
		{name: "administrator", user: &model.UserBase{Id: 9, Role: common.RoleAdminUser}, activated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(dashboardCredentialContextKey, dashboardCredentialResult{
					user:           test.user,
					credentialKind: dashboardCredentialInternal,
				})
				c.Next()
			})
			router.Use(ConsoleAccessGate())
			router.GET("/api/status", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"activated": ConsoleActivationGranted(c)})
			})

			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))

			assert.Equal(t, http.StatusOK, response.Code)
			assert.JSONEq(t, `{"activated":`+strconv.FormatBool(test.activated)+`}`, response.Body.String())
		})
	}
}

func TestConsoleAccessGateAllowsL1OverrideToRestrictedRoutes(t *testing.T) {
	levelOne := 1
	user := &model.UserBase{Id: 18, Role: common.RoleCommonUser, TrustLevelOverride: &levelOne}

	for _, path := range []string{
		"/api/assistant/pricing",
		"/api/channel",
		"/api/custom-oauth-provider",
		"/api/data/self",
		"/api/deployments",
		"/api/group",
		"/api/log/self",
		"/api/mj",
		"/api/models",
		"/api/open-source-bounties/mcp-token",
		"/api/option",
		"/api/performance/stats",
		"/api/perf-metrics/summary",
		"/api/prefill_group",
		"/api/pricing",
		"/api/rankings",
		"/api/ratio_config",
		"/api/ratio_sync/channels",
		"/api/redemption",
		"/api/status/test",
		"/api/subscription/plans",
		"/api/system-info/instances",
		"/api/system-task/list",
		"/api/task/self",
		"/api/token/",
		"/api/usage",
		"/api/user/groups",
		"/api/user/models",
		"/api/user/self/groups",
		"/api/vendors/search",
	} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(dashboardCredentialContextKey, dashboardCredentialResult{
				user:           user,
				credentialKind: dashboardCredentialInternal,
			})
			c.Next()
		})
		router.Use(ConsoleAccessGate())
		router.GET(path, func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"activated": ConsoleActivationGranted(c)})
		})

		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, response.Code, path)
		assert.JSONEq(t, `{"activated":true}`, response.Body.String(), path)
	}
}

func TestConsoleAccessGateFailsClosedWhenTrustCalculationFails(t *testing.T) {
	previousDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = previousDB })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(dashboardCredentialContextKey, dashboardCredentialResult{
			user:           &model.UserBase{Id: 17, Role: common.RoleCommonUser},
			credentialKind: dashboardCredentialInternal,
		})
		c.Next()
	})
	router.Use(ConsoleAccessGate())
	router.GET("/api/models", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/models", nil))

	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.JSONEq(t, `{"message":"Not Found"}`, response.Body.String())
}
