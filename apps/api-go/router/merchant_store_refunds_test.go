package router

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreRefundRouterOwnershipStrictBodyAndPurePickup(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	require.NoError(t, db.Model(product).Updates(map[string]any{"pickup_login_required": false}).Error)
	buyerToken := "refund-router-buyer"
	buyer := model.User{Username: "refund-buyer", AffCode: "refund-buyer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AccessToken: &buyerToken, Quota: 2000000}
	require.NoError(t, db.Create(&buyer).Error)
	require.NoError(t, model.AcceptMerchantStoreDisclaimer(buyer.Id, model.MerchantStoreDisclaimerVersion))
	o, _, e := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: buyer.Id, ProductID: product.ID, Quantity: 2, RequestKey: "router-refund", PaymentMethod: "balance", PickupCode: "refund-pickup"})
	require.NoError(t, e)
	token, e := model.GetMerchantStoreOrderPickupToken(buyer.Id, o.ID)
	require.NoError(t, e)
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	path := "/api/store/orders/" + o.ID + "/refunds"
	response := shopRequest(engine, "GET", path, "", "")
	require.NotEqual(t, 200, response.Code)
	response = shopRequest(engine, "GET", path, sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	adminToken := "refund-ordinary-admin"
	admin := model.User{Username: "refund-admin", AffCode: "refund-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AccessToken: &adminToken}
	require.NoError(t, db.Create(&admin).Error)
	response = shopRequest(engine, "GET", path, adminToken, "")
	require.Equal(t, 403, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", path, rootToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Card fixture")
	input := `{"request_key":"partial","reason":"One card issue","mode":"quantity","quantity":1}`
	for _, bad := range []string{`{"request_key":"bad","reason":"Issue","mode":"full","buyer_id":999}`, `{"request_key":"bad","reason":"Issue","mode":"full"} {}`, `{"request_key":"bad","reason":"Issue","mode":"amount","amount_quota":1.5}`} {
		response = shopRequest(engine, "POST", path, buyerToken, bad)
		require.Equal(t, 422, response.Code, response.Body.String())
	}
	response = shopRequest(engine, "POST", path, sellerToken, input)
	require.Equal(t, 403, response.Code, response.Body.String())
	proof := `{"order_id":"` + o.ID + `","token":"` + token + `","code":"refund-pickup"}`
	response = shopRequest(engine, "POST", "/api/store/pickup/refunds/read", "", proof)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	require.NoError(t, db.First(o, "id = ?", o.ID).Error)
	require.Zero(t, o.ClaimedAt)
	response = shopRequest(engine, "POST", "/api/store/pickup/refunds/request", "", `{"order_id":"`+o.ID+`","token":"`+token+`","code":"refund-pickup","input":`+input+`}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	var body struct {
		Data model.MerchantStoreRefund `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	r := body.Data
	require.Equal(t, buyer.Id, r.RequestedBy)
	require.Len(t, r.StockIDs, 1)
	response = shopRequest(engine, "POST", path+"/"+r.ID+"/decision", buyerToken, `{"decision":"approve"}`)
	require.Equal(t, 403, response.Code, response.Body.String())
	response = shopRequest(engine, "POST", path+"/"+r.ID+"/decision", adminToken, `{"decision":"approve"}`)
	require.Equal(t, 403, response.Code, response.Body.String())
	response = shopRequest(engine, "POST", path+"/"+r.ID+"/decision", sellerToken, `{"decision":"approve"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"status":"completed"`)
	response = shopRequest(engine, "POST", path+"/proactive", rootToken, `{"request_key":"root-full","reason":"Confirmed issue","mode":"full"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NoError(t, db.First(o, "id = ?", o.ID).Error)
	require.Equal(t, "refunded", o.Status)
	require.Zero(t, o.ClaimedAt)
	response = shopRequest(engine, "POST", "/api/store/claim/"+token, "", `{"pickup_code":"refund-pickup"}`)
	require.Equal(t, 403, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
	for _, suffix := range []string{"/complete", "/provider-complete", "/settle"} {
		response = shopRequest(engine, "POST", path+suffix, rootToken, `{"success":true}`)
		require.Equal(t, 404, response.Code)
	}
	response = shopRequest(engine, "POST", path+"/"+r.ID+"/reconcile", buyerToken, `{}`)
	require.Equal(t, 403, response.Code, "buyer cannot trigger seller's provider operation")
	response = shopRequest(engine, "POST", path+"/"+r.ID+"/reconcile", adminToken, `{}`)
	require.Equal(t, 403, response.Code)
	response = shopRequest(engine, "POST", path+"/"+r.ID+"/reconcile", rootToken, `{"completed":true}`)
	require.Equal(t, 422, response.Code, "no browser-supplied evidence")
}
