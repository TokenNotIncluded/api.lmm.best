package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/gin-gonic/gin"
)

func setWaffoPancakePortalRouter(router *gin.Engine) {
	// Register outside the assistant operation registry: its response is a
	// bearer login, not tool output. Do not gate subscription management on
	// console activation or the ability to make another purchase.
	router.POST("/api/user/self/waffo-pancake/portal",
		middleware.RouteTag("api"),
		middleware.DisableCache(),
		middleware.GlobalAPIRateLimit(),
		middleware.RequestBodyLimit(1024),
		middleware.SessionCookieOriginGuard(),
		middleware.UserAuth(),
		middleware.CriticalRateLimit(),
		controller.RequestWaffoPancakeCustomerPortal,
	)
}
