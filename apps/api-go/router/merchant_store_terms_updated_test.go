package router

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreTermsUpdatedProofFollowsGuestReplayAndExactAuthority(t *testing.T) {
	engine, db, sellerToken, seller, _, root := merchantStoreTestRouter(t)
	p := shopPublishedProduct(t, db, seller, root)
	require.GreaterOrEqual(t, model.MerchantStoreWriterCapability, 5, "requires the centrally qualified phase-5 candidate")
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "5").Error)
	require.True(t, model.MerchantStoreAccessSupported())
	terms, err := model.SaveMerchantStoreSellerTerms(seller.Id, model.MerchantStoreTermsInput{Content: "Merchant terms for the selected card specification."})
	require.NoError(t, err)
	public, no := "public", false
	in := shopTestModeInput(p, nil)
	in.Visibility, in.PurchaseLoginRequired, in.PickupLoginRequired = &public, &no, false
	p, err = model.SaveMerchantStoreProduct(seller.Id, p.ID, in)
	require.NoError(t, err)
	require.NoError(t, model.SubmitMerchantStoreProduct(seller.Id, p.ID))
	require.NoError(t, model.ReviewMerchantStoreProduct(root.Id, p.ID, true, ""))
	bps := 10000
	promotion, err := model.SaveMerchantStoreDiscountCode(seller.Id, p.ID, "", model.MerchantStoreDiscountCodeInput{DiscountBPS: &bps})
	require.NoError(t, err)
	a, err := model.CreateMerchantStoreGuestSession()
	require.NoError(t, err)
	b, err := model.CreateMerchantStoreGuestSession()
	require.NoError(t, err)
	for _, token := range []string{a.Token, b.Token} {
		require.NoError(t, model.AcceptMerchantStoreGuestDisclaimer(token, model.MerchantStoreDisclaimerVersion))
	}
	input := map[string]any{"product_id": p.ID, "quantity": 1, "request_key": "same-key-for-two-guests", "payment_method": "free", "promotion_code": promotion.Code, "pickup_code": "private-pickup-code", "seller_terms_version": terms.Version, "accept_seller_terms": true}
	body, err := json.Marshal(input)
	require.NoError(t, err)
	path := "/api/store/guest/orders"
	response := storeAccessRequest(engine, "POST", path, sellerToken, a.Token, string(body))
	require.Equal(t, 200, response.Code, response.Body.String())
	first, err := model.FindMerchantStoreGuestOrderByRequestKey(a.Token, input["request_key"].(string))
	require.NoError(t, err)
	require.Equal(t, "paid", first.Status)
	revised, err := model.SaveMerchantStoreSellerTerms(seller.Id, model.MerchantStoreTermsInput{Content: "Revised merchant card support terms.", ExpectedVersion: terms.Version})
	require.NoError(t, err)
	response = storeAccessRequest(engine, "POST", path, sellerToken, a.Token, string(body))
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "order_created")
	replay, err := model.FindMerchantStoreGuestOrderByRequestKey(a.Token, input["request_key"].(string))
	require.NoError(t, err)
	require.Equal(t, first.ID, replay.ID)
	require.Equal(t, "paid", replay.Status)
	response = storeAccessRequest(engine, "POST", path, sellerToken, b.Token, string(body))
	require.Equal(t, 409, response.Code, response.Body.String())
	var proof struct {
		Success      bool   `json:"success"`
		Code         string `json:"code"`
		RequestKey   string `json:"request_key"`
		OrderCreated *bool  `json:"order_created"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &proof))
	require.False(t, proof.Success)
	require.Equal(t, "STORE_TERMS_UPDATED", proof.Code)
	require.Equal(t, input["request_key"], proof.RequestKey)
	require.NotNil(t, proof.OrderCreated)
	require.False(t, *proof.OrderCreated)
	_, err = model.FindMerchantStoreGuestOrderByRequestKey(b.Token, proof.RequestKey)
	require.Error(t, err)
	response = storeAccessRequest(engine, "POST", path, sellerToken, "invalid-guest-proof", string(body))
	require.Equal(t, 403, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "order_created")
	require.NotContains(t, response.Body.String(), "request_key")
	input["seller_terms_version"] = revised.Version
	newBody, err := json.Marshal(input)
	require.NoError(t, err)
	response = storeAccessRequest(engine, "POST", path, sellerToken, a.Token, string(newBody))
	require.Equal(t, 409, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_CONFLICT")
	require.NotContains(t, response.Body.String(), "order_created")
	input["accept_seller_terms"] = false
	newBody, err = json.Marshal(input)
	require.NoError(t, err)
	response = storeAccessRequest(engine, "POST", path, sellerToken, b.Token, string(newBody))
	require.Equal(t, 409, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_SELLER_TERMS_REQUIRED")
	require.NotContains(t, response.Body.String(), "order_created")
	input["accept_seller_terms"] = true
	newBody, err = json.Marshal(input)
	require.NoError(t, err)
	response = storeAccessRequest(engine, "POST", path, sellerToken, b.Token, string(newBody))
	require.Equal(t, 200, response.Code, response.Body.String())
	second, err := model.FindMerchantStoreGuestOrderByRequestKey(b.Token, input["request_key"].(string))
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, "paid", second.Status)
	var transfers int64
	require.NoError(t, db.Model(&model.MerchantStoreTransfer{}).Count(&transfers).Error)
	require.Zero(t, transfers)
}
