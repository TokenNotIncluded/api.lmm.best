package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantStoreRefundSyncPostgresSerializesDuplicateNativeReceipts(t *testing.T) {
	db, schema, observer, _ := merchantStorePGDB(t)
	f := merchantStorePGFixture(t, db, "platform:waffo_pancake")
	_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"first", "second"})
	require.NoError(t, err)
	in := f.checkout("external-refund-pg", "platform:waffo_pancake")
	in.Quantity = 2
	o, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 3, "USD", "1"))
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "original-provider-ord"))
	o, err = GetMerchantStorePaymentOrder(o.ID)
	require.NoError(t, err)
	refundActivate(t)
	refundNativeBasis(t, o, 3)
	require.NoError(t, db.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "7").Error)
	require.NoError(t, ActivateMerchantStoreRefundSync(db, 7))
	proof := refundSyncProof("pg-native-execution", 1)
	op := func() error {
		id, e := RecordMerchantStoreExternalRefund(o.ID, proof)
		if e != nil {
			return e
		}
		return ReconcileMerchantStoreExternalRefund(id)
	}
	for _, err := range merchantStorePGContend(t, db, observer, schema, []func() error{op, op, op}) {
		require.NoError(t, err)
	}
	view, err := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, err)
	require.Len(t, view.Refunds, 1)
	require.EqualValues(t, 1, *view.RefundedAmountMinor)
	var count int64
	require.NoError(t, db.Model(&MerchantStoreTransfer{}).Where("order_id = ? AND kind = ?", o.ID, "refund_external").Count(&count).Error)
	require.EqualValues(t, 1, count)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 10656667)
}
