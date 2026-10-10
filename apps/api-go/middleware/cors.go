package middleware

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func CORS() gin.HandlerFunc {
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowCredentials = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	// Authorization is not covered by the CORS request-header wildcard.
	// Keep explicit Bearer clients working without reflecting credentialed origins.
	config.AllowHeaders = []string{"*", "Authorization"}
	return cors.New(config)
}

func Version() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-New-Api-Version", common.Version)
		c.Next()
	}
}
