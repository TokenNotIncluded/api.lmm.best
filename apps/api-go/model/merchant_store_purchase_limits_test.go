package model

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func storePurchaseGateFour(t *testing.T) {
	t.Helper()
	require.GreaterOrEqual(t, MerchantStoreWriterCapability, 4, "purchase limits require the reviewed capability-4 integration")
	storeWriterGateForTest(t, "4")
}

func TestMerchantStorePurchaseLimitInputPresenceAndValidation(t *testing.T) {
	for _, test := range []struct {
		body    string
		present bool
		value   *int64
	}{
		{`{}`, false, nil},
		{`{"max_quantity_per_order":null}`, true, nil},
		{`{"max_quantity_per_order":7}`, true, storeLimit(7)},
	} {
		var in MerchantStoreProductInput
		require.NoError(t, json.Unmarshal([]byte(test.body), &in))
		require.Equal(t, test.present, in.maxQuantityPerOrderPresent)
		require.Equal(t, test.value, in.MaxQuantityPerOrder)
	}
	for _, field := range []string{"max_quantity_per_order", "max_quantity_per_buyer"} {
		for _, value := range []string{"0", "-1", "1.5", `"2"`, "true", "9007199254740992"} {
			var in MerchantStoreProductInput
			err := json.Unmarshal([]byte(fmt.Sprintf(`{"title":"Limit","price_quota":500000,"%s":%s}`, field, value)), &in)
			if err == nil {
				require.ErrorIs(t, validateStoreProduct(&in), ErrMerchantStoreInput, field+value)
			}
		}
	}
}

func TestMerchantStoreProductInputKeepsTestModeAndLimitPresenceTogether(t *testing.T) {
	var in MerchantStoreProductInput
	require.NoError(t, json.Unmarshal([]byte(`{"test_mode":false,"max_quantity_per_order":null,"max_quantity_per_buyer":2}`), &in))
	require.NotNil(t, in.TestMode)
	require.False(t, *in.TestMode)
	require.True(t, in.maxQuantityPerOrderPresent)
	require.Nil(t, in.MaxQuantityPerOrder)
	require.True(t, in.maxQuantityPerBuyerPresent)
	require.EqualValues(t, 2, *in.MaxQuantityPerBuyer)
	for _, body := range []string{
		`{"test_mode":null,"max_quantity_per_order":null}`,
		`{"TEST_MODE":null,"max_quantity_per_buyer":2}`,
	} {
		require.ErrorIs(t, json.Unmarshal([]byte(body), &in), ErrMerchantStoreInput)
	}
}

func TestMerchantStorePurchaseLimitsOldEditorPreservesAndExplicitNullClears(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storePurchaseGateFour(t)
	in := MerchantStoreProductInput{Title: "Limits", PriceQuota: 500000, PaymentMethods: []string{"balance"}, MaxQuantityPerOrder: storeLimit(2), MaxQuantityPerBuyer: storeLimit(7)}
	p, err := SaveMerchantStoreProduct(f.seller.Id, f.product.ID, in)
	require.NoError(t, err)
	require.EqualValues(t, 2, *p.MaxQuantityPerOrder)
	require.EqualValues(t, 7, *p.MaxQuantityPerBuyer)
	var old MerchantStoreProductInput
	require.NoError(t, json.Unmarshal([]byte(`{"title":"Old editor","price_quota":500000,"payment_methods":["balance"]}`), &old))
	p, err = SaveMerchantStoreProduct(f.seller.Id, p.ID, old)
	require.NoError(t, err)
	require.EqualValues(t, 2, *p.MaxQuantityPerOrder)
	require.EqualValues(t, 7, *p.MaxQuantityPerBuyer)
	var clear MerchantStoreProductInput
	require.NoError(t, json.Unmarshal([]byte(`{"title":"Clear one","price_quota":500000,"payment_methods":["balance"],"max_quantity_per_order":null}`), &clear))
	p, err = SaveMerchantStoreProduct(f.seller.Id, p.ID, clear)
	require.NoError(t, err)
	require.Nil(t, p.MaxQuantityPerOrder)
	require.EqualValues(t, 7, *p.MaxQuantityPerBuyer)
}

func TestMerchantStorePurchaseLimitsRequireFloorFourWithoutBreakingReplay(t *testing.T) {
	f := newStoreFixture(t, "balance")
	in := f.checkout("before-limit", "balance")
	o, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.NoError(t, DB.Model(f.product).Update("max_quantity_per_order", 1).Error)
	before := storeWriterSnapshot(t)
	_, _, err = CreateMerchantStoreOrder(f.checkout("blocked-old-floor", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	require.Equal(t, before, storeWriterSnapshot(t))
	replay, created, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, o.ID, replay.ID)
	_, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, MerchantStoreProductInput{Title: "Not yet", PriceQuota: 500000, MaxQuantityPerBuyer: storeLimit(2)})
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
}

func TestMerchantStorePurchaseLimitOrderAndPendingCancellation(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake", "balance")
	storePurchaseGateFour(t)
	require.NoError(t, DB.Model(f.product).Updates(map[string]any{"max_quantity_per_order": 1, "max_quantity_per_buyer": 1}).Error)
	in := f.checkout("too-many", "balance")
	in.Quantity = 2
	before := storeWriterSnapshot(t)
	_, _, err := CreateMerchantStoreOrder(in)
	require.ErrorIs(t, err, ErrMerchantStorePurchaseLimit)
	require.Equal(t, before, storeWriterSnapshot(t), "rejection cannot hold stock or charge any wallet")
	o, _, err := CreateMerchantStoreOrder(f.checkout("pending-hold", "platform:waffo_pancake"))
	require.NoError(t, err)
	_, _, err = CreateMerchantStoreOrder(f.checkout("balance-bypass", "balance"))
	require.ErrorIs(t, err, ErrMerchantStorePurchaseLimit)
	require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
	_, _, err = CreateMerchantStoreOrder(f.checkout("after-release", "balance"))
	require.NoError(t, err)
	_, _, err = CreateMerchantStoreOrder(f.checkout("after-paid", "balance"))
	require.ErrorIs(t, err, ErrMerchantStorePurchaseLimit)
	storeBalance(t, f.buyer.Id, 9500000)
}

func TestMerchantStoreBuyerPurchaseUsageUsesOnlyCompletedFrozenStockAcrossVariants(t *testing.T) {
	f := newStoreFixture(t, "balance")
	defaultID, customID := MerchantStoreDefaultVariantID(f.product.ID), uuid.NewString()
	otherBuyer := marketTestUser(t, DB, "limit-other-buyer", 10000000, common.RoleCommonUser)
	orders := []MerchantStoreOrder{
		{ID: "legacy-paid", TradeNo: "limit-legacy", ProductID: f.product.ID, BuyerID: f.buyer.Id, Quantity: 3, Status: "paid", PaidAt: 10},
		{ID: "default-refund", TradeNo: "limit-default", ProductID: f.product.ID, VariantID: defaultID, BuyerID: f.buyer.Id, Quantity: 2, Status: "refund_pending", PaidAt: 10},
		{ID: "custom-refund", TradeNo: "limit-custom", ProductID: f.product.ID, VariantID: customID, BuyerID: f.buyer.Id, Quantity: 2, Status: "refunded", PaidAt: 10},
		{ID: "verified-held", TradeNo: "limit-verified", ProductID: f.product.ID, BuyerID: f.buyer.Id, Quantity: 1, Status: "reconciliation_pending", VerifiedPaymentIssueAt: 10},
		{ID: "real-held", TradeNo: "limit-held", ProductID: f.product.ID, BuyerID: f.buyer.Id, Quantity: 3, Status: "pending"},
		{ID: "empty-pending", TradeNo: "limit-empty", ProductID: f.product.ID, BuyerID: f.buyer.Id, Quantity: 7, Status: "pending"},
		{ID: "other-buyer", TradeNo: "limit-other", ProductID: f.product.ID, BuyerID: otherBuyer.Id, Quantity: 9, Status: "paid"},
	}
	require.NoError(t, DB.Create(&orders).Error)
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", "real-held").UpdateColumn("paid_at", nil).Error)
	add := func(order, product, state string, variant *string) {
		t.Helper()
		require.NoError(t, DB.Create(&MerchantStoreStock{ID: uuid.NewString(), ProductID: product, VariantID: variant, OrderID: order, State: state}).Error)
	}
	add("legacy-paid", f.product.ID, "refunded", &customID) // historical empty variant accepts its frozen pool
	add("default-refund", f.product.ID, "refunded", nil)
	add("default-refund", f.product.ID, "refunded", &customID) // unrelated variant does not release quantity
	add("custom-refund", f.product.ID, "refunded", &customID)
	add("custom-refund", f.product.ID, "refunded", &customID)
	add("custom-refund", "different-product", "refunded", &customID)
	add("verified-held", f.product.ID, "reserved", nil) // paid evidence is counted once
	add("real-held", f.product.ID, "reserved", nil)
	add("real-held", f.product.ID, "reserved", &defaultID) // count actual two holds, not order quantity three
	add("real-held", "different-product", "reserved", nil)
	used, err := storeBuyerPurchaseUsage(DB, f.product.ID, f.buyer.Id)
	require.NoError(t, err)
	require.EqualValues(t, 6, used) // (3-1)+(2-1)+(2-2)+1+2
	// Amount-only refunds and pending requests never change stock state, so no
	// quantity is restored until completion revokes an actual frozen item.
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", "legacy-paid").Update("status", "refund_pending").Error)
	again, err := storeBuyerPurchaseUsage(DB, f.product.ID, f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, used, again)
	p := *f.product
	p.MaxQuantityPerBuyer = storeLimit(7)
	require.NoError(t, PopulateMerchantStoreBuyerPurchaseRemaining(f.buyer.Id, &p))
	require.EqualValues(t, 1, *p.BuyerPurchaseRemaining)
	require.NoError(t, PopulateMerchantStoreBuyerPurchaseRemaining(0, &p))
	require.Nil(t, p.BuyerPurchaseRemaining)
	add("custom-refund", f.product.ID, "refunded", &customID)
	_, err = storeBuyerPurchaseUsage(DB, f.product.ID, f.buyer.Id)
	require.ErrorIs(t, err, ErrMerchantStoreConflict, "corrupt over-refund cannot manufacture an allowance")
}
