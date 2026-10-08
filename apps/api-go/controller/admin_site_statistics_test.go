/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminSiteStatisticsHandlerRejectsUnprivilegedContextsBeforeDatabase(t *testing.T) {
	previous := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = previous })
	for _, role := range []int{0, common.RoleGuestUser, common.RoleCommonUser, 11} {
		res := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(res)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/finance/site-statistics", nil)
		c.Set("role", role)
		GetAdminSiteStatistics(c)
		require.Equal(t, http.StatusForbidden, res.Code)
		require.NotContains(t, res.Body.String(), "total_balance_credits")
	}
	for _, role := range []int{common.RoleAdminUser, common.RoleRootUser} {
		res := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(res)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/finance/site-statistics", nil)
		c.Set("role", role)
		GetAdminSiteStatistics(c)
		require.Equal(t, http.StatusServiceUnavailable, res.Code)
		require.NotContains(t, res.Body.String(), "total_balance_credits", "source failure must not render a zero total")
	}
}
