package model

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The integration owner adds these immutable snapshot columns to the order
// model. This test-only projection allows the independent source branch to
// exercise real SQL without editing that owner's production order file.
type merchantStoreDiscountOrderProjection struct {
	PromotionID        string `gorm:"size:36;index"`
	PromotionCode      string `gorm:"size:64"`
	DiscountBPS        int
	OriginalPriceQuota int `gorm:"type:bigint"`
	DiscountQuota      int `gorm:"type:bigint"`
}

func (merchantStoreDiscountOrderProjection) TableName() string { return "merchant_store_orders" }

func newStoreDiscountFixture(t *testing.T) storeFixture {
	t.Helper()
	require.GreaterOrEqual(t, MerchantStoreWriterCapability, 4, "coupon integration tests require the central capability-4 protocol; the source base must not pretend to support coupons")
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.AutoMigrate(&MerchantStoreDiscountCode{}))
	for _, field := range []string{"PromotionID", "PromotionCode", "DiscountBPS", "OriginalPriceQuota", "DiscountQuota"} {
		if !DB.Migrator().HasColumn(&merchantStoreDiscountOrderProjection{}, field) {
			require.NoError(t, DB.Migrator().AddColumn(&merchantStoreDiscountOrderProjection{}, field))
		}
	}
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	return f
}

func storeDiscountInt(value int) *int       { return &value }
func storeDiscountInt64(value int64) *int64 { return &value }

func createStoreDiscount(t *testing.T, f storeFixture, bps int, limit *int64) *MerchantStoreDiscountCode {
	t.Helper()
	code, err := SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, "", MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(bps), MaxUses: limit})
	require.NoError(t, err)
	return code
}

func storeDiscountTestOrder(t *testing.T, f storeFixture, code *MerchantStoreDiscountCode, id, status string, paidAt int64) {
	t.Helper()
	require.NoError(t, DB.Table("merchant_store_orders").Create(map[string]any{
		"id": id, "trade_no": "coupon-" + id, "buyer_id": f.buyer.Id, "seller_id": f.seller.Id,
		"product_id": f.product.ID, "status": status, "quantity": 1000, "paid_at": paidAt,
		"promotion_id": code.ID, "promotion_code": code.Code, "discount_bps": code.DiscountBPS,
		"original_price_quota": 500000, "discount_quota": 500000, "expires_at": common.GetTimestamp() - 3600,
	}).Error)
}

func TestMerchantStoreDiscountRoundingIndependentBigIntegerOracle(t *testing.T) {
	for _, original := range []int{1, 2, 9999, 10000, 10001, 500000, common.MaxWalletQuota} {
		for _, bps := range []int{0, 1, 100, 4999, 5000, 9999, 10000} {
			t.Run(fmt.Sprintf("%d-%d", original, bps), func(t *testing.T) {
				numerator := new(big.Int).Mul(big.NewInt(int64(original)), big.NewInt(int64(10000-bps)))
				numerator.Add(numerator, big.NewInt(9999))
				expected := new(big.Int).Quo(numerator, big.NewInt(10000)).Int64()
				actual, err := storeDiscountNetQuota(original, bps)
				require.NoError(t, err)
				require.EqualValues(t, expected, actual)
				require.Equal(t, bps == 10000, actual == 0)
			})
		}
	}
	for _, input := range [][2]int{{0, 10000}, {-1, 0}, {common.MaxWalletQuota + 1, 100}, {1, -1}, {1, 10001}} {
		_, err := storeDiscountNetQuota(input[0], input[1])
		require.ErrorIs(t, err, ErrMerchantStoreInput)
	}
}

func TestMerchantStoreDiscountRequiresActivatedCapabilityFourBeforeAnyUse(t *testing.T) {
	db := marketTestDB(t)
	// Deliberately do not create the coupon table or snapshot columns. Older
	// floors must reject a nonempty code before querying either new schema.
	for _, floor := range []string{"1", "2", "3", "04", "4 ", fmt.Sprint(MerchantStoreWriterCapability + 1), "unknown"} {
		require.NoError(t, db.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", floor).Error)
		require.ErrorIs(t, storeRequireDiscountCodeWriter(db), ErrMerchantStoreWriterFrozen)
		_, _, err := storeDiscountCodeTx(db, &MerchantStoreProduct{ID: uuid.NewString()}, strings.Repeat("a", 43), uuid.NewString(), 1, 500000)
		require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
		promotion, unchanged, err := storeDiscountCodeTx(db, nil, "", "", 1, 500000)
		require.NoError(t, err)
		require.Nil(t, promotion)
		require.Equal(t, 500000, unchanged)
	}
}

func TestMerchantStoreDiscountOwnershipGeneratedIdentityAndInputBounds(t *testing.T) {
	f := newStoreDiscountFixture(t)
	code := createStoreDiscount(t, f, 0, nil)
	require.Len(t, code.Code, 43)
	require.Contains(t, code.SharePath, "?promotion="+code.Code)
	updated, err := SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, code.ID, MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(10000)})
	require.NoError(t, err)
	require.Equal(t, code.ID, updated.ID)
	require.Equal(t, code.Code, updated.Code)
	require.Equal(t, code.CreatedAt, updated.CreatedAt)
	for _, actor := range []int{0, f.buyer.Id} {
		_, err = SaveMerchantStoreDiscountCode(actor, f.product.ID, "", MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(500)})
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		_, _, err = ListMerchantStoreDiscountCodes(actor, f.product.ID, 0, 10)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
	}
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.buyer.Id).Update("role", common.RoleAdminUser).Error)
	_, err = SaveMerchantStoreDiscountCode(f.buyer.Id, f.product.ID, "", MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(500)})
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	rootCode, err := SaveMerchantStoreDiscountCode(f.root.Id, f.product.ID, "", MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(500)})
	require.NoError(t, err)
	require.Equal(t, f.seller.Id, rootCode.SellerID)
	for _, input := range []MerchantStoreDiscountCodeInput{
		{}, {DiscountBPS: storeDiscountInt(-1)}, {DiscountBPS: storeDiscountInt(10001)},
		{DiscountBPS: storeDiscountInt(100), Status: "unexpected"},
		{DiscountBPS: storeDiscountInt(100), MaxUses: storeDiscountInt64(-1)},
		{DiscountBPS: storeDiscountInt(100), ExpiresAt: storeDiscountInt64(0)},
		{DiscountBPS: storeDiscountInt(100), VariantIDs: []string{uuid.NewString()}},
		{DiscountBPS: storeDiscountInt(100), VariantIDs: []string{MerchantStoreDefaultVariantID(f.product.ID), MerchantStoreDefaultVariantID(f.product.ID)}},
	} {
		_, err = SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, "", input)
		require.ErrorIs(t, err, ErrMerchantStoreInput)
	}
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "not-canonical").Error)
	_, err = SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, "", MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(100)})
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
}

func TestMerchantStoreDiscountLifetimeOrderUseAndUnpaidObligations(t *testing.T) {
	f := newStoreDiscountFixture(t)
	code := createStoreDiscount(t, f, 1000, nil)
	for _, row := range []struct {
		id, status string
		paid       int64
	}{
		{"paid", "paid", 1}, {"refund", "refunded", 2},
		{"pending", "pending", 0}, {"reconcile", "reconciliation_pending", 0},
		{"refund-pending", "refund_pending", 0}, {"unknown", "future-status", 0},
		{"reserved", "expired", 0}, {"verified", "cancelled", 0},
		{"issued", "expired", 0}, {"session", "cancelled", 0},
		{"closed-stock", "expired", 0}, {"closed", "expired", 0},
		{"cancelled", "cancelled", 0}, {"expired", "expired", 0},
	} {
		storeDiscountTestOrder(t, f, code, row.id, row.status, row.paid)
	}
	for _, id := range []string{"reserved", "closed-stock"} {
		require.NoError(t, DB.Create(&MerchantStoreStock{ID: uuid.NewString(), ProductID: f.product.ID, OrderID: id, State: "reserved", Ciphertext: "opaque-test-ciphertext"}).Error)
	}
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", "verified").Update("verified_payment_issue_at", common.GetTimestamp()).Error)
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", "issued").Update("gateway_snapshot", "opaque-issued-context").Error)
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", "session").Update("provider_session_id", "issued-session").Error)
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id IN ?", []string{"closed", "closed-stock"}).Updates(map[string]any{"provider_session_id": "closed-session", "provider_closure_reference": "provider-confirmed-closed"}).Error)
	rows, total, err := ListMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.EqualValues(t, 2, rows[0].UsesCount)
	require.EqualValues(t, 9, rows[0].ReservedCount)
	// Item quantity=1000 and past local expiry above never change order counts.
	_, err = SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, code.ID, MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(1000), MaxUses: storeDiscountInt64(11)})
	require.NoError(t, err)
	_, err = ResolveMerchantStoreDiscountCode(0, f.product.ID, code.Code)
	require.ErrorIs(t, err, ErrMerchantStoreDiscountLimit)
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", "pending").Update("status", "cancelled").Error)
	_, err = ResolveMerchantStoreDiscountCode(0, f.product.ID, code.Code)
	require.NoError(t, err)
	// Paid history remains used even when its current status becomes cancelled.
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", "paid").Update("status", "cancelled").Error)
	rows, _, err = ListMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 2, rows[0].UsesCount)
}

func TestMerchantStoreDiscountBatchAtomicRevocationAndHistorySafeHardDelete(t *testing.T) {
	f := newStoreDiscountFixture(t)
	first := createStoreDiscount(t, f, 10000, nil)
	second := createStoreDiscount(t, f, 1000, nil)
	n, err := BatchMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, []string{first.ID, uuid.NewString()}, "pause")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.Zero(t, n)
	_, err = ResolveMerchantStoreDiscountCode(0, f.product.ID, first.Code)
	require.NoError(t, err)
	n, err = BatchMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, []string{first.ID, second.ID}, "pause")
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	_, err = ResolveMerchantStoreDiscountCode(0, f.product.ID, first.Code)
	require.ErrorIs(t, err, ErrMerchantStoreDiscountUnavailable)
	_, err = BatchMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, []string{first.ID, second.ID}, "resume")
	require.NoError(t, err)
	_, err = BatchMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, []string{first.ID}, "revoke")
	require.NoError(t, err)
	_, err = BatchMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, []string{first.ID, second.ID}, "resume")
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
	_, err = SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, first.ID, MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(100), Status: "active"})
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
	storeDiscountTestOrder(t, f, first, "frozen", "paid", 123)
	var before, after map[string]any
	require.NoError(t, DB.Table("merchant_store_orders").Where("id = ?", "frozen").Take(&before).Error)
	var stockBefore, stockAfter int64
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Count(&stockBefore).Error)
	n, err = BatchMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, []string{first.ID}, "delete")
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.NoError(t, DB.Table("merchant_store_orders").Where("id = ?", "frozen").Take(&after).Error)
	require.Equal(t, before, after)
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Count(&stockAfter).Error)
	require.Equal(t, stockBefore, stockAfter)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 10000000)
	_, err = ResolveMerchantStoreDiscountCode(0, f.product.ID, first.Code)
	require.ErrorIs(t, err, ErrMerchantStoreDiscountUnavailable)
}

func TestMerchantStoreDiscountScopeVisibilityAndFreeQuoteHasNoWrites(t *testing.T) {
	f := newStoreDiscountFixture(t)
	variant := MerchantStoreDefaultVariantID(f.product.ID)
	code, err := SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, "", MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(10000), VariantIDs: []string{variant}})
	require.NoError(t, err)
	// No enabled payment method is required for an explicitly free quotation.
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("payment_methods", "[]").Error)
	var beforeVariants, afterVariants, beforeOrders, afterOrders int64
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Where("product_id = ?", f.product.ID).Count(&beforeVariants).Error)
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Count(&beforeOrders).Error)
	quote, err := QuoteMerchantStoreDiscountCode(0, f.product.ID, " "+code.Code+" ", variant, 2)
	require.NoError(t, err)
	require.True(t, quote.Free)
	require.False(t, quote.CheckoutAllowed)
	require.Zero(t, quote.PriceQuota)
	require.Equal(t, 1000000, quote.OriginalPriceQuota)
	require.Equal(t, 1000000, quote.DiscountQuota)
	require.Equal(t, []string{"free"}, quote.PaymentMethods)
	require.Equal(t, f.seller.Id, quote.SellerID)
	encoded, err := json.Marshal(quote)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "seller_id")
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Where("product_id = ?", f.product.ID).Count(&afterVariants).Error)
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Count(&afterOrders).Error)
	require.Equal(t, beforeVariants, afterVariants)
	require.Equal(t, beforeOrders, afterOrders)
	rows, _, err := ListMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, 0, 10)
	require.NoError(t, err)
	require.Zero(t, rows[0].ReservedCount)
	_, err = QuoteMerchantStoreDiscountCode(0, f.product.ID, code.Code, uuid.NewString(), 1)
	require.Error(t, err)
	_, err = QuoteMerchantStoreDiscountCode(0, f.product.ID, code.Code, variant, 3)
	require.ErrorIs(t, err, ErrMerchantStoreStock)
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeDiscountInt64(1)))
	_, err = QuoteMerchantStoreDiscountCode(0, f.product.ID, code.Code, variant, 2)
	require.ErrorIs(t, err, ErrMerchantStoreStock)
	require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, nil))
	// A real enabled alternative still cannot use a default-only coupon.
	other := MerchantStoreVariant{ID: uuid.NewString(), ProductID: f.product.ID, Name: "Another spec", PriceQuota: 500000, Template: f.product.Template, Enabled: true}
	require.NoError(t, DB.Create(&other).Error)
	_, err = QuoteMerchantStoreDiscountCode(0, f.product.ID, code.Code, other.ID, 1)
	require.ErrorIs(t, err, ErrMerchantStoreDiscountUnavailable)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Updates(map[string]any{"test_mode": true, "status": "draft"}).Error)
	for _, actor := range []int{0, f.buyer.Id, f.root.Id} {
		_, err = ResolveMerchantStoreDiscountCode(actor, f.product.ID, code.Code)
		require.ErrorIs(t, err, ErrMerchantStoreDiscountUnavailable)
		_, err = QuoteMerchantStoreDiscountCode(actor, f.product.ID, code.Code, variant, 1)
		require.ErrorIs(t, err, ErrMerchantStoreDiscountUnavailable)
	}
	_, err = QuoteMerchantStoreDiscountCode(f.seller.Id, f.product.ID, code.Code, variant, 1)
	require.NoError(t, err)
	// A discount cannot waive the configured minimum original unit price.
	require.NoError(t, PatchMerchantStoreConfig(f.root.Id, MerchantStoreConfigPatch{MinimumUnitPriceQuota: storeDiscountInt(500001)}))
	_, err = QuoteMerchantStoreDiscountCode(f.seller.Id, f.product.ID, code.Code, variant, 1)
	require.ErrorIs(t, err, ErrMerchantStoreMinimumPrice)
}

func TestMerchantStoreDiscountCleanupPagesPastActiveRowsAndRetainsBusyCodes(t *testing.T) {
	f := newStoreDiscountFixture(t)
	active := make([]MerchantStoreDiscountCode, 201)
	for i := range active {
		code, err := storeToken()
		require.NoError(t, err)
		active[i] = MerchantStoreDiscountCode{ID: uuid.NewString(), ProductID: f.product.ID, SellerID: f.seller.Id, Code: code, DiscountBPS: 100, Status: "active", CreatedAt: int64(i + 1)}
	}
	require.NoError(t, DB.Create(&active).Error)
	// Saving beyond 200 existing codes is permitted; only page/batch sizes are bounded.
	createStoreDiscount(t, f, 100, nil)
	expired, err := SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, "", MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(100), ExpiresAt: storeDiscountInt64(common.GetTimestamp() - 1)})
	require.NoError(t, err)
	exhausted := createStoreDiscount(t, f, 100, storeDiscountInt64(1))
	storeDiscountTestOrder(t, f, exhausted, "exhausted-paid", "refunded", 456)
	busy := createStoreDiscount(t, f, 100, storeDiscountInt64(1))
	storeDiscountTestOrder(t, f, busy, "busy-pending", "pending", 0)
	_, err = BatchMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, []string{busy.ID}, "pause")
	require.NoError(t, err)
	revoked := createStoreDiscount(t, f, 100, nil)
	storeDiscountTestOrder(t, f, revoked, "revoked-paid", "paid", 789)
	var revokedBefore, revokedAfter map[string]any
	require.NoError(t, DB.Table("merchant_store_orders").Where("id = ?", "revoked-paid").Take(&revokedBefore).Error)
	_, err = BatchMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, []string{revoked.ID}, "revoke")
	require.NoError(t, err)
	for _, code := range []*MerchantStoreDiscountCode{expired, exhausted} {
		_, err := ResolveMerchantStoreDiscountCode(0, f.product.ID, code.Code)
		require.Error(t, err)
	}
	n, err := CleanupMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = CleanupMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = CleanupMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = CleanupMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, 1)
	require.NoError(t, err)
	require.Zero(t, n)
	var retained MerchantStoreDiscountCode
	require.NoError(t, DB.First(&retained, "id = ?", busy.ID).Error)
	require.Equal(t, "paused", retained.Status)
	var history int64
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", "exhausted-paid").Count(&history).Error)
	require.EqualValues(t, 1, history)
	require.NoError(t, DB.Table("merchant_store_orders").Where("id = ?", "revoked-paid").Take(&revokedAfter).Error)
	require.Equal(t, revokedBefore, revokedAfter)
	first, total, err := ListMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, 0, 1000)
	require.NoError(t, err)
	require.Len(t, first, 100)
	require.EqualValues(t, 203, total)
	second, nextTotal, err := ListMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, 100, 100)
	require.NoError(t, err)
	require.Equal(t, total, nextTotal)
	seen := map[string]bool{}
	for _, row := range first {
		seen[row.ID] = true
	}
	for _, row := range second {
		require.False(t, seen[row.ID])
	}
}

func TestMerchantStoreDiscountCaseSensitiveScopeAndTerminalExpiry(t *testing.T) {
	f := newStoreDiscountFixture(t)
	code := createStoreDiscount(t, f, 100, nil)
	// Deterministic spelling: avoid assuming a random token contains letters.
	code.Code = strings.Repeat("a", 43)
	require.NoError(t, DB.Model(&MerchantStoreDiscountCode{}).Where("id = ?", code.ID).Update("code", code.Code).Error)
	changed := "A" + code.Code[1:]
	_, err := ResolveMerchantStoreDiscountCode(0, f.product.ID, changed)
	require.ErrorIs(t, err, ErrMerchantStoreDiscountUnavailable)
	p, err := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "Separate product", PriceQuota: 500000})
	require.NoError(t, err)
	_, err = ResolveMerchantStoreDiscountCode(f.seller.Id, p.ID, code.Code)
	require.ErrorIs(t, err, ErrMerchantStoreDiscountUnavailable)
	_, err = SaveMerchantStoreDiscountCode(f.seller.Id, p.ID, code.ID, MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(500)})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, code.ID, MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(100), ExpiresAt: storeDiscountInt64(common.GetTimestamp())})
	require.NoError(t, err)
	_, err = ResolveMerchantStoreDiscountCode(0, f.product.ID, code.Code)
	require.ErrorIs(t, err, ErrMerchantStoreDiscountUnavailable)
}

func TestMerchantStoreDiscountLastUseSerializesWithFrozenOrderCreation(t *testing.T) {
	f := newStoreDiscountFixture(t)
	code := createStoreDiscount(t, f, 10000, storeDiscountInt64(1))
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			results <- storeWithActiveProduct(f.product.ID, func(tx *gorm.DB, p *MerchantStoreProduct) error {
				promotion, price, err := storeDiscountCodeTx(tx, p, code.Code, MerchantStoreDefaultVariantID(p.ID), 1, 500000)
				if err != nil {
					return err
				}
				id := fmt.Sprintf("atomic-%d", index)
				return tx.Table("merchant_store_orders").Create(map[string]any{"id": id, "trade_no": id, "buyer_id": f.buyer.Id, "seller_id": f.seller.Id, "product_id": p.ID, "status": "pending", "quantity": 1, "price_quota": price, "promotion_id": promotion.ID, "promotion_code": promotion.Code, "discount_bps": promotion.DiscountBPS, "original_price_quota": 500000, "discount_quota": 500000 - price}).Error
			})
		}(i)
	}
	workers.Wait()
	close(results)
	var success, limited int
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrMerchantStoreDiscountLimit)
			limited++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, limited)
}
