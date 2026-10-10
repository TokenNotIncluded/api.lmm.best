package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestToolMarketOAuthClientListIsAccountScopedAndContainsOnlyTargets(t *testing.T) {
	db, user, _ := setupOpenSourceBountyMCPControllerTest(t)
	require.NoError(t, model.MigrateOAuthBilling(db))
	integration, err := service.ConfigureOAuthIntegration(db, service.OAuthServerConfig{Enabled: true, Issuer: "https://oauth.example.test", Groups: []string{"default"}})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = service.ConfigureOAuthIntegration(nil, service.OAuthServerConfig{}) })
	now := time.Now().UnixMilli()
	for _, item := range []struct {
		id, client string
		userID     int
	}{{"private-own-family", service.OAuthPiClientID, user.Id}, {"private-other-family", service.OAuthDshClientID, user.Id + 100}, {"unregistered-family", "pretend-pi", user.Id}} {
		require.NoError(t, db.Create(&model.OAuthServerGrant{ID: item.id, ClientID: item.client, UserID: int64(item.userID), Issuer: integration.Issuer, Resource: integration.Resource, Scope: "market:discover market:invoke", AbsoluteExpiresAtMs: now + 3600000}).Error)
		require.NoError(t, db.Create(&model.OAuthServerToken{Digest: item.id + "-secret-digest", FamilyID: item.id, Issuer: integration.Issuer, Kind: "access", Scope: "market:discover market:invoke", ExpiresAtMs: now + 600000}).Error)
	}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Set("id", user.Id)
	c.Params = gin.Params{{Key: "kind", Value: "oauth-clients"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/tool-market/mine/oauth-clients?user_id=99999&client_id=pretend-pi&issuer=https://other.example.test", nil)
	ListToolMarketAccountResources(c)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Success bool                          `json:"success"`
		Data    []model.ToolMarketOAuthClient `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Equal(t, []model.ToolMarketOAuthClient{{ClientID: "oauth:lmm-pi"}}, body.Data)
	require.JSONEq(t, `{"success":true,"message":"","data":[{"client_id":"oauth:lmm-pi"}]}`, response.Body.String())
	for _, hidden := range []string{"private-own-family", "private-other-family", "secret-digest", "scope", "active", "market:manage", "pretend-pi"} {
		require.NotContains(t, response.Body.String(), hidden)
	}
	// Issuing personal credentials still cannot impersonate reserved identities.
	_, _, err = model.CreateToolMarketToken(user.Id, "oauth:lmm-pi", true, true, time.Now().Unix()+3600)
	require.ErrorIs(t, err, model.ErrToolMarketInput)
}

func TestToolMarketOAuthClientListIsEmptyWhenOAuthIsDisabled(t *testing.T) {
	_, err := service.ConfigureOAuthIntegration(nil, service.OAuthServerConfig{})
	require.NoError(t, err)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Set("id", 1)
	c.Params = gin.Params{{Key: "kind", Value: "oauth-clients"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/tool-market/mine/oauth-clients", nil)
	ListToolMarketAccountResources(c)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"data":[]`)
}
