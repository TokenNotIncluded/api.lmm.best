package router

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestToolMarketGrantRecordRoutesRequireRevocationAndOwnership(t *testing.T) {
	router, db, auth, user := toolMarketTestRouter(t)
	own := model.ToolMarketGrant{ID: "own-grant", UserID: user.Id, ClientID: "my-agent", ToolID: "lookup", VersionID: "v1", ExpiresAt: common.GetTimestamp() + 3600}
	other := model.ToolMarketGrant{ID: "other-grant", UserID: user.Id + 1, ClientID: "my-agent", ToolID: "lookup", VersionID: "v1", RevokedAt: 1}
	require.NoError(t, db.Create(&own).Error)
	require.NoError(t, db.Create(&other).Error)
	const revokePath = "/api/tool-market/grants/own-grant"
	const removePath = revokePath + "/record"
	response := toolMarketHTTPRequest(router, "DELETE", removePath, "", "")
	require.NotEqual(t, 200, response.Code)
	response = toolMarketHTTPRequest(router, "DELETE", removePath, auth, "")
	require.Equal(t, 409, response.Code, response.Body.String())
	response = toolMarketHTTPRequest(router, "DELETE", "/api/tool-market/grants/other-grant/record", auth, "")
	require.Equal(t, 404, response.Code, response.Body.String())
	response = toolMarketHTTPRequest(router, "DELETE", revokePath, auth, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	rows, err := model.ListToolMarketAccountResources(user.Id, "grants", 0, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the existing DELETE endpoint still only revokes")
	response = toolMarketHTTPRequest(router, "DELETE", removePath, auth, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	response = toolMarketHTTPRequest(router, "GET", "/api/tool-market/mine/grants", auth, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	var body struct {
		Success bool                    `json:"success"`
		Data    []model.ToolMarketGrant `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Empty(t, body.Data)
	var retained model.ToolMarketGrant
	require.NoError(t, db.First(&retained, "id = ?", own.ID).Error)
	require.NotZero(t, retained.RevokedAt)
}

func TestToolMarketTokenRecordRouteKeepsRevokedCredentialRejected(t *testing.T) {
	router, db, auth, user := toolMarketTestRouter(t)
	require.NoError(t, db.AutoMigrate(&model.ToolMarketToken{}))
	raw, token, err := model.CreateToolMarketToken(user.Id, "my-agent", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	path := "/api/tool-market/tokens/" + token.ID
	response := toolMarketHTTPRequest(router, "DELETE", path+"/record", auth, "")
	require.Equal(t, 409, response.Code, response.Body.String())
	response = toolMarketHTTPRequest(router, "DELETE", path, auth, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	response = toolMarketHTTPRequest(router, "DELETE", path+"/record", auth, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	_, err = model.VerifyToolMarketToken(raw)
	require.ErrorIs(t, err, model.ErrToolMarketDenied)
	var retained model.ToolMarketToken
	require.NoError(t, db.First(&retained, "id = ?", token.ID).Error)
	require.Equal(t, token.Digest, retained.Digest)
	require.NotZero(t, retained.RevokedAt)
}

func TestToolMarketClientRemoveRouteUsesOnlyAuthenticatedOwner(t *testing.T) {
	router, db, auth, user := toolMarketTestRouter(t)
	require.NoError(t, db.AutoMigrate(&model.ToolMarketToken{}))
	other := model.User{Username: "other-record-owner", AffCode: "other-record-owner", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&other).Error)
	_, own, err := model.CreateToolMarketToken(user.Id, "my-agent", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	otherRaw, _, err := model.CreateToolMarketToken(other.Id, "my-agent", true, false, common.GetTimestamp()+3600)
	require.NoError(t, err)
	require.NoError(t, model.RevokeToolMarketToken(user.Id, own.ID))
	const path = "/api/tool-market/clients/remove"
	response := toolMarketHTTPRequest(router, "POST", path, "", `{"client_id":"my-agent"}`)
	require.NotEqual(t, 200, response.Code)
	input, err := json.Marshal(map[string]any{"client_id": "my-agent", "user_id": other.Id})
	require.NoError(t, err)
	response = toolMarketHTTPRequest(router, "POST", path, auth, string(input))
	require.Equal(t, 200, response.Code, response.Body.String())
	rows, err := model.ListToolMarketAccountResources(user.Id, "tokens", 0, 100)
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = model.VerifyToolMarketToken(otherRaw)
	require.NoError(t, err)
	response = toolMarketHTTPRequest(router, "POST", path, auth, `{"client_id":"oauth:lmm-pi"}`)
	require.Equal(t, 422, response.Code, response.Body.String())
	response = toolMarketHTTPRequest(router, "POST", path, auth, `{"client_id":`)
	require.Equal(t, 422, response.Code, response.Body.String())
}
