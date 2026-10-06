package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
)

// Store routes deliberately do not use ConsoleAccessGate. Public browsing and
// authenticated shopping/selling are available before API console activation.
func setMerchantStoreRouter(parent *assistantRouterGroup) {
	// The existing refresh cookie is scoped to /api/user/auth. Only these
	// token-specific pickup endpoints may use it without issuing an access token.
	claimSession := parent.Group("/user/auth/store-claim")
	claimSession.Use(middleware.DisableCache(), middleware.TryUserAuth())
	claimSession.GET("/:token", controller.InspectMerchantStoreSessionClaim)
	claimSession.POST("/:token", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.ClaimMerchantStoreSessionOrder)

	public := parent.Group("/store")
	public.Use(middleware.DisableCache(), middleware.TryUserAuth())
	public.GET("/products", controller.ListMerchantStore)
	public.GET("/products/:id", controller.GetPublicMerchantStoreProduct)
	public.GET("/config", controller.GetMerchantStoreConfig)
	public.GET("/disclaimer", controller.GetMerchantStoreDisclaimer)
	public.GET("/claim/:token", controller.InspectMerchantStoreClaim)
	public.POST("/claim/:token", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.ClaimMerchantStoreOrder)
	public.POST("/order-search/email/send", middleware.RequestBodyLimit(1<<10), middleware.EmailVerificationRateLimit(), middleware.CriticalRateLimit(), controller.SendMerchantStoreOrderSearchVerification)
	public.POST("/order-search/email/confirm", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.ConfirmMerchantStoreOrderSearchVerification)
	public.POST("/order-search", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.ListMerchantStoreOrdersByEmail)
	public.GET("/order-search/:trade_no", middleware.CriticalRateLimit(), controller.GetMerchantStoreOrderByNumber)

	self := parent.Group("/store")
	self.Use(middleware.UserAuth(), middleware.DisableCache())
	self.POST("/disclaimer/accept", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.AcceptMerchantStoreDisclaimer)
	self.GET("/email/status", controller.GetMerchantStoreEmailStatus)
	self.POST("/email/verification/send", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.SendMerchantStoreEmailVerification)
	self.POST("/email/verification/confirm", middleware.RequestBodyLimit(1<<10), middleware.CriticalRateLimit(), controller.ConfirmMerchantStoreEmailVerification)
	self.GET("/my/products", controller.ListMyMerchantStoreProducts)
	self.GET("/my/products/:id", controller.GetMerchantStoreProductDraft)
	self.GET("/my/products/:id/preview", controller.GetMerchantStoreProductPreview)
	self.GET("/products/:id/ai-reviews", controller.ListStoreAIReviews)
	self.POST("/products", middleware.RequestBodyLimit(512<<10), middleware.CriticalRateLimit(), controller.SaveMerchantStoreProduct)
	self.PUT("/products/:id", middleware.RequestBodyLimit(512<<10), middleware.CriticalRateLimit(), controller.SaveMerchantStoreProduct)
	self.POST("/products/:id/submit", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.SubmitMerchantStoreProduct)
	self.POST("/products/:id/unlist", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.UnlistMerchantStoreProduct)
	self.DELETE("/products/:id", middleware.CriticalRateLimit(), controller.DeleteMerchantStoreProduct)
	self.PUT("/products/:id/paused", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.SetMerchantStoreProductPaused)
	self.PUT("/products/:id/sale-limit", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.SetMerchantStoreProductSaleLimit)
	self.PUT("/products/:id/listing", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.SetMerchantStoreProductListed)
	self.GET("/products/:id/inventory", controller.ListMerchantStoreInventory)
	self.POST("/products/:id/inventory", middleware.RequestBodyLimit(2<<20), middleware.CriticalRateLimit(), controller.AddMerchantStoreInventory)
	self.DELETE("/products/:id/inventory/:stock_id", middleware.CriticalRateLimit(), controller.DeleteMerchantStoreInventory)
	self.POST("/products/:id/variants", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.CreateMerchantStoreVariant)
	self.PUT("/products/:id/variants/:variant_id", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.SaveMerchantStoreVariant)
	self.PUT("/products/:id/variants/:variant_id/enabled", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.SetMerchantStoreVariantEnabled)
	self.GET("/products/:id/variants/:variant_id/inventory", controller.ListMerchantStoreVariantInventory)
	self.POST("/products/:id/variants/:variant_id/inventory", middleware.RequestBodyLimit(2<<20), middleware.CriticalRateLimit(), controller.AddMerchantStoreVariantInventory)
	self.DELETE("/products/:id/variants/:variant_id/inventory/:stock_id", middleware.CriticalRateLimit(), controller.DeleteMerchantStoreVariantInventory)
	self.POST("/products/:id/promotion", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.PurchaseMerchantStorePromotion)
	self.GET("/my/orders", controller.ListMyMerchantStoreOrders)
	self.POST("/orders", middleware.RequestBodyLimit(8<<10), middleware.CriticalRateLimit(), controller.CreateMerchantStoreOrder)
	self.GET("/orders/:id", controller.GetMerchantStoreOrder)
	self.GET("/orders/:id/pickup-link", controller.GetMerchantStorePickupLink)
	self.POST("/orders/:id/cancel", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.CancelMerchantStoreOrder)
	self.POST("/orders/:id/pay", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.RequestMerchantStorePayment)
	self.POST("/orders/:id/reconcile", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.ReconcileMerchantStorePayment)
	self.GET("/payments/settings", controller.GetMerchantStorePaymentSettings)
	self.PUT("/payments/settings", middleware.RequestBodyLimit(64<<10), middleware.CriticalRateLimit(), controller.SaveMerchantStorePaymentSettings)
	self.PUT("/payments/categories", middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.SetMerchantStorePaymentCategories)

	admin := parent.Group("/store")
	admin.Use(middleware.AdminAuth(), middleware.DisableCache())
	admin.GET("/reviews", controller.ListMerchantStoreReviews)
	admin.POST("/products/:id/review", middleware.RequestBodyLimit(8<<10), middleware.CriticalRateLimit(), controller.ReviewMerchantStoreProduct)
	admin.PUT("/promotion-config", middleware.RootAuth(), middleware.RequestBodyLimit(4<<10), middleware.CriticalRateLimit(), controller.SetMerchantStorePromotionPrice)
	admin.PUT("/config", middleware.RootAuth(), middleware.RequestBodyLimit(8<<10), middleware.CriticalRateLimit(), controller.SetMerchantStoreConfig)

	// Dedicated, unauthenticated callbacks are separate from top-up callbacks.
	// Only the service adapter may attest a verified payment to the store model.
	callback := parent.Group("/store/payments")
	callback.Use(middleware.DisableCache())
	callback.GET("/epay/:id/notify", controller.MerchantStoreEpayNotify)
	callback.POST("/epay/:id/notify", middleware.RequestBodyLimit(64<<10), controller.MerchantStoreEpayNotify)
	callback.POST("/pancake/:scope/:seller_id/:env/webhook", middleware.RequestBodyLimit(256<<10), controller.MerchantStorePancakeWebhook)
}
