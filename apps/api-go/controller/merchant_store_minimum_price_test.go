package controller

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func merchantStoreMinimumControllerRoot(t *testing.T) model.User {
	t.Helper()
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	require.NoError(t, model.BootstrapMerchantStoreWriterGate(db))
	require.NoError(t, db.AutoMigrate(model.MerchantStoreModels()...))
	root := model.User{Username: "minimum-controller-root", AffCode: "minimum-controller-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&root).Error)
	return root
}

func TestMerchantStoreMinimumPricePublicConfigDefaultAndFirstRootZero(t *testing.T) {
	root := merchantStoreMinimumControllerRoot(t)
	response := merchantStoreControllerRequest(t, 0, `{}`, GetMerchantStoreConfig)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Minimum *int `json:"minimum_unit_price_quota"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.NotNil(t, payload.Data.Minimum)
	require.Equal(t, 500000, *payload.Data.Minimum)
	var count int64
	require.NoError(t, model.DB.Model(&model.MerchantStoreConfig{}).Count(&count).Error)
	require.Zero(t, count)
	response = merchantStoreControllerRequest(t, root.Id, `{"minimum_unit_price_quota":0}`, SetMerchantStoreConfig)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	config, err := model.GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Zero(t, config.MinimumUnitPriceQuota)
	require.Equal(t, 100, config.FeeBPS)
	require.Equal(t, 500000, config.PromotionQuota)
	require.Equal(t, root.Id, config.RecipientID)
	response = merchantStoreControllerRequest(t, 0, `{}`, GetMerchantStoreConfig)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	payload.Data.Minimum = nil
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.NotNil(t, payload.Data.Minimum, "zero must still be included in public configuration")
	require.Zero(t, *payload.Data.Minimum)
}

func TestMerchantStoreMinimumPriceRootPatchPreservesOmittedFloorAndOtherFields(t *testing.T) {
	root := merchantStoreMinimumControllerRoot(t)
	require.NoError(t, model.SetMerchantStoreConfig(root.Id, model.MerchantStoreConfig{
		FeeBPS: 100, RecipientID: root.Id, PromotionQuota: 500000,
		LinuxDOUnitsPerUSD: "2.5", MinimumUnitPriceQuota: 500000,
	}))
	response := merchantStoreControllerRequest(t, root.Id, `{"fee_bps":325,"promotion_quota":750000}`, SetMerchantStoreConfig)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	config, err := model.GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Equal(t, 500000, config.MinimumUnitPriceQuota)
	require.Equal(t, 325, config.FeeBPS)
	require.Equal(t, 750000, config.PromotionQuota)
	require.Equal(t, "2.5", config.LinuxDOUnitsPerUSD)
	response = merchantStoreControllerRequest(t, root.Id, `{"minimum_unit_price_quota":0}`, SetMerchantStoreConfig)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = merchantStoreControllerRequest(t, root.Id, `{"fee_bps":0,"linuxdo_units_per_usd":""}`, SetMerchantStoreConfig)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	config, err = model.GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Zero(t, config.MinimumUnitPriceQuota)
	require.Zero(t, config.FeeBPS)
	require.Empty(t, config.LinuxDOUnitsPerUSD)
	require.Equal(t, 750000, config.PromotionQuota)
	require.Equal(t, root.Id, config.RecipientID)
}

func TestMerchantStoreMinimumPriceConfigRejectsNonRootAndInvalidBounds(t *testing.T) {
	root := merchantStoreMinimumControllerRoot(t)
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser} {
		name := "minimum-config-role-" + strconv.Itoa(role)
		actor := model.User{Username: name, AffCode: name, Role: role, Status: common.UserStatusEnabled}
		require.NoError(t, model.DB.Create(&actor).Error)
		response := merchantStoreControllerRequest(t, actor.Id, `{"minimum_unit_price_quota":0}`, SetMerchantStoreConfig)
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "STORE_ACCESS_DENIED")
	}
	for _, minimum := range []int{-1, common.MaxWalletQuota + 1} {
		body := `{"minimum_unit_price_quota":` + strconv.Itoa(minimum) + `}`
		response := merchantStoreControllerRequest(t, root.Id, body, SetMerchantStoreConfig)
		require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "STORE_INVALID_INPUT")
	}
	config, err := model.GetMerchantStoreConfig()
	require.NoError(t, err)
	require.Equal(t, 500000, config.MinimumUnitPriceQuota)
}

func TestMerchantStoreMinimumPriceProductSaveReturnsPolicyCodeAndZeroFloorKeepsPositivePrices(t *testing.T) {
	root := merchantStoreMinimumControllerRoot(t)
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "minimum-controller-fixture-encryption-key-20261006-123456789")
	seller := model.User{Username: "minimum-controller-seller", AffCode: "minimum-controller-seller", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&seller).Error)
	require.NoError(t, model.SetMerchantStoreConfig(root.Id, model.MerchantStoreConfig{
		FeeBPS: 100, RecipientID: root.Id, PromotionQuota: 500000, MinimumUnitPriceQuota: 500000,
	}))
	response := merchantStoreControllerRequest(t, seller.Id, `{"title":"Below minimum","price_quota":499999}`, SaveMerchantStoreProduct)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_MINIMUM_PRICE")
	var count int64
	require.NoError(t, model.DB.Model(&model.MerchantStoreProduct{}).Count(&count).Error)
	require.Zero(t, count)
	response = merchantStoreControllerRequest(t, root.Id, `{"minimum_unit_price_quota":0}`, SetMerchantStoreConfig)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = merchantStoreControllerRequest(t, seller.Id, `{"title":"One quota unit","price_quota":1}`, SaveMerchantStoreProduct)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = merchantStoreControllerRequest(t, seller.Id, `{"title":"Zero price","price_quota":0}`, SaveMerchantStoreProduct)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "STORE_INVALID_INPUT")
	require.NoError(t, model.DB.Model(&model.MerchantStoreProduct{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
