package model

import (
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func storeCatalogueFixture(t *testing.T) storeFixture {
	t.Helper()
	require.GreaterOrEqual(t, MerchantStoreWriterCapability, 5, "catalogue writes require centrally registered capability 5")
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.AutoMigrate(MerchantStoreCatalogueModels()...))
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "5").Error)
	require.NoError(t, BackfillMerchantStoreCatalogueMappings(DB))
	return f
}

func TestMerchantStoreCatalogueSalesIgnoreSelfGiftPendingAndOnlySubtractQuantityRefunds(t *testing.T) {
	f := storeCatalogueFixture(t)
	orders := []MerchantStoreOrder{
		{ID: "real", TradeNo: "real", BuyerID: f.buyer.Id, SellerID: f.seller.Id, ProductID: f.product.ID, Quantity: 5, PriceQuota: 2500000, Status: "paid", PaidAt: 10},
		{ID: "guest", TradeNo: "guest", BuyerID: 0, SellerID: f.seller.Id, ProductID: f.product.ID, Quantity: 2, PriceQuota: 1000000, Status: "paid", PaidAt: 10},
		{ID: "self", TradeNo: "self", BuyerID: f.seller.Id, SellerID: f.seller.Id, ProductID: f.product.ID, Quantity: 500, PriceQuota: 500000, Status: "paid", PaidAt: 10},
		{ID: "gift", TradeNo: "gift", BuyerID: f.buyer.Id, SellerID: f.seller.Id, ProductID: f.product.ID, Quantity: 100, PriceQuota: 0, Status: "paid", PaidAt: 10},
		{ID: "pending", TradeNo: "pending", BuyerID: f.buyer.Id, SellerID: f.seller.Id, ProductID: f.product.ID, Quantity: 100, PriceQuota: 500000, Status: "pending"},
		{ID: "cancelled", TradeNo: "cancelled", BuyerID: f.buyer.Id, SellerID: f.seller.Id, ProductID: f.product.ID, Quantity: 100, PriceQuota: 500000, Status: "cancelled"},
	}
	require.NoError(t, DB.Create(&orders).Error)
	refunds := []MerchantStoreRefund{
		{ID: "quantity", OrderID: "real", RequestKey: "q", Mode: "quantity", Quantity: 2, Status: "completed", CompletedAt: 20},
		{ID: "amount", OrderID: "real", RequestKey: "a", Mode: "amount", Quantity: 3, Status: "completed", CompletedAt: 20},
		{ID: "unverified", OrderID: "guest", RequestKey: "u", Mode: "quantity", Quantity: 2, Status: "pending"},
	}
	require.NoError(t, DB.Create(&refunds).Error)
	rows, err := ListMerchantStoreCatalogue(f.buyer.Id, "", 0, 0, 1, MerchantStoreCatalogueQuery{Sort: "sales"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].NetPaidQuantity)
	require.EqualValues(t, 5, *rows[0].NetPaidQuantity)
}

func TestMerchantStoreCatalogueTagFilterPrecedesPaginationAndLegacyStockUsesCanonicalDefault(t *testing.T) {
	f := storeCatalogueFixture(t)
	input := MerchantStoreCatalogueMetadata{CustomTags: []string{" custom %_ tag ", "custom %_ tag"}, AutoDelivery: true, AIProcessing: true}
	require.NoError(t, SetMerchantStoreCatalogueMetadata(f.seller.Id, f.product.ID, input))
	require.ErrorIs(t, SetMerchantStoreCatalogueMetadata(f.root.Id, f.product.ID, input), ErrMerchantStoreDenied)
	// An old empty association maps to the canonical default, never another SKU.
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("product_id = ?", f.product.ID).Update("variant_id", "").Error)
	enabled := true
	rows, err := ListMerchantStoreCatalogue(f.buyer.Id, "", 0, 0, 1, MerchantStoreCatalogueQuery{Tag: "custom %_ tag", Stock: "in_stock", AutoDelivery: &enabled})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, []string{"custom %_ tag"}, rows[0].Catalogue.CustomTags)
	require.Contains(t, rows[0].DisplayTags, "in_stock")
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Where("id = ?", MerchantStoreDefaultVariantID(f.product.ID)).Update("enabled", false).Error)
	rows, err = ListMerchantStoreCatalogue(f.buyer.Id, "", 0, 0, 1, MerchantStoreCatalogueQuery{Stock: "in_stock"})
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestMerchantStoreCatalogueMappingBackfillPreservesExistingDataAndAnnotations(t *testing.T) {
	f := storeCatalogueFixture(t)
	require.NoError(t, SetMerchantStoreCatalogueMetadata(f.seller.Id, f.product.ID, MerchantStoreCatalogueMetadata{CustomTags: []string{"arbitrary label"}, AIProcessing: true}))
	var before MerchantStoreProduct
	var stockBefore []MerchantStoreStock
	require.NoError(t, DB.First(&before, "id = ?", f.product.ID).Error)
	require.NoError(t, DB.Where("product_id = ?", f.product.ID).Order("id").Find(&stockBefore).Error)
	require.NoError(t, BackfillMerchantStoreCatalogueMappings(DB))
	var after MerchantStoreProduct
	var stockAfter []MerchantStoreStock
	require.NoError(t, DB.First(&after, "id = ?", f.product.ID).Error)
	require.NoError(t, DB.Where("product_id = ?", f.product.ID).Order("id").Find(&stockAfter).Error)
	require.Equal(t, before, after)
	require.Equal(t, stockBefore, stockAfter)
	var metadata MerchantStoreCatalogueMetadata
	require.NoError(t, DB.First(&metadata, "product_id = ?", f.product.ID).Error)
	require.Equal(t, MerchantStoreDefaultVariantID(f.product.ID), metadata.DefaultVariantID)
	require.Equal(t, []string{"arbitrary label"}, metadata.CustomTags)
	require.True(t, metadata.AIProcessing)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	require.ErrorIs(t, SetMerchantStoreCatalogueMetadata(f.seller.Id, f.product.ID, metadata), ErrMerchantStoreWriterFrozen)
	require.False(t, MerchantStoreCatalogueSupported())
}

func TestMerchantStoreCatalogueIntegerFeeMatchesCheckoutAtLargeBoundary(t *testing.T) {
	marketTestDB(t)
	for _, price := range []int{500001, int(common.MaxWalletQuota)} {
		for _, bps := range []int{0, 1, 99, 100, 9999, 10000} {
			var value int64
			require.NoError(t, DB.Raw("SELECT "+storeCatalogueFeeSQL(strconv.Itoa(price), bps)).Scan(&value).Error)
			require.EqualValues(t, storeFee(price, bps), value)
		}
	}
}
