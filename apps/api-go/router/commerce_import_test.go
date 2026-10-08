package router

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestCommerceImportRouterRequiresLoginAndStrictMutationJSON(t *testing.T) {
	engine, _, sellerToken, _, _, _ := merchantStoreTestRouter(t)
	for _, tc := range []struct{ method, path, body string }{{"GET", "/api/store/commerce-import/config", ""}, {"GET", "/api/store/commerce-import/connections", ""}, {"POST", "/api/store/commerce-import/connections", `{"origin":"https://example.com","client_id":"public"}`}, {"GET", "/api/store/commerce-import/connections/other/catalog", ""}, {"POST", "/api/store/commerce-import/connections/other/restock", `{}`}, {"DELETE", "/api/store/commerce-import/connections/other", ""}} {
		response := shopRequest(engine, tc.method, tc.path, "", tc.body)
		require.NotEqual(t, 200, response.Code)
	}
	for _, body := range []string{`{"origin":"https://one.example.com","origin":"https://two.example.com","client_id":"public"}`, `{"Origin":"https://one.example.com","client_id":"public"}`, `{"origin":"https://one.example.com","client_id":"public","seller_id":999}`, `{} {}`, `null`, `[]`} {
		response := shopRequest(engine, "POST", "/api/store/commerce-import/connections", sellerToken, body)
		require.Equal(t, 422, response.Code, response.Body.String())
	}
}

func TestCommerceImportCallbackUsesStandalonePageBeforeAnyAppResources(t *testing.T) {
	engine, _, _, _, _, _ := merchantStoreTestRouter(t)
	old := system_setting.ServerAddress
	system_setting.ServerAddress = "https://sales.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = old })
	request := httptest.NewRequest("GET", "https://sales.example.com"+service.CommerceImportCallbackPath+"?code=ephemeral-code&state=example-state&iss=https%3A%2F%2Fredemption.example.com", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
	require.Contains(t, response.Header().Get("Content-Security-Policy"), "default-src 'none'")
	require.Contains(t, response.Header().Get("Content-Security-Policy"), "script-src 'nonce-")
	body := response.Body.String()
	require.Contains(t, body, "history.replaceState")
	require.Less(t, strings.Index(body, "history.replaceState"), strings.Index(body, "fetch("))
	require.NotContains(t, body, "cdn.agentlane.com")
	require.NotContains(t, body, "/assets/")
	require.NotContains(t, body, "<script src")
	require.NotContains(t, body, "localStorage")
	request = httptest.NewRequest("GET", "https://wrong.example.com"+service.CommerceImportCallbackPath, nil)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, 422, response.Code)
}
