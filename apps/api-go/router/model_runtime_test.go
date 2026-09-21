// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package router

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelRuntimeRespectsCatalogGateAndAdminWrites(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	old := common.OptionMap["HeaderNavModules"]
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMap["HeaderNavModules"] = `{"pricing":{"enabled":true,"requireAuth":true}}`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap["HeaderNavModules"] = old
		common.OptionMapRWMutex.Unlock()
	})
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	for _, path := range []string{"/api/pricing/runtime", "/api/models/"} {
		w := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"models":["private"],"operational_status":"maintenance"}`))
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, request)
		require.Equal(t, http.StatusNotFound, w.Code, path)

	}
}
