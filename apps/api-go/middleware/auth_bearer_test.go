package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRelayBearerSchemePreservesCredentialSemantics(t *testing.T) {
	tests := []struct {
		name, header, fallback, wantKey string
		wantParts                       []string
	}{
		{"canonical", "Bearer sk-AbC123-42", "", "AbC123", []string{"AbC123", "42"}},
		{"lowercase", "bearer sk-AbC123-42", "", "AbC123", []string{"AbC123", "42"}},
		{"uppercase", "BEARER sk-AbC123-42", "", "AbC123", []string{"AbC123", "42"}},
		{"mixed-case", "bEaReR sk-AbC123-42", "", "AbC123", []string{"AbC123", "42"}},
		{"bearer-whitespace", "bEaReR   AbC123 \t", "", "AbC123", []string{"AbC123"}},
		{"raw-key", "AbC123", "", "AbC123", []string{"AbC123"}},
		{"raw-prefixed-key", "sk-AbC123-42", "", "AbC123", []string{"AbC123", "42"}},
		{"raw-whitespace-preserved", " AbC123 ", "", " AbC123 ", []string{" AbC123 "}},
		{"other-scheme-unchanged", "Basic AbC123", "", "Basic AbC123", []string{"Basic AbC123"}},
		{"tab-delimiter-unchanged", "Bearer\tAbC123", "", "Bearer\tAbC123", []string{"Bearer\tAbC123"}},
		{"no-delimiter-unchanged", "BearerAbC123", "", "BearerAbC123", []string{"BearerAbC123"}},
		{"fallback-mixed-case", "", "bEaReR sk-AbC123-42", "AbC123", []string{"AbC123", "42"}},
		{"sentinel-fallback", "midjourney-proxy", "bEaReR AbC123", "AbC123", []string{"AbC123"}},
		{"header-precedence", "bEaReR AbC123", "Bearer Other", "AbC123", []string{"AbC123"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			c.Request.Header.Set("Authorization", tc.header)
			c.Request.Header.Set("mj-api-secret", tc.fallback)
			key, parts := relayTokenCredential(c)
			require.Equal(t, tc.wantKey, key)
			require.Equal(t, tc.wantParts, parts)
			require.Equal(t, tc.header, c.GetHeader("Authorization"), "parsing must not rewrite the credential")
		})
	}
}

func TestTokenAuthReadOnlyBearerSchemeAndDenials(t *testing.T) {
	setupDashboardAuthMiddlewareTest(t)
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, model.DB.AutoMigrate(&model.Token{}))
	user := createMiddlewarePATUser(t, "readonly-bearer", "readonly-dashboard-pat")
	token := model.Token{UserId: user.Id, Key: "ReadOnlyCaseKey", Status: common.TokenStatusEnabled, ExpiredTime: -1}
	require.NoError(t, model.DB.Create(&token).Error)
	engine := gin.New()
	engine.GET("/read-only", TokenAuthReadOnly(), func(c *gin.Context) {
		require.Equal(t, user.Id, c.GetInt("id"))
		require.Equal(t, token.Id, c.GetInt("token_id"))
		c.Status(http.StatusNoContent)
	})
	request := func(header string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/read-only", nil)
		req.Header.Set("Authorization", header)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		return response
	}
	for _, prefix := range []string{"Bearer ", "bearer ", "BEARER ", "bEaReR ", ""} {
		t.Run(prefix, func(t *testing.T) {
			require.Equal(t, http.StatusNoContent, request(prefix+token.Key).Code)
		})
	}
	require.Equal(t, http.StatusUnauthorized, request("bEaReR readonlycasekey").Code, "key bytes remain case-sensitive")
	require.Equal(t, http.StatusUnauthorized, request("bEaReR unknownkey").Code)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", token.Id).Update("status", common.TokenStatusDisabled).Error)
	require.Equal(t, http.StatusUnauthorized, request("bEaReR "+token.Key).Code)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", token.Id).Update("status", common.TokenStatusEnabled).Error)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", user.Id).Update("status", common.UserStatusDisabled).Error)
	require.Equal(t, http.StatusForbidden, request("bEaReR "+token.Key).Code)
}
