package router

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreFixedContentRouterSecretAndImportBoundaries(t *testing.T) {
	engine, db, sellerToken, seller, rootToken, root := merchantStoreTestRouter(t)
	shopPublishedProduct(t, db, seller, root)
	require.NoError(t, model.PrepareMerchantStoreSchema(db, 1))
	require.NoError(t, model.ActivateMerchantStoreVariants(db, 1))
	require.NoError(t, model.ActivateMerchantStoreProductLifecycle(db, 2))
	require.NoError(t, model.ActivateMerchantStoreRefunds(db, 3))
	require.NoError(t, model.ActivateMerchantStoreAccess(db, 4))
	require.NoError(t, model.PrepareMerchantStorePhaseSix(db, 5))
	require.NoError(t, model.ActivateMerchantStorePhaseSix(db, 5))
	require.NoError(t, model.PrepareMerchantStoreFixedContent(db, 6))
	content := strings.Repeat("PRIVATE-TUTORIAL-CONTENT\n", 300)
	input, err := json.Marshal(model.MerchantStoreProductInput{Title: "Reusable document", PriceQuota: 500000, Template: model.MerchantStoreFixedContentTemplate, FixedContent: &content, PaymentMethods: []string{"balance"}})
	require.NoError(t, err)
	response := shopRequest(engine, "POST", "/api/store/products", sellerToken, string(input))
	require.Equal(t, 503, response.Code, "prepared schema cannot admit fixed-content writes before activation")
	require.NotContains(t, response.Body.String(), "PRIVATE-TUTORIAL")
	require.NoError(t, model.ActivateMerchantStoreFixedContent(db, 6))
	_, err = model.SaveMerchantStoreSellerTerms(seller.Id, model.MerchantStoreTermsInput{Content: "The same document is supplied for each purchase."})
	require.NoError(t, err)
	response = shopRequest(engine, "POST", "/api/store/products", sellerToken, string(input))
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "PRIVATE-TUTORIAL")
	var result struct {
		Data model.MerchantStoreProduct `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	productID := result.Data.ID
	variantID := model.MerchantStoreDefaultVariantID(productID)
	privatePath := "/api/store/products/" + productID + "/variants/" + variantID + "/fixed-content"
	for _, auth := range []string{"", rootToken} {
		response = shopRequest(engine, "GET", privatePath, auth, "")
		require.NotEqual(t, 200, response.Code)
		require.NotContains(t, response.Body.String(), "PRIVATE-TUTORIAL")
	}
	response = shopRequest(engine, "GET", privatePath, sellerToken, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "PRIVATE-TUTORIAL")
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	response = shopRequest(engine, "GET", "/api/store/my/products/"+productID, sellerToken, "")
	require.Equal(t, 200, response.Code)
	require.NotContains(t, response.Body.String(), "PRIVATE-TUTORIAL")
	require.NoError(t, model.SubmitMerchantStoreProduct(seller.Id, productID))
	require.NoError(t, model.ReviewMerchantStoreProduct(root.Id, productID, true, ""))
	response = shopRequest(engine, "GET", "/api/store/products/"+productID, "", "")
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"unlimited_supply":true`)
	require.NotContains(t, response.Body.String(), "PRIVATE-TUTORIAL")
	response = shopRequest(engine, "POST", "/api/store/products/"+productID+"/variants/"+variantID+"/inventory", sellerToken, `{"items":["fake stock"]}`)
	require.Equal(t, 422, response.Code, response.Body.String())
	var rows int64
	require.NoError(t, db.Model(&model.MerchantStoreStock{}).Where("product_id = ?", productID).Count(&rows).Error)
	require.Zero(t, rows)
}
