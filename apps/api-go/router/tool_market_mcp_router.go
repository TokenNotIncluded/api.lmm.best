package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/gin-gonic/gin"
)

func SetToolMarketMCPRouter(router *gin.Engine) {
	route := router.Group("/mcp/market", middleware.RouteTag("mcp"), middleware.GlobalAPIRateLimit())
	handler := gin.WrapH(controller.NewToolMarketMCPHandler())
	route.Any("", handler)
	route.Any("/", handler)
}
