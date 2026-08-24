// Command route-manifest renders the authoritative Gin route table in a
// stable format for Rust strangler-route drift checks.
package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/LIghtJUNction/api.lmm.best/router"
	"github.com/gin-gonic/gin"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	router.SetApiRouter(engine)
	router.SetOpenSourceBountyMCPRouter(engine)
	router.SetDrawingMCPRouter(engine)
	router.SetDashboardRouter(engine)
	router.SetRelayRouter(engine)
	router.SetVideoRouter(engine)

	routes := engine.Routes()
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path == routes[j].Path {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Path < routes[j].Path
	})
	for _, route := range routes {
		if _, err := fmt.Fprintf(os.Stdout, "%s\t%s\t%s\n", route.Method, route.Path, route.Handler); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "write route manifest: %v\n", err)
			os.Exit(1)
		}
	}
}
