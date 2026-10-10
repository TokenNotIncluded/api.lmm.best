package model

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const storeFixedTestContent = "# Private tutorial\n\nThe exact shared text.\nSecond line with a secret: FIXED-SECRET."

func storeActivateFixedTest(t *testing.T, db *gorm.DB) {
	t.Helper()
	storeActivatePhaseFiveForSixTest(t, db)
	require.NoError(t, PrepareMerchantStorePhaseSix(db, 5))
	require.NoError(t, ActivateMerchantStorePhaseSix(db, 5))
	require.NoError(t, PrepareMerchantStoreFixedContent(db, 6))
	require.NoError(t, VerifyMerchantStoreFixedContent(db))
	require.NoError(t, ActivateMerchantStoreFixedContent(db, 6))
}

func storeCreateFixedTestProduct(t *testing.T, f storeFixture, methods ...string) storeFixture {
	t.Helper()
	_, err := SaveMerchantStoreSellerTerms(f.seller.Id, MerchantStoreTermsInput{Content: "Shared text is supplied after payment."})
	require.NoError(t, err)
	content := storeFixedTestContent
	p, err := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{
		Title: "Shared tutorial", Description: "Public description", PriceQuota: 500000,
		Template: MerchantStoreFixedContentTemplate, FixedContent: &content,
		PaymentMethods: methods, PickupLoginRequired: true, PickupCodeRequired: true,
	})
	require.NoError(t, err)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, p.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, p.ID, true, ""))
	f.product = p
	return f
}

func newStoreFixedTestFixture(t *testing.T, methods ...string) storeFixture {
	t.Helper()
	f := newStoreFixture(t, methods...)
	storeActivateFixedTest(t, DB)
	return storeCreateFixedTestProduct(t, f, methods...)
}

func storeFixedCheckout(t *testing.T, f storeFixture, key, method string) MerchantStoreCheckoutInput {
	t.Helper()
	in := f.checkout(key, method)
	terms, err := GetMerchantStoreSellerTerms(f.seller.Id)
	require.NoError(t, err)
	in.SellerTermsVersion, in.AcceptSellerTerms = terms.Version, true
	return in
}

func storeFixedStockless(t *testing.T, productID string) {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("product_id = ?", productID).Count(&count).Error)
	require.Zero(t, count, "shared text never creates inventory")
	require.NoError(t, DB.Model(&MerchantStoreRefundItem{}).Where("order_id IN (?)", DB.Model(&MerchantStoreOrder{}).Select("id").Where("product_id = ?", productID)).Count(&count).Error)
	require.Zero(t, count, "quantity refunds never create phantom stock IDs")
}

func storeFixedNoStockWrites(t *testing.T, db *gorm.DB) {
	t.Helper()
	check := func(tx *gorm.DB) {
		if tx.Statement.Table == "merchant_store_stocks" || tx.Statement.Table == "merchant_store_refund_items" {
			t.Errorf("fixed-content flow attempted a stock mutation: %s", tx.Statement.Table)
			tx.AddError(ErrMerchantStoreConflict)
		}
	}
	name := "test:fixed-content-no-stock"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(name, check))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, check))
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(name, check))
	t.Cleanup(func() {
		_ = db.Callback().Create().Remove(name)
		_ = db.Callback().Update().Remove(name)
		_ = db.Callback().Delete().Remove(name)
	})
}

func TestMerchantStoreFixedContentBalanceDeliverySnapshotsAndPrivacy(t *testing.T) {
	f := newStoreFixedTestFixture(t, "balance")
	storeFixedNoStockWrites(t, DB)
	public, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.True(t, public.UnlimitedSupply)
	require.False(t, public.TradingPaused)
	require.Zero(t, public.AvailableStock)
	require.Zero(t, public.SaleAvailable)
	require.True(t, public.Variants[0].UnlimitedSupply)
	encoded, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "FIXED-SECRET")
	require.NotContains(t, string(encoded), "fixed_content")
	_, err = GetMerchantStoreFixedContent(f.buyer.Id, f.product.ID, MerchantStoreDefaultVariantID(f.product.ID))
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	content, err := GetMerchantStoreFixedContent(f.seller.Id, f.product.ID, MerchantStoreDefaultVariantID(f.product.ID))
	require.NoError(t, err)
	require.Equal(t, storeFixedTestContent, content)
	in := storeFixedCheckout(t, f, "shared-paid", "balance")
	in.Quantity = 3
	o, created, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, 1500000, o.PriceQuota)
	replay, created, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, o.ID, replay.ID)
	var shared MerchantStoreFixedContent
	var snapshot MerchantStoreOrderFixedDelivery
	require.NoError(t, DB.First(&shared, "variant_id = ?", o.VariantID).Error)
	require.NoError(t, DB.First(&snapshot, "order_id = ?", o.ID).Error)
	require.NotContains(t, shared.Ciphertext, "FIXED-SECRET")
	require.NotContains(t, snapshot.Ciphertext, "FIXED-SECRET")
	require.NotEqual(t, shared.Ciphertext, snapshot.Ciphertext)
	_, err = storeDecrypt("order-fixed-delivery", "another-order", snapshot.Ciphertext)
	require.Error(t, err, "ciphertext is bound to its original order")
	updated := "Edited tutorial for later orders"
	_, err = SaveMerchantStoreVariant(f.seller.Id, f.product.ID, o.VariantID, MerchantStoreVariantInput{Name: "Default", PriceQuota: 500000, Template: MerchantStoreFixedContentTemplate, Enabled: true, FixedContent: &updated})
	require.NoError(t, err)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, err)
	meta, err := InspectMerchantStoreClaim(token)
	require.NoError(t, err)
	encoded, err = json.Marshal(meta)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "FIXED-SECRET")
	_, err = ClaimMerchantStoreOrder(token, "wrong-code", f.buyer.Id)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.seller.Id)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, storeFixedTestContent, claim.FixedContent)
	require.Equal(t, 3, claim.Quantity)
	require.Empty(t, claim.Items)
	require.Empty(t, claim.ItemStockIDs)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, ""))
	later, _, err := CreateMerchantStoreOrder(storeFixedCheckout(t, f, "later", "balance"))
	require.NoError(t, err)
	laterContent, err := storeReadOrderFixedContent(DB, later)
	require.NoError(t, err)
	require.Equal(t, updated, laterContent)
	storeFixedStockless(t, f.product.ID)
}

func TestMerchantStoreFixedContentQuantityAndAmountRefundLedger(t *testing.T) {
	f := newStoreFixedTestFixture(t, "balance")
	storeFixedNoStockWrites(t, DB)
	in := storeFixedCheckout(t, f, "refundable", "balance")
	in.Quantity = 3
	o, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, err)
	view, err := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, err)
	require.Equal(t, MerchantStoreFixedContentTemplate, view.DeliveryTemplate)
	require.Equal(t, 3, view.MaxQuantity)
	require.Empty(t, view.EligibleItems)
	quantity := MerchantStoreRefundInput{RequestKey: "one", Reason: "One quantity", Mode: "quantity", Quantity: 1}
	r, err := RequestMerchantStoreRefund(f.buyer.Id, o.ID, quantity)
	require.NoError(t, err)
	require.Empty(t, r.StockIDs)
	require.Equal(t, 1, r.Quantity)
	require.Equal(t, 500000, r.PrincipalQuota)
	replay, err := RequestMerchantStoreRefund(f.buyer.Id, o.ID, quantity)
	require.NoError(t, err)
	require.Equal(t, r.ID, replay.ID)
	tooMany := MerchantStoreRefundInput{RequestKey: "over-reserved", Reason: "Too many", Mode: "quantity", Quantity: 3}
	_, err = RequestMerchantStoreRefund(f.buyer.Id, o.ID, tooMany)
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
	badID := quantity
	badID.RequestKey, badID.StockIDs = "fake-card", []string{strings.Repeat("a", 36)}
	_, err = RequestMerchantStoreRefund(f.buyer.Id, o.ID, badID)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	refundApprove(t, f.seller.Id, o, r)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, 2, claim.Quantity)
	require.Equal(t, storeFixedTestContent, claim.FixedContent)
	adjustment, err := RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: "amount", Reason: "Price difference", Mode: "amount", AmountQuota: 12345})
	require.NoError(t, err)
	require.Zero(t, adjustment.Quantity)
	refundApprove(t, f.seller.Id, o, adjustment)
	claim, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, 2, claim.Quantity)
	remainder, err := RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("rest"))
	require.NoError(t, err)
	require.Equal(t, 987655, remainder.PrincipalQuota)
	require.Equal(t, 2, remainder.Quantity)
	refundApprove(t, f.seller.Id, o, remainder)
	refundApprove(t, f.seller.Id, o, remainder)
	require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	require.Equal(t, "refunded", o.Status)
	require.Equal(t, 3, o.Quantity)
	_, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 9985000)
	storeBalance(t, f.root.Id, 15000)
	usage, err := storeSalesUsage(DB, f.product.ID)
	require.NoError(t, err)
	require.EqualValues(t, 3, usage.Paid, "refunds preserve lifetime sales cap")
	storeFixedStockless(t, f.product.ID)
}

func TestMerchantStoreFixedContentPendingSnapshotsCapsCancellationAndPayment(t *testing.T) {
	f := newStoreFixedTestFixture(t, "platform:waffo_pancake")
	storeFixedNoStockWrites(t, DB)
	cap := int64(3)
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, &cap))
	in := storeFixedCheckout(t, f, "pending", "platform:waffo_pancake")
	in.Quantity = 2
	o, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	usage, err := storeSalesUsage(DB, f.product.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, usage.Reserved)
	used, err := storeBuyerPurchaseUsage(DB, f.product.ID, f.buyer.Id)
	require.NoError(t, err)
	require.EqualValues(t, 2, used)
	require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
	require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
	usage, err = storeSalesUsage(DB, f.product.ID)
	require.NoError(t, err)
	require.Zero(t, usage.Reserved)
	storeBalance(t, f.seller.Id, 10000000)
	in.RequestKey = "issued"
	o, _, err = CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 200, "USD", "1"))
	require.NoError(t, BindMerchantStorePaymentContext(o.ID, "frozen-context"))
	updated := "Later content"
	_, err = SaveMerchantStoreVariant(f.seller.Id, f.product.ID, o.VariantID, MerchantStoreVariantInput{Name: "Default", PriceQuota: 500000, Template: MerchantStoreFixedContentTemplate, FixedContent: &updated, Enabled: true})
	require.NoError(t, err)
	require.NoError(t, DB.Model(o).Update("expires_at", common.GetTimestamp()-1).Error)
	_, err = ExpireMerchantStoreOrders(30)
	require.NoError(t, err)
	require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	require.Equal(t, "reconciliation_pending", o.Status)
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "fixed-paid-receipt"))
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "fixed-paid-receipt"))
	content, err := storeReadOrderFixedContent(DB, o)
	require.NoError(t, err)
	require.Equal(t, storeFixedTestContent, content, "pending order retains its original content through later payment")
	usage, err = storeSalesUsage(DB, f.product.ID)
	require.NoError(t, err)
	require.Zero(t, usage.Reserved)
	require.EqualValues(t, 2, usage.Paid)
	storeBalance(t, f.seller.Id, 10990000)
	storeFixedStockless(t, f.product.ID)
}

func TestMerchantStoreFixedContentConfigurationGateAndImportIsolation(t *testing.T) {
	f := newStoreFixture(t, "balance")
	content := storeFixedTestContent
	_, err := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "No phase seven", PriceQuota: 500000, Template: MerchantStoreFixedContentTemplate, FixedContent: &content})
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	storeActivateFixedTest(t, DB)
	_, err = SaveMerchantStoreVariant(f.seller.Id, f.product.ID, MerchantStoreDefaultVariantID(f.product.ID), MerchantStoreVariantInput{Name: "Default", PriceQuota: 500000, Template: MerchantStoreFixedContentTemplate, FixedContent: &content, Enabled: true})
	require.ErrorIs(t, err, ErrMerchantStoreConflict, "existing available cards cannot become shared configuration")
	f = storeCreateFixedTestProduct(t, f, "balance")
	_, err = AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"never-an-inventory-unit"})
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	for _, invalid := range []string{" ", strings.Repeat("x", MerchantStoreFixedContentMaxBytes+1), "text\x00tail", string([]byte{0xff})} {
		_, err = SaveMerchantStoreVariant(f.seller.Id, f.product.ID, MerchantStoreDefaultVariantID(f.product.ID), MerchantStoreVariantInput{Name: "Default", PriceQuota: 500000, Template: MerchantStoreFixedContentTemplate, FixedContent: &invalid, Enabled: true})
		require.ErrorIs(t, err, ErrMerchantStoreInput)
	}
	storeFixedStockless(t, f.product.ID)
}

func TestMerchantStoreFixedContentNativeRefundEvidenceAndHeldQuantity(t *testing.T) {
	f := newStoreFixedTestFixture(t, "platform:waffo_pancake")
	storeFixedNoStockWrites(t, DB)
	in := storeFixedCheckout(t, f, "native-order", "platform:waffo_pancake")
	in.Quantity = 2
	o, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 3, "USD", "1"))
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "paid-native-receipt"))
	require.NoError(t, RecordMerchantStoreRefundPaymentBasis(o.ID, VerifiedMerchantStoreRefundPaymentBasis{ReceiptReference: "paid-native-receipt", PaymentReference: "original-native-payment", AmountMinor: 3, Currency: "USD", EvidenceHash: strings.Repeat("a", 64)}))
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, err)
	r, err := RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: "native-one", Reason: "Quantity issue", Mode: "quantity", Quantity: 1})
	require.NoError(t, err)
	require.EqualValues(t, 1, r.AmountMinor)
	require.Equal(t, 333333, r.PrincipalQuota, "refund uses attested native charge rather than reconstructing an exchange rate")
	refundApprove(t, f.seller.Id, o, r)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, 1, claim.Quantity, "provider-held quantity is withheld without fake stock IDs")
	used, err := storeBuyerPurchaseUsage(DB, f.product.ID, f.buyer.Id)
	require.NoError(t, err)
	require.EqualValues(t, 2, used, "pending native refund does not release buyer capacity")
	evidence := MerchantStoreVerifiedRefundEvidence{PaymentReference: "original-native-payment", RefundReference: "returned-native-one", AmountMinor: 1, Currency: "USD", EvidenceHash: strings.Repeat("b", 64)}
	require.NoError(t, CompleteMerchantStoreVerifiedRefund(r.ID, evidence))
	require.NoError(t, CompleteMerchantStoreVerifiedRefund(r.ID, evidence))
	used, err = storeBuyerPurchaseUsage(DB, f.product.ID, f.buyer.Id)
	require.NoError(t, err)
	require.EqualValues(t, 1, used)
	last, err := RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("native-rest"))
	require.NoError(t, err)
	require.EqualValues(t, 2, last.AmountMinor)
	require.Equal(t, 1, last.Quantity)
	refundApprove(t, f.seller.Id, o, last)
	evidence.RefundReference, evidence.AmountMinor = "returned-native-rest", 2
	require.NoError(t, CompleteMerchantStoreVerifiedRefund(last.ID, evidence))
	storeBalance(t, f.seller.Id, 9990000)
	storeBalance(t, f.root.Id, 10000)
	storeBalance(t, f.buyer.Id, 10000000)
	storeFixedStockless(t, f.product.ID)
}

func TestMerchantStoreFixedContentFullDiscountAndFutureFloorPreserveDelivery(t *testing.T) {
	f := newStoreFixedTestFixture(t, "balance")
	storeFixedNoStockWrites(t, DB)
	bps := 10000
	code, err := SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, "", MerchantStoreDiscountCodeInput{DiscountBPS: &bps})
	require.NoError(t, err)
	in := storeFixedCheckout(t, f, "free-shared", "free")
	in.Quantity, in.PromotionCode = 2, code.Code
	o, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.Equal(t, "paid", o.Status)
	require.Zero(t, o.PriceQuota)
	require.Zero(t, o.FeeQuota)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 10000000)
	var transfers int64
	require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ?", o.ID).Count(&transfers).Error)
	require.Zero(t, transfers)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, err)
	storeWriterGateForTest(t, strconv.Itoa(MerchantStoreWriterCapability+1))
	_, _, err = CreateMerchantStoreOrder(storeFixedCheckout(t, f, "new-future-floor", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err, "delivery of an existing order does not depend on new-write activation")
	require.Equal(t, storeFixedTestContent, claim.FixedContent)
	require.Equal(t, 2, claim.Quantity)
	storeFixedStockless(t, f.product.ID)
}
