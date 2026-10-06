package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMarketAIReviewRouteRolesStayPrivateAndRootWritable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var chain []string
	engine.Use(func(c *gin.Context) { chain = c.HandlerNames(); c.AbortWithStatus(http.StatusNoContent) })
	SetApiRouter(engine)
	for _, tc := range []struct{ method, path, auth string }{
		{"GET", "/api/security/market-ai-review/settings", "AdminAuth"},
		{"PUT", "/api/security/market-ai-review/settings", "RootAuth"},
		{"GET", "/api/tool-market/services/test/ai-reviews", "UserAuth"},
		{"GET", "/api/store/products/test/ai-reviews", "UserAuth"},
	} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		require.Equal(t, http.StatusNoContent, w.Code, tc.path)
		require.Contains(t, strings.Join(chain, "\n"), tc.auth, tc.path)
		if tc.method == "PUT" {
			require.Contains(t, strings.Join(chain, "\n"), "RequestBodyLimit")
		}
	}
}

func TestMarketAIReviewStoreResultsRequireCurrentOwnerOrAdministrator(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, _ := merchantStoreTestRouter(t)
	require.NoError(t, db.Create(&model.Option{Key: setting.StoreAIReviewModeOptionKey, Value: setting.MarketAIReviewAssist}).Error)
	p, err := model.SaveMerchantStoreProduct(seller.Id, "", model.MerchantStoreProductInput{Title: "Public listing", Description: "Public copy", PriceQuota: 100, PaymentMethods: []string{"balance"}})
	require.NoError(t, err)
	require.NoError(t, model.SubmitMerchantStoreProduct(seller.Id, p.ID))
	path := "/api/store/products/" + p.ID + "/ai-reviews"
	response := shopRequest(engine, "GET", path, "", "")
	require.NotEqual(t, 200, response.Code)
	buyerToken := "market-review-buyer"
	buyer := model.User{Username: "market-review-buyer", AffCode: "market-review-buyer", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AccessToken: &buyerToken}
	require.NoError(t, db.Create(&buyer).Error)
	response = shopRequest(engine, "GET", path, buyerToken, "")
	require.Equal(t, 403, response.Code, response.Body.String())
	for _, token := range []string{sellerToken, rootToken} {
		response = shopRequest(engine, "GET", path, token, "")
		require.Equal(t, 200, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), `"status":"pending"`)
		require.Contains(t, response.Body.String(), `"recommendation":null`)
		require.NotContains(t, response.Body.String(), "payload")
		require.NotContains(t, response.Body.String(), "provider_calls")
	}
	// A previously authenticated seller cannot keep reading after being disabled.
	require.NoError(t, db.Model(&seller).Update("status", common.UserStatusDisabled).Error)
	response = shopRequest(engine, "GET", path, sellerToken, "")
	require.NotEqual(t, 200, response.Code)
}
