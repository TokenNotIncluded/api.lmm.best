package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/service/authz"
)

func setAcquisitionRouter(parent *assistantRouterGroup) {
	public := parent.Group("/acquisition")
	public.Use(middleware.DisableCache(), middleware.RequestBodyLimit(4096), middleware.TryUserAuth())
	public.POST("/visit", controller.ObserveAcquisition)
	public.DELETE("/consent", controller.RevokeAcquisition)
	public.POST("/consent", middleware.UserAuth(), controller.GrantAcquisitionConsent)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		public.Handle(method, "/self-report", middleware.UserAuth(), controller.AcquisitionSelfReport)
	}
	admin := parent.Group("/admin/acquisition")
	admin.Use(middleware.AdminAuth(), middleware.DisableCache(), middleware.RequestBodyLimit(4096))
	admin.GET("/links/:id/preview", middleware.RequirePermission(authz.AcquisitionRead), controller.PreviewAcquisitionLink)
	admin.GET("/cost", middleware.RequirePermission(authz.AcquisitionRead), controller.GetAcquisitionCost)
	admin.PUT("/cost", middleware.RequirePermission(authz.AcquisitionWrite), controller.SaveAcquisitionCost)
	admin.GET("/links", middleware.RequirePermission(authz.AcquisitionRead), controller.ListAcquisitionLinks)
	admin.POST("/links", middleware.RequirePermission(authz.AcquisitionWrite), controller.SaveAcquisitionLink)
	admin.DELETE("/links/:id", middleware.RequirePermission(authz.AcquisitionWrite), controller.DeleteAcquisitionLink)
	admin.POST("/activity/rebuild", middleware.RequirePermission(authz.AcquisitionWrite), controller.RebuildAcquisitionActivity)
	admin.GET("/users/export", middleware.RequirePermission(authz.AcquisitionDetails), middleware.RequirePermission(authz.AcquisitionExport), controller.ExportAcquisitionUsers)
	admin.GET("/users/:id/corrections", middleware.RequirePermission(authz.AcquisitionDetails), controller.GetAcquisitionCorrections)
	admin.POST("/users/:id/corrections", middleware.RequirePermission(authz.AcquisitionDetails), middleware.RequirePermission(authz.AcquisitionWrite), controller.SaveAcquisitionCorrection)
	admin.GET("/users", middleware.RequirePermission(authz.AcquisitionDetails), controller.ListAcquisitionUsers)
	admin.GET("/users/:id", middleware.RequirePermission(authz.AcquisitionDetails), controller.GetAcquisitionUserDetail)
	admin.PUT("/lookback", middleware.RequirePermission(authz.AcquisitionWrite), controller.SetAcquisitionLookback)
	admin.GET("/visitors", middleware.RequirePermission(authz.AcquisitionRead), controller.AcquisitionVisitors)
	admin.GET("/funnel", middleware.RequirePermission(authz.AcquisitionRead), controller.AcquisitionFunnel)
	admin.GET("/report", middleware.RequirePermission(authz.AcquisitionRead), controller.AcquisitionReport)
}
