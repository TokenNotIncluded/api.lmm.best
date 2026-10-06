package router

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreSelfPurchaseRouterAuthenticatesSellerAsBuyerAndDeliversOnce(t *testing.T) {
	engine, db, sellerToken, seller, _, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	response := shopRequest(engine, "POST", "/api/store/disclaimer/accept", sellerToken, `{"version":"`+model.MerchantStoreDisclaimerVersion+`","accepted":true}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	input := `{"product_id":"` + product.ID + `","quantity":1,"request_key":"normal-self-router","payment_method":"balance","pickup_code":"seller-private-code","buyer_id":999}`
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
	require.Equal(t, seller.Id, body.Data.Order.BuyerID, "a body-supplied buyer cannot replace the authenticated merchant")
	require.Equal(t, seller.Id, body.Data.Order.SellerID)
	require.Equal(t, "paid", body.Data.Order.Status)
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	orderID := body.Data.Order.ID
	response = shopRequest(engine, "POST", "/api/store/orders", sellerToken, input)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.False(t, body.Data.Created)
	require.Equal(t, orderID, body.Data.Order.ID)
	var liveSeller, liveRoot model.User
	require.NoError(t, db.First(&liveSeller, seller.Id).Error)
	require.NoError(t, db.First(&liveRoot, root.Id).Error)
	require.Equal(t, 995000, liveSeller.Quota)
	require.Equal(t, 1005000, liveRoot.Quota)
	token, err := model.GetMerchantStoreOrderPickupToken(seller.Id, orderID)
	require.NoError(t, err)
	response = shopRequest(engine, "POST", "/api/store/claim/"+token, "", `{"pickup_code":"seller-private-code"}`)
	require.NotEqual(t, 200, response.Code)
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	response = shopRequest(engine, "POST", "/api/store/claim/"+token, sellerToken, `{"pickup_code":"seller-private-code"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "PRIVATE-CARD-ONE")
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD-TWO")
	var delivered int64
	require.NoError(t, db.Model(&model.MerchantStoreStock{}).Where("product_id = ? AND state = ?", product.ID, "delivered").Count(&delivered).Error)
	require.EqualValues(t, 1, delivered)
}
