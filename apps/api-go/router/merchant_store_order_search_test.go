package router

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreRouterPublicSearchNeedsMailboxProofAndKeepsPickupProtection(t *testing.T) {
	engine, db, sellerToken, seller, _, root := merchantStoreTestRouter(t)
	product := shopPublishedProduct(t, db, seller, root)
	buyerToken := "search-buyer-token"
	buyer := model.User{Username: "search-buyer", AffCode: "search-buyer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AccessToken: &buyerToken, Quota: 2000000}
	require.NoError(t, db.Create(&buyer).Error)
	require.NoError(t, model.AcceptMerchantStoreDisclaimer(buyer.Id, model.MerchantStoreDisclaimerVersion))
	order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: buyer.Id, ProductID: product.ID, Quantity: 1, RequestKey: "search-order", PaymentMethod: "balance", PickupCode: "buyer-private-code", PickupEmail: "order-mailbox@example.test"})
	require.NoError(t, err)
	origin := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example.test"
	t.Cleanup(func() { system_setting.ServerAddress = origin })
	for _, auth := range []string{"", sellerToken} {
		r := shopRequest(engine, "GET", "/api/store/order-search/"+order.TradeNo, auth, "")
		require.Equal(t, 200, r.Code, r.Body.String())
		require.Contains(t, r.Body.String(), order.TradeNo)
		for _, secret := range []string{order.ID, "order-mailbox", "PRIVATE-CARD", "pickup_url", "price_quota", "buyer_id", "seller_id"} {
			require.NotContains(t, r.Body.String(), secret)
		}
	}
	r := shopRequest(engine, "POST", "/api/store/order-search", "", `{"email":"order-mailbox@example.test"}`)
	require.Equal(t, 403, r.Code, r.Body.String())
	challengeID, code, _, err := model.BeginMerchantStoreOrderSearch("other-mailbox@example.test")
	require.NoError(t, err)
	r = shopRequest(engine, "POST", "/api/store/order-search/email/confirm", "", `{"challenge_id":"`+challengeID+`","code":"`+code+`"}`)
	require.Equal(t, 200, r.Code, r.Body.String())
	var confirmation struct {
		Data struct {
			Token string `json:"search_token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &confirmation))
	r = shopRequest(engine, "POST", "/api/store/order-search", "", `{"search_token":"`+confirmation.Data.Token+`","email":"order-mailbox@example.test"}`)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), `"items":[]`)
	challengeID, code, _, err = model.BeginMerchantStoreOrderSearch("order-mailbox@example.test")
	require.NoError(t, err)
	r = shopRequest(engine, "POST", "/api/store/order-search/email/confirm", "", `{"challenge_id":"`+challengeID+`","code":"`+code+`"}`)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &confirmation))
	r = shopRequest(engine, "POST", "/api/store/order-search", "", `{"search_token":"`+confirmation.Data.Token+`"}`)
	require.Equal(t, 200, r.Code, r.Body.String())
	var results struct {
		Data struct {
			Items []struct {
				PickupURL string `json:"pickup_url"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &results))
	require.Len(t, results.Data.Items, 1)
	token := strings.TrimPrefix(results.Data.Items[0].PickupURL, "https://api.example.test/store/claim/")
	require.Len(t, token, 43)
	// Mailbox proof exposes only a link: it does not satisfy order login or code.
	for _, auth := range []string{"", sellerToken} {
		r = shopRequest(engine, "POST", "/api/store/claim/"+token, auth, `{"pickup_code":"buyer-private-code"}`)
		require.NotEqual(t, 200, r.Code)
		require.NotContains(t, r.Body.String(), "PRIVATE-CARD")
	}
	r = shopRequest(engine, "POST", "/api/store/claim/"+token, buyerToken, `{"pickup_code":"wrong-code"}`)
	require.NotEqual(t, 200, r.Code)
	r = shopRequest(engine, "POST", "/api/store/claim/"+token, buyerToken, `{"pickup_code":"buyer-private-code"}`)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), "PRIVATE-CARD-ONE")
	// Pre-upgrade predictable numbers retain their original account boundary.
	legacyTrade := "MS" + order.ID[:30]
	require.NoError(t, db.Model(order).Update("trade_no", legacyTrade).Error)
	for _, auth := range []string{"", sellerToken, buyerToken} {
		r = shopRequest(engine, "GET", "/api/store/order-search/"+legacyTrade, auth, "")
		if auth == "" {
			require.Equal(t, 404, r.Code, r.Body.String())
		} else {
			require.Equal(t, 200, r.Code, r.Body.String())
		}
	}
}
