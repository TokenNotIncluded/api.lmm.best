package router

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"

	"github.com/gin-gonic/gin"
)

func SetRouter(router *gin.Engine) error {
	// These loopback-only routes are used by the package-managed Nginx edge
	// policy. They intentionally sit outside the /api group so a policy check
	// does not consume API rate-limit budget or the console discovery gate.
	router.GET("/internal/access-ip-policy", middleware.DisableCache(), controller.CheckIPAccessRoutingPolicy)
	router.GET("/internal/errors/access-policy", controller.GetAccessPolicyErrorPage)

	SetApiRouter(router)
	SetOpenSourceBountyMCPRouter(router)
	SetDrawingMCPRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	SetVideoRouter(router)

	frontendBaseUrl := os.Getenv("FRONTEND_BASE_URL")
	if common.IsMasterNode && frontendBaseUrl != "" {
		frontendBaseUrl = ""
		common.SysLog("FRONTEND_BASE_URL is ignored on master node")
	}
	if frontendBaseUrl != "" {
		frontendBaseUrl = strings.TrimSuffix(frontendBaseUrl, "/")
	}
	frontend, err := newFrontendHandler(os.Getenv("LMM_API_FRONTEND_DIR"))
	if err != nil {
		return fmt.Errorf("configure packaged frontend: %w", err)
	}
	if frontend != nil && frontendBaseUrl != "" {
		return fmt.Errorf("LMM_API_FRONTEND_DIR and FRONTEND_BASE_URL are mutually exclusive")
	}

	router.NoRoute(func(c *gin.Context) {
		c.Set(middleware.RouteTagKey, "web")
		if frontend != nil && isFrontendAssetPath(c.Request.URL.Path) {
			frontend.ServeHTTP(c.Writer, c.Request)
			return
		}
		if isBackendPath(c.Request.RequestURI) {
			controller.RelayNotFound(c)
			return
		}
		if frontend != nil {
			frontend.ServeHTTP(c.Writer, c.Request)
			return
		}
		if frontendBaseUrl == "" {
			controller.RelayNotFound(c)
			return
		}
		c.Redirect(http.StatusMovedPermanently, fmt.Sprintf("%s%s", frontendBaseUrl, c.Request.RequestURI))
	})
	return nil
}

type frontendHandler struct {
	root       string
	indexPath  string
	fileServer http.Handler
}

func newFrontendHandler(configuredRoot string) (http.Handler, error) {
	configuredRoot = strings.TrimSpace(configuredRoot)
	if configuredRoot == "" {
		return nil, nil
	}
	if !filepath.IsAbs(configuredRoot) {
		return nil, fmt.Errorf("LMM_API_FRONTEND_DIR must be an absolute path")
	}
	cleanRoot := filepath.Clean(configuredRoot)
	evaluatedRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve frontend directory: %w", err)
	}
	if evaluatedRoot != cleanRoot {
		frontendRoot := filepath.Dir(cleanRoot)
		releasesRoot := filepath.Join(frontendRoot, "releases")
		evaluatedFrontendRoot, rootErr := filepath.EvalSymlinks(frontendRoot)
		if rootErr != nil || evaluatedFrontendRoot != frontendRoot || filepath.Base(cleanRoot) != "current" || filepath.Dir(evaluatedRoot) != releasesRoot {
			return nil, fmt.Errorf("LMM_API_FRONTEND_DIR symlink must be an atomic current link into its sibling releases directory")
		}
		cleanRoot = evaluatedRoot
	}
	if err := filepath.WalkDir(cleanRoot, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("frontend directory contains a symlink: %s", entry.Name())
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return fmt.Errorf("frontend directory contains an unsupported file: %s", entry.Name())
		}
		return nil
	}); err != nil {
		return nil, err
	}
	indexPath := filepath.Join(cleanRoot, "index.html")
	indexInfo, err := os.Stat(indexPath)
	if err != nil {
		return nil, fmt.Errorf("frontend index is unavailable: %w", err)
	}
	if !indexInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("frontend index is not a regular file")
	}
	return &frontendHandler{
		root:       cleanRoot,
		indexPath:  indexPath,
		fileServer: http.FileServer(http.Dir(cleanRoot)),
	}, nil
}

func (handler *frontendHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requestPath := path.Clean("/" + request.URL.Path)
	if requestPath != "/" {
		relativeRequestPath := filepath.FromSlash(strings.TrimPrefix(requestPath, "/"))
		if filepath.IsLocal(relativeRequestPath) {
			candidate := filepath.Join(handler.root, relativeRequestPath)
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				handler.fileServer.ServeHTTP(writer, request)
				return
			}
		}
	}
	if isFrontendAssetPath(requestPath) {
		http.NotFound(writer, request)
		return
	}
	http.ServeFile(writer, request, handler.indexPath)
}

func isFrontendAssetPath(requestPath string) bool {
	for _, prefix := range []string{"/assets", "/static"} {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	return false
}

func isBackendPath(requestURI string) bool {
	path := requestURI
	if queryStart := strings.IndexByte(path, '?'); queryStart >= 0 {
		path = path[:queryStart]
	}

	for _, prefix := range []string{
		"/api/",
		"/assets/",
		"/mcp/",
		"/v1/",
		"/v1beta/",
		"/pg/",
		"/mj/",
		"/suno/",
		"/kling/v1/",
		"/jimeng/",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}

	switch path {
	case "/api", "/assets", "/mcp", "/v1", "/v1beta", "/pg", "/mj", "/suno", "/kling/v1", "/jimeng", "/dashboard/billing/subscription", "/dashboard/billing/usage":
		return true
	}

	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	return len(segments) >= 2 && segments[0] != "" && segments[1] == "mj"
}
