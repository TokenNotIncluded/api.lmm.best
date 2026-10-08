package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
)

func setMerchantStoreSocialRoutes(parent *assistantRouterGroup) {
	public := parent.Group("/store")
	public.Use(middleware.DisableCache(), middleware.TryUserAuth())
	public.GET("/products/:id/likes", controller.GetMerchantStoreProductLikes)

	self := parent.Group("/store")
	self.Use(middleware.UserAuth(), middleware.DisableCache())
	self.PUT("/products/:id/likes", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.LikeMerchantStoreProduct)
	self.DELETE("/products/:id/likes", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.UnlikeMerchantStoreProduct)
}
