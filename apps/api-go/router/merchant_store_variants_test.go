package router

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreVariantsRouterSelectedSKUReviewAndSecretBoundaries(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	p := shopPublishedProduct(t, db, seller, root)
	require.NoError(t, model.ActivateMerchantStoreVariants(db, 1))
	response := shopRequest(engine, "POST", "/api/store/products/"+p.ID+"/variants", sellerToken, `{"name":"Plus 2 months","price_quota":1000000,"template":"card-key","enabled":true}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	var body struct {
		Data model.MerchantStoreVariant `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	v := body.Data
	require.NotEmpty(t, v.ID)
	require.Equal(t, p.ID, v.ProductID)
	require.Equal(t, 1000000, v.PriceQuota)
	response = shopRequest(engine, "GET", "/api/store/products/"+p.ID, "", "")
	require.Equal(t, 404, response.Code, "editing specs retires public review")
	stockPath := "/api/store/products/" + p.ID + "/variants/" + v.ID + "/inventory"
	response = shopRequest(engine, "POST", stockPath, sellerToken, `{"items":["PLUS-SECRET-ONE","PLUS-SECRET-TWO"]}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", stockPath, sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), v.ID)
	require.NotContains(t, response.Body.String(), "PLUS-SECRET")
	for _, token := range []string{"", rootToken} {
		response = shopRequest(engine, "GET", stockPath, token, "")
		require.NotEqual(t, 200, response.Code)
		require.NotContains(t, response.Body.String(), "PLUS-SECRET")
	}
	require.NoError(t, model.SubmitMerchantStoreProduct(seller.Id, p.ID))
	require.NoError(t, model.ReviewMerchantStoreProduct(root.Id, p.ID, true, "reviewed spec names"))
	response = shopRequest(engine, "GET", "/api/store/products/"+p.ID, "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Plus 2 months")
	require.NotContains(t, response.Body.String(), "PLUS-SECRET")
	require.NoError(t, model.AcceptMerchantStoreDisclaimer(root.Id, model.MerchantStoreDisclaimerVersion))
	base := `"product_id":"` + p.ID + `","quantity":1,"payment_method":"balance","pickup_code":"private-test-code"`
	response = shopRequest(engine, "POST", "/api/store/orders", rootToken, `{`+base+`,"request_key":"missing-sku"}`)
	require.Equal(t, 422, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_VARIANT_REQUIRED")
	response = shopRequest(engine, "POST", "/api/store/orders", rootToken, `{`+base+`,"variant_id":"`+v.ID+`","request_key":"selected-sku"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	var paid struct {
		Data struct {
			Order model.MerchantStoreOrder `json:"order"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &paid))
	require.Equal(t, v.ID, paid.Data.Order.VariantID)
	require.Equal(t, 1000000, paid.Data.Order.UnitPriceQuota)
	require.NotContains(t, response.Body.String(), "PLUS-SECRET")
	token, err := model.GetMerchantStoreOrderPickupToken(root.Id, paid.Data.Order.ID)
	require.NoError(t, err)
	response = shopRequest(engine, "GET", "/api/store/claim/"+token, "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Plus 2 months")
	require.NotContains(t, response.Body.String(), "PLUS-SECRET")
	response = shopRequest(engine, "POST", "/api/store/claim/"+token, rootToken, `{"pickup_code":"private-test-code"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "PLUS-SECRET-ONE")
	require.NotContains(t, response.Body.String(), "PRIVATE-CARD-ONE")
}

func TestMerchantStoreVariantsRouterGateAndExplicitBoolean(t *testing.T) {
	engine, db, sellerToken, seller, _, root := merchantStoreTestRouter(t)
	p := shopPublishedProduct(t, db, seller, root)
	path := "/api/store/products/" + p.ID + "/variants"
	response := shopRequest(engine, "POST", path, sellerToken, `{"name":"Plus","price_quota":1000000,"template":"card-key","enabled":true}`)
	require.Equal(t, 503, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_UPGRADE_IN_PROGRESS")
	require.NoError(t, model.ActivateMerchantStoreVariants(db, 1))
	v, err := model.SaveMerchantStoreVariant(seller.Id, p.ID, "", model.MerchantStoreVariantInput{Name: "Plus", PriceQuota: 1000000, Template: "card-key", Enabled: true})
	require.NoError(t, err)
	for _, body := range []string{`{}`, `{"enabled":null}`} {
		response = shopRequest(engine, "PUT", path+"/"+v.ID+"/enabled", sellerToken, body)
		require.Equal(t, 422, response.Code, response.Body.String())
	}
	response = shopRequest(engine, "PUT", path+"/"+v.ID+"/enabled", sellerToken, `{"enabled":false}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "POST", path+"/"+v.ID+"/inventory", sellerToken, `{"items":["RESTOCK-WHILE-DISABLED"]}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "RESTOCK-WHILE-DISABLED")
}
