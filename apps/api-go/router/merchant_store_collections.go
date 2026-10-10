package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
)

func setMerchantStoreCollectionRoutes(parent *assistantRouterGroup) {
	self := parent.Group("/store")
	self.Use(middleware.UserAuth(), middleware.DisableCache())
	self.GET("/cart", controller.ListMerchantStoreCart)
	self.PUT("/cart", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.SetMerchantStoreCartItem)
	self.DELETE("/cart", middleware.CriticalRateLimit(), controller.ClearMerchantStoreCart)
	self.DELETE("/cart/:item_id", middleware.CriticalRateLimit(), controller.DeleteMerchantStoreCartItem)
	self.GET("/favorites", controller.ListMerchantStoreFavorites)
	self.PUT("/favorites", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.SetMerchantStoreFavorite)
	self.DELETE("/favorites", middleware.CriticalRateLimit(), controller.ClearMerchantStoreFavorites)
	self.DELETE("/favorites/:product_id", middleware.CriticalRateLimit(), controller.DeleteMerchantStoreFavorite)
	self.POST("/collections/cleanup", middleware.CriticalRateLimit(), controller.CleanupMerchantStoreCollections)
	self.PUT("/products/:id/catalogue", middleware.RequestBodyLimit(64<<10), middleware.CriticalRateLimit(), controller.SetMerchantStoreCatalogueMetadata)
}
