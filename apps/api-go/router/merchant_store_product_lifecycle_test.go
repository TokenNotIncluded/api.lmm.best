package router

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreRouterSellerCanUnlistAndDeleteOnlyOwnProduct(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	require.NoError(t, model.ActivateMerchantStoreVariants(db, 1))
	require.NoError(t, model.ActivateMerchantStoreProductLifecycle(db, 2))
	path := "/api/store/products/" + product.ID
	for _, action := range []struct{ method, path string }{{"POST", path + "/unlist"}, {"DELETE", path}} {
		response := shopRequest(engine, action.method, action.path, "", "")
		require.NotEqual(t, 200, response.Code)
		response = shopRequest(engine, action.method, action.path, rootToken, "")
		require.Equal(t, 403, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "STORE_ACCESS_DENIED")
	}
	response := shopRequest(engine, "POST", path+"/unlist", sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.JSONEq(t, `{"success":true,"message":"","data":null}`, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/my/products/"+product.ID, sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"status":"unlisted"`)
	response = shopRequest(engine, "GET", path, "", "")
	require.Equal(t, 404, response.Code, response.Body.String())
	response = shopRequest(engine, "PUT", path+"/paused", sellerToken, `{"paused":false}`)
	require.Equal(t, 409, response.Code, response.Body.String())
	response = shopRequest(engine, "DELETE", path, sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "DELETE", path, sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	for _, view := range []string{path, "/api/store/my/products/" + product.ID, path + "/inventory", path + "/ai-reviews"} {
		response = shopRequest(engine, "GET", view, sellerToken, "")
		require.Equal(t, 404, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "STORE_NOT_FOUND")
	}
	response = shopRequest(engine, "GET", "/api/store/my/products", sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), product.ID)
	response = shopRequest(engine, "GET", "/api/store/reviews", rootToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), product.ID)
	response = shopRequest(engine, "PUT", path, sellerToken, `{"title":"Resurrection","price_quota":1}`)
	require.Equal(t, 404, response.Code, response.Body.String())
	var retained model.MerchantStoreProduct
	require.NoError(t, db.First(&retained, "id = ?", product.ID).Error)
	require.Equal(t, "deleted", retained.Status)
	require.Equal(t, seller.Id, retained.SellerID)
}
