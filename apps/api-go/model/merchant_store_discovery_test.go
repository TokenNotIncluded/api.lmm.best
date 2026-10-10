package model

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMerchantStoreDiscoveryRestrictsOwnerTestsAndRetiredProducts(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storeSetTestMode(t, &f, true)
	for _, status := range []string{"draft", "pending", "published"} {
		require.NoError(t, DB.Model(f.product).Update("status", status).Error)
		product, err := GetMerchantStoreProductForViewer(f.seller.Id, f.product.ID)
		require.NoError(t, err)
		require.True(t, product.TestMode)
		require.Empty(t, product.ReviewNote)
		rows, err := ListMerchantStoreProductsForViewer(f.seller.Id, f.product.Title, 0, 1)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		for _, viewer := range []int{0, f.buyer.Id, f.root.Id} {
			_, err = GetMerchantStoreProductForViewer(viewer, f.product.ID)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			rows, err = ListMerchantStoreProductsForViewer(viewer, "", 0, 30)
			require.NoError(t, err)
			require.Empty(t, rows)
		}
	}
	for _, status := range []string{"paused", "off_shelf", "unlisted", "deleted", "rejected"} {
		require.NoError(t, DB.Model(f.product).Update("status", status).Error)
		_, err := GetMerchantStoreProductForViewer(f.seller.Id, f.product.ID)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		rows, err := ListMerchantStoreProductsForViewer(f.seller.Id, "", 0, 30)
		require.NoError(t, err)
		require.Empty(t, rows)
	}
	require.NoError(t, DB.Model(f.product).Update("status", "published").Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("status", common.UserStatusDisabled).Error)
	_, err := GetMerchantStoreProductForViewer(f.seller.Id, f.product.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	rows, err := ListMerchantStoreProductsForViewer(f.seller.Id, "", 0, 30)
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestMerchantStoreDiscoveryFiltersBeforePaginationAndUsesExactVariants(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, ActivateMerchantStoreVariants(DB, 1))
	variant := storeTestVariant(t, f, "任意商家规格 / 93天", 875432, "PRIVATE-THIS-SKU")
	storePublishVariants(t, f)
	disabled := marketTestUser(t, DB, "hidden-catalog-seller", 1000000, common.RoleCommonUser)
	require.NoError(t, DB.Model(&disabled).Update("status", common.UserStatusDisabled).Error)
	for _, hidden := range []MerchantStoreProduct{
		{ID: uuid.NewString(), SellerID: disabled.Id, Title: "Hidden disabled seller", Status: "published", PromotionExpiresAt: common.GetTimestamp() + 9000},
		{ID: uuid.NewString(), SellerID: f.seller.Id, Title: "Hidden retired", Status: "deleted", PromotionExpiresAt: common.GetTimestamp() + 10000},
		{ID: uuid.NewString(), SellerID: f.seller.Id, Title: "Hidden test", Status: "published", TestMode: true, PromotionExpiresAt: common.GetTimestamp() + 11000},
	} {
		require.NoError(t, DB.Create(&hidden).Error)
	}
	rows, err := ListMerchantStoreProductsForViewer(f.buyer.Id, "", 0, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1, "invisible listings must not consume a page slot")
	require.Equal(t, f.product.ID, rows[0].ID)
	var actual *MerchantStoreVariant
	for index := range rows[0].Variants {
		if rows[0].Variants[index].ID == variant.ID {
			actual = &rows[0].Variants[index]
		}
	}
	require.NotNil(t, actual)
	require.Equal(t, "任意商家规格 / 93天", actual.Name)
	require.Equal(t, 875432, actual.PriceQuota)
	require.EqualValues(t, 1, actual.InventoryAvailable)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "PRIVATE-THIS-SKU")
	require.NotContains(t, string(encoded), "private reviewer note")
	rows, err = ListMerchantStoreProductsForViewer(f.buyer.Id, "%_", 0, 30)
	require.NoError(t, err)
	require.Empty(t, rows, "search wildcards remain literal")
}
