package router

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreHomeRoutesHaveRealAuthAndPrivateSettingsBoundary(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	path := fmt.Sprintf("/api/store/merchants/%d", seller.Id)
	response := shopRequest(engine, "GET", path, "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	response = shopRequest(engine, "PUT", "/api/store/my/home", sellerToken, `{"biography":"**Custom shop**","announcement":"Shipping today","header_image":"https://example.test/header.svg","expected_version":0}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "PUT", "/api/store/my/home", sellerToken, `{"biography":"Forgery","seller_id":999}`)
	require.Equal(t, 422, response.Code)
	response = shopRequest(engine, "PUT", "/api/store/my/home", sellerToken, `{"biography":"Stale","expected_version":0}`)
	require.Equal(t, 409, response.Code)
	response = shopRequest(engine, "PUT", "/api/store/my/home", "", `{}`)
	require.NotEqual(t, 200, response.Code)
	response = shopRequest(engine, "PUT", "/api/store/announcement", sellerToken, `{"content":"Not an administrator"}`)
	require.Equal(t, 403, response.Code)
	adminToken, _ := shopTestModeActor(t, db, common.RoleAdminUser)
	response = shopRequest(engine, "PUT", "/api/store/announcement", adminToken, `{"content":"## Public news"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/announcement", "", "")
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), "## Public news")
	response = shopRequest(engine, "PUT", "/api/store/announcement", rootToken, `{"content":"Updated","unexpected":true}`)
	require.Equal(t, 422, response.Code)
	// Catalogue filtering remains server-side on the merchant home URL.
	response = shopRequest(engine, "GET", "/api/store/products?seller_id="+fmt.Sprint(seller.Id), "", "")
	require.Equal(t, 200, response.Code)
	var catalogue struct {
		Data struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &catalogue))
	require.Len(t, catalogue.Data.Items, 1)
	require.Equal(t, product.ID, catalogue.Data.Items[0].ID)
	for _, bad := range []string{"01", "-1", "2147483648", "not-a-user"} {
		response = shopRequest(engine, "GET", "/api/store/merchants/"+bad, "", "")
		require.Equal(t, 422, response.Code)
	}
}
