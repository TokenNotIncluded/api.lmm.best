package router

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreWriterGateRouterReturnsUpgradeStatusAndKeepsPaidPickup(t *testing.T) {
	engine, db, _, seller, rootToken, root := merchantStoreTestRouter(t)
	p := shopPublishedProduct(t, db, seller, root)
	require.NoError(t, model.AcceptMerchantStoreDisclaimer(root.Id, model.MerchantStoreDisclaimerVersion))
	o, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: root.Id, ProductID: p.ID, Quantity: 1, RequestKey: "pre-upgrade", PaymentMethod: "balance", PickupCode: "private-test-code"})
	require.NoError(t, err)
	token, err := model.GetMerchantStoreOrderPickupToken(root.Id, o.ID)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "2").Error)
	response := shopRequest(engine, "POST", "/api/store/orders", rootToken, `{"product_id":"`+p.ID+`","quantity":1,"request_key":"new-after-upgrade","payment_method":"balance","pickup_code":"private-test-code"}`)
	require.Equal(t, 503, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_UPGRADE_IN_PROGRESS")
	response = shopRequest(engine, "PUT", "/api/store/config", rootToken, `{"minimum_unit_price_quota":750000}`)
	require.Equal(t, 503, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_UPGRADE_IN_PROGRESS")
	config, err := model.GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Equal(t, 500000, config.MinimumUnitPriceQuota)
	response = shopRequest(engine, "GET", "/api/store/products/"+p.ID, "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD-ONE")
	response = shopRequest(engine, "POST", "/api/store/claim/"+token, rootToken, `{"pickup_code":"private-test-code"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "PRIVATE-CARD-ONE")
	var count int64
	require.NoError(t, db.Model(&model.MerchantStoreOrder{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
