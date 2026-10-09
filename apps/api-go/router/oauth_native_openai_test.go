package router

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/stretchr/testify/require"
)

func TestOAuthNativeOpenAIUsesOneCodewhaleGrantAndExistingBilling(t *testing.T) {
	h := setupOAuthHTTP(t)
	query, err := url.ParseQuery(h.query)
	require.NoError(t, err)
	query.Set("client_id", service.OAuthCodewhaleClientID)
	query.Set("scope", strings.Join([]string{service.OAuthCatalogScope, service.OAuthBalanceScope, service.OAuthUsageScope, service.OAuthInvokeScope}, " "))
	h.query = query.Encode()
	grant, _ := h.approve(t)
	headers := map[string]string{"Authorization": "Bearer " + grant.AccessToken, "Content-Type": "application/json"}
	list := h.request("GET", service.OAuthOpenAIBasePath+"/models", "", headers)
	require.Equal(t, 200, list.Code, list.Body.String())
	require.Contains(t, list.Header().Get("Cache-Control"), "no-store")
	var catalog struct {
		Data []struct {
			ID string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &catalog))
	require.Len(t, catalog.Data, 2)
	id := "lmm:" + service.OAuthGroupID("vip") + ":" + base64.RawURLEncoding.EncodeToString([]byte("gpt-4o"))
	require.Contains(t, list.Body.String(), id)
	require.Contains(t, list.Body.String(), "vip / gpt-4o")
	invoke := h.request("POST", service.OAuthOpenAIBasePath+"/chat/completions", `{"model":"`+id+`","messages":[]}`, headers)
	require.Equal(t, 200, invoke.Code, invoke.Body.String())
	require.Contains(t, invoke.Body.String(), `"group":"vip"`)
	require.Contains(t, invoke.Body.String(), `"cross_group_retry":false`)
	var user model.User
	require.NoError(t, h.db.First(&user, h.user.Id).Error)
	require.Equal(t, 9920, user.Quota)
	before := user.Quota
	headers[service.OAuthGroupHeader] = service.OAuthGroupID("default")
	require.Equal(t, 400, h.request("POST", service.OAuthOpenAIBasePath+"/chat/completions", `{"model":"`+id+`"}`, headers).Code)
	delete(headers, service.OAuthGroupHeader)
	for _, body := range []string{
		`{"model":"gpt-4o"}`, `{"model":"` + id + `","model":"` + id + `"}`,
		`{"Model":"` + id + `"}`, `{"model":"` + id + `","group":"default"}`,
		`{"model":"` + id + `","api_key":"forbidden"}`, `{"model":"` + id + `"} {}`,
		`{"model":"` + id + `="}`,
	} {
		require.NotEqual(t, 200, h.request("POST", service.OAuthOpenAIBasePath+"/chat/completions", body, headers).Code, body)
	}
	future := "lmm:" + service.OAuthGroupID("future") + ":" + base64.RawURLEncoding.EncodeToString([]byte("gpt-4o"))
	require.Equal(t, 403, h.request("POST", service.OAuthOpenAIBasePath+"/chat/completions", `{"model":"`+future+`"}`, headers).Code)
	require.NoError(t, h.db.First(&user, h.user.Id).Error)
	require.Equal(t, before, user.Quota, "rejected requests must not create charges")
	revoke := url.Values{"client_id": {service.OAuthCodewhaleClientID}, "token": {grant.RefreshToken}}
	require.Equal(t, 200, h.request("POST", "/api/oauth2/revoke", revoke.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}).Code)
	require.NotEqual(t, 200, h.request("GET", service.OAuthOpenAIBasePath+"/models", "", headers).Code)
	require.NotEqual(t, 200, h.request("POST", service.OAuthOpenAIBasePath+"/chat/completions", `{"model":"`+id+`"}`, headers).Code)
}
