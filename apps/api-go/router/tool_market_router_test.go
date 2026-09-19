package router

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func toolMarketTestRouter(t *testing.T) (*gin.Engine, *gorm.DB, string, model.User) {
	t.Helper()
	oldDB, oldRedis := model.DB, common.RedisEnabled
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, oldLog)
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.ToolMarketService{}, &model.ToolMarketVersion{}, &model.ToolMarketTool{}, &model.ToolMarketToolVersion{}, &model.ToolMarketAccess{}, &model.ToolMarketFavorite{}, &model.ToolMarketInstallation{}, &model.ToolMarketGrant{}, &model.ToolMarketBudget{}, &model.ToolMarketCall{}, &model.ToolMarketTransfer{}, &model.ToolMarketEvent{}, &model.ToolMarketConfig{}))
	token := "market-router-token"
	user := model.User{Username: "market-user", AffCode: "market-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AccessToken: &token}
	require.NoError(t, db.Create(&user).Error)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	setToolMarketRouter(&assistantRouterGroup{group: engine.Group("/api")})
	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		common.SetDatabaseTypes(oldMain, oldLog)
		pool, _ := db.DB()
		_ = pool.Close()
	})
	return engine, db, token, user
}

func toolMarketHTTPRequest(router *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func TestToolMarketRoutesKeepPublicCatalogAndPrivateOperationsSeparate(t *testing.T) {
	router, db, token, user := toolMarketTestRouter(t)
	response := toolMarketHTTPRequest(router, "GET", "/api/tool-market", "", "")
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"data":[]`)
	response = toolMarketHTTPRequest(router, "GET", "/api/tool-market/calls", "", "")
	require.NotEqual(t, 200, response.Code)
	input := `{"name":"Private tool","execution_type":"remote","visibility":"private","endpoint":"https://example.com/mcp","owner_id":999,"validation_digest":"forged","tools":[{"name":"read","input_schema":{"type":"object"},"price_quota":10}]}`
	response = toolMarketHTTPRequest(router, "POST", "/api/tool-market/services", token, input)
	require.Equal(t, 200, response.Code, response.Body.String())
	var body struct {
		Data model.ToolMarketService `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, user.Id, body.Data.OwnerID)
	var version model.ToolMarketVersion
	require.NoError(t, db.First(&version, "id = ?", body.Data.DraftVersionID).Error)
	require.Empty(t, version.ValidationDigest)
	response = toolMarketHTTPRequest(router, "GET", "/api/tool-market/services/"+body.Data.ID, "", "")
	require.Equal(t, 404, response.Code)
	response = toolMarketHTTPRequest(router, "GET", "/api/tool-market/services/"+body.Data.ID+"/draft", token, "")
	require.Equal(t, 200, response.Code)
	response = toolMarketHTTPRequest(router, "POST", "/api/tool-market/services/"+body.Data.ID+"/review", token, `{"approve":true}`)
	require.NotEqual(t, 200, response.Code)
	response = toolMarketHTTPRequest(router, "PUT", "/api/tool-market/config", token, `{"enabled":true,"fee_bps":0,"recipient_id":1}`)
	require.NotEqual(t, 200, response.Code)
	for _, path := range []string{"/api/tool-market/calls/abc/settle", "/api/tool-market/calls/abc/start", "/api/tool-market/calls/abc/success", "/api/tool-market/withdraw"} {
		response = toolMarketHTTPRequest(router, "POST", path, token, `{"success":true}`)
		require.Equal(t, 404, response.Code, path)
	}
}

func TestToolMarketRoutesDoNotLeakOtherAccountsOrInputDigests(t *testing.T) {
	router, db, token, user := toolMarketTestRouter(t)
	require.NoError(t, db.Create(&model.ToolMarketCall{ID: "owned", UserID: user.Id, InputDigest: "must-not-leak"}).Error)
	require.NoError(t, db.Create(&model.ToolMarketCall{ID: "other-call", UserID: user.Id + 1}).Error)
	require.NoError(t, db.Create(&model.ToolMarketTransfer{ID: "income", CallID: "owned", FromUserID: 999, ToUserID: user.Id, Quota: 20}).Error)
	require.NoError(t, db.Create(&model.ToolMarketTransfer{ID: "other-income", CallID: "other-call", FromUserID: 999, ToUserID: user.Id + 1, Quota: 20}).Error)
	response := toolMarketHTTPRequest(router, "GET", "/api/tool-market/calls?user_id=999", token, "")
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), "owned")
	require.NotContains(t, response.Body.String(), "other-call")
	require.NotContains(t, response.Body.String(), "must-not-leak")
	response = toolMarketHTTPRequest(router, "GET", "/api/tool-market/income", token, "")
	require.Equal(t, 200, response.Code)
	require.NotContains(t, response.Body.String(), "other-income")
	require.NotContains(t, response.Body.String(), "from_user_id")
	response = toolMarketHTTPRequest(router, "GET", "/api/tool-market?limit=100000", token, "")
	require.Equal(t, 422, response.Code)
	response = toolMarketHTTPRequest(router, "POST", "/api/tool-market/services", token, strings.Repeat(" ", 257<<10)+"{}")
	require.NotEqual(t, 200, response.Code)
}
