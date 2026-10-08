package router

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStorePurchaseLimitRouterValidatesAndRequiresReviewedFloor(t *testing.T) {
	engine, db, sellerToken, seller, _, root := merchantStoreTestRouter(t)
	p := shopPublishedProduct(t, db, seller, root)
	path := "/api/store/products/" + p.ID
	for _, field := range []string{"max_quantity_per_order", "max_quantity_per_buyer"} {
		for _, value := range []string{"0", "-1", "1.5", `"2"`, "true", "9007199254740992"} {
			body := fmt.Sprintf(`{"title":"Limits","price_quota":500000,"payment_methods":["balance"],"%s":%s}`, field, value)
			response := shopRequest(engine, "PUT", path, sellerToken, body)
			require.Equal(t, 422, response.Code, field+value+response.Body.String())
		}
	}
	response := shopRequest(engine, "PUT", path, sellerToken, `{"title":"Limits","price_quota":500000,"payment_methods":["balance"],"max_quantity_per_order":2}`)
	require.Equal(t, 503, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_UPGRADE_IN_PROGRESS")
	var stored model.MerchantStoreProduct
	require.NoError(t, db.First(&stored, "id = ?", p.ID).Error)
	require.Nil(t, stored.MaxQuantityPerOrder)
	require.Equal(t, "published", stored.Status)
	response = shopRequest(engine, "GET", "/api/store/config", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"product_purchase_limits_supported":false`)
}

func TestMerchantStorePurchaseLimitRouterRemainderIsPrivateAndCheckoutReturnsStableCode(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	p := shopPublishedProduct(t, db, seller, root)
	require.GreaterOrEqual(t, model.MerchantStoreWriterCapability, 4)
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	require.NoError(t, db.Model(p).Updates(map[string]any{"max_quantity_per_order": 1, "max_quantity_per_buyer": 2}).Error)
	require.NoError(t, db.Create(&model.MerchantStoreOrder{ID: "private-limit-history", TradeNo: "private-limit-trade", ProductID: p.ID, BuyerID: seller.Id, Quantity: 1, Status: "paid", PaidAt: 1}).Error)
	for _, test := range []struct {
		token     string
		remaining *int64
	}{
		{"", nil},
		{sellerToken, pointerStoreQuantity(1)},
		{rootToken, pointerStoreQuantity(2)},
	} {
		response := shopRequest(engine, "GET", "/api/store/products/"+p.ID, test.token, "")
		require.Equal(t, 200, response.Code, response.Body.String())
		var envelope struct {
			Data model.MerchantStoreProduct `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		require.Equal(t, test.remaining, envelope.Data.BuyerPurchaseRemaining)
		require.NotContains(t, response.Body.String(), "private-limit-history")
		require.NotContains(t, response.Body.String(), "PRIVATE-CARD")
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
	response := shopRequest(engine, "POST", "/api/store/orders", sellerToken, `{"product_id":"`+p.ID+`","quantity":2,"request_key":"limited-router","payment_method":"balance","pickup_code":"private-limit-code"}`)
	require.Equal(t, 409, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_PURCHASE_LIMIT")
}

func pointerStoreQuantity(value int64) *int64 { return &value }
