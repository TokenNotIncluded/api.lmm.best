package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
)

func setMerchantStoreAnalyticsRoutes(parent *assistantRouterGroup) {
	public := parent.Group("/store")
	public.Use(middleware.DisableCache(), middleware.TryUserAuth())
	// Low-impact traffic uses the existing global API limit. CriticalRateLimit
	// is shared with checkout and must not be consumed by scrolling over cards.
	public.POST("/products/:id/analytics", middleware.RequestBodyLimit(1<<10), controller.RecordMerchantStoreTraffic)
	self := parent.Group("/store")
	self.Use(middleware.UserAuth(), middleware.DisableCache())
	self.GET("/my/analytics", controller.ListMyMerchantStoreAnalytics)
	admin := parent.Group("/store")
	admin.Use(middleware.AdminAuth(), middleware.DisableCache())
	admin.GET("/analytics", controller.ListAdminMerchantStoreAnalytics)
	admin.GET("/analytics/config", controller.GetMerchantStoreAnalyticsConfig)
	admin.PUT("/analytics/config", middleware.RootAuth(), middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.SaveMerchantStoreAnalyticsConfig)
}
