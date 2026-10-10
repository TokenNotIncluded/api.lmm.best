// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package router

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreSupportRateLimitScopesAndIdentity(t *testing.T) {
	redisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = redisEnabled })
	router := gin.New()
	router.Use(func(c *gin.Context) {
		id, _ := strconv.Atoi(c.GetHeader("X-Test-User"))
		c.Set("id", id)
	})
	for _, scope := range []string{"create", "message", "receipt", "manage"} {
		router.POST("/"+scope, middleware.MerchantStoreSupportRateLimit(scope), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}
	request := func(scope string, user int, address string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/"+scope, nil)
		req.Header.Set("X-Test-User", strconv.Itoa(user))
		req.RemoteAddr = address
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	for i := 0; i < 60; i++ {
		require.Equal(t, http.StatusNoContent, request("message", 719301, "192.0.2.1:1000").Code)
	}
	limited := request("message", 719301, "192.0.2.2:1000")
	require.Equal(t, http.StatusTooManyRequests, limited.Code, "changing IP must not reset an account limit")
	require.NotEmpty(t, limited.Header().Get("Retry-After"))
	require.Equal(t, http.StatusNoContent, request("receipt", 719301, "192.0.2.1:1000").Code)
	require.Equal(t, http.StatusNoContent, request("manage", 719301, "192.0.2.1:1000").Code)
	require.Equal(t, http.StatusNoContent, request("create", 719301, "192.0.2.1:1000").Code)
	require.Equal(t, http.StatusNoContent, request("message", 719302, "192.0.2.1:1000").Code, "another account on the same network has its own limit")
	require.Equal(t, http.StatusUnauthorized, request("message", 0, "192.0.2.1:1000").Code)
}
