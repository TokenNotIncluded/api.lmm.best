package router

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestToolMarketMetaCentralRouterUsesOwnerAndZeroBudget(t *testing.T) {
	_, db, ownerToken, owner := toolMarketTestRouter(t)
	require.NoError(t, db.AutoMigrate(&model.ToolMarketToken{}))
	_, record, err := model.CreateToolMarketToken(owner.Id, "wiring-agent", true, true, common.GetTimestamp()+3600)
	require.NoError(t, err)
	engine := gin.New()
	SetApiRouter(engine)
	path := "/api/tool-market/meta-delegations/personal/" + record.ID
	response := toolMarketHTTPRequest(engine, "PUT", path, "", `{"enabled":true,"max_total_quota":0}`)
	require.Equal(t, http.StatusUnauthorized, response.Code)
	response = toolMarketHTTPRequest(engine, "PUT", path, ownerToken, `{"enabled":true,"max_total_quota":0}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var budget model.ToolMarketBudget
	require.NoError(t, db.First(&budget, "user_id = ? AND scope = 'client' AND scope_id = ?", owner.Id, record.ClientID).Error)
	require.Zero(t, budget.LimitQuota, "saved zero must be an actual client cap")
	response = toolMarketHTTPRequest(engine, "GET", path, ownerToken, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var view struct {
		Data model.ToolMarketMetaDelegation `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &view))
	require.True(t, view.Data.Enabled)
	require.Zero(t, view.Data.MaxTotalQuota)
	foreignToken := "meta-foreign-api-token"
	foreign := model.User{Username: "meta-foreign", AffCode: "meta-foreign", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AccessToken: &foreignToken}
	require.NoError(t, db.Create(&foreign).Error)
	for _, method := range []string{"GET", "PUT"} {
		response = toolMarketHTTPRequest(engine, method, path, foreignToken, `{"enabled":true,"max_total_quota":100}`)
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
	response = toolMarketHTTPRequest(engine, "GET", "/api/tool-market/meta-delegations/oauth-clients", "", "")
	require.Equal(t, http.StatusUnauthorized, response.Code)
	response = toolMarketHTTPRequest(engine, "GET", "/api/tool-market/meta-delegations/oauth-clients", ownerToken, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}
