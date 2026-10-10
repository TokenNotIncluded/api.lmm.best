/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminSiteStatisticsRegisteredRouteRequiresLiveAdminRole(t *testing.T) {
	installRouterCurrencyFixture(t)
	db := setupOpenSourceBountyAccessRouterTest(t)
	require.NoError(t, db.AutoMigrate(&model.UserSession{}, &model.TopUp{}, &model.SubscriptionOrder{}))
	previousSecret, previousRateLimit := common.SessionSecret, common.GlobalApiRateLimitEnable
	common.SessionSecret, common.GlobalApiRateLimitEnable = "isolated-site-statistics-session-test", false
	t.Cleanup(func() { common.SessionSecret, common.GlobalApiRateLimitEnable = previousSecret, previousRateLimit })
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	request := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/finance/site-statistics", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		return res
	}
	require.Equal(t, http.StatusUnauthorized, request("").Code)
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		t.Run(fmt.Sprintf("role-%d", role), func(t *testing.T) {
			level := 3
			user := model.User{Username: fmt.Sprintf("statistics-role-%d", role), AffCode: fmt.Sprintf("statistics-role-%d", role), Role: role, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, Quota: 1234567, UsedQuota: 7654321, TrustLevelOverride: &level}
			require.NoError(t, db.Create(&user).Error)
			bundle, err := service.CreateLoginSession(user.Id, "password", "192.0.2.7", "isolated-site-statistics")
			require.NoError(t, err)
			response := request(bundle.AccessToken)
			if role == common.RoleCommonUser {
				require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
				require.NotContains(t, response.Body.String(), "total_balance_credits")
				return
			}
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var payload struct {
				Success bool                      `json:"success"`
				Data    model.AdminSiteStatistics `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
			require.True(t, payload.Success)
			require.NotEmpty(t, payload.Data.TotalUsedCredits)
			require.NotContains(t, response.Body.String(), user.Username)
			require.Contains(t, response.Header().Get("Cache-Control"), "no-store")
			// The same still-valid token cannot retain old administrator access
			// after the account is demoted in the authoritative user table.
			require.NoError(t, db.Model(&user).Update("role", common.RoleCommonUser).Error)
			response = request(bundle.AccessToken)
			require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), "total_balance_credits")
		})
	}
}
