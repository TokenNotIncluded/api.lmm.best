package router

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreLinkPresetsRootWritesAndPublicReadContract(t *testing.T) {
	engine, db, sellerToken, _, rootToken, root := merchantStoreTestRouter(t)
	adminToken := "shop-link-preset-admin"
	admin := model.User{Username: "preset-admin", AffCode: "preset-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AccessToken: &adminToken}
	require.NoError(t, db.Create(&admin).Error)
	input := `{"presets":[{"id":"custom-docs","title":"Merchant documentation","url":"https://docs.example.test/custom","description":"Public instructions"}]}`
	for _, token := range []string{"", sellerToken, adminToken} {
		response := shopRequest(engine, "PUT", "/api/store/product-link-presets", token, input)
		require.NotEqual(t, 200, response.Code, response.Body.String())
	}
	response := shopRequest(engine, "PUT", "/api/store/product-link-presets", rootToken, input)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = shopRequest(engine, "GET", "/api/store/config", "", "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	var public []map[string]any
	require.NoError(t, json.Unmarshal(body.Data["product_link_presets"], &public))
	require.Equal(t, []map[string]any{{"id": "custom-docs", "title": "Merchant documentation", "url": "https://docs.example.test/custom", "description": "Public instructions"}}, public)
	require.NotContains(t, body.Data, "recipient_id")
	require.NotContains(t, response.Body.String(), rootToken)
	for _, invalid := range []string{`{}`, `{"presets":null}`, `{"presets":[],"recipient_id":999}`, `{"presets":[]} {}`, `{"presets":[{"id":"a","title":"Guide","url":"https://secret@example.test","secret":"private"}]}`} {
		response = shopRequest(engine, "PUT", "/api/store/product-link-presets", rootToken, invalid)
		require.Equal(t, 422, response.Code, response.Body.String())
		stored, err := model.GetMerchantStoreLinkPresets()
		require.NoError(t, err)
		require.Len(t, stored, 1)
	}
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", root.Id).Update("role", common.RoleCommonUser).Error)
	response = shopRequest(engine, "PUT", "/api/store/product-link-presets", rootToken, `{"presets":[]}`)
	require.Equal(t, 403, response.Code, response.Body.String())
}
