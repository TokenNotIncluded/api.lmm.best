package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
)

func setCommerceImportRoutes(parent *assistantRouterGroup) {
	self := parent.Group("/store/commerce-import")
	self.Use(middleware.UserAuth(), middleware.DisableCache())
	self.GET("/config", controller.GetCommerceImportConfiguration)
	self.GET("/connections", controller.ListCommerceImportConnections)
	self.POST("/connections", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.CreateCommerceImportConnection)
	self.POST("/connections/:connection_id/authorize", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.AuthorizeCommerceImportConnection)
	self.GET("/connections/:connection_id/catalog", controller.GetCommerceImportCatalog)
	self.POST("/connections/:connection_id/import", middleware.RequestBodyLimit(64<<10), middleware.CriticalRateLimit(), controller.ImportCommerceImportProduct)
	self.POST("/connections/:connection_id/restock", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.RestockCommerceImportProduct)
	self.GET("/connections/:connection_id/requests", controller.ListCommerceImportRestockRequests)
	self.POST("/connections/:connection_id/requests/:request_id/recover", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.RecoverCommerceImportRestock)
	self.DELETE("/connections/:connection_id", middleware.CriticalRateLimit(), controller.DisconnectCommerceImportConnection)
	callback := parent.Group("/user/auth/store-commerce-import")
	callback.Use(middleware.DisableCache())
	callback.GET("/callback", middleware.CriticalRateLimit(), controller.CommerceImportCallback)
	callback.POST("/complete", middleware.RequestBodyLimit(12<<10), middleware.CriticalRateLimit(), controller.CompleteCommerceImportCallback)
}
