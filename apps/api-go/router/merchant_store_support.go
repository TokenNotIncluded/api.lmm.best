// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
)

func setMerchantStoreSupportRoutes(parent *assistantRouterGroup) {
	self := parent.Group("/store")
	self.Use(middleware.UserAuth(), middleware.DisableCache())
	self.GET("/support/conversations", controller.ListMerchantStoreSupport)
	self.POST("/support/conversations", middleware.RequestBodyLimit(4<<10), middleware.MerchantStoreSupportRateLimit("create"), controller.OpenMerchantStoreSupport)
	self.GET("/support/conversations/:id/messages", controller.GetMerchantStoreSupportHistory)
	self.POST("/support/conversations/:id/messages", middleware.RequestBodyLimit(32<<10), middleware.MerchantStoreSupportRateLimit("message"), controller.SendMerchantStoreSupportMessage)
	self.PUT("/support/conversations/:id/read", middleware.RequestBodyLimit(1<<10), middleware.MerchantStoreSupportRateLimit("receipt"), controller.MarkMerchantStoreSupportRead)
	self.PUT("/support/conversations/:id/status", middleware.RequestBodyLimit(1<<10), middleware.MerchantStoreSupportRateLimit("manage"), controller.SetMerchantStoreSupportStatus)
	self.GET("/support/conversations/:id/assistant-context", controller.GetMerchantStoreSupportAssistantContext)
	self.GET("/my/customers", controller.ListMerchantStoreCustomers)
	self.PUT("/my/customers/:buyer_id", middleware.RequestBodyLimit(32<<10), middleware.MerchantStoreSupportRateLimit("manage"), controller.SaveMerchantStoreCustomer)
}
