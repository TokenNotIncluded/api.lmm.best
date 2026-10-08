package router

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func activateStoreSocialRouter(t *testing.T, activate bool) {
	t.Helper()
	require.GreaterOrEqual(t, model.MerchantStoreWriterCapability, 6, "social routes require reviewed phase-six source")
	require.NoError(t, model.PrepareMerchantStoreSchema(model.DB, 1))
	require.NoError(t, model.ActivateMerchantStoreVariants(model.DB, 1))
	require.NoError(t, model.ActivateMerchantStoreProductLifecycle(model.DB, 2))
	require.NoError(t, model.ActivateMerchantStoreRefunds(model.DB, 3))
	require.NoError(t, model.ActivateMerchantStoreAccess(model.DB, 4))
	if activate {
		require.NoError(t, model.ActivateMerchantStorePhaseSix(model.DB, 5))
	}
}

func storeSocialResponse(t *testing.T, response *httptest.ResponseRecorder) model.MerchantStoreProductLikes {
	t.Helper()
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.NotContains(t, response.Body.String(), "user_id")
	var body struct {
		Data model.MerchantStoreProductLikes `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	return body.Data
}

func TestMerchantStoreProductLikesRouterPhaseFiveStaysUnsupported(t *testing.T) {
	engine, db, sellerToken, seller, _, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	activateStoreSocialRouter(t, false)
	path := "/api/store/products/" + product.ID + "/likes"
	view := storeSocialResponse(t, shopRequest(engine, "GET", path, "", ""))
	require.False(t, view.Supported)
	require.Nil(t, view.Count)
	require.False(t, view.Liked)
	response := shopRequest(engine, "GET", "/api/store/config", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"store_likes_supported":false`)
	for _, method := range []string{"PUT", "DELETE"} {
		response = shopRequest(engine, method, path, sellerToken, `{}`)
		require.Equal(t, 503, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "STORE_UPGRADE_IN_PROGRESS")
		response = shopRequest(engine, method, path, "", `{}`)
		require.NotEqual(t, 200, response.Code)
	}
	var count int64
	require.NoError(t, db.Model(&model.MerchantStoreProductLike{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestMerchantStoreProductLikesRouterUsesAuthenticatedAccountAndFreshCounts(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	activateStoreSocialRouter(t, true)
	path := "/api/store/products/" + product.ID + "/likes"
	view := storeSocialResponse(t, shopRequest(engine, "GET", path, "", ""))
	require.True(t, view.Supported)
	require.EqualValues(t, 0, *view.Count)
	require.False(t, view.Liked)
	response := shopRequest(engine, "PUT", path, sellerToken, `{"user_id":`+strconv.Itoa(root.Id)+`}`)
	require.Equal(t, 422, response.Code, response.Body.String())
	response = shopRequest(engine, "PUT", path, sellerToken, strings.Repeat(" ", 2048))
	require.Equal(t, 413, response.Code)
	for i := 0; i < 2; i++ {
		view = storeSocialResponse(t, shopRequest(engine, "PUT", path, rootToken, `{}`))
		require.True(t, view.Liked)
		require.EqualValues(t, 1, *view.Count)
	}
	var stored model.MerchantStoreProductLike
	require.NoError(t, db.First(&stored, "user_id = ? AND product_id = ?", root.Id, product.ID).Error)
	require.Equal(t, root.Id, stored.UserID)
	view = storeSocialResponse(t, shopRequest(engine, "GET", path+"?viewer="+strconv.Itoa(root.Id), "", ""))
	require.False(t, view.Liked, "a query viewer ID cannot identify the requesting account")
	require.EqualValues(t, 1, *view.Count)
	view = storeSocialResponse(t, shopRequest(engine, "GET", path, sellerToken, ""))
	require.False(t, view.Liked)
	// A guest credential does not inherit the registered account's personal state.
	request := httptest.NewRequest("GET", path, nil)
	request.Header.Set("Authorization", "Bearer "+rootToken)
	request.Header.Set("X-Store-Guest", "untrusted-guest-credential")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	view = storeSocialResponse(t, response)
	require.False(t, view.Liked)
	for i := 0; i < 2; i++ {
		view = storeSocialResponse(t, shopRequest(engine, "DELETE", path, rootToken, ""))
		require.False(t, view.Liked)
		require.EqualValues(t, 0, *view.Count)
	}
	response = shopRequest(engine, "GET", "/api/store/config", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"store_likes_supported":true`)
}

func TestMerchantStoreProductLikesRouterHidesPrivateAndRetiredProducts(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	activateStoreSocialRouter(t, true)
	path := "/api/store/products/" + product.ID + "/likes"
	storeSocialResponse(t, shopRequest(engine, "PUT", path, rootToken, `{}`))
	require.NoError(t, db.Model(&model.MerchantStoreProduct{}).Where("id = ?", product.ID).Updates(map[string]any{"visibility": "private", "test_mode": true}).Error)
	for _, token := range []string{"", rootToken} {
		response := shopRequest(engine, "GET", path, token, "")
		require.Equal(t, 404, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), `"count"`)
		if token != "" {
			response = shopRequest(engine, "PUT", path, token, `{}`)
			require.Equal(t, 404, response.Code, response.Body.String())
		}
	}
	view := storeSocialResponse(t, shopRequest(engine, "GET", path, sellerToken, ""))
	require.EqualValues(t, 1, *view.Count)
	require.False(t, view.Liked)
	for _, status := range []string{"unlisted", "deleted"} {
		require.NoError(t, db.Model(&model.MerchantStoreProduct{}).Where("id = ?", product.ID).UpdateColumn("status", status).Error)
		for _, method := range []string{"GET", "PUT"} {
			response := shopRequest(engine, method, path, sellerToken, `{}`)
			require.Equal(t, 404, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), `"count"`)
		}
	}
}

func TestMerchantStoreProductLikesRouterProjectsViewerStateInCatalogueAndDetail(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	activateStoreSocialRouter(t, true)
	storeSocialResponse(t, shopRequest(engine, "PUT", "/api/store/products/"+product.ID+"/likes", rootToken, `{}`))
	for _, token := range []string{"", rootToken, sellerToken} {
		response := shopRequest(engine, "GET", "/api/store/products", token, "")
		require.Equal(t, 200, response.Code, response.Body.String())
		var list struct {
			Data struct {
				Items []model.MerchantStoreProduct `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
		require.Len(t, list.Data.Items, 1)
		likes := list.Data.Items[0].Likes
		require.NotNil(t, likes)
		require.True(t, likes.Supported)
		require.EqualValues(t, 1, *likes.Count)
		require.Equal(t, token == rootToken, likes.Liked)
		response = shopRequest(engine, "GET", "/api/store/products/"+product.ID, token, "")
		require.Equal(t, 200, response.Code, response.Body.String())
		var detail struct {
			Data model.MerchantStoreProduct `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &detail))
		require.Equal(t, likes, detail.Data.Likes)
	}
}
