package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
)

func setMerchantStoreAccessPublicRoutes(group *assistantRouterGroup) {
	group.GET("/products/:id/terms", controller.GetMerchantStoreProductTerms)
	group.POST("/guest/orders", middleware.RequestBodyLimit(8<<10), middleware.CriticalRateLimit(), controller.CreateMerchantStoreGuestOrder)
	group.POST("/guest/session", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.CreateMerchantStoreGuestSession)
	group.POST("/guest/disclaimer/accept", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.AcceptMerchantStoreGuestDisclaimer)
	group.POST("/guest/orders/lookup", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.LookupMerchantStoreGuestOrder)
	group.GET("/guest/orders/:id", middleware.CriticalRateLimit(), controller.GetMerchantStoreGuestOrder)
	group.POST("/guest/orders/:id/pay", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.PayMerchantStoreGuestOrder)
	group.POST("/guest/orders/:id/cancel", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.CancelMerchantStoreGuestOrder)
	group.POST("/guest/orders/:id/reconcile", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.ReconcileMerchantStoreGuestOrder)
	group.GET("/guest/orders/:id/pickup-link", middleware.CriticalRateLimit(), controller.GetMerchantStoreGuestPickupLink)
}

func setMerchantStoreAccessSelfRoutes(group *assistantRouterGroup) {
	group.GET("/my/terms", controller.GetMyMerchantStoreTerms)
	group.PUT("/my/terms", middleware.RequestBodyLimit(128<<10), middleware.CriticalRateLimit(), controller.SaveMyMerchantStoreTerms)
	group.GET("/orders/by-request-key/:request_key", controller.LookupMyMerchantStoreOrder)
}
