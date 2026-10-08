package router

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreCatalogueCollectionRoutesUseActualAccountAndCurrentFacts(t *testing.T) {
	require.GreaterOrEqual(t, model.MerchantStoreWriterCapability, 5, "collections require registered phase 5")
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "5").Error)
	_, err := model.SaveMerchantStoreSellerTerms(seller.Id, model.MerchantStoreTermsInput{Content: "Delivery uses the selected specification. Contact the merchant for support."})
	require.NoError(t, err)
	product := shopPublishedProduct(t, db, seller, root)
	for _, path := range []string{"/api/store/cart", "/api/store/favorites"} {
		response := shopRequest(engine, "GET", path, "", "")
		require.NotEqual(t, 200, response.Code, path)
	}
	response := shopRequest(engine, "PUT", "/api/store/products/"+product.ID+"/catalogue", rootToken, `{"custom_tags":["wrong owner"]}`)
	require.Equal(t, 403, response.Code, response.Body.String())
	response = shopRequest(engine, "PUT", "/api/store/products/"+product.ID+"/catalogue", sellerToken, `{"custom_tags":["custom annotation"],"auto_delivery":true,"ai_processing":false,"default_variant_id":"caller-cannot-change-this"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "default_variant_id")
	response = shopRequest(engine, "GET", "/api/store/products?tag=custom%20annotation&stock=in_stock&auto_delivery=true&sort=newest", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), product.ID)
	require.Contains(t, response.Body.String(), `"net_paid_quantity":0`)
	for _, query := range []string{"auto_delivery=1", "stock=in_stock&stock=out_of_stock", "sort=arbitrary"} {
		response = shopRequest(engine, "GET", "/api/store/products?"+query, "", "")
		require.Equal(t, 422, response.Code, response.Body.String())
	}
	response = shopRequest(engine, "PUT", "/api/store/cart", rootToken, `{"product_id":"`+product.ID+`","quantity":1,"user_id":`+strconv.Itoa(seller.Id)+`}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	var body struct {
		Data model.MerchantStoreCartItem `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	var persisted model.MerchantStoreCartItem
	require.NoError(t, db.First(&persisted, "id = ?", body.Data.ID).Error)
	require.Equal(t, root.Id, persisted.UserID)
	require.NotContains(t, response.Body.String(), "price_quota")
	response = shopRequest(engine, "PUT", "/api/store/cart", rootToken, `{"product_id":"`+product.ID+`","quantity":1.5}`)
	require.Equal(t, 422, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/cart", sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), body.Data.ID)
	response = shopRequest(engine, "DELETE", "/api/store/cart/"+body.Data.ID, sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	var count int64
	require.NoError(t, db.Model(&model.MerchantStoreCartItem{}).Where("user_id = ?", root.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Model(&model.MerchantStoreProduct{}).Where("id = ?", product.ID).Update("status", "paused").Error)
	response = shopRequest(engine, "PUT", "/api/store/favorites", rootToken, `{"product_id":"`+product.ID+`"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/cart", rootToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"valid":false`)
	require.Contains(t, response.Body.String(), `"unavailable_reason":"unavailable"`)
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	response = shopRequest(engine, "POST", "/api/store/collections/cleanup", rootToken, `{}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"cart_deleted":0`)
	require.Contains(t, response.Body.String(), `"favorites_deleted":0`)
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	response = shopRequest(engine, "GET", "/api/store/config", rootToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"store_collections_supported":false`)
	response = shopRequest(engine, "PUT", "/api/store/favorites", rootToken, `{"product_id":"`+product.ID+`"}`)
	require.Equal(t, 503, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_UPGRADE_IN_PROGRESS")
}
