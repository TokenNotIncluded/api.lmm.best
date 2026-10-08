package model

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCommerceImportRefundViewRetainsUnknownWithoutChangingLocalFacts(t *testing.T) {
	db := marketTestDB(t)
	f := commerceImportInventorySetup(t, db)
	var root User
	require.NoError(t, db.Where("username = ?", "import-root").First(&root).Error)
	terms, err := SaveMerchantStoreSellerTerms(f.seller.Id, MerchantStoreTermsInput{Content: "Digital cards are delivered after payment; external redemption needs merchant verification."})
	require.NoError(t, err)
	require.NoError(t, SetMerchantStorePaymentCategories(f.seller.Id, MerchantStorePaymentCategories{PlatformEnabled: true}))
	_, err = SaveMerchantStoreGateway(f.seller.Id, "balance", true, "")
	require.NoError(t, err)
	in := commerceImportInventoryDraft()
	in.Product.PaymentMethods = []string{"balance"}
	mapping, err := ImportCommerceImportProduct(f.seller.Id, f.lease, in)
	require.NoError(t, err)
	request, _, err := PrepareCommerceImportRestock(f.seller.Id, f.lease, commerceImportInventoryRequest(mapping, "refund-view-batch"))
	require.NoError(t, err)
	request, err = BeginCommerceImportIssue(f.seller.Id, f.lease, request.ID)
	require.NoError(t, err)
	_, imported, err := ReceiveCommerceImportBatch(f.seller.Id, f.lease, request.ID, commerceImportInventoryBatch(request))
	require.NoError(t, err)
	require.True(t, imported)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, mapping.LocalProductID))
	require.NoError(t, ReviewMerchantStoreProduct(root.Id, mapping.LocalProductID, true, ""))
	require.NoError(t, AcceptMerchantStoreDisclaimer(f.other.Id, MerchantStoreDisclaimerVersion))
	variantID := ""
	for _, variant := range mapping.Variants {
		if variant.ExternalID == "standard" {
			variantID = variant.LocalVariantID
		}
	}
	require.NotEmpty(t, variantID)
	checkout := MerchantStoreCheckoutInput{BuyerID: f.other.Id, ProductID: mapping.LocalProductID, VariantID: variantID, Quantity: 1, RequestKey: "refund-view-imported-order", PaymentMethod: "balance", SellerTermsVersion: terms.Version, AcceptSellerTerms: true}
	order, created, err := CreateMerchantStoreOrder(checkout)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "paid", order.Status)
	stranger := marketTestUser(t, db, "refund-view-stranger", 10000000, common.RoleCommonUser)
	commerceReads := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("refund-view-commerce-access-order", func(tx *gorm.DB) {
		if storeCommerceImportTable(tx.Statement.Table) {
			commerceReads++
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove("refund-view-commerce-access-order") })

	before := storeWriterSnapshot(t)
	for _, actor := range []int{f.other.Id, f.seller.Id, root.Id} {
		view, err := GetMerchantStoreRefunds(actor, order.ID)
		require.NoError(t, err)
		require.Equal(t, "unknown", view.ExternalRedemptionStatus)
		require.Equal(t, order.PriceQuota, view.RemainingQuota)
		require.Equal(t, 1, view.MaxQuantity)
		require.Len(t, view.EligibleItems, 1)
		encoded, err := json.Marshal(view)
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"external_redemption_status":"unknown"`)
		require.NotContains(t, string(encoded), "SYNTHETIC-CARD")
	}
	readsBeforeDenial := commerceReads
	view, err := GetMerchantStoreRefunds(stranger.Id, order.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	require.Nil(t, view)
	require.Equal(t, readsBeforeDenial, commerceReads, "unauthorized actors must be denied before querying source mappings")
	token, err := GetMerchantStoreOrderPickupToken(f.other.Id, order.ID)
	require.NoError(t, err)
	view, err = GetMerchantStoreRefundsWithPickupProof(f.other.Id, MerchantStoreRefundPickupProof{OrderID: order.ID, Token: token})
	require.NoError(t, err)
	require.Equal(t, "unknown", view.ExternalRedemptionStatus)
	require.Equal(t, before, storeWriterSnapshot(t), "refund source visibility must not mutate wallets, orders, stock or local audit facts")

	ordinary, err := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "Ordinary local card", PriceQuota: 700000, PaymentMethods: []string{"balance"}})
	require.NoError(t, err)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, ordinary.ID))
	require.NoError(t, ReviewMerchantStoreProduct(root.Id, ordinary.ID, true, ""))
	_, err = AddMerchantStoreStock(f.seller.Id, ordinary.ID, []string{"LOCAL-ONLY-CARD"})
	require.NoError(t, err)
	checkout.ProductID, checkout.VariantID, checkout.RequestKey = ordinary.ID, "", "refund-view-ordinary-order"
	ordinaryOrder, created, err := CreateMerchantStoreOrder(checkout)
	require.NoError(t, err)
	require.True(t, created)
	before = storeWriterSnapshot(t)
	view, err = GetMerchantStoreRefunds(f.other.Id, ordinaryOrder.ID)
	require.NoError(t, err)
	require.Empty(t, view.ExternalRedemptionStatus)
	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "external_redemption_status")
	require.Equal(t, before, storeWriterSnapshot(t))
	// A mapping for the same product under a different seller cannot mark the
	// order; both frozen ownership fields must match the original source.
	require.NoError(t, db.Model(&MerchantStoreCommerceProductMapping{}).Where("id = ?", mapping.ID).Update("seller_id", stranger.Id).Error)
	view, err = GetMerchantStoreRefunds(f.other.Id, order.ID)
	require.NoError(t, err)
	require.Empty(t, view.ExternalRedemptionStatus)
	require.Equal(t, before, storeWriterSnapshot(t))
	require.NoError(t, db.Model(&MerchantStoreCommerceProductMapping{}).Where("id = ?", mapping.ID).Update("seller_id", f.seller.Id).Error)

	require.NoError(t, DisconnectCommerceImportConnection(f.seller.Id, f.lease))
	before = storeWriterSnapshot(t)
	view, err = GetMerchantStoreRefunds(f.other.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, "unknown", view.ExternalRedemptionStatus)
	require.Equal(t, order.PriceQuota, view.RemainingQuota)
	require.Equal(t, 1, view.MaxQuantity)
	require.Equal(t, before, storeWriterSnapshot(t))
	var retained MerchantStoreCommerceProductMapping
	require.NoError(t, db.First(&retained, "id = ?", mapping.ID).Error)
	require.Equal(t, mapping.LocalProductID, retained.LocalProductID)
}

func TestCommerceImportRefundViewLegacyFloorNeverReadsCommerceTables(t *testing.T) {
	f, order, _ := refundPaidOrder(t, "balance", 1)
	for _, model := range CommerceImportModels() {
		require.NoError(t, DB.Migrator().DropTable(model))
	}
	commerceReads := 0
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register("refund-view-no-commerce-reads", func(tx *gorm.DB) {
		if storeCommerceImportTable(tx.Statement.Table) {
			commerceReads++
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove("refund-view-no-commerce-reads") })
	for _, floor := range []string{"1", "4", "7"} {
		storeWriterGateForTest(t, floor)
		before := storeWriterSnapshot(t)
		view, err := GetMerchantStoreRefunds(f.buyer.Id, order.ID)
		require.NoError(t, err, "legacy floor %s", floor)
		require.Empty(t, view.ExternalRedemptionStatus)
		require.Equal(t, before, storeWriterSnapshot(t))
	}
	require.NoError(t, DB.Where("key = ?", MerchantStoreWriterCapabilityOption).Delete(&Option{}).Error)
	view, err := GetMerchantStoreRefunds(f.buyer.Id, order.ID)
	require.NoError(t, err, "missing historical floor retains existing authenticated refund reads")
	require.Empty(t, view.ExternalRedemptionStatus)
	require.Zero(t, commerceReads)
	require.NoError(t, DB.Create(&Option{Key: MerchantStoreWriterCapabilityOption, Value: "8"}).Error)
	view, err = GetMerchantStoreRefunds(f.buyer.Id, order.ID)
	require.Error(t, err, "a supported floor with missing mappings cannot silently present an ordinary local order")
	require.Nil(t, view)
	require.Equal(t, 1, commerceReads)
}
