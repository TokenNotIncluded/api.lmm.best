package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQuotaQueryRateLimitSharesAccountBudget(t *testing.T) {
	old := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = old })
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", 8826251) })
	limiter := QuotaQueryRateLimit()
	r.GET("/v1/usage", limiter, func(c *gin.Context) { c.Status(200) })
	r.GET("/v1/pricing", limiter, func(c *gin.Context) { c.Status(200) })
	for i := 0; i < 31; i++ {
		path := "/v1/usage"
		if i%2 == 0 {
			path = "/v1/pricing"
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if i < 30 {
			require.Equal(t, 200, w.Code)
		} else {
			require.Equal(t, 429, w.Code)
			require.NotEmpty(t, w.Header().Get("Retry-After"))
		}
	}
}
