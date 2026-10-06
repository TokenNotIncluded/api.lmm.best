package router

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreSalesRouterRequiresExplicitNullableLimitAndOwner(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	path := "/api/store/products/" + product.ID + "/sale-limit"
	response := shopRequest(engine, "PUT", path, "", `{"sale_limit":1}`)
	require.NotEqual(t, 200, response.Code)
	for _, body := range []string{`{}`, `null`, `{"sale_limit":-1}`, `{"sale_limit":1.5}`, `{"sale_limit":"1"}`, `{"sale_limit":true}`, `{"sale_limit":9007199254740992}`} {
		response = shopRequest(engine, "PUT", path, sellerToken, body)
		require.Equal(t, 422, response.Code, body)
	}
	response = shopRequest(engine, "PUT", path, sellerToken, `{"sale_limit":1}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	var stored model.MerchantStoreProduct
	require.NoError(t, db.First(&stored, "id = ?", product.ID).Error)
	require.EqualValues(t, 1, *stored.SaleLimit)
	require.Equal(t, "published", stored.Status)
	response = shopRequest(engine, "PUT", path, sellerToken, `{}`)
	require.Equal(t, 422, response.Code)
	require.NoError(t, db.First(&stored, "id = ?", product.ID).Error)
	require.EqualValues(t, 1, *stored.SaleLimit)
	response = shopRequest(engine, "PUT", path, rootToken, `{"sale_limit":0}`)
	require.Equal(t, 200, response.Code)
	response = shopRequest(engine, "GET", "/api/store/products/"+product.ID, "", "")
	require.Equal(t, 200, response.Code)
	var envelope struct {
		Data model.MerchantStoreProduct `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Zero(t, envelope.Data.AvailableStock)
	require.Zero(t, envelope.Data.SaleAvailable)
	require.True(t, envelope.Data.TradingPaused)
	response = shopRequest(engine, "GET", "/api/store/my/products/"+product.ID, sellerToken, "")
	require.Equal(t, 200, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.EqualValues(t, 2, envelope.Data.AvailableStock)
	response = shopRequest(engine, "PUT", path, sellerToken, `{"sale_limit":null}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	stored = model.MerchantStoreProduct{}
	require.NoError(t, db.First(&stored, "id = ?", product.ID).Error)
	require.Nil(t, stored.SaleLimit)
}

func TestMerchantStoreSalesRouterOffShelfPreservesInventoryAndExplicitListing(t *testing.T) {
	engine, db, sellerToken, seller, _, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	path := "/api/store/products/" + product.ID + "/listing"
	for _, body := range []string{`{}`, `{"listed":null}`, `{"listed":"false"}`} {
		response := shopRequest(engine, "PUT", path, sellerToken, body)
		require.Equal(t, 422, response.Code, body)
	}
	response := shopRequest(engine, "PUT", path, sellerToken, `{"listed":false}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/products/"+product.ID, "", "")
	require.Equal(t, 404, response.Code)
	var count int64
	require.NoError(t, db.Model(&model.MerchantStoreStock{}).Where("product_id = ?", product.ID).Count(&count).Error)
	require.EqualValues(t, 2, count)
	response = shopRequest(engine, "PUT", "/api/store/products/"+product.ID+"/paused", sellerToken, `{"paused":false}`)
	require.Equal(t, 409, response.Code)
	response = shopRequest(engine, "PUT", path, sellerToken, `{"listed":true}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/products/"+product.ID, "", "")
	require.Equal(t, 200, response.Code)
}
