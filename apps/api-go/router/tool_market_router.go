package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
)

func setToolMarketRouter(parent *assistantRouterGroup) {
	public := parent.Group("/tool-market")
	public.Use(middleware.DisableCache(), middleware.TryUserAuth())
	public.GET("", controller.ListToolMarket)
	public.GET("/config", controller.GetToolMarketConfig)
	public.GET("/services/:id", controller.GetToolMarket)
	self := parent.Group("/tool-market")
	self.Use(middleware.UserAuth(), middleware.DisableCache(), middleware.RequestBodyLimit(256<<10))
	self.POST("/services", middleware.CriticalRateLimit(), controller.SaveToolMarketDraft)
	self.GET("/services/:id/draft", controller.GetToolMarketDraft)
	self.PUT("/services/:id/draft", middleware.CriticalRateLimit(), controller.SaveToolMarketDraft)
	self.POST("/services/:id/submit", middleware.CriticalRateLimit(), controller.SubmitToolMarketDraft)
	self.PUT("/services/:id/favorite", controller.SetToolMarketFavorite)
	self.PUT("/installations", controller.SetToolMarketInstallation)
	self.POST("/grants", middleware.CriticalRateLimit(), controller.CreateToolMarketGrant)
	self.DELETE("/grants/:id", controller.RevokeToolMarketGrant)
	self.PUT("/budgets", middleware.CriticalRateLimit(), controller.SetToolMarketBudget)
	self.GET("/calls", controller.ListToolMarketCalls)
	self.GET("/income", controller.ListToolMarketIncome)
	self.GET("/mine/:kind", controller.ListToolMarketAccountResources)
	self.POST("/inspect", middleware.CriticalRateLimit(), controller.InspectToolMarketRemote)
	self.POST("/services/:id/validate", middleware.CriticalRateLimit(), controller.ValidateToolMarketRemote)
	self.PUT("/services/:id/paused", middleware.CriticalRateLimit(), controller.SetToolMarketPaused)
	self.GET("/reviews", middleware.AdminAuth(), controller.ListToolMarketReviews)
	self.GET("/services/:id/review", middleware.AdminAuth(), controller.GetToolMarketReview)
	self.POST("/invoke", middleware.CriticalRateLimit(), controller.InvokeToolMarket)
	self.GET("/calls/:id/result", controller.GetToolMarketCallResult)
	self.POST("/tokens", middleware.CriticalRateLimit(), controller.CreateToolMarketToken)
	self.DELETE("/tokens/:id", controller.RevokeToolMarketToken)
	self.POST("/clients/disconnect", middleware.CriticalRateLimit(), controller.DisconnectToolMarketClient)
	self.POST("/services/:id/review", middleware.AdminAuth(), middleware.CriticalRateLimit(), controller.ReviewToolMarketDraft)
	self.PUT("/config", middleware.RootAuth(), middleware.CriticalRateLimit(), controller.SetToolMarketConfig)
	// Deliberately no reserve/start/settle endpoint. Only a trusted execution
	// adapter may attest a business result and trigger a wallet transfer.
}
