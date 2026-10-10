package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

func SetWalletTransferRouter(router *gin.Engine) error {
	if err := model.EnsureWalletTransferSchemaAtStartup(); err != nil {
		return err
	}
	// Recipients may claim before paid console activation, just like redemption.
	group := router.Group("/api/wallet-transfer", middleware.RouteTag("api"), middleware.BodyStorageCleanup(), middleware.GlobalAPIRateLimit(), middleware.UserAuth(), middleware.DisableCache())
	group.GET("", controller.ListWalletTransfers)
	group.POST("", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.CreateWalletTransfer)
	group.POST("/inspect", middleware.RequestBodyLimit(4<<10), controller.InspectWalletTransfer)
	group.POST("/claim", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.ClaimWalletTransfer)
	group.POST("/:id/cancel", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.CancelWalletTransfer)
	return nil
}
