package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantStoreRefundPostgresLocksPartialRequestsAndApproval(t *testing.T) {
	db, name, observer, _ := merchantStorePGDB(t)
	f := merchantStorePGFixture(t, db, "balance")
	_, e := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"paid-card-one", "paid-card-two"})
	require.NoError(t, e)
	in := f.checkout("paid-before-refund", "balance")
	in.Quantity = 2
	in.PickupCode = ""
	in.PickupEmail = ""
	o, _, e := CreateMerchantStoreOrder(in)
	require.NoError(t, e)
	refundActivate(t)
	requests := []func() error{
		func() error {
			_, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: "a", Reason: "A", Mode: "amount", AmountQuota: 600000})
			return e
		},
		func() error {
			_, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: "b", Reason: "B", Mode: "amount", AmountQuota: 600000})
			return e
		},
	}
	success := 0
	for _, e := range merchantStorePGContend(t, db, observer, name, requests) {
		if e == nil {
			success++
		} else {
			require.ErrorIs(t, e, ErrMerchantStoreConflict)
		}
	}
	require.Equal(t, 1, success)
	view, e := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, e)
	require.Len(t, view.Refunds, 1)
	r := view.Refunds[0]
	approvals := []func() error{
		func() error {
			_, e := DecideMerchantStoreRefund(f.seller.Id, o.ID, r.ID, MerchantStoreRefundDecision{Decision: "approve"})
			return e
		},
		func() error {
			_, e := DecideMerchantStoreRefund(f.root.Id, o.ID, r.ID, MerchantStoreRefundDecision{Decision: "approve"})
			return e
		},
	}
	for _, e := range merchantStorePGContend(t, db, observer, name, approvals) {
		require.NoError(t, e)
	}
	storeBalance(t, f.buyer.Id, 9600000)
	storeBalance(t, f.seller.Id, 10390000)
	storeBalance(t, f.root.Id, 10000)
	var n int64
	require.NoError(t, db.Model(&MerchantStoreTransfer{}).Where("order_id = ? AND kind = ?", o.ID, "refund").Count(&n).Error)
	require.EqualValues(t, 1, n)
	r2, e := ProactivelyRefundMerchantStoreOrder(f.seller.Id, o.ID, refundFull("rest"))
	require.NoError(t, e)
	require.Equal(t, "completed", r2.Status)
	require.NoError(t, db.First(o, "id = ?", o.ID).Error)
	require.Equal(t, "refunded", o.Status)
	refundStockCount(t, o, "refunded", 2)
	refundStockCount(t, o, "available", 0)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 9990000)
	storeBalance(t, f.root.Id, 10000)
}
