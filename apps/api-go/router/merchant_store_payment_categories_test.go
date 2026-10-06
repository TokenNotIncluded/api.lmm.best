package router

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreCategoryAPIAllowsL0AndStrictlyOwnsTwoMasterFlags(t *testing.T) {
	engine, db, token, seller, rootToken, _ := merchantStoreTestRouter(t)
	require.NoError(t, db.Model(&seller).Update("trust_level_override", 0).Error)
	response := shopRequest(engine, "PUT", "/api/store/payments/categories", "", `{"platform_enabled":true,"external_enabled":false}`)
	require.NotEqual(t, 200, response.Code)
	for _, body := range []string{`{}`, `null`, `{"platform_enabled":true}`, `{"platform_enabled":null,"external_enabled":false}`, `{"platform_enabled":true,"external_enabled":false,"seller_id":999}`, `{"platform_enabled":true,"external_enabled":false} {}`, `{"platform_enabled":"true","external_enabled":false}`} {
		response = shopRequest(engine, "PUT", "/api/store/payments/categories", token, body)
		require.Equal(t, 422, response.Code, response.Body.String())
	}
	response = shopRequest(engine, "PUT", "/api/store/payments/categories", token, `{"platform_enabled":true,"external_enabled":false}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	categories, err := model.GetMerchantStorePaymentCategories(seller.Id)
	require.NoError(t, err)
	require.True(t, categories.PlatformEnabled)
	require.False(t, categories.ExternalEnabled)
	response = shopRequest(engine, "GET", "/api/store/payments/settings", token, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "category:platform")
	var body struct {
		Data struct {
			Categories model.MerchantStorePaymentCategories `json:"categories"`
			Items      []struct {
				Provider  string `json:"provider"`
				Category  string `json:"category"`
				Effective bool   `json:"effective_enabled"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, categories, body.Data.Categories)
	require.Len(t, body.Data.Items, 5)
	for _, item := range body.Data.Items {
		require.NotEmpty(t, item.Category)
		require.False(t, item.Effective)
	}
	// Admin can review listings, but global prices are Root-only.
	require.NoError(t, db.Model(&seller).Update("role", common.RoleAdminUser).Error)
	response = shopRequest(engine, "PUT", "/api/store/promotion-config", token, `{"promotion_quota":123}`)
	require.Equal(t, 403, response.Code, response.Body.String())
	response = shopRequest(engine, "PUT", "/api/store/promotion-config", rootToken, `{"promotion_quota":123}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/config", token, "")
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"promotion_quota":123`)
	require.NotContains(t, response.Body.String(), "recipient_id")
}
