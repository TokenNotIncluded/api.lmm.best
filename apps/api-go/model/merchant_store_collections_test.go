package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMerchantStoreCollectionsAccountIsolationMergeFreshFactsAndNoReservation(t *testing.T) {
	f := storeCatalogueFixture(t)
	item, err := SetMerchantStoreCartItem(f.buyer.Id, MerchantStoreCartInput{ProductID: f.product.ID, Quantity: 1})
	require.NoError(t, err)
	merged, err := SetMerchantStoreCartItem(f.buyer.Id, MerchantStoreCartInput{ProductID: f.product.ID, VariantID: MerchantStoreDefaultVariantID(f.product.ID), Quantity: 2})
	require.NoError(t, err)
	require.Equal(t, item.ID, merged.ID)
	require.EqualValues(t, 2, merged.Quantity)
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreCartItem{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("state = ?", "reserved").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, SetMerchantStoreFavorite(f.buyer.Id, f.product.ID))
	require.NoError(t, SetMerchantStoreFavorite(f.buyer.Id, f.product.ID))
	require.NoError(t, DB.Model(&MerchantStoreFavorite{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, DeleteMerchantStoreCartItem(f.root.Id, item.ID))
	own, err := ListMerchantStoreCart(f.buyer.Id, 0, 30)
	require.NoError(t, err)
	require.Len(t, own, 1)
	foreign, err := ListMerchantStoreCart(f.root.Id, 0, 30)
	require.NoError(t, err)
	require.Empty(t, foreign)
	_, err = ListMerchantStoreCart(0, 0, 30)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = SetMerchantStoreCartItem(0, MerchantStoreCartInput{ProductID: f.product.ID, Quantity: 1})
	require.Error(t, err)
	_, err = SetMerchantStoreCartItem(f.buyer.Id, MerchantStoreCartInput{ProductID: f.product.ID, Quantity: 0})
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	_, err = SetMerchantStoreCartItem(f.buyer.Id, MerchantStoreCartInput{ProductID: f.product.ID, Quantity: 3})
	require.ErrorIs(t, err, ErrMerchantStoreStock)
	// Previously stored carts must refresh changed quantity limits and stock.
	max := int64(1)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("max_quantity_per_order", max).Error)
	own, err = ListMerchantStoreCart(f.buyer.Id, 0, 30)
	require.NoError(t, err)
	require.Len(t, own, 1)
	require.False(t, own[0].Valid)
	require.Equal(t, "purchase_limit", *own[0].UnavailableReason)
	require.NotNil(t, own[0].Product)
	require.EqualValues(t, 1, *own[0].Product.MaxQuantityPerOrder)
	require.NoError(t, ClearMerchantStoreCollections(f.buyer.Id, true))
	own, err = ListMerchantStoreCart(f.buyer.Id, 0, 30)
	require.NoError(t, err)
	require.Empty(t, own)
}

func TestMerchantStoreCollectionsHideRetiredProductsAndCleanupOnlyOwnInvalidEntries(t *testing.T) {
	f := storeCatalogueFixture(t)
	_, err := SetMerchantStoreCartItem(f.buyer.Id, MerchantStoreCartInput{ProductID: f.product.ID, Quantity: 1})
	require.NoError(t, err)
	require.NoError(t, SetMerchantStoreFavorite(f.buyer.Id, f.product.ID))
	other := marketTestUser(t, DB, "collection-other", 1000000, common.RoleCommonUser)
	_, err = SetMerchantStoreCartItem(other.Id, MerchantStoreCartInput{ProductID: f.product.ID, Quantity: 1})
	require.NoError(t, err)
	require.NoError(t, SetMerchantStoreFavorite(other.Id, f.product.ID))
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("product_id = ?", f.product.ID).Update("state", "reserved").Error)
	cartDeleted, favDeleted, err := CleanupMerchantStoreCollections(f.buyer.Id)
	require.NoError(t, err)
	require.Zero(t, cartDeleted)
	require.Zero(t, favDeleted)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("status", "deleted").Error)
	items, err := ListMerchantStoreCart(f.buyer.Id, 0, 30)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.False(t, items[0].Valid)
	require.Nil(t, items[0].Product)
	require.Equal(t, "not_visible", *items[0].UnavailableReason)
	_, err = SetMerchantStoreCartItem(f.buyer.Id, MerchantStoreCartInput{ProductID: f.product.ID, Quantity: 1})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	cartDeleted, favDeleted, err = CleanupMerchantStoreCollections(f.buyer.Id)
	require.NoError(t, err)
	require.EqualValues(t, 1, cartDeleted)
	require.EqualValues(t, 1, favDeleted)
	var otherCart int64
	require.NoError(t, DB.Model(&MerchantStoreCartItem{}).Where("user_id = ?", other.Id).Count(&otherCart).Error)
	require.EqualValues(t, 1, otherCart)
}
