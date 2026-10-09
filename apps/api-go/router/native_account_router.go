package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

// This identity-management pilot is deliberately outside ConsoleAccessGate:
// natural personal L0 users can manage invitations without acquiring personal
// API access. It does not register a team billing or API-key creation surface.
func SetNativeAccountRouter(router *gin.Engine) error {
	enabled, err := model.NativeAccountsEnabledFromEnv()
	if err != nil || !enabled {
		return err
	}
	if err := model.EnsureNativeAccountSchemaAtStartup(model.DB); err != nil {
		return err
	}
	group := router.Group("/api/accounts", middleware.RouteTag("api"), middleware.BodyStorageCleanup(), middleware.GlobalAPIRateLimit(), middleware.RequestBodyLimit(4<<10), middleware.SessionCookieOriginGuard(), middleware.UserAuth(), middleware.DisableCache())
	group.GET("", controller.ListNativeAccounts)
	group.POST("/teams", middleware.CriticalRateLimit(), controller.CreateNativeTeam)
	group.GET("/teams/:team_id", controller.GetNativeTeam)
	group.GET("/teams/:team_id/members", controller.ListNativeTeamMembers)
	group.POST("/teams/:team_id/invitations", middleware.CriticalRateLimit(), controller.InviteNativeTeamMember)
	group.GET("/invitations", controller.ListNativeTeamInvitations)
	group.POST("/invitations/:invitation_id/accept", middleware.CriticalRateLimit(), controller.AcceptNativeTeamInvitation)
	group.PATCH("/teams/:team_id/members/:user_id", middleware.CriticalRateLimit(), controller.UpdateNativeTeamMember)
	group.DELETE("/teams/:team_id/members/:user_id", middleware.CriticalRateLimit(), controller.RemoveNativeTeamMember)
	group.DELETE("/teams/:team_id/invitations/:invitation_id", middleware.CriticalRateLimit(), controller.RevokeNativeTeamInvitation)
	return nil
}
