package model

import (
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
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
	guest, err := CreateMerchantStoreGuestSession()
	require.NoError(t, err)
	require.Len(t, guest.GuestID, 36)
	orders := []MerchantStoreOrder{
		{ID: "real", TradeNo: "real", BuyerID: f.buyer.Id, SellerID: f.seller.Id, ProductID: f.product.ID, Quantity: 5, PriceQuota: 2500000, Status: "paid", PaidAt: 10},
		{ID: "guest", TradeNo: "guest", GuestID: guest.GuestID, BuyerID: 0, SellerID: f.seller.Id, ProductID: f.product.ID, Quantity: 2, PriceQuota: 1000000, Status: "paid", PaidAt: 10},
		{ID: "anonymous-not-guest", TradeNo: "anonymous-not-guest", BuyerID: 0, SellerID: f.seller.Id, ProductID: f.product.ID, Quantity: 100, PriceQuota: 500000, Status: "paid", PaidAt: 10},
		{ID: "invalid-guest", TradeNo: "invalid-guest", GuestID: "invalid", BuyerID: 0, SellerID: f.seller.Id, ProductID: f.product.ID, Quantity: 100, PriceQuota: 500000, Status: "paid", PaidAt: 10},
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

func TestMerchantStoreCatalogueRankingIsStableAndVisibilityPrecedesPage(t *testing.T) {
	f := storeCatalogueFixture(t)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("status", "deleted").Error)
	makeProduct := func(title, visibility string, created, promotion int64, stock bool) MerchantStoreProduct {
		p := *f.product
		p.ID, p.Title, p.Visibility, p.TestMode = uuid.NewString(), title, visibility, visibility == "private"
		p.CreatedAt, p.PromotionExpiresAt, p.Status = created, promotion, "published"
		require.NoError(t, DB.Create(&p).Error)
		if stock {
			// A real persisted SKU with its own stock; no synthetic availability.
			v := MerchantStoreVariant{ID: MerchantStoreDefaultVariantID(p.ID), ProductID: p.ID, Name: "Default", PriceQuota: p.PriceQuota, Enabled: true}
			require.NoError(t, DB.Create(&v).Error)
			_, err := AddMerchantStoreStock(f.seller.Id, p.ID, []string{"RANK-SECRET"})
			require.NoError(t, err)
		}
		return p
	}
	now := common.GetTimestamp()
	promoted := makeProduct("promoted", "public", 100, now+1000, true)
	seller := makeProduct("net seller", "public", 200, 0, true)
	tieA := makeProduct("same time A", "public", 300, 0, true)
	tieB := makeProduct("same time B", "public", 300, 0, true)
	soldOut := makeProduct("promoted but sold out", "public", 900, now+1000, false)
	hidden := makeProduct("private newest", "private", 10000, now+10000, true)
	registered := makeProduct("registered newest", "registered", 9000, now+10000, true)
	require.NoError(t, BackfillMerchantStoreCatalogueMappings(DB))
	require.NoError(t, DB.Create(&MerchantStoreOrder{ID: "rank-paid", TradeNo: "rank-paid", BuyerID: f.buyer.Id, SellerID: f.seller.Id, ProductID: seller.ID, Quantity: 3, PriceQuota: 1500000, Status: "paid", PaidAt: 10}).Error)
	ids := func(rows []MerchantStoreProduct) []string {
		out := []string{}
		for _, p := range rows {
			out = append(out, p.ID)
		}
		return out
	}
	firstTie, secondTie := tieA.ID, tieB.ID
	if firstTie > secondTie {
		firstTie, secondTie = secondTie, firstTie
	}
	rows, err := ListMerchantStoreCatalogue(0, "", 0, 0, 10, MerchantStoreCatalogueQuery{Sort: "comprehensive"})
	require.NoError(t, err)
	require.Equal(t, []string{promoted.ID, seller.ID, firstTie, secondTie, soldOut.ID}, ids(rows))
	rows, err = ListMerchantStoreCatalogue(0, "", 0, 1, 1, MerchantStoreCatalogueQuery{Sort: "sales"})
	require.NoError(t, err)
	require.Equal(t, []string{soldOut.ID}, ids(rows)) // SQL offset follows net-sale ranking.
	rows, err = ListMerchantStoreCatalogue(0, "", 0, 0, 1, MerchantStoreCatalogueQuery{Sort: "newest"})
	require.NoError(t, err)
	require.Equal(t, []string{soldOut.ID}, ids(rows)) // Hidden rows never consume a page slot.
	rows, err = ListMerchantStoreCatalogue(f.buyer.Id, "", 0, 0, 1, MerchantStoreCatalogueQuery{Sort: "newest"})
	require.NoError(t, err)
	require.Equal(t, []string{registered.ID}, ids(rows))
	rows, err = ListMerchantStoreCatalogue(f.root.Id, "", 0, 0, 10, MerchantStoreCatalogueQuery{Sort: "newest"})
	require.NoError(t, err)
	require.NotContains(t, ids(rows), hidden.ID) // Administrator status never exposes another seller's private item.
}

func TestMerchantStoreCatalogueGuestLabelRespectsVisibilityLoginBoundary(t *testing.T) {
	f := storeCatalogueFixture(t)
	for _, visibility := range []string{"public", "registered", "private"} {
		require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Updates(map[string]interface{}{"visibility": visibility, "test_mode": visibility == "private", "purchase_login_required": false, "pickup_login_required": false}).Error)
		p, err := GetMerchantStoreProductForViewer(f.seller.Id, f.product.ID)
		require.NoError(t, err)
		guestAllowed := visibility == "public"
		if guestAllowed {
			require.Contains(t, p.DisplayTags, "guest_purchase")
			require.NoError(t, storeProductNewBuyer(p, 0))
		} else {
			require.NotContains(t, p.DisplayTags, "guest_purchase")
			require.ErrorIs(t, storeProductNewBuyer(p, 0), ErrMerchantStoreDenied)
		}
		rows, err := ListMerchantStoreCatalogue(f.seller.Id, "", 0, 0, 10, MerchantStoreCatalogueQuery{GuestPurchase: &guestAllowed})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		opposite := !guestAllowed
		rows, err = ListMerchantStoreCatalogue(f.seller.Id, "", 0, 0, 10, MerchantStoreCatalogueQuery{GuestPurchase: &opposite})
		require.NoError(t, err)
		require.Empty(t, rows)
	}
	// Missing legacy input is not affirmative permission for anonymous purchase.
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	legacy := MerchantStoreProduct{ID: f.product.ID}
	require.NoError(t, PopulateMerchantStoreCatalogue(DB, &legacy))
	require.NotContains(t, legacy.DisplayTags, "guest_purchase")
	require.Nil(t, legacy.NetPaidQuantity)

}
