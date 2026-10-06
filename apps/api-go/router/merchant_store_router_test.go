package router

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func merchantStoreTestRouter(t *testing.T) (*gin.Engine, *gorm.DB, string, model.User, string, model.User) {
	t.Helper()
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "store-router-fixture-encryption-key-20261006-123456789")
	engine, db, sellerToken, seller := toolMarketTestRouter(t)
	require.NoError(t, db.AutoMigrate(model.MerchantStoreModels()...))
	previousLog := model.LOG_DB
	model.LOG_DB = db
	t.Cleanup(func() { model.LOG_DB = previousLog })
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	rootToken := "shop-router-root"
	root := model.User{Username: "shop-root", AffCode: "shop-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AccessToken: &rootToken, Quota: 1000000}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Model(&seller).UpdateColumn("quota", 1000000).Error)
	seller.Quota = 1000000
	setMerchantStoreRouter(&assistantRouterGroup{group: engine.Group("/api")})
	return engine, db, sellerToken, seller, rootToken, root
}

func shopRequest(engine *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)
	return response
}

func shopPublishedProduct(t *testing.T, db *gorm.DB, seller, root model.User) *model.MerchantStoreProduct {
	t.Helper()
	require.NoError(t, model.SetMerchantStorePaymentCategories(seller.Id, model.MerchantStorePaymentCategories{PlatformEnabled: true}))
	_, gatewayErr := model.SaveMerchantStoreGateway(seller.Id, "balance", true, "")
	require.NoError(t, gatewayErr)
	product, err := model.SaveMerchantStoreProduct(seller.Id, "", model.MerchantStoreProductInput{Title: "Card fixture", Description: "A digital card", PriceQuota: 500000, Template: "card-key", DeliveryStrategy: "sequential", PaymentMethods: []string{"balance"}, PickupLoginRequired: true, PickupCodeRequired: true})
	require.NoError(t, err)
	_, err = model.AddMerchantStoreStock(seller.Id, product.ID, []string{"PRIVATE-CARD-ONE", "PRIVATE-CARD-TWO"})
	require.NoError(t, err)
	require.NoError(t, model.SubmitMerchantStoreProduct(seller.Id, product.ID))
	require.NoError(t, model.ReviewMerchantStoreProduct(root.Id, product.ID, true, "internal-review-fixture"))
	return product
}

func TestMerchantStoreRouterSeparatesPublicPrivateAndRootSurfaces(t *testing.T) {
	engine, db, sellerToken, seller, _, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	for _, path := range []string{"/api/store/products", "/api/store/products/" + product.ID, "/api/store/config", "/api/store/disclaimer"} {
		response := shopRequest(engine, "GET", path, "", "")
		require.Equal(t, 200, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
		require.NotContains(t, response.Body.String(), "internal-review-fixture")
		require.NotContains(t, response.Body.String(), "pickup_token")
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
	for _, path := range []string{"/api/store/my/products", "/api/store/my/orders", "/api/store/payments/settings", "/api/store/reviews"} {
		response := shopRequest(engine, "GET", path, "", "")
		require.NotEqual(t, 200, response.Code, path)
	}
	for _, path := range []string{"/api/store/products/" + product.ID + "/review", "/api/store/config", "/api/store/promotion-config"} {
		method := "PUT"
		if strings.HasSuffix(path, "/review") {
			method = "POST"
		}
		response := shopRequest(engine, method, path, sellerToken, `{"approve":true,"fee_bps":0,"promotion_quota":1}`)
		require.Equal(t, 403, response.Code, response.Body.String())
	}
	response := shopRequest(engine, "GET", "/api/store/products?limit=10000", "", "")
	require.Equal(t, 422, response.Code)
	for _, path := range []string{"/api/store/orders/abc/settle", "/api/store/withdraw", "/api/store/claim/abc/success"} {
		response = shopRequest(engine, "POST", path, sellerToken, `{"success":true}`)
		require.Equal(t, 404, response.Code, path)
	}
}

func TestMerchantStoreRouterOrdersUseAuthenticatedBuyerAndExplicitDisclaimer(t *testing.T) {
	engine, db, sellerToken, seller, _, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	buyerToken := "shop-router-buyer"
	buyer := model.User{Username: "shop-buyer", AffCode: "shop-buyer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AccessToken: &buyerToken, Quota: 2000000, Email: "private-buyer@example.test"}
	require.NoError(t, db.Create(&buyer).Error)
	input := `{"product_id":"` + product.ID + `","quantity":1,"request_key":"router-order","payment_method":"balance","pickup_code":"buyer-private-code","buyer_id":999,"disclaimer_version":"` + model.MerchantStoreDisclaimerVersion + `"}`
	response := shopRequest(engine, "POST", "/api/store/orders", buyerToken, input)
	require.Equal(t, 409, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_DISCLAIMER_REQUIRED")
	response = shopRequest(engine, "POST", "/api/store/disclaimer/accept", buyerToken, `{"version":"`+model.MerchantStoreDisclaimerVersion+`","accepted":false}`)
	require.Equal(t, 422, response.Code)
	response = shopRequest(engine, "POST", "/api/store/disclaimer/accept", buyerToken, `{"version":"`+model.MerchantStoreDisclaimerVersion+`","accepted":true}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "POST", "/api/store/orders", buyerToken, input)
	require.Equal(t, 200, response.Code, response.Body.String())
	var body struct {
		Data struct {
			Order   model.MerchantStoreOrder `json:"order"`
			Created bool                     `json:"created"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Data.Created)
	require.Equal(t, buyer.Id, body.Data.Order.BuyerID)
	require.Equal(t, "paid", body.Data.Order.Status)
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	require.NotContains(t, response.Body.String(), buyer.Email)
	token, err := model.GetMerchantStoreOrderPickupToken(buyer.Id, body.Data.Order.ID)
	require.NoError(t, err)
	for _, auth := range []string{"", sellerToken} {
		response = shopRequest(engine, "POST", "/api/store/claim/"+token, auth, `{"pickup_code":"buyer-private-code"}`)
		require.NotEqual(t, 200, response.Code)
		require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	}
	response = shopRequest(engine, "POST", "/api/store/claim/"+token, buyerToken, `{"pickup_code":"wrong"}`)
	require.NotEqual(t, 200, response.Code)
	response = shopRequest(engine, "POST", "/api/store/claim/"+token, buyerToken, `{"pickup_code":"buyer-private-code"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "PRIVATE-CARD-ONE")
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD-TWO")
	require.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
	response = shopRequest(engine, "GET", "/api/store/my/orders?role=seller&user_id=999", sellerToken, "")
	require.Equal(t, 200, response.Code)
	require.NotContains(t, response.Body.String(), buyer.Email)
	require.NotContains(t, response.Body.String(), token)
	response = shopRequest(engine, "GET", "/api/store/orders/"+body.Data.Order.ID+"/pickup-link", sellerToken, "")
	require.NotEqual(t, 200, response.Code)
}

func TestMerchantStoreRouterRejectsOversizedInventoryAndDuplicateCallbackFields(t *testing.T) {
	engine, _, token, _, _, _ := merchantStoreTestRouter(t)
	response := shopRequest(engine, "POST", "/api/store/products/unknown/inventory", token, strings.Repeat(" ", 2<<20)+`{"items":[]}`)
	require.NotEqual(t, 200, response.Code)
	response = shopRequest(engine, "GET", "/api/store/payments/epay/"+strings.Repeat("a", 64)+"/notify?money=1&money=2", "", "")
	require.Equal(t, 400, response.Code)
	require.Equal(t, "fail", response.Body.String())
	response = shopRequest(engine, "POST", "/api/store/payments/pancake/platform/3/prod/webhook", "", "{}")
	require.Equal(t, 422, response.Code)
}
