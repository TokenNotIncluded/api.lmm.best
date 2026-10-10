package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantStoreCatalogueStockTagsRespectFixedContentSalesQuota(t *testing.T) {
	f := newStoreFixedTestFixture(t, "balance")
	storeFixedNoStockWrites(t, DB)

	assertTag := func(tag string) *MerchantStoreProduct {
		t.Helper()
		product, err := GetPublicMerchantStoreProduct(f.product.ID)
		require.NoError(t, err)
		require.Contains(t, product.DisplayTags, tag)
		for _, other := range []string{"in_stock", "out_of_stock", "trading_paused"} {
			if other != tag {
				require.NotContains(t, product.DisplayTags, other)
			}
		}
		storeFixedStockless(t, f.product.ID)
		return product
	}

	unlimited := assertTag("in_stock")
	require.True(t, unlimited.UnlimitedSupply)
	require.Nil(t, unlimited.SaleLimit)
	require.Zero(t, unlimited.SaleAvailable, "unbounded supply is not a physical stock count")
	rows, err := ListMerchantStoreCatalogue(f.buyer.Id, "Shared tutorial", 0, 0, 20, MerchantStoreCatalogueQuery{Stock: "in_stock"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Contains(t, rows[0].DisplayTags, "in_stock", "stock-filter results and labels must agree")

	quota := int64(5)
	require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, &quota))
	finite := assertTag("in_stock")
	require.EqualValues(t, 5, finite.SaleAvailable)
	_, _, err = CreateMerchantStoreOrder(storeFixedCheckout(t, f, "stock-status-paid", "balance"))
	require.NoError(t, err)
	require.EqualValues(t, 4, assertTag("in_stock").SaleAvailable)

	quota = 0
	require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, &quota))
	paused := assertTag("trading_paused")
	require.True(t, paused.TradingPaused)
	_, _, err = CreateMerchantStoreOrder(storeFixedCheckout(t, f, "stock-status-stopped", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreStock, "a label correction must not bypass a zero sales quota")

	require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, nil))
	unlimited = assertTag("in_stock")
	require.False(t, unlimited.TradingPaused)
	require.Nil(t, unlimited.SaleLimit)
	_, _, err = CreateMerchantStoreOrder(storeFixedCheckout(t, f, "stock-status-restored", "balance"))
	require.NoError(t, err)
	storeFixedStockless(t, f.product.ID)
}

func TestMerchantStoreCatalogueDistinguishesPausedInventoryFromEmptySupply(t *testing.T) {
	f := newStoreFixedTestFixture(t, "balance")
	var stockedProduct MerchantStoreProduct
	require.NoError(t, DB.Where("seller_id = ? AND template = ?", f.seller.Id, "card-key").First(&stockedProduct).Error)
	f.product = &stockedProduct
	product, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.Positive(t, product.InventoryAvailable)
	inventory := product.InventoryAvailable

	quota := int64(10)
	require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, &quota))
	require.NoError(t, DB.Model(&MerchantStoreConfig{}).Where("id = ?", 1).
		Update("minimum_unit_price_quota", product.PriceQuota+1).Error)
	product, err = GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.True(t, product.TradingPaused)
	require.Equal(t, inventory, product.InventoryAvailable)
	require.Zero(t, product.SaleAvailable)
	require.Contains(t, product.DisplayTags, "trading_paused")
	require.NotContains(t, product.DisplayTags, "out_of_stock")
	_, _, err = CreateMerchantStoreOrder(storeFixedCheckout(t, f, "stock-status-below-minimum", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreMinimumPrice, "real stock does not bypass minimum-price checks")

	rows, err := ListMerchantStoreCatalogue(f.buyer.Id, f.product.Title, 0, 0, 20, MerchantStoreCatalogueQuery{Stock: "in_stock"})
	require.NoError(t, err)
	require.Empty(t, rows, "the in-stock filter still requires proven purchase availability")
	rows, err = ListMerchantStoreCatalogue(f.buyer.Id, f.product.Title, 0, 0, 20, MerchantStoreCatalogueQuery{Stock: "out_of_stock"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Contains(t, rows[0].DisplayTags, "trading_paused", "the unavailable filter includes paused inventory")

	require.NoError(t, DB.Model(&MerchantStoreConfig{}).Where("id = ?", 1).
		Update("minimum_unit_price_quota", product.PriceQuota).Error)
	product, err = GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.Contains(t, product.DisplayTags, "in_stock")

	require.NoError(t, DB.Where("product_id = ?", f.product.ID).Delete(&MerchantStoreStock{}).Error)
	for _, limit := range []*int64{&quota, nil} {
		require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, limit))
		product, err = GetPublicMerchantStoreProduct(f.product.ID)
		require.NoError(t, err)
		require.False(t, product.UnlimitedSupply)
		require.Zero(t, product.InventoryAvailable)
		require.Contains(t, product.DisplayTags, "out_of_stock", "changing sales quota never manufactures inventory")
	}
}
