package router

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreSellerProfileHTTPFiltersAndDoesNotExposeAccountEmail(t *testing.T) {
	engine, db, _, seller, rootToken, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	require.NoError(t, db.Model(&seller).Updates(map[string]any{"email": "PRIVATE-ACCOUNT@example.test", "display_name": "Public merchant"}).Error)
	require.NoError(t, db.Model(product).Update("contact", "public-contact@example.test").Error)
	response := shopRequest(engine, "GET", fmt.Sprintf("/api/store/products?seller_id=%d&limit=1", seller.Id), rootToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "PRIVATE-ACCOUNT")
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	var body struct {
		Data struct {
			Items  []model.MerchantStoreProduct     `json:"items"`
			Seller *model.MerchantStorePublicSeller `json:"seller"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Len(t, body.Data.Items, 1)
	require.Equal(t, seller.Id, body.Data.Seller.ID)
	require.Equal(t, seller.Id, body.Data.Items[0].Seller.ID)
	require.Equal(t, "public-contact@example.test", body.Data.Seller.ContactEmail)
	for _, value := range []string{"", "0", "-1", "01", "+1", "2147483648", "1 OR 1=1"} {
		response = shopRequest(engine, "GET", "/api/store/products?seller_id="+url.QueryEscape(value), "", "")
		require.Equal(t, 422, response.Code, value)
	}
	response = shopRequest(engine, "GET", "/api/store/products?seller_id=1&seller_id=2", "", "")
	require.Equal(t, 422, response.Code)
	require.NoError(t, db.Model(&seller).Update("status", common.UserStatusDisabled).Error)
	response = shopRequest(engine, "GET", fmt.Sprintf("/api/store/products?seller_id=%d", seller.Id), "", "")
	require.Equal(t, 200, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Empty(t, body.Data.Items)
	require.Nil(t, body.Data.Seller)
}
