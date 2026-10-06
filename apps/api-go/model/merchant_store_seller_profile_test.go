package model

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreSellerProfileProjectsOnlyActualSellerAndPublicContact(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Updates(map[string]any{
		"display_name": "Actual merchant", "email": "PRIVATE-ACCOUNT@example.test", "access_token": "PRIVATE-ACCESS-TOKEN",
	}).Error)
	require.NoError(t, DB.Model(f.product).Update("contact", "Sales desk <public-sales@example.test>").Error)
	product, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.NotNil(t, product.Seller)
	require.Equal(t, f.seller.Id, product.Seller.ID)
	require.NotEqual(t, f.buyer.Id, product.Seller.ID)
	require.Equal(t, f.seller.Username, product.Seller.Username)
	require.Equal(t, "Actual merchant", product.Seller.DisplayName)
	require.Equal(t, "public-sales@example.test", product.Seller.ContactEmail)
	serialized, err := json.Marshal(product.Seller)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "PRIVATE-ACCOUNT")
	require.NotContains(t, string(serialized), "PRIVATE-ACCESS")
	require.NotContains(t, string(serialized), "quota")
	var keys map[string]any
	require.NoError(t, json.Unmarshal(serialized, &keys))
	require.Len(t, keys, 4)

	for _, contact := range []string{"", "Contact via my website", "sales@example.test\r\nBcc: private@example.test", "first@example.test,second@example.test"} {
		require.NoError(t, DB.Model(f.product).Update("contact", contact).Error)
		profile, err := GetPublicMerchantStoreSellerProfile(f.seller.Id)
		require.NoError(t, err)
		require.Empty(t, profile.ContactEmail, "never fall back to the private account email")
	}
}

func TestMerchantStoreSellerFilterKeepsVisibilityAndFiltersBeforePagination(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Model(f.product).Updates(map[string]any{"contact": "public@example.test", "created_at": 1}).Error)
	for i, status := range []string{"draft", "pending", "paused", "unlisted", "deleted"} {
		copy := *f.product
		copy.ID, copy.Title, copy.Status, copy.Contact = status+"-profile-fixture", status, status, "HIDDEN-"+status+"@example.test"
		copy.CreatedAt = int64(20 + i)
		require.NoError(t, DB.Create(&copy).Error)
	}
	testProduct := *f.product
	testProduct.ID, testProduct.TestMode, testProduct.Contact = "private-test-profile", true, "PRIVATE-TEST@example.test"
	testProduct.CreatedAt = 30
	require.NoError(t, DB.Create(&testProduct).Error)
	other := *f.product
	other.ID, other.SellerID, other.Title, other.CreatedAt = "another-merchant-profile", f.buyer.Id, "Other merchant", 100
	other.Status = "published"
	require.NoError(t, DB.Create(&other).Error)
	rows, err := ListPublicMerchantStoreProductsForSeller("", f.seller.Id, 0, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, f.product.ID, rows[0].ID)
	profile, err := GetPublicMerchantStoreSellerProfile(f.seller.Id)
	require.NoError(t, err)
	require.Equal(t, "public@example.test", profile.ContactEmail)
	rows, err = ListPublicMerchantStoreProductsForSeller("missing", f.seller.Id, 0, 30)
	require.NoError(t, err)
	require.Empty(t, rows)
	for _, id := range []int{-1, 2147483648} {
		_, err := ListPublicMerchantStoreProductsForSeller("", id, 0, 30)
		require.ErrorIs(t, err, ErrMerchantStoreInput)
	}
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("status", common.UserStatusDisabled).Error)
	rows, err = ListPublicMerchantStoreProductsForSeller("", f.seller.Id, 0, 30)
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = GetPublicMerchantStoreSellerProfile(f.seller.Id)
	require.Error(t, err)

	// Account soft-deletion must be filtered before LIMIT, just like disabled
	// merchants; its product rows are retained for existing order history.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("status", common.UserStatusEnabled).Error)
	require.NoError(t, DB.Delete(&User{}, f.seller.Id).Error)
	rows, err = ListPublicMerchantStoreProductsForSeller("", 0, 0, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, other.ID, rows[0].ID)
}
