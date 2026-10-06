package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
)

// Called beside setToolMarketRouter by the central integration. Kept separate
// from client-record cleanup routes, which have a different file owner.
func setToolMarketMetaRouter(parent *assistantRouterGroup) {
	self := parent.Group("/tool-market/meta-delegations")
	self.Use(middleware.UserAuth(), middleware.DisableCache(), middleware.RequestBodyLimit(1024))
	self.GET("/:kind/:id", controller.GetToolMarketMetaDelegation)
	self.PUT("/:kind/:id", middleware.CriticalRateLimit(), controller.SetToolMarketMetaDelegation)
}
