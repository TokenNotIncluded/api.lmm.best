package router

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func shopTestModeInput(p *model.MerchantStoreProduct, mode *bool) model.MerchantStoreProductInput {
	return model.MerchantStoreProductInput{Title: p.Title, Description: p.Description, ImageURLs: p.ImageURLs, Contact: p.Contact, Links: p.Links, PriceQuota: p.PriceQuota, TestMode: mode, Template: p.Template, DeliveryStrategy: p.DeliveryStrategy, PaymentMethods: p.PaymentMethods, PickupLoginRequired: p.PickupLoginRequired, PickupCodeRequired: p.PickupCodeRequired, EmailPickupLink: p.EmailPickupLink}
}

func shopTestModeActor(t *testing.T, db *gorm.DB, role int) (string, model.User) {
	t.Helper()
	token := fmt.Sprintf("test-mode-actor-%d", role)
	u := model.User{Username: token, AffCode: token, Role: role, Status: common.UserStatusEnabled, AccessToken: &token, Quota: 2000000}
	require.NoError(t, db.Create(&u).Error)
	return token, u
}

func TestMerchantStoreTestModeRouterRequiresBooleanAndPreservesOmittedFlag(t *testing.T) {
	engine, db, token, seller, _, root := merchantStoreTestRouter(t)
	p := shopPublishedProduct(t, db, seller, root)
	response := shopRequest(engine, "GET", "/api/store/config", "", "")
	require.Equal(t, 200, response.Code)
	var config struct {
		Data struct {
			Supported bool `json:"product_test_mode_supported"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &config))
	require.True(t, config.Data.Supported)
	input, err := json.Marshal(shopTestModeInput(p, nil))
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(input, &fields))
	for _, flag := range []any{nil, "true", 1, []bool{true}, map[string]bool{"enabled": true}} {
		fields["test_mode"] = flag
		body, err := json.Marshal(fields)
		require.NoError(t, err)
		response = shopRequest(engine, "PUT", "/api/store/products/"+p.ID, token, string(body))
		require.Equal(t, 422, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "STORE_INVALID_INPUT")
	}
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.False(t, p.TestMode)
	require.Equal(t, "published", p.Status, "invalid flags must leave the public version unchanged")
	fields["test_mode"], fields["seller_id"] = true, root.Id
	body, err := json.Marshal(fields)
	require.NoError(t, err)
	response = shopRequest(engine, "PUT", "/api/store/products/"+p.ID, token, string(body))
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.True(t, p.TestMode)
	require.Equal(t, seller.Id, p.SellerID, "body fields cannot transfer private ownership")
	require.Equal(t, "draft", p.Status)
	delete(fields, "test_mode")
	body, err = json.Marshal(fields)
	require.NoError(t, err)
	response = shopRequest(engine, "PUT", "/api/store/products/"+p.ID, token, string(body))
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NoError(t, db.First(p, "id = ?", p.ID).Error)
	require.True(t, p.TestMode)
	fields["test_mode"] = false
	body, err = json.Marshal(fields)
	require.NoError(t, err)
	response = shopRequest(engine, "PUT", "/api/store/products/"+p.ID, token, string(body))
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/products/"+p.ID, "", "")
	require.Equal(t, 404, response.Code, "leaving testing must require a fresh normal review")
}

func TestMerchantStoreTestModeRouterKeepsPreviewAndPublicationPrivate(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	p := shopPublishedProduct(t, db, seller, root)
	p.Title = "Hidden private test listing"
	enabled := true
	p, err := model.SaveMerchantStoreProduct(seller.Id, p.ID, shopTestModeInput(p, &enabled))
	require.NoError(t, err)
	adminToken, _ := shopTestModeActor(t, db, common.RoleAdminUser)
	buyerToken, _ := shopTestModeActor(t, db, common.RoleCommonUser)
	for _, state := range []string{"draft", "pending", "published", "rejected"} {
		require.NoError(t, db.Model(p).Update("status", state).Error)
		for _, auth := range []string{"", sellerToken, buyerToken, adminToken, rootToken} {
			for _, path := range []string{"/api/store/products/" + p.ID, "/api/store/products?q=Hidden&limit=1&test_mode=true", "/api/store/products?limit=1&test_mode=true"} {
				response := shopRequest(engine, "GET", path, auth, "")
				if path == "/api/store/products/"+p.ID {
					require.Equal(t, 404, response.Code)
				} else {
					require.Equal(t, 200, response.Code)
					require.Contains(t, response.Body.String(), `"items":[]`)
					require.Contains(t, response.Body.String(), `"has_more":false`)
				}
				require.NotContains(t, response.Body.String(), p.Title)
				require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
			}
		}
	}
	for _, auth := range []string{"", buyerToken, adminToken, rootToken} {
		response := shopRequest(engine, "GET", "/api/store/my/products/"+p.ID+"/preview?user_id="+fmt.Sprint(seller.Id), auth, "")
		require.NotEqual(t, 200, response.Code)
		require.NotContains(t, response.Body.String(), p.Title)
		require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	}
	for _, auth := range []string{buyerToken, adminToken, rootToken} {
		for _, path := range []string{"/api/store/my/products/" + p.ID, "/api/store/products/" + p.ID + "/ai-reviews", "/api/store/products/" + p.ID + "/inventory"} {
			response := shopRequest(engine, "GET", path, auth, "")
			require.Equal(t, 403, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), p.Title)
		}
	}
	for _, auth := range []string{adminToken, rootToken} {
		response := shopRequest(engine, "GET", "/api/store/reviews", auth, "")
		require.Equal(t, 200, response.Code)
		require.Contains(t, response.Body.String(), `"items":[]`)
	}
	require.NoError(t, db.Model(p).Update("status", "draft").Error)
	response := shopRequest(engine, "GET", "/api/store/my/products/"+p.ID+"/preview", sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), p.Title)
	require.Contains(t, response.Body.String(), `"trading_paused":false`)
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	for _, request := range []struct{ method, suffix, body string }{
		{"POST", "/submit", `{}`}, {"PUT", "/listing", `{"listed":true}`}, {"POST", "/promotion", `{"months":1,"request_key":"private-promote"}`},
	} {
		response = shopRequest(engine, request.method, "/api/store/products/"+p.ID+request.suffix, sellerToken, request.body)
		require.Equal(t, 422, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "STORE_TEST_MODE")
	}
	response = shopRequest(engine, "POST", "/api/store/products/"+p.ID+"/review", rootToken, `{"approve":true}`)
	require.Equal(t, 403, response.Code)
	rootProduct, err := model.SaveMerchantStoreProduct(root.Id, "", model.MerchantStoreProductInput{Title: "Root owned private test", PriceQuota: 500000, TestMode: &enabled, Template: "card-key"})
	require.NoError(t, err)
	response = shopRequest(engine, "POST", "/api/store/products/"+rootProduct.ID+"/review", rootToken, `{"approve":true}`)
	require.Equal(t, 422, response.Code)
	require.Contains(t, response.Body.String(), "STORE_TEST_MODE")
}

func TestMerchantStoreTestModeRouterSelfPurchaseAndReplayUseAuthenticatedOwner(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	p := shopPublishedProduct(t, db, seller, root)
	enabled := true
	p, err := model.SaveMerchantStoreProduct(seller.Id, p.ID, shopTestModeInput(p, &enabled))
	require.NoError(t, err)
	buyerToken, _ := shopTestModeActor(t, db, common.RoleCommonUser)
	adminToken, _ := shopTestModeActor(t, db, common.RoleAdminUser)
	input := fmt.Sprintf(`{"product_id":%q,"quantity":1,"request_key":"private-self-order","payment_method":"balance","pickup_code":"seller-private-code","buyer_id":%d}`, p.ID, seller.Id)
	for _, auth := range []string{buyerToken, adminToken, rootToken} {
		response := shopRequest(engine, "POST", "/api/store/orders", auth, input)
		require.Equal(t, 403, response.Code, "a body-supplied owner must not bypass authenticated buyer policy")
	}
	response := shopRequest(engine, "POST", "/api/store/disclaimer/accept", sellerToken, `{"version":"`+model.MerchantStoreDisclaimerVersion+`","accepted":true}`)
	require.Equal(t, 200, response.Code)
	response = shopRequest(engine, "POST", "/api/store/orders", sellerToken, input)
	require.Equal(t, 200, response.Code, response.Body.String())
	var body struct {
		Data struct {
			Order   model.MerchantStoreOrder `json:"order"`
			Created bool                     `json:"created"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.Data.Created)
	require.Equal(t, seller.Id, body.Data.Order.BuyerID)
	require.Equal(t, seller.Id, body.Data.Order.SellerID)
	require.Equal(t, "paid", body.Data.Order.Status)
	require.Equal(t, 500000, body.Data.Order.PriceQuota)
	require.Equal(t, 5000, body.Data.Order.FeeQuota)
	require.Equal(t, "card-key", body.Data.Order.DeliveryTemplate)
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	orderID := body.Data.Order.ID
	var liveSeller, liveRoot model.User
	require.NoError(t, db.First(&liveSeller, seller.Id).Error)
	require.NoError(t, db.First(&liveRoot, root.Id).Error)
	require.Equal(t, 995000, liveSeller.Quota)
	require.Equal(t, 1005000, liveRoot.Quota)
	disabled := false
	p.Template = "custom-text"
	_, err = model.SaveMerchantStoreProduct(seller.Id, p.ID, shopTestModeInput(p, &disabled))
	require.NoError(t, err)
	response = shopRequest(engine, "POST", "/api/store/orders", sellerToken, input)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.False(t, body.Data.Created)
	require.Equal(t, orderID, body.Data.Order.ID)
	require.Equal(t, "card-key", body.Data.Order.DeliveryTemplate, "replay keeps the frozen delivery template")
	token, err := model.GetMerchantStoreOrderPickupToken(seller.Id, orderID)
	require.NoError(t, err)
	response = shopRequest(engine, "POST", "/api/store/claim/"+token, sellerToken, `{"pickup_code":"seller-private-code"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "PRIVATE-CARD-ONE")
	require.Contains(t, response.Body.String(), `"delivery_template":"card-key"`)
	var delivered int64
	require.NoError(t, db.Model(&model.MerchantStoreStock{}).Where("product_id = ? AND state = ?", p.ID, "delivered").Count(&delivered).Error)
	require.EqualValues(t, 1, delivered)
}
