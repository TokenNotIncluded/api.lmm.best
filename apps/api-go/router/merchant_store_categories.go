package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
)

func setMerchantStoreCategoryRoutes(parent *assistantRouterGroup) {
	public := parent.Group("/store")
	public.Use(middleware.DisableCache())
	public.GET("/categories", controller.ListMerchantStoreCategories)

	self := parent.Group("/store")
	self.Use(middleware.UserAuth(), middleware.DisableCache())
	self.PUT("/products/:id/category", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.SetMerchantStoreProductCategory)

	admin := parent.Group("/store/admin")
	admin.Use(middleware.AdminAuth(), middleware.DisableCache())
	admin.GET("/categories", controller.ListAdminMerchantStoreCategories)
	admin.POST("/categories", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.SaveMerchantStoreCategory)
	admin.PUT("/categories/:id", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.SaveMerchantStoreCategory)
}
