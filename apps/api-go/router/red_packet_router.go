package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

const redPacketMutationRequestMaxBytes = 4 << 20

// SetRedPacketRouter keeps share-link discovery outside ConsoleAccessGate so a
// recipient can open a packet before signing in. Claim and management paths
// still enforce the normal user/admin authorization boundaries.
func SetRedPacketRouter(router *gin.Engine) error {
	if err := model.EnsureRedPacketSchemaAtStartup(); err != nil {
		return err
	}

	group := router.Group("/api/red-packet")
	group.Use(middleware.RouteTag("api"))
	group.Use(gzip.Gzip(gzip.DefaultCompression))
	group.Use(middleware.BodyStorageCleanup())
	group.Use(middleware.GlobalAPIRateLimit())

	group.GET("/:slug", middleware.DisableCache(), controller.GetRedPacket)
	group.GET("/:slug/claims/me", middleware.UserAuth(), middleware.DisableCache(), controller.GetMyRedPacketClaims)
	group.POST("/:slug/claim", middleware.RequestBodyLimit(4<<10), middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.DisableCache(), controller.ClaimRedPacket)

	admin := group.Group("/admin")
	admin.Use(middleware.AdminAuth(), middleware.DisableCache())
	{
		admin.GET("", controller.AdminListRedPackets)
		admin.POST("", middleware.RequestBodyLimit(redPacketMutationRequestMaxBytes), middleware.CriticalRateLimit(), controller.AdminCreateRedPacket)
		admin.PUT("/:id", middleware.RequestBodyLimit(redPacketMutationRequestMaxBytes), middleware.CriticalRateLimit(), controller.AdminUpdateRedPacket)
		admin.DELETE("/:id", middleware.CriticalRateLimit(), controller.AdminDeleteRedPacket)
	}

	// Detailed redemption response for new web clients. The existing
	// /api/user/topup endpoint remains unchanged for compatibility.
	user := router.Group("/api/user")
	user.Use(middleware.RouteTag("api"), middleware.BodyStorageCleanup(), middleware.GlobalAPIRateLimit())
	user.POST("/redemption", middleware.RequestBodyLimit(4<<10), middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.DisableCache(), controller.RedeemCodeV2)
	return nil
}
