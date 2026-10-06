package service

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func merchantStoreSelfPurchaseServiceFixture(t *testing.T, method string) merchantStoreServiceFixture {
	t.Helper()
	f := merchantStoreServiceDB(t, method)
	f.buyer = f.seller
	require.NoError(t, model.AcceptMerchantStoreDisclaimer(f.seller.Id, model.MerchantStoreDisclaimerVersion))
	return f
}

func merchantStoreSelfPurchaseCallbackBalances(t *testing.T, f merchantStoreServiceFixture, orderID string) {
	t.Helper()
	var seller, root model.User
	require.NoError(t, model.DB.First(&seller, f.seller.Id).Error)
	require.NoError(t, model.DB.First(&root, f.root.Id).Error)
	require.Equal(t, 9995000, seller.Quota, "external self-payment settles outside the wallet; only the normal fee is charged")
	require.Equal(t, 5000, root.Quota)
	var receipts, delivered int64
	require.NoError(t, model.DB.Model(&model.MerchantStorePaymentReceipt{}).Where("order_id = ?", orderID).Count(&receipts).Error)
	require.NoError(t, model.DB.Model(&model.MerchantStoreStock{}).Where("order_id = ? AND state = ?", orderID, "delivered").Count(&delivered).Error)
	require.EqualValues(t, 1, receipts)
	require.EqualValues(t, 1, delivered)
}

func TestMerchantStoreSelfPurchaseEpaySignedCallbackKeepsMoneyAndReplayGuards(t *testing.T) {
	f := merchantStoreSelfPurchaseServiceFixture(t, MerchantStoreExternalEpay)
	config := merchantStoreGatewayConfig{GatewayURL: "https://pay.example.com", PartnerID: "123", Key: "self-fixture-key", PaymentType: "alipay", Currency: "CNY"}
	order := merchantStoreTestOrder(t, f, MerchantStoreExternalEpay, config, 672, "CNY")
	require.Equal(t, f.seller.Id, order.BuyerID)
	contextSnapshot, err := loadMerchantStorePaymentContext(order)
	require.NoError(t, err)
	wrongMoney := merchantStoreTestEpaySigned(order, contextSnapshot, func(fields map[string]string) { fields["money"] = "0.01" })
	require.Error(t, HandleMerchantStoreEpayCallback(context.Background(), order.ID, wrongMoney))
	values := merchantStoreTestEpaySigned(order, contextSnapshot, nil)
	require.NoError(t, HandleMerchantStoreEpayCallback(context.Background(), order.ID, values))
	require.NoError(t, HandleMerchantStoreEpayCallback(context.Background(), order.ID, values))
	merchantStoreSelfPurchaseCallbackBalances(t, f, order.ID)
}

func TestMerchantStoreSelfPurchasePancakeSignedCallbackRequiresActualBuyerIdentity(t *testing.T) {
	f := merchantStoreSelfPurchaseServiceFixture(t, MerchantStoreExternalPancake)
	key, private := merchantStorePancakeTestKey(t)
	config := merchantStoreGatewayConfig{MerchantID: "MER_AbCdEfGhIjKlMnOpQrStUv", PrivateKey: private, StoreID: "STO_AbCdEfGhIjKlMnOpQrStUv", ProductID: "PROD_AbCdEfGhIjKlMnOpQrStUv", Environment: "prod", Currency: "USD"}
	order := merchantStoreTestOrder(t, f, MerchantStoreExternalPancake, config, 100, "USD")
	require.Equal(t, f.seller.Id, order.BuyerID)
	require.Positive(t, order.BuyerID)
	data := map[string]any{"orderId": "ORD_self_example", "orderStatus": "completed", "paymentStatus": "succeeded", "orderMerchantExternalId": order.TradeNo, "merchantProvidedBuyerIdentity": WaffoPancakeBuyerIdentityFromUserID(f.root.Id), "currency": "USD", "amount": "1.10", "taxAmount": "0.10", "orderMetadata": map[string]string{"lmm_store_order_id": order.ID, "lmm_store_product_id": order.ProductID, "lmm_pancake_product_id": config.ProductID, "lmm_store_seller_id": strconv.Itoa(order.SellerID)}}
	event := map[string]any{"id": "ORD_self_example", "eventId": "ORD_self_example-completed", "eventType": "order.completed", "mode": "prod", "storeId": config.StoreID, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "data": data}
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	require.Error(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", order.SellerID, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	data["merchantProvidedBuyerIdentity"] = WaffoPancakeBuyerIdentityFromUserID(order.BuyerID)
	data["amount"] = "0.10"
	payload, err = json.Marshal(event)
	require.NoError(t, err)
	require.Error(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", order.SellerID, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	data["amount"] = "1.10"
	payload, err = json.Marshal(event)
	require.NoError(t, err)
	require.NoError(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", order.SellerID, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	require.NoError(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", order.SellerID, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	merchantStoreSelfPurchaseCallbackBalances(t, f, order.ID)
}
