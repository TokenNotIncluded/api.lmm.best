package router

import (
	"net/http"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestToolMarketDeleteRouteRequiresOwnerOrAdmin(t *testing.T) {
	router, db, token, owner := toolMarketTestRouter(t)
	service := model.ToolMarketService{ID: "delete-owned", OwnerID: owner.Id, Status: "draft"}
	require.NoError(t, db.Create(&service).Error)
	response := toolMarketHTTPRequest(router, "DELETE", "/api/tool-market/services/"+service.ID, "", "")
	require.Equal(t, http.StatusUnauthorized, response.Code)
	otherToken := "delete-other-token"
	other := model.User{Username: "delete-other", AffCode: "delete-other", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AccessToken: &otherToken}
	require.NoError(t, db.Create(&other).Error)
	response = toolMarketHTTPRequest(router, "DELETE", "/api/tool-market/services/"+service.ID, otherToken, "")
	require.Equal(t, http.StatusForbidden, response.Code)
	response = toolMarketHTTPRequest(router, "DELETE", "/api/tool-market/services/"+service.ID, token, "")
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, db.First(&service, "id = ?", service.ID).Error)
	require.Equal(t, model.ToolMarketServiceDeleted, service.Status)
	adminToken := "delete-admin-token"
	admin := model.User{Username: "delete-admin", AffCode: "delete-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AccessToken: &adminToken}
	require.NoError(t, db.Create(&admin).Error)
	second := model.ToolMarketService{ID: "delete-admin-managed", OwnerID: owner.Id, Status: "draft"}
	require.NoError(t, db.Create(&second).Error)
	response = toolMarketHTTPRequest(router, "DELETE", "/api/tool-market/services/"+second.ID, adminToken, "")
	require.Equal(t, http.StatusOK, response.Code)
}
