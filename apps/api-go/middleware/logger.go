package middleware

import (
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
)

func loggedRequestPath(param gin.LogFormatterParams) string {
	if param.Request != nil && param.Request.URL != nil &&
		(strings.HasPrefix(param.Request.URL.Path, "/api/oauth2/") ||
			strings.HasPrefix(param.Request.URL.Path, "/api/user/auth/oauth2/") ||
			strings.HasPrefix(param.Request.URL.Path, "/oauth/")) {
		return param.Request.URL.Path
	}
	return param.Path
}

const RouteTagKey = "route_tag"

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	server.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		var requestID string
		if param.Keys != nil {
			requestID, _ = param.Keys[common.RequestIdKey].(string)
		}
		tag, _ := param.Keys[RouteTagKey].(string)
		if tag == "" {
			tag = "web"
		}
		return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			tag,
			requestID,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.Method,
			loggedRequestPath(param),
		)
	}))
}
