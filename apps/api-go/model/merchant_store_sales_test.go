package model

import (
	"fmt"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

func storeLimit(value int64) *int64 { return &value }

func storeSalesProduct(t *testing.T, f storeFixture) *MerchantStoreProduct {
	t.Helper()
	p, err := GetMerchantStoreProduct(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	return p
}

func TestMerchantStoreSalesLimitSeparatesInventoryFromLifetimeSales(t *testing.T) {
	f := newStoreFixture(t, "balance")
	items := make([]string, 98)
	for i := range items {
		items[i] = fmt.Sprintf("private-inventory-%d", i)
	}
	_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, items)
	require.NoError(t, err)
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(10)))
	p := storeSalesProduct(t, f)
	require.EqualValues(t, 100, p.AvailableStock)
	require.EqualValues(t, 10, p.SaleAvailable)
	public, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.EqualValues(t, 10, public.AvailableStock, "legacy public stock means immediately purchasable stock")
	in := f.checkout("too-many", "balance")
	in.Quantity = 11
	_, _, err = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreStock)
	in = f.checkout("first-ten", "balance")
	in.Quantity = 10
	_, _, err = CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	p = storeSalesProduct(t, f)
	require.EqualValues(t, 90, p.AvailableStock)
	require.EqualValues(t, 10, p.PaidQuantity)
	require.Zero(t, p.ReservedQuantity)
	require.Zero(t, p.SaleAvailable)
	require.True(t, p.TradingPaused)
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(20)))
	require.EqualValues(t, 10, storeSalesProduct(t, f).SaleAvailable)
	in = f.checkout("next-ten", "balance")
	in.Quantity = 10
	_, _, err = CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	p = storeSalesProduct(t, f)
	require.EqualValues(t, 80, p.AvailableStock)
	require.EqualValues(t, 20, p.PaidQuantity)
	require.Zero(t, p.SaleAvailable)
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, nil))
	require.EqualValues(t, 80, storeSalesProduct(t, f).SaleAvailable)
}

func TestMerchantStoreSalesReservationCountsOnceAndCancelReleases(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(2)))
	in := f.checkout("reserve-two", "platform:waffo_pancake")
	in.Quantity = 2
	o, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	p := storeSalesProduct(t, f)
	require.Zero(t, p.PaidQuantity)
	require.EqualValues(t, 2, p.ReservedQuantity)
	require.Zero(t, p.SaleAvailable)
	require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
	p = storeSalesProduct(t, f)
	require.Zero(t, p.ReservedQuantity)
	require.EqualValues(t, 2, p.SaleAvailable)
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(1)))
	_, _, err = CreateMerchantStoreOrder(f.checkout("reserve-one", "platform:waffo_pancake"))
	require.NoError(t, err)
	p = storeSalesProduct(t, f)
	require.EqualValues(t, 1, p.AvailableStock)
	require.EqualValues(t, 1, p.ReservedQuantity)
	require.Zero(t, p.SaleAvailable)
}

func TestMerchantStoreSalesIssuedOrdersSurviveLowerLimitAndOffShelf(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(1)))
	in := f.checkout("issued", "platform:waffo_pancake")
	o, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(0)))
	require.NoError(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, false))
	replay, created, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, o.ID, replay.ID)
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "frozen-valid-receipt"))
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, err)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Len(t, claim.Items, 1)
	p := storeSalesProduct(t, f)
	require.Equal(t, "off_shelf", p.Status)
	require.EqualValues(t, 1, p.PaidQuantity)
	require.Zero(t, p.ReservedQuantity)
	require.Zero(t, p.SaleAvailable)
	require.NoError(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, true))
	_, _, err = CreateMerchantStoreOrder(f.checkout("new-blocked", "platform:waffo_pancake"))
	require.ErrorIs(t, err, ErrMerchantStoreStock)
}

func TestMerchantStoreSalesVerifiedPaymentLiabilityWithoutStockIsNotLost(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(1)))
	o, _, err := CreateMerchantStoreOrder(f.checkout("closed-before-proof", "platform:waffo_pancake"))
	require.NoError(t, err)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
	require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
	require.EqualValues(t, 1, storeSalesProduct(t, f).SaleAvailable)
	require.NoError(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, "late-verified-receipt", "settlement_unavailable"))
	p := storeSalesProduct(t, f)
	require.EqualValues(t, 2, p.AvailableStock)
	require.EqualValues(t, 1, p.PaidQuantity)
	require.Zero(t, p.ReservedQuantity)
	require.Zero(t, p.SaleAvailable)
	other := marketTestUser(t, DB, "sales-other-buyer", 10000000, common.RoleCommonUser)
	require.NoError(t, AcceptMerchantStoreDisclaimer(other.Id, MerchantStoreDisclaimerVersion))
	in := f.checkout("new-after-proof", "platform:waffo_pancake")
	in.BuyerID = other.Id
	_, _, err = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreStock)
	// A vanished product cannot have new sales, but must not erase money proof.
	require.NoError(t, DB.Where("id = ?", f.product.ID).Delete(&MerchantStoreProduct{}).Error)
	require.NoError(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, "late-verified-receipt", "stock_unavailable"))
	stored, err := GetMerchantStorePaymentOrder(o.ID)
	require.NoError(t, err)
	require.Positive(t, stored.VerifiedPaymentIssueAt)
	require.Equal(t, "late-verified-receipt", stored.ProviderTradeID)
}

func TestMerchantStoreSalesVerifiedReservedPaymentDoesNotDoubleCount(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	o, _, err := CreateMerchantStoreOrder(f.checkout("verified-held", "platform:waffo_pancake"))
	require.NoError(t, err)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
	require.NoError(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, "verified-held-receipt", "settlement_unavailable"))
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(2)))
	p := storeSalesProduct(t, f)
	require.EqualValues(t, 1, p.PaidQuantity)
	require.Zero(t, p.ReservedQuantity)
	require.EqualValues(t, 1, p.SaleAvailable)
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "verified-held-receipt"))
	p = storeSalesProduct(t, f)
	require.EqualValues(t, 1, p.PaidQuantity)
	require.Zero(t, p.ReservedQuantity)
	require.EqualValues(t, 1, p.SaleAvailable)
}

func TestMerchantStoreSalesLegacyNullVerificationStillCountsReservation(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(1)))
	o, _, err := CreateMerchantStoreOrder(f.checkout("legacy-null", "platform:waffo_pancake"))
	require.NoError(t, err)
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", o.ID).Update("verified_payment_issue_at", nil).Error)
	p := storeSalesProduct(t, f)
	require.Zero(t, p.PaidQuantity)
	require.EqualValues(t, 1, p.ReservedQuantity)
	require.Zero(t, p.SaleAvailable)
	other := marketTestUser(t, DB, "sales-null-buyer", 10000000, common.RoleCommonUser)
	require.NoError(t, AcceptMerchantStoreDisclaimer(other.Id, MerchantStoreDisclaimerVersion))
	in := f.checkout("null-held-other-buyer", "platform:waffo_pancake")
	in.BuyerID = other.Id
	_, _, err = CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStoreStock)
}

func TestMerchantStoreSalesOffShelfCannotBypassReviewOrResetLimit(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(1)))
	p := storeSalesProduct(t, f)
	reviewedAt := p.ReviewedAt
	require.NoError(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, false))
	require.NoError(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, false))
	_, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.Error(t, err)
	require.ErrorIs(t, SetMerchantStoreProductPaused(f.seller.Id, f.product.ID, false), ErrMerchantStoreConflict)
	require.NoError(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, true))
	require.Equal(t, reviewedAt, storeSalesProduct(t, f).ReviewedAt)
	require.NoError(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, false))
	_, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, MerchantStoreProductInput{Title: "Changed details", PriceQuota: 500000, PaymentMethods: []string{"balance"}})
	require.NoError(t, err)
	p = storeSalesProduct(t, f)
	require.Equal(t, "draft", p.Status)
	require.Zero(t, p.ReviewedAt)
	require.EqualValues(t, 1, *p.SaleLimit, "older content editors cannot clear a sales limit")
	require.ErrorIs(t, SetMerchantStoreProductListed(f.seller.Id, f.product.ID, true), ErrMerchantStoreConflict)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, "reapproved"))
	require.Equal(t, "published", storeSalesProduct(t, f).Status)
}

func TestMerchantStoreSalesControlsEnforceOwnerAndIntegerBounds(t *testing.T) {
	f := newStoreFixture(t, "balance")
	outsider := marketTestUser(t, DB, "sales-outsider", 0, common.RoleCommonUser)
	require.ErrorIs(t, SetMerchantStoreProductSaleLimit(outsider.Id, f.product.ID, storeLimit(1)), ErrMerchantStoreDenied)
	require.ErrorIs(t, SetMerchantStoreProductListed(outsider.Id, f.product.ID, false), ErrMerchantStoreDenied)
	require.ErrorIs(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(-1)), ErrMerchantStoreInput)
	require.ErrorIs(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(int64(common.MaxWalletQuota)+1)), ErrMerchantStoreInput)
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.root.Id, f.product.ID, storeLimit(0)))
	_, _, err := CreateMerchantStoreOrder(f.checkout("zero", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreStock)
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.root.Id, f.product.ID, nil))
	require.Nil(t, storeSalesProduct(t, f).SaleLimit)
}

func TestMerchantStoreSalesOffShelfRetiresAppliedAIReview(t *testing.T) {
	db, seller, root, product := marketAIProduct(t, setting.MarketAIReviewAuto)
	job := marketAIClaim(t)
	require.NoError(t, marketAIComplete(t, job, false, true))
	require.NoError(t, db.First(product, "id = ?", product.ID).Error)
	require.Equal(t, "published", product.Status)
	require.NotEmpty(t, product.AIReviewToken)
	reviewedAt := product.ReviewedAt
	require.NoError(t, SetMerchantStoreProductListed(seller.Id, product.ID, false))
	require.NoError(t, db.First(product, "id = ?", product.ID).Error)
	require.Empty(t, product.AIReviewToken)
	require.Equal(t, "off_shelf", product.Status)
	require.ErrorIs(t, ReviewMerchantStoreProduct(root.Id, product.ID, true, "old AI review"), ErrMerchantStoreConflict)
	require.NoError(t, marketAIComplete(t, job, false, true))
	require.NoError(t, db.First(product, "id = ?", product.ID).Error)
	require.Equal(t, "off_shelf", product.Status)
	require.NoError(t, SetMerchantStoreProductListed(seller.Id, product.ID, true))
	require.NoError(t, db.First(product, "id = ?", product.ID).Error)
	require.Equal(t, reviewedAt, product.ReviewedAt)
	require.Equal(t, "published", product.Status)
}

func TestMerchantStoreSalesSchemaHasOnlyOneNullablePersistentField(t *testing.T) {
	parsed, err := schema.Parse(&MerchantStoreProduct{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	field := parsed.LookUpField("SaleLimit")
	require.NotNil(t, field)
	require.Equal(t, "sale_limit", field.DBName)
	require.Equal(t, "bigint", field.TagSettings["TYPE"])
	require.False(t, field.NotNull)
	require.False(t, field.HasDefaultValue)
	for _, name := range []string{"PaidQuantity", "ReservedQuantity", "SaleAvailable"} {
		require.Empty(t, parsed.LookUpField(name).DBName, "sales counters must remain derived, not competing facts")
	}
	for _, index := range parsed.ParseIndexes() {
		for _, entry := range index.Fields {
			require.NotEqual(t, "sale_limit", entry.DBName)
		}
	}
}
