package middleware

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBackendJourneyPreActivationCapabilitiesAreMethodScoped(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/user/topup/info"}, {http.MethodGet, "/api/user/topup/self"},
		{http.MethodPost, "/api/user/amount"}, {http.MethodPost, "/api/user/pay"},
		{http.MethodPost, "/api/user/stripe/amount"}, {http.MethodPost, "/api/user/stripe/pay"},
		{http.MethodPost, "/api/user/creem/pay"}, {http.MethodPost, "/api/user/waffo/amount"},
		{http.MethodPost, "/api/user/waffo/pay"}, {http.MethodPost, "/api/user/waffo-pancake/amount"},
		{http.MethodPost, "/api/user/waffo-pancake/pay"}, {http.MethodPost, "/api/user/discount-code/validate"},
		{http.MethodPost, "/api/verify/email"}, {http.MethodGet, "/api/scripts"},
		{http.MethodGet, "/api/scripts/install.sh/raw"}, {http.MethodGet, "/api/uptime/status"},
		{http.MethodGet, "/api/livez"}, {http.MethodGet, "/api/games/signal/daily"},
		{http.MethodGet, "/api/share/profile/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.svg"},
		{http.MethodGet, "/api/games/signal/leaderboard"}, {http.MethodPost, "/api/games/signal/attempts"},
		{http.MethodPost, "/api/games/signal/finish"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			require.True(t, preActivationRouteAllowed(tc.method, tc.path))
			require.True(t, preActivationRouteAllowed(tc.method, tc.path+"/"))
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
				if method != tc.method {
					require.False(t, preActivationRouteAllowed(method, tc.path), method)
				}
			}
		})
	}
	for _, path := range []string{
		"/api/scripts/repository", "/api/scripts/repository/pull", "/api/scripts/install.sh",
		"/api/scripts//raw", "/api/scripts/parent/child/raw", "/api/scripts-probe",
		"/api/user/topup", "/api/user/topup/complete", "/api/user/aff",
		"/api/token", "/api/models", "/api/option", "/api/subscription/admin/plans",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			require.False(t, preActivationRouteAllowed(method, path), "%s %s", method, path)
		}
	}
}

func TestBackendJourneyPublicCapabilitiesDoNotDisappearAfterL0Login(t *testing.T) {
	gin.SetMode(gin.TestMode)
	levelZero := 0
	for _, signedIn := range []bool{false, true} {
		for _, tc := range []struct{ method, path string }{
			{http.MethodGet, "/api/scripts"}, {http.MethodGet, "/api/scripts/install.sh/raw"},
			{http.MethodGet, "/api/uptime/status"}, {http.MethodGet, "/api/games/signal/daily"},
			{http.MethodGet, "/api/games/signal/leaderboard"}, {http.MethodPost, "/api/games/signal/attempts"},
			{http.MethodPost, "/api/games/signal/finish"},
		} {
			engine := gin.New()
			if signedIn {
				engine.Use(func(c *gin.Context) {
					c.Set(dashboardCredentialContextKey, dashboardCredentialResult{
						user:           &model.UserBase{Id: 1, Role: common.RoleCommonUser, TrustLevelOverride: &levelZero},
						credentialKind: dashboardCredentialInternal,
					})
					c.Next()
				})
			}
			engine.Use(ConsoleAccessGate())
			engine.Handle(tc.method, tc.path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, http.StatusNoContent, response.Code, "signedIn=%t %s %s", signedIn, tc.method, tc.path)
		}
	}
}
