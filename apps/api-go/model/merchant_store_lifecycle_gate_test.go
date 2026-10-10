package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMerchantStoreLifecycleRequiresReviewedFloorThree(t *testing.T) {
	f := newStoreFixture(t, "balance")
	for _, floor := range []string{"1", "2"} {
		storeWriterGateForTest(t, floor)
		before := storeWriterSnapshot(t)
		require.ErrorIs(t, UnlistMerchantStoreProduct(f.seller.Id, f.product.ID), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, DeleteMerchantStoreProduct(f.seller.Id, f.product.ID), ErrMerchantStoreWriterFrozen)
		require.Equal(t, before, storeWriterSnapshot(t), "retirement cannot begin while an older writer could revive the listing")
	}
	storeWriterGateForTest(t, "1")
	require.ErrorIs(t, ActivateMerchantStoreProductLifecycle(DB, 1), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, ActivateMerchantStoreProductLifecycle(DB, 2), ErrMerchantStoreWriterFrozen)
	require.NoError(t, ActivateMerchantStoreVariants(DB, 1))
	require.NoError(t, ActivateMerchantStoreProductLifecycle(DB, 2))
	require.NoError(t, ActivateMerchantStoreProductLifecycle(DB, 2))
	require.NoError(t, ActivateMerchantStoreProductLifecycle(DB, 3))
	require.ErrorIs(t, ActivateMerchantStoreVariants(DB, 2), ErrMerchantStoreWriterFrozen)
	require.NoError(t, BootstrapMerchantStoreWriterGate(DB))
	status, err := GetMerchantStoreWriterGateStatus(DB)
	require.NoError(t, err)
	require.Equal(t, 3, status.RequiredCapability)
	require.True(t, status.NewWritesAllowed)
	require.NoError(t, UnlistMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, DeleteMerchantStoreProduct(f.seller.Id, f.product.ID))
}

func TestMerchantStoreLifecycleKeepsExactVariantDeliveryAndRejectsEveryDeletedMutation(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	v := storeTestVariant(t, f, "Merchant custom edition · 93 days", 750000, "CORRECT-POOL")
	storeTestVariant(t, f, "Another independent pool", 1000000, "MUST-NOT-DELIVER")
	storePublishVariants(t, f)
	require.NoError(t, ActivateMerchantStoreProductLifecycle(DB, 2))
	in := storeVariantCheckout(f, v, "before-retirement", "balance")
	o, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.Equal(t, v.Name, o.VariantName)
	require.Equal(t, 750000, o.UnitPriceQuota)
	require.NoError(t, DeleteMerchantStoreProduct(f.seller.Id, f.product.ID))
	before := storeWriterSnapshot(t)
	var variantsBefore, variantsAfter []MerchantStoreVariant
	require.NoError(t, DB.Where("product_id = ?", f.product.ID).Order("id").Find(&variantsBefore).Error)
	limit := int64(100)
	for _, op := range []func() error{
		func() error {
			_, e := SaveMerchantStoreVariant(f.seller.Id, f.product.ID, v.ID, MerchantStoreVariantInput{Name: "Revive", PriceQuota: 500000, Template: "card-key", Enabled: true})
			return e
		},
		func() error {
			_, e := SaveMerchantStoreVariant(f.seller.Id, f.product.ID, "", MerchantStoreVariantInput{Name: "New", PriceQuota: 500000, Template: "card-key", Enabled: true})
			return e
		},
		func() error { return SetMerchantStoreVariantEnabled(f.seller.Id, f.product.ID, v.ID, true) },
		func() error {
			_, e := AddMerchantStoreVariantStock(f.seller.Id, f.product.ID, v.ID, []string{"NEW-STOCK"})
			return e
		},
		func() error { return SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, &limit) },
		func() error { return SetMerchantStoreProductListed(f.seller.Id, f.product.ID, true) },
	} {
		require.ErrorIs(t, op(), gorm.ErrRecordNotFound)
	}
	require.Equal(t, before, storeWriterSnapshot(t))
	require.NoError(t, DB.Where("product_id = ?", f.product.ID).Order("id").Find(&variantsAfter).Error)
	require.Equal(t, variantsBefore, variantsAfter)
	replay, created, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, o.ID, replay.ID)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, err)
	for range 2 {
		claim, e := ClaimMerchantStoreOrder(token, in.PickupCode, f.buyer.Id)
		require.NoError(t, e)
		require.Equal(t, v.ID, claim.VariantID)
		require.Equal(t, v.Name, claim.VariantName)
		require.Equal(t, []string{"CORRECT-POOL"}, claim.Items)
	}
}
