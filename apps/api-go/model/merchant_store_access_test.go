package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func storeAccessHistoricalGuest(t *testing.T) (MerchantStoreGuest, string) {
	t.Helper()
	session, err := CreateMerchantStoreGuestSession()
	require.NoError(t, err)
	guest, err := ResolveMerchantStoreGuest(DB, session.Token)
	require.NoError(t, err)
	return *guest, session.Token
}

func storeAccessActivateTest(t *testing.T) {
	t.Helper()
	if MerchantStoreWriterCapability < 5 {
		t.Skip("requires the centrally signed capability-5 candidate; no test-only capability override")
	}
	// Existing products were written before catalogue installation. Populate
	// the same new identity mapping required by the real preparation action;
	// do not weaken the public reader's missing-mapping guard for a fixture.
	require.NoError(t, BackfillMerchantStoreCatalogueMappings(DB))
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "5").Error)
	require.True(t, MerchantStoreAccessSupported())
}

func TestMerchantStoreAccessScopeFiltersBeforePagination(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storeAccessActivateTest(t)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		required, err := storeWriterGateRow(tx.Model(&MerchantStoreProduct{}).Where("id = ?", "not-an-option-key"), "SHARE")
		require.NoError(t, err, "the product statement must not filter or poison the gate read")
		require.Equal(t, 5, required)
		return nil
	}))
	registered, private, yes, no := "registered", "private", true, false
	in := storeModeInput(f.product, &no)
	in.Visibility = &registered
	product, err := SaveMerchantStoreProduct(f.seller.Id, f.product.ID, in)
	require.NoError(t, err, "registered and false are compatible canonical/legacy fields")
	require.Equal(t, "registered", product.Visibility)
	require.False(t, product.TestMode)
	in = storeModeInput(product, &no)
	product, err = SaveMerchantStoreProduct(f.seller.Id, product.ID, in)
	require.NoError(t, err)
	require.Equal(t, "registered", product.Visibility, "a cached old editor sending false must not make registered public")
	in.Visibility, in.TestMode = &registered, &yes
	_, err = SaveMerchantStoreProduct(f.seller.Id, product.ID, in)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	in.Visibility, in.TestMode = &private, &no
	_, err = SaveMerchantStoreProduct(f.seller.Id, product.ID, in)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	in.TestMode = &yes
	product, err = SaveMerchantStoreProduct(f.seller.Id, product.ID, in)
	require.NoError(t, err)
	in = storeModeInput(product, &no)
	product, err = SaveMerchantStoreProduct(f.seller.Id, product.ID, in)
	require.NoError(t, err)
	require.Equal(t, "public", product.Visibility, "leaving private with only the legacy flag defaults to public")
	rows := []MerchantStoreProduct{
		{ID: "hidden-private", SellerID: f.seller.Id, Title: "Private", Visibility: "private", Status: "published", CreatedAt: 50},
		{ID: "hidden-registered", SellerID: f.seller.Id, Title: "Registered", Visibility: "registered", Status: "published", CreatedAt: 40},
		{ID: "hidden-draft", SellerID: f.seller.Id, Title: "Draft", Visibility: "public", Status: "draft", CreatedAt: 30},
		{ID: "visible-first", SellerID: f.seller.Id, Title: "Public one", Visibility: "public", Status: "published", CreatedAt: 20},
		{ID: "visible-second", SellerID: f.seller.Id, Title: "Public two", Visibility: "", Status: "published", CreatedAt: 10},
		{ID: "legacy-private", SellerID: f.seller.Id, Title: "Legacy private", Visibility: "", TestMode: true, Status: "pending", CreatedAt: 60},
		{ID: "paused-public", SellerID: f.seller.Id, Title: "Paused", Visibility: "public", Status: "paused", CreatedAt: 70},
		{ID: "deleted-public", SellerID: f.seller.Id, Title: "Deleted", Visibility: "public", Status: "deleted", CreatedAt: 80},
	}
	require.NoError(t, DB.Create(&rows).Error)
	var page []MerchantStoreProduct
	require.NoError(t, MerchantStoreVisibleProductsForViewer(DB, 0).Where("id <> ?", f.product.ID).Order("created_at DESC").Offset(1).Limit(1).Find(&page).Error)
	require.Len(t, page, 1)
	require.Equal(t, "visible-second", page[0].ID)
	var ids []string
	require.NoError(t, MerchantStoreVisibleProductsForViewer(DB, f.buyer.Id).Where("id <> ?", f.product.ID).Order("id").Pluck("id", &ids).Error)
	require.Equal(t, []string{"hidden-registered", "visible-first", "visible-second"}, ids)
	var retained []string
	require.NoError(t, MerchantStoreRetainedProductsForViewer(DB, f.buyer.Id).Where("id <> ?", f.product.ID).Order("id").Pluck("id", &retained).Error)
	require.Contains(t, retained, "paused-public")
	require.NotContains(t, retained, "deleted-public")
	require.NotContains(t, retained, "hidden-private")
	require.NoError(t, MerchantStoreVisibleProductsForViewer(DB, f.seller.Id).Where("id <> ?", f.product.ID).Order("id").Pluck("id", &ids).Error)
	require.Equal(t, []string{"hidden-private", "hidden-registered", "legacy-private", "visible-first", "visible-second"}, ids)
	require.NoError(t, MerchantStoreVisibleProductsForViewer(DB, f.root.Id).Where("id <> ?", f.product.ID).Order("id").Pluck("id", &ids).Error)
	require.NotContains(t, ids, "hidden-private")
	for _, gate := range []string{"missing", "broken", "6"} {
		t.Run("invalid gate cannot downgrade visibility/"+gate, func(t *testing.T) {
			if gate == "missing" {
				require.NoError(t, DB.Where("key = ?", MerchantStoreWriterCapabilityOption).Delete(&Option{}).Error)
			} else {
				storeWriterGateForTest(t, gate)
			}
			products, err := ListPublicMerchantStoreProducts("", 0, 30)
			require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
			require.Empty(t, products)
			product, err := GetPublicMerchantStoreProduct("hidden-registered")
			require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
			require.Nil(t, product)
			var retained []MerchantStoreProduct
			require.ErrorIs(t, MerchantStoreRetainedProductsForViewer(DB, f.buyer.Id).Find(&retained).Error, ErrMerchantStoreWriterFrozen)
			require.Empty(t, retained)
			var original MerchantStoreProduct
			require.NoError(t, DB.First(&original, "id = ?", "hidden-registered").Error)
			require.Equal(t, "registered", original.Visibility)
			if gate == "missing" {
				require.NoError(t, DB.Create(&Option{Key: MerchantStoreWriterCapabilityOption, Value: "5"}).Error)
			} else {
				storeWriterGateForTest(t, "5")
			}
		})
	}
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.buyer.Id).Update("status", common.UserStatusDisabled).Error)
	require.NoError(t, MerchantStoreVisibleProductsForViewer(DB, f.buyer.Id).Where("id <> ?", f.product.ID).Order("id").Pluck("id", &ids).Error)
	require.Equal(t, []string{"visible-first", "visible-second"}, ids)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("status", common.UserStatusDisabled).Error)
	var count int64
	require.NoError(t, MerchantStoreVisibleProductsForViewer(DB, f.seller.Id).Count(&count).Error)
	require.Zero(t, count)
}

func TestMerchantStoreAccessDefaultsAliasAndFailsClosed(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.True(t, f.product.PurchaseLoginRequired)
	require.Equal(t, "public", MerchantStoreProductVisibility(f.product))
	for _, body := range []string{`{"visibility":null}`, `{"purchase_login_required":null}`, `{"test_mode":null}`} {
		var in MerchantStoreProductInput
		require.Error(t, json.Unmarshal([]byte(body), &in), body)
	}
	public, yes, no := "public", true, false
	p := *f.product
	require.ErrorIs(t, storeApplyProductAccess(DB, &p, MerchantStoreProductInput{Visibility: &public, TestMode: &yes}, false), ErrMerchantStoreInput)
	require.ErrorIs(t, storeApplyProductAccess(DB, &p, MerchantStoreProductInput{PurchaseLoginRequired: &no}, false), ErrMerchantStoreWriterFrozen)
	require.True(t, p.PurchaseLoginRequired)
	require.False(t, MerchantStoreAccessSupported())
	_, err := CreateMerchantStoreGuestSession()
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	_, err = SaveMerchantStoreSellerTerms(f.seller.Id, MerchantStoreTermsInput{Content: "My actual delivery and support terms"})
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	for _, model := range []any{&MerchantStoreGuest{}, &MerchantStoreSellerTerms{}, &MerchantStoreTermsAcceptance{}} {
		var n int64
		require.NoError(t, DB.Model(model).Count(&n).Error)
		require.Zero(t, n)
	}
}

func TestMerchantStoreHistoricalGuestAuthorityAndUnknownLookup(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storeAccessActivateTest(t)
	guest, token := storeAccessHistoricalGuest(t)
	other, otherToken := storeAccessHistoricalGuest(t)
	key := "uncertain-response"
	order := MerchantStoreOrder{ID: storeHash("order:guest:" + guest.ID + ":" + key), GuestID: guest.ID, BuyerID: 0, SellerID: f.seller.Id, ProductID: f.product.ID, ProductTitle: "Private historical guest sale", Quantity: 1, PriceQuota: 500000, Status: "pending", TradeNo: "MS0123456789abcdefghijklmnopqrst", CreatedAt: common.GetTimestamp(), ExpiresAt: common.GetTimestamp() + 1800}
	require.NoError(t, DB.Create(&order).Error)
	read, err := GetMerchantStoreGuestOrder(token, order.ID)
	require.NoError(t, err)
	require.Equal(t, order.ID, read.ID)
	require.Equal(t, "new-api-store-guest-"+guest.ID, MerchantStoreOrderBuyerIdentity(read))
	encoded, err := json.Marshal(read)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), token)
	require.NotContains(t, string(encoded), guest.ID)
	_, err = GetMerchantStoreGuestOrder(otherToken, order.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = GetMerchantStoreGuestOrder("", order.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = GetMerchantStoreOrder(0, order.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = ListMerchantStoreOrders(0, false, 0, 20)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	read, err = FindMerchantStoreGuestOrderByRequestKey(token, key)
	require.NoError(t, err)
	require.Equal(t, order.ID, read.ID)
	_, err = FindMerchantStoreGuestOrderByRequestKey(otherToken, key)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetMerchantStoreOrderSearchSummaryWithGuest(order.TradeNo, 0, "")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetMerchantStoreOrderSearchSummaryWithGuest(order.TradeNo, 0, otherToken)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetMerchantStoreOrderSearchSummaryWithGuest(order.TradeNo, 0, token)
	require.NoError(t, err)
	_, err = GetMerchantStoreOrderSearchSummaryWithGuest(order.TradeNo, f.seller.Id, "")
	require.NoError(t, err)
	_, err = GetMerchantStoreOrderSearchSummaryWithGuest(order.TradeNo, f.root.Id, "")
	require.NoError(t, err)
	require.ErrorIs(t, CancelMerchantStoreGuestOrder(otherToken, order.ID), ErrMerchantStoreDenied)
	require.NoError(t, DB.Model(&MerchantStoreGuest{}).Where("id = ?", guest.ID).Update("expires_at", common.GetTimestamp()-1).Error)
	_, err = GetMerchantStoreGuestOrder(token, order.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	var users int64
	require.NoError(t, DB.Model(&User{}).Where("id = 0").Count(&users).Error)
	require.Zero(t, users)
	require.NotEqual(t, guest.ID, other.ID)
}

func TestMerchantStoreTermsAndPlatformAcceptanceAreDistinctSubjects(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storeAccessActivateTest(t)
	guest, token := storeAccessHistoricalGuest(t)
	_, otherToken := storeAccessHistoricalGuest(t)
	terms := MerchantStoreSellerTerms{SellerID: f.seller.Id, Version: uuid.NewString(), Content: "Merchant-defined **support** terms", UpdatedAt: common.GetTimestamp()}
	require.NoError(t, DB.Create(&terms).Error)
	subject := "guest:" + guest.ID
	acceptance := MerchantStoreTermsAcceptance{ID: storeAgreementAcceptanceID("seller", subject, f.seller.Id, terms.Version), Kind: "seller", Subject: subject, SellerID: f.seller.Id, Version: terms.Version, AcceptedAt: common.GetTimestamp()}
	require.NoError(t, DB.Create(&acceptance).Error)
	view, err := GetMerchantStoreProductTerms(0, token, f.product.ID)
	require.NoError(t, err)
	require.True(t, view.Accepted)
	view, err = GetMerchantStoreProductTerms(0, otherToken, f.product.ID)
	require.NoError(t, err)
	require.False(t, view.Accepted)
	accepted, err := HasMerchantStoreGuestDisclaimerAcceptance(token)
	require.NoError(t, err)
	require.False(t, accepted, "seller terms do not replace the nonofficial platform explanation")
	platform := MerchantStoreTermsAcceptance{ID: storeAgreementAcceptanceID("platform", subject, 0, MerchantStoreDisclaimerVersion), Kind: "platform", Subject: subject, SellerID: 0, Version: MerchantStoreDisclaimerVersion, AcceptedAt: common.GetTimestamp()}
	require.NoError(t, DB.Create(&platform).Error)
	accepted, err = HasMerchantStoreGuestDisclaimerAcceptance(token)
	require.NoError(t, err)
	require.True(t, accepted)
	accepted, err = HasMerchantStoreGuestDisclaimerAcceptance(otherToken)
	require.NoError(t, err)
	require.False(t, accepted)
	require.NoError(t, DB.Model(&terms).Update("version", uuid.NewString()).Error)
	view, err = GetMerchantStoreProductTerms(0, token, f.product.ID)
	require.NoError(t, err)
	require.False(t, view.Accepted, "a merchant revision needs a new agreement")
	_, err = SaveMerchantStoreSellerTerms(f.seller.Id, MerchantStoreTermsInput{Content: " \n\t "})
	require.ErrorIs(t, err, ErrMerchantStoreInput)
}

func TestMerchantStoreMemberRequestLookupDoesNotRepeatBalancePayment(t *testing.T) {
	f := newStoreFixture(t, "balance")
	o, made, err := CreateMerchantStoreOrder(f.checkout("lost-response", "balance"))
	require.NoError(t, err)
	require.True(t, made)
	read, err := FindMerchantStoreOrderByRequestKey(f.buyer.Id, "lost-response")
	require.NoError(t, err)
	require.Equal(t, o.ID, read.ID)
	_, err = FindMerchantStoreOrderByRequestKey(f.seller.Id, "lost-response")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	storeBalance(t, f.buyer.Id, 9500000)
	var n int64
	require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ?", o.ID).Count(&n).Error)
	require.EqualValues(t, 2, n)
}

func TestMerchantStoreAccessLegacySchemaDoesNotNeedPhase5DDL(t *testing.T) {
	f := newStoreFixture(t, "balance", "external:epay")
	// Remove access, email, promotion and purchase-limit additions. These are real absent
	// columns/tables, rather than a latest-schema fixture with an old option.
	require.NoError(t, DB.Migrator().DropIndex(&MerchantStoreProduct{}, "idx_merchant_store_products_visibility"))
	require.NoError(t, DB.Migrator().DropIndex(&MerchantStoreOrder{}, "idx_merchant_store_orders_guest_id"))
	require.NoError(t, DB.Migrator().DropIndex(&MerchantStoreOrder{}, "idx_merchant_store_orders_promotion_id"))
	for _, column := range []string{"visibility", "purchase_login_required", "max_quantity_per_order", "max_quantity_per_buyer"} {
		require.NoError(t, DB.Exec("ALTER TABLE merchant_store_products DROP COLUMN "+column).Error)
		require.False(t, DB.Migrator().HasColumn(&MerchantStoreProduct{}, column))
	}
	for _, column := range []string{"guest_id", "seller_terms_version", "seller_terms_content", "seller_terms_accepted_at", "original_price_quota", "discount_quota", "discount_bps", "promotion_id", "promotion_code"} {
		require.NoError(t, DB.Exec("ALTER TABLE merchant_store_orders DROP COLUMN "+column).Error)
		require.False(t, DB.Migrator().HasColumn(&MerchantStoreOrder{}, column))
	}
	for _, row := range []any{&MerchantStoreGuest{}, &MerchantStoreSellerTerms{}, &MerchantStoreTermsAcceptance{}, &MerchantStoreGuestEmailVerification{}, &MerchantStoreDiscountCode{}} {
		require.NoError(t, DB.Migrator().DropTable(row))
		require.False(t, DB.Migrator().HasTable(row))
	}
	public, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.Equal(t, "public", public.Visibility)
	require.True(t, public.PurchaseLoginRequired)
	_, err = ListMerchantStoreProductsForViewer(f.buyer.Id, "", 0, 20)
	require.NoError(t, err)
	_, err = GetMerchantStoreSellerProfileForViewer(0, f.seller.Id)
	require.NoError(t, err)
	view, err := GetMerchantStoreProductTerms(0, "", f.product.ID)
	require.NoError(t, err)
	require.False(t, view.Required)
	view, err = GetMerchantStoreSellerTerms(f.seller.Id)
	require.NoError(t, err)
	require.False(t, view.Configured)
	_, err = ResolveMerchantStoreGuest(DB, strings.Repeat("a", 43))
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	o, made, err := CreateMerchantStoreOrder(f.checkout("old-schema-balance", "balance"))
	require.NoError(t, err)
	require.True(t, made)
	require.Equal(t, "paid", o.Status)
	_, err = FindMerchantStoreOrderByRequestKey(f.buyer.Id, "old-schema-balance")
	require.NoError(t, err)
	pending, made, err := CreateMerchantStoreOrder(f.checkout("old-schema-pending", "external:epay"))
	require.NoError(t, err)
	require.True(t, made)
	require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, pending.ID))
	in := storeModeInput(f.product, nil)
	in.Title = "Still editable before the access migration"
	_, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, in)
	require.NoError(t, err)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, ""))
	_, err = GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	storeBalance(t, f.buyer.Id, 9500000)
}
