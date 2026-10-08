package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMerchantStoreVariantsGateOneUsesLatestLegacyPriceWithoutMaterializingDefault(t *testing.T) {
	f := newStoreFixture(t, "balance")
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Where("product_id = ?", f.product.ID).Count(&count).Error)
	require.Zero(t, count)
	// This is exactly what the still-compatible N-1 product writer knows:
	// product price/template only, with no durable spec update.
	require.NoError(t, DB.Model(f.product).Updates(map[string]any{"price_quota": 1000000, "template": "text"}).Error)
	p, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.Len(t, p.Variants, 1)
	require.Equal(t, 1000000, p.Variants[0].PriceQuota)
	require.Equal(t, "text", p.Variants[0].Template)
	o, _, err := CreateMerchantStoreOrder(f.checkout("gate-one-default", "balance"))
	require.NoError(t, err)
	require.Equal(t, 1000000, o.UnitPriceQuota)
	require.Equal(t, "text", o.DeliveryTemplate)
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Where("product_id = ?", f.product.ID).Count(&count).Error)
	require.Zero(t, count, "GET and checkout cannot create a second price authority at gate 1")
	require.NoError(t, ActivateMerchantStoreVariants(DB, 1))
	_, err = AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"AFTER-ACTIVATION"})
	require.NoError(t, err)
	var defaultVariant MerchantStoreVariant
	require.NoError(t, DB.First(&defaultVariant, "id = ?", MerchantStoreDefaultVariantID(f.product.ID)).Error)
	require.Equal(t, 1000000, defaultVariant.PriceQuota)
	require.Equal(t, "text", defaultVariant.Template)
}

func TestMerchantStoreVariantsGateOneRejectsSpecMutationsAndUnexpectedDurableRows(t *testing.T) {
	f := newStoreFixture(t, "balance")
	before := storeWriterSnapshot(t)
	input := MerchantStoreVariantInput{Name: "Custom default", PriceQuota: 500000, Template: "card-key", Enabled: true}
	_, err := SaveMerchantStoreVariant(f.seller.Id, f.product.ID, "", input)
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	_, err = SaveMerchantStoreVariant(f.seller.Id, f.product.ID, MerchantStoreDefaultVariantID(f.product.ID), input)
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, SetMerchantStoreVariantEnabled(f.seller.Id, f.product.ID, MerchantStoreDefaultVariantID(f.product.ID), false), ErrMerchantStoreWriterFrozen)
	_, err = AddMerchantStoreVariantStock(f.seller.Id, f.product.ID, "00000000-0000-0000-0000-000000000001", []string{"MUST-NOT-IMPORT"})
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	require.Equal(t, before, storeWriterSnapshot(t))
	unexpected := storeVirtualDefaultVariant(f.product)
	unexpected.PriceQuota = 750000
	require.NoError(t, DB.Create(&unexpected).Error)
	_, err = GetPublicMerchantStoreProduct(f.product.ID)
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	_, _, err = CreateMerchantStoreOrder(f.checkout("unsafe-stale-default", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	var stored MerchantStoreVariant
	require.NoError(t, DB.First(&stored, "id = ?", unexpected.ID).Error)
	require.Equal(t, 750000, stored.PriceQuota, "fail-closed reads must not silently rewrite unexpected rows")
}

func TestMerchantStoreVariantsPrivateTestPreviewAndEditsPreserveTradingStops(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	enabled := true
	input := storeModeInput(f.product, &enabled)
	input.EmailPickupLink = false
	var err error
	f.product, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, input)
	require.NoError(t, err)
	v := storeTestVariant(t, f, "Owner test edition", 1000000, "OWNER-ONLY-SPEC")
	preview, err := GetMerchantStoreProductPreview(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.False(t, preview.TradingPaused)
	for _, spec := range preview.Variants {
		if spec.ID == v.ID {
			require.False(t, spec.TradingPaused)
		}
	}
	_, err = GetPublicMerchantStoreProduct(f.product.ID)
	require.Error(t, err)
	_, err = GetMerchantStoreProductPreview(f.root.Id, f.product.ID)
	require.Error(t, err)
	in := storeVariantCheckout(f, v, "non-owner-test-spec", "balance")
	_, _, err = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	require.NoError(t, SetMerchantStoreProductPaused(f.seller.Id, f.product.ID, true))
	_, err = SaveMerchantStoreVariant(f.seller.Id, f.product.ID, v.ID, MerchantStoreVariantInput{Name: "Edited while paused", PriceQuota: 1000000, Template: "card-key", Enabled: true})
	require.NoError(t, err)
	preview, err = GetMerchantStoreProductPreview(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.Equal(t, "paused", preview.Status)
	require.True(t, preview.TradingPaused)
	in.BuyerID, in.PickupEmail = f.seller.Id, ""
	_, _, err = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreUnavailable)
	require.NoError(t, SetMerchantStoreProductPaused(f.seller.Id, f.product.ID, false))
	require.NoError(t, AcceptMerchantStoreDisclaimer(f.seller.Id, MerchantStoreDisclaimerVersion))
	in.RequestKey = "owner-test-spec"
	o, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.Equal(t, "paid", o.Status)
	storeBalance(t, f.seller.Id, 9990000)
	require.NoError(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, false))
	_, err = SaveMerchantStoreVariant(f.seller.Id, f.product.ID, v.ID, MerchantStoreVariantInput{Name: "Edited off shelf", PriceQuota: 1500000, Template: "card-key", Enabled: true})
	require.NoError(t, err)
	preview, err = GetMerchantStoreProductPreview(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.Equal(t, "off_shelf", preview.Status)
	require.True(t, preview.TradingPaused)
	token, err := GetMerchantStoreOrderPickupToken(f.seller.Id, o.ID)
	require.NoError(t, err)
	claim, err := ClaimMerchantStoreOrder(token, in.PickupCode, f.seller.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"OWNER-ONLY-SPEC"}, claim.Items)
	require.Equal(t, "Edited while paused", claim.VariantName)
}

func newVariantsFixture(t *testing.T, methods ...string) storeFixture {
	t.Helper()
	f := newStoreFixture(t, methods...)
	require.NoError(t, ActivateMerchantStoreVariants(DB, 1))
	return f
}

func storeTestVariant(t *testing.T, f storeFixture, name string, price int, items ...string) *MerchantStoreVariant {
	t.Helper()
	v, err := SaveMerchantStoreVariant(f.seller.Id, f.product.ID, "", MerchantStoreVariantInput{Name: name, PriceQuota: price, Template: "card-key", Enabled: true})
	require.NoError(t, err)
	if len(items) != 0 {
		_, err = AddMerchantStoreVariantStock(f.seller.Id, f.product.ID, v.ID, items)
		require.NoError(t, err)
	}
	return v
}

func storePublishVariants(t *testing.T, f storeFixture) {
	t.Helper()
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, "reviewed variant names/prices"))
}

func storeVariantCheckout(f storeFixture, v *MerchantStoreVariant, key, method string) MerchantStoreCheckoutInput {
	in := f.checkout(key, method)
	in.VariantID = v.ID
	return in
}

func TestMerchantStoreVariantsLegacyDefaultReadDoesNotWriteOrChangeStock(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	require.NoError(t, DB.Where("product_id = ?", f.product.ID).Delete(&MerchantStoreVariant{}).Error)
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("product_id = ?", f.product.ID).Update("variant_id", nil).Error)
	var before, after []MerchantStoreStock
	require.NoError(t, DB.Where("product_id = ?", f.product.ID).Order("id").Find(&before).Error)
	for range 2 {
		p, err := GetPublicMerchantStoreProduct(f.product.ID)
		require.NoError(t, err)
		require.Len(t, p.Variants, 1)
		require.True(t, p.Variants[0].IsDefault)
		require.Equal(t, MerchantStoreDefaultVariantID(p.ID), p.Variants[0].ID)
		require.EqualValues(t, 2, p.InventoryTotal)
	}
	var defaults int64
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Count(&defaults).Error)
	require.Zero(t, defaults)
	require.NoError(t, DB.Where("product_id = ?", f.product.ID).Order("id").Find(&after).Error)
	require.Equal(t, before, after, "GET does not backfill, re-encrypt, move, or consume legacy stock")
	order, made, err := CreateMerchantStoreOrder(f.checkout("legacy-default", "balance"))
	require.NoError(t, err)
	require.True(t, made)
	require.Equal(t, MerchantStoreDefaultVariantID(f.product.ID), order.VariantID)
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Count(&defaults).Error)
	require.EqualValues(t, 1, defaults)
	require.NoError(t, DB.Where("product_id = ?", f.product.ID).Order("id").Find(&after).Error)
	for i := range before {
		require.Equal(t, before[i].ID, after[i].ID)
		require.Equal(t, before[i].ProductID, after[i].ProductID)
		require.Equal(t, before[i].Ciphertext, after[i].Ciphertext)
		require.Equal(t, before[i].Position, after[i].Position)
		require.Nil(t, after[i].VariantID, "lazy default does not rewrite old stock associations")
	}
}

func TestMerchantStoreVariantsDeliverOnlySelectedStockAndFreezePrice(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	v := storeTestVariant(t, f, "Plus · 2 months", 1000000, "PLUS-ONLY-ONE", "PLUS-ONLY-TWO")
	storePublishVariants(t, f)
	p, err := GetMerchantStoreProduct(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.EqualValues(t, 4, p.InventoryTotal)
	require.EqualValues(t, 4, p.InventoryAvailable)
	require.Equal(t, 500000, p.PriceMinQuota)
	require.Equal(t, 1000000, p.PriceMaxQuota)
	order, made, err := CreateMerchantStoreOrder(storeVariantCheckout(f, v, "plus-purchase", "balance"))
	require.NoError(t, err)
	require.True(t, made)
	require.Equal(t, v.ID, order.VariantID)
	require.Equal(t, "Plus · 2 months", order.VariantName)
	require.Equal(t, 1000000, order.UnitPriceQuota)
	storeBalance(t, f.buyer.Id, 9000000)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
	require.NoError(t, err)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"PLUS-ONLY-ONE"}, claim.Items)
	require.Equal(t, v.ID, claim.VariantID)
	require.Equal(t, "Plus · 2 months", claim.VariantName)
	var defaultAvailable int64
	require.NoError(t, storeVariantStock(DB.Model(&MerchantStoreStock{}), f.product.ID, MerchantStoreDefaultVariantID(f.product.ID)).Where("state = ?", "available").Count(&defaultAvailable).Error)
	require.EqualValues(t, 2, defaultAvailable, "buying Plus never consumes a default card")
}

func TestMerchantStoreVariantsCannotFallbackOrChangeIdempotentSelection(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	v := storeTestVariant(t, f, "Plus", 500000, "PLUS-ONLY")
	storePublishVariants(t, f)
	_, made, err := CreateMerchantStoreOrder(f.checkout("missing-selection", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreVariantRequired)
	require.False(t, made)
	unknown := *v
	unknown.ID = "not-a-real-variant"
	_, made, err = CreateMerchantStoreOrder(storeVariantCheckout(f, &unknown, "bad-selection", "balance"))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.False(t, made)
	order, _, err := CreateMerchantStoreOrder(storeVariantCheckout(f, v, "selected-once", "balance"))
	require.NoError(t, err)
	defaultVariant := storeVirtualDefaultVariant(f.product)
	_, made, err = CreateMerchantStoreOrder(storeVariantCheckout(f, &defaultVariant, "selected-once", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
	require.False(t, made)
	_, made, err = CreateMerchantStoreOrder(storeVariantCheckout(f, v, "plus-sold-out", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreStock)
	require.False(t, made)
	replay, made, err := CreateMerchantStoreOrder(storeVariantCheckout(f, v, "selected-once", "balance"))
	require.NoError(t, err)
	require.False(t, made)
	require.Equal(t, order.ID, replay.ID)
	storeBalance(t, f.buyer.Id, 9500000)
}

func TestMerchantStoreVariantsProductLimitClipsAggregateOnlyOnce(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	v := storeTestVariant(t, f, "Plus", 500000, "PLUS-ONE", "PLUS-TWO")
	storePublishVariants(t, f)
	limit := int64(1)
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, &limit))
	p, err := GetMerchantStoreProduct(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.EqualValues(t, 4, p.InventoryTotal)
	require.EqualValues(t, 1, p.SaleAvailable, "a one-item product limit is not multiplied by the number of variants")
	_, _, err = CreateMerchantStoreOrder(storeVariantCheckout(f, v, "cap-plus", "balance"))
	require.NoError(t, err)
	defaultVariant := storeVirtualDefaultVariant(f.product)
	_, made, err := CreateMerchantStoreOrder(storeVariantCheckout(f, &defaultVariant, "cap-default", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreStock)
	require.False(t, made)
	p, err = GetMerchantStoreProduct(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.EqualValues(t, 3, p.InventoryTotal)
	require.EqualValues(t, 3, p.InventoryAvailable)
	require.Zero(t, p.SaleAvailable)
	require.True(t, p.TradingPaused)
}

func TestMerchantStoreVariantsDisabledAndEmptyRemainReplenishable(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	v := storeTestVariant(t, f, "Future edition", 500000)
	storePublishVariants(t, f)
	require.NoError(t, SetMerchantStoreVariantEnabled(f.seller.Id, f.product.ID, v.ID, false))
	_, made, err := CreateMerchantStoreOrder(storeVariantCheckout(f, v, "disabled", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreUnavailable)
	require.False(t, made)
	added, err := AddMerchantStoreVariantStock(f.seller.Id, f.product.ID, v.ID, []string{"LATER-EDITION"})
	require.NoError(t, err)
	require.Equal(t, 1, added)
	require.NoError(t, SetMerchantStoreVariantEnabled(f.seller.Id, f.product.ID, MerchantStoreDefaultVariantID(f.product.ID), false))
	p, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.True(t, p.TradingPaused)
	require.Empty(t, p.Variants)
	require.EqualValues(t, 3, p.InventoryTotal, "disabled inventory is retained and counted")
	require.NoError(t, SetMerchantStoreVariantEnabled(f.seller.Id, f.product.ID, v.ID, true))
	_, _, err = CreateMerchantStoreOrder(storeVariantCheckout(f, v, "enabled-again", "balance"))
	require.NoError(t, err)
}

func TestMerchantStoreVariantsCurrentMinimumChecksEachUnitAndHonorsFrozenOrder(t *testing.T) {
	f := newVariantsFixture(t, "external:epay")
	v := storeTestVariant(t, f, "Plus", 1000000, "PAID-FROZEN-PLUS")
	storePublishVariants(t, f)
	order, _, err := CreateMerchantStoreOrder(storeVariantCheckout(f, v, "already-reserved", "external:epay"))
	require.NoError(t, err)
	require.NoError(t, BindMerchantStorePaymentQuote(order.ID, 200, "USD", "1"))
	_, err = SaveMerchantStoreVariant(f.seller.Id, f.product.ID, v.ID, MerchantStoreVariantInput{Name: "Renamed", PriceQuota: 3000000, Template: "license-key", Enabled: false})
	require.NoError(t, err)
	minimum := 2000000
	require.NoError(t, PatchMerchantStoreConfig(f.root.Id, MerchantStoreConfigPatch{MinimumUnitPriceQuota: &minimum}))
	_, err = SaveMerchantStoreVariant(f.seller.Id, f.product.ID, "", MerchantStoreVariantInput{Name: "Underpriced per unit", PriceQuota: 1999999, Template: "card-key", Enabled: true})
	require.ErrorIs(t, err, ErrMerchantStoreMinimumPrice)
	require.ErrorIs(t, SubmitMerchantStoreProduct(f.seller.Id, f.product.ID), ErrMerchantStoreMinimumPrice)
	require.NoError(t, CompleteMerchantStorePayment(order.ID, "real-frozen-receipt"))
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
	require.NoError(t, err)
	metadata, err := InspectMerchantStoreClaim(token)
	require.NoError(t, err)
	require.Equal(t, "Plus", metadata.VariantName)
	require.Equal(t, "card-key", metadata.DeliveryTemplate)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"PAID-FROZEN-PLUS"}, claim.Items)
	require.Equal(t, "Plus", claim.VariantName)
	require.Equal(t, "card-key", claim.DeliveryTemplate)
	paid, err := GetMerchantStoreOrder(f.buyer.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, 1000000, paid.UnitPriceQuota)
	require.Equal(t, 1000000, paid.PriceQuota)
	require.Equal(t, 200, int(paid.AmountMinor))
}

func TestMerchantStoreVariantsLegacyEditorAndCancellationKeepAssociations(t *testing.T) {
	f := newVariantsFixture(t, "external:epay")
	v := storeTestVariant(t, f, "Plus", 1000000, "PLUS-RESERVATION")
	storePublishVariants(t, f)
	order, _, err := CreateMerchantStoreOrder(storeVariantCheckout(f, v, "cancel-only-plus", "external:epay"))
	require.NoError(t, err)
	_, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, MerchantStoreProductInput{Title: "Legacy editor", PriceQuota: 750000, Template: "text", PaymentMethods: []string{"external:epay"}})
	require.NoError(t, err)
	stored, err := GetMerchantStoreVariant(f.seller.Id, f.product.ID, v.ID)
	require.NoError(t, err)
	require.Equal(t, 1000000, stored.PriceQuota)
	require.Equal(t, "card-key", stored.Template)
	require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, order.ID))
	var rows []MerchantStoreStock
	require.NoError(t, DB.Where("product_id = ? AND variant_id = ?", f.product.ID, v.ID).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "available", rows[0].State)
	require.Empty(t, rows[0].OrderID)
	require.Equal(t, v.ID, *rows[0].VariantID)
	storeBalance(t, f.seller.Id, 10000000)
}

func TestMerchantStoreVariantsHistoricalDefaultReplayAndDeliveryStayRaw(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	in := f.checkout("old-paid-default", "balance")
	order, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.NoError(t, DB.Model(order).Updates(map[string]any{"variant_id": "", "variant_name": "", "delivery_template": ""}).Error)
	storeTestVariant(t, f, "Other edition", 1000000, "NOT-THE-OLD-CARD")
	in.VariantID = MerchantStoreDefaultVariantID(f.product.ID)
	replay, made, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.False(t, made)
	require.Equal(t, order.ID, replay.ID)
	require.Empty(t, replay.VariantName)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
	require.NoError(t, err)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
	require.Empty(t, claim.DeliveryTemplate)
	require.Empty(t, claim.VariantID)
	storeBalance(t, f.buyer.Id, 9500000)
}

func TestMerchantStoreVariantsRejectCrossProductReferencesAndPriceOverflow(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	other, err := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "Another product", PriceQuota: 500000, PaymentMethods: []string{"balance"}})
	require.NoError(t, err)
	otherDefault := storeVirtualDefaultVariant(other)
	_, made, err := CreateMerchantStoreOrder(storeVariantCheckout(f, &otherDefault, "cross-product", "balance"))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.False(t, made)
	_, err = AddMerchantStoreVariantStock(f.seller.Id, f.product.ID, otherDefault.ID, []string{"WRONG-PRODUCT"})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	v := storeTestVariant(t, f, "Large price", common.MaxWalletQuota, "OVERFLOW-ONE", "OVERFLOW-TWO")
	storePublishVariants(t, f)
	in := storeVariantCheckout(f, v, "overflow", "balance")
	in.Quantity = 2
	_, made, err = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	require.False(t, made)
	storeBalance(t, f.buyer.Id, 10000000)
}
