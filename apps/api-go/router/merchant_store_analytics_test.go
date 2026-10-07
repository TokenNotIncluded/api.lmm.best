package router

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreAnalyticsRouterScopesTrafficAndConfiguration(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	activateStoreSocialRouter(t, true)
	path := "/api/store/products/" + product.ID + "/analytics"
	body := fmt.Sprintf(`{"kind":"impression","page_key":"%s","page_started_at":%d}`, uuid.NewString(), common.GetTimestamp())
	response := shopRequest(engine, "POST", path, rootToken, body)
	require.Equal(t, 503, response.Code, "installation alone cannot enable traffic on old floor")
	require.NoError(t, model.PrepareMerchantStoreFixedContent(db, 6))
	require.NoError(t, model.ActivateMerchantStoreFixedContent(db, 6))
	for i := 0; i < 2; i++ {
		response = shopRequest(engine, "POST", path, rootToken, body)
		require.Equal(t, 202, response.Code, response.Body.String())
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		require.NotContains(t, response.Body.String(), "impressions", "public reporting cannot read traffic totals")
	}
	response = shopRequest(engine, "POST", path, sellerToken, body)
	require.Equal(t, 202, response.Code)
	response = shopRequest(engine, "POST", path, rootToken, `{"kind":"impression","page_key":"`+uuid.NewString()+`","page_started_at":1,"count":9000}`)
	require.Equal(t, 422, response.Code, "the client cannot choose its count")
	for _, route := range []string{"/api/store/my/analytics", "/api/store/analytics", "/api/store/analytics/config"} {
		response = shopRequest(engine, "GET", route, "", "")
		require.NotEqual(t, 200, response.Code, "analytics requires an authenticated authorized role")
	}
	response = shopRequest(engine, "GET", "/api/store/analytics", sellerToken, "")
	require.Equal(t, 403, response.Code)
	response = shopRequest(engine, "GET", "/api/store/my/analytics?days=7&seller_id="+fmt.Sprint(root.Id), sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	var result struct {
		Data model.MerchantStoreAnalytics `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.EqualValues(t, 1, *result.Data.Totals.Impressions)
	for _, item := range result.Data.Items {
		require.Equal(t, seller.Id, item.SellerID)
	}
	for _, private := range []string{"buyer_id", "email", "pickup", "page_key"} {
		require.NotContains(t, response.Body.String(), private)
	}
	for _, query := range []string{"days=0", "days=1&days=7", "days=3651", "days=banana"} {
		response = shopRequest(engine, "GET", "/api/store/my/analytics?"+query, sellerToken, "")
		require.Equal(t, 422, response.Code)
	}
	response = shopRequest(engine, "PUT", "/api/store/analytics/config", sellerToken, `{"retention_days":30,"dedupe_days":7}`)
	require.Equal(t, 403, response.Code)
	response = shopRequest(engine, "PUT", "/api/store/analytics/config", rootToken, `{"retention_days":30,"dedupe_days":7}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "PUT", "/api/store/analytics/config", rootToken, `{"retention_days":2,"dedupe_days":3}`)
	require.Equal(t, 422, response.Code)
}
