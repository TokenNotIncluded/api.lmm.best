package router

import (
	"net/http"
	"path"
	"reflect"
	"runtime"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/gin-gonic/gin"
)

// assistantRouterGroup records the same routes that Gin registers; there is no
// second endpoint allowlist to drift from the dashboard's authorization chain.
// The registry is owned by one engine, never shared between servers or tests.
type assistantRouterGroup struct {
	group      *gin.RouterGroup
	operations *controller.AssistantAdminOperationRegistry
	minRole    int
}

func (g *assistantRouterGroup) Group(relativePath string, handlers ...gin.HandlerFunc) *assistantRouterGroup {
	return &assistantRouterGroup{group: g.group.Group(relativePath, handlers...), operations: g.operations, minRole: assistantRouteRole(g.minRole, handlers)}
}

func (g *assistantRouterGroup) Use(handlers ...gin.HandlerFunc) {
	g.minRole = assistantRouteRole(g.minRole, handlers)
	g.group.Use(handlers...)
}

func assistantRouteRole(role int, handlers []gin.HandlerFunc) int {
	for _, handler := range handlers {
		if required := middleware.RequiredDashboardRole(handler); required > role {
			role = required
		}
	}
	return role
}

func (g *assistantRouterGroup) Handle(method, relativePath string, handlers ...gin.HandlerFunc) {
	g.group.Handle(method, relativePath, handlers...)
	if g.operations == nil || len(handlers) == 0 {
		return
	}
	fullPath := g.group.BasePath()
	if relativePath != "" {
		fullPath = path.Join(fullPath, relativePath)
		if strings.HasSuffix(relativePath, "/") && !strings.HasSuffix(fullPath, "/") {
			fullPath += "/"
		}
	}
	handler := runtime.FuncForPC(reflect.ValueOf(handlers[len(handlers)-1]).Pointer()).Name()
	handler = handler[strings.LastIndex(handler, ".")+1:]
	g.operations.Register(method, fullPath, handler, assistantRouteRole(g.minRole, handlers))
}

func (g *assistantRouterGroup) GET(path string, handlers ...gin.HandlerFunc) {
	g.Handle(http.MethodGet, path, handlers...)
}
func (g *assistantRouterGroup) POST(path string, handlers ...gin.HandlerFunc) {
	g.Handle(http.MethodPost, path, handlers...)
}
func (g *assistantRouterGroup) PUT(path string, handlers ...gin.HandlerFunc) {
	g.Handle(http.MethodPut, path, handlers...)
}
func (g *assistantRouterGroup) PATCH(path string, handlers ...gin.HandlerFunc) {
	g.Handle(http.MethodPatch, path, handlers...)
}
func (g *assistantRouterGroup) DELETE(path string, handlers ...gin.HandlerFunc) {
	g.Handle(http.MethodDelete, path, handlers...)
}
func (g *assistantRouterGroup) HEAD(path string, handlers ...gin.HandlerFunc) {
	g.Handle(http.MethodHead, path, handlers...)
}
func (g *assistantRouterGroup) OPTIONS(path string, handlers ...gin.HandlerFunc) {
	g.Handle(http.MethodOptions, path, handlers...)
}
