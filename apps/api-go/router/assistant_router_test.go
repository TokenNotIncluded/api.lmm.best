package router

import (
	"net/http"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantRouterTracksInheritedAndInlineRoles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	registry := controller.NewAssistantAdminOperationRegistry(engine)
	api := &assistantRouterGroup{group: engine.Group("/api"), operations: registry}
	admin := api.Group("/admin", middleware.AdminAuth())
	assert.Equal(t, common.RoleAdminUser, admin.minRole)
	root := admin.Group("/root")
	root.Use(middleware.RootAuth())
	assert.Equal(t, common.RoleRootUser, root.minRole)
	assert.Equal(t, common.RoleAdminUser, admin.minRole)
	assert.Zero(t, api.minRole)
	assert.Equal(t, common.RoleRootUser, assistantRouteRole(admin.minRole, []gin.HandlerFunc{middleware.RootAuth()}))
	handler := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	admin.GET("/", handler)
	root.PATCH("/:id", handler)
	routes := engine.Routes()
	require.Len(t, routes, 2)
	assert.Equal(t, "/api/admin/", routes[0].Path)
	assert.Equal(t, "/api/admin/root/:id", routes[1].Path)
}

func TestAssistantRouterRegistersCompleteDashboardWithoutConflicts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	require.NotPanics(t, func() { SetApiRouter(engine) })
	assert.NotEmpty(t, engine.Routes())
}
