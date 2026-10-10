package router

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

func mountPiRemoteFixture(h *oauthHTTPTest) {
	group := h.engine.Group("/api/remote-control/v1/pi", middleware.PiRemoteAuth(), middleware.DisableCache())
	group.GET("/sessions", controller.PiRemoteListSessions)
	group.PUT("/sessions/:session_id", middleware.RequestBodyLimit(24<<10), controller.PiRemoteUpsertSession)
	group.DELETE("/sessions/:session_id", controller.PiRemoteDeleteSession)
	group.GET("/sessions/:session_id/messages", controller.PiRemoteGetMessages)
	group.POST("/sessions/:session_id/messages", middleware.RequestBodyLimit(80<<10), controller.PiRemoteAppendMessage)
}

func TestPiRemoteOAuthExplicitConsentAndNoModelBilling(t *testing.T) {
	h := setupOAuthHTTP(t)
	mountPiRemoteFixture(h)
	legacy, _ := h.approve(t)
	require.Equal(t, 403, h.request("GET", "/api/remote-control/v1/pi/sessions", "", map[string]string{"Authorization": "Bearer " + legacy.AccessToken}).Code)
	query, err := url.ParseQuery(h.query)
	require.NoError(t, err)
	query.Set("scope", query.Get("scope")+" "+service.OAuthRemoteControlScope)
	h.query = query.Encode()
	current, _ := h.approve(t)
	require.Contains(t, strings.Fields(current.Scope), service.OAuthRemoteControlScope)
	// No model balance is required for account-bound remote messages.
	require.NoError(t, h.db.Model(&model.User{}).Where("id = ?", h.user.Id).Update("quota", 0).Error)
	var tokensBefore int64
	require.NoError(t, h.db.Model(&model.Token{}).Count(&tokensBefore).Error)
	headers := map[string]string{"Authorization": "Bearer " + current.AccessToken, "Content-Type": "application/json"}
	nonce := base64.RawURLEncoding.EncodeToString([]byte("123456789012"))
	ciphertext := base64.RawURLEncoding.EncodeToString([]byte("opaque-test-authentication-tag"))
	metadata, err := json.Marshal(map[string]any{"device_id": "fixture_device_remote", "metadata": map[string]string{"nonce": nonce, "ciphertext": ciphertext}})
	require.NoError(t, err)
	path := "/api/remote-control/v1/pi/sessions/fixture_session_remote"
	created := h.request("PUT", path, string(metadata), headers)
	require.Equal(t, 200, created.Code, created.Body.String())
	var envelope struct {
		Data struct {
			Generation string `json:"generation"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &envelope))
	require.NotEmpty(t, envelope.Data.Generation)
	message, err := json.Marshal(map[string]string{"sender": "plugin", "nonce": nonce, "ciphertext": ciphertext})
	require.NoError(t, err)
	require.Equal(t, 200, h.request("POST", path+"/messages", string(message), headers).Code)
	read := h.request("GET", path+"/messages?after=0", "", headers)
	require.Equal(t, 200, read.Code, read.Body.String())
	require.Contains(t, read.Body.String(), ciphertext)
	require.Contains(t, read.Header().Get("Cache-Control"), "no-store")
	for _, cursor := range []string{"-1", "18446744073709551616", "9007199254740992", "1&after=2", ""} {
		require.Equal(t, 400, h.request("GET", path+"/messages?after="+cursor, "", headers).Code, cursor)
	}
	browser := map[string]string{"Authorization": "Bearer " + h.login.AccessToken, "New-Api-User": strconv.Itoa(h.user.Id)}
	require.Equal(t, 200, h.request("GET", path+"/messages", "", browser).Code)
	other := map[string]string{"Authorization": "Bearer " + h.otherLogin.AccessToken}
	require.Equal(t, 404, h.request("GET", path+"/messages", "", other).Code)
	require.Equal(t, 200, h.request("DELETE", path, "", other).Code)
	require.Equal(t, 200, h.request("GET", path+"/messages", "", headers).Code)
	var tokensAfter int64
	require.NoError(t, h.db.Model(&model.Token{}).Count(&tokensAfter).Error)
	require.Equal(t, tokensBefore, tokensAfter, "remote transport must not create relay billing tokens")
	require.Equal(t, 200, h.request("DELETE", path, "", headers).Code)
	require.Equal(t, 404, h.request("GET", path+"/messages", "", headers).Code)
	// Revocation is checked on every remote request; no dashboard/API-key fallback.
	revoke := url.Values{"client_id": {service.OAuthPiClientID}, "token": {current.AccessToken}}.Encode()
	require.Equal(t, 200, h.request("POST", "/api/oauth2/revoke", revoke, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}).Code)
	require.Equal(t, 403, h.request("GET", "/api/remote-control/v1/pi/sessions", "", headers).Code)
}

func TestPiRemoteScopeDoesNotLeakToOtherClientsAndTracksAccountStatus(t *testing.T) {
	h := setupOAuthHTTP(t)
	mountPiRemoteFixture(h)
	for _, client := range []string{service.OAuthDshClientID, service.OAuthOpenCodeClientID, service.OAuthCLIClientID, service.OAuthCodewhaleClientID} {
		query, err := url.ParseQuery(h.query)
		require.NoError(t, err)
		query.Set("client_id", client)
		query.Set("scope", query.Get("scope")+" "+service.OAuthRemoteControlScope)
		_, _, err = h.integration.ConsentQuery(query.Encode(), &h.user)
		require.Error(t, err, client)
	}
	query, err := url.ParseQuery(h.query)
	require.NoError(t, err)
	query.Set("scope", query.Get("scope")+" "+service.OAuthRemoteControlScope)
	h.query = query.Encode()
	current, _ := h.approve(t)
	headers := map[string]string{"Authorization": "Bearer " + current.AccessToken}
	require.NoError(t, h.db.Model(&model.User{}).Where("id = ?", h.user.Id).Update("status", common.UserStatusDisabled).Error)
	require.Equal(t, 403, h.request("GET", "/api/remote-control/v1/pi/sessions", "", headers).Code)
}
