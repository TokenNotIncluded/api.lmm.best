package router

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestToolMarketDisconnectRouteUsesOnlyAuthenticatedAccount(t *testing.T) {
	router, db, accessToken, user := toolMarketTestRouter(t)
	require.NoError(t, db.AutoMigrate(&model.ToolMarketToken{}))
	other := model.User{Username: "other-market-user", AffCode: "other-market-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&other).Error)
	ownSecret, _, err := model.CreateToolMarketToken(user.Id, "my-agent", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	otherSecret, _, err := model.CreateToolMarketToken(other.Id, "my-agent", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	const path = "/api/tool-market/clients/disconnect"
	response := toolMarketHTTPRequest(router, "POST", path, "", `{"client_id":"my-agent"}`)
	require.NotEqual(t, 200, response.Code)
	_, err = model.VerifyToolMarketToken(ownSecret)
	require.NoError(t, err)

	input, err := json.Marshal(map[string]any{"client_id": "my-agent", "user_id": other.Id})
	require.NoError(t, err)
	response = toolMarketHTTPRequest(router, "POST", path, accessToken, string(input))
	require.Equal(t, 200, response.Code, response.Body.String())
	var body struct {
		Success bool                             `json:"success"`
		Data    model.ToolMarketClientDisconnect `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.EqualValues(t, 1, body.Data.TokensRevoked)
	_, err = model.VerifyToolMarketToken(ownSecret)
	require.ErrorIs(t, err, model.ErrToolMarketDenied)
	_, err = model.VerifyToolMarketToken(otherSecret)
	require.NoError(t, err)

	response = toolMarketHTTPRequest(router, "POST", path, accessToken, `{"client_id":"oauth:lmm-pi"}`)
	require.Equal(t, 422, response.Code, response.Body.String())
	response = toolMarketHTTPRequest(router, "POST", path, accessToken, `{"client_id":`)
	require.Equal(t, 422, response.Code, response.Body.String())
}
