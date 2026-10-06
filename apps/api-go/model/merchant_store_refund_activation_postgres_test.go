package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantStoreRefundActivationPostgresQualifiesActualSchemaAndTerminalCallbacks(t *testing.T) {
	db, schemaName, _, fingerprint := merchantStorePGDB(t)
	f := merchantStorePGFixture(t, db, "platform:waffo_pancake")
	_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"paid-before-cap4"})
	require.NoError(t, err)
	in := f.checkout("paid-before-cap4", "platform:waffo_pancake")
	in.PickupEmail, in.PickupCode = "", ""
	o, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "immutable-provider-receipt"))
	require.NoError(t, db.First(o, "id = ?", o.ID).Error)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, err)
	require.NoError(t, ActivateMerchantStoreVariants(db, 1))
	require.NoError(t, ActivateMerchantStoreProductLifecycle(db, 2))
	before := storeWriterSnapshot(t)
	// PostgreSQL constraints, widths, ordered indexes and legacy nullable fields
	// are read from the actual test-owned schema, not reconstructed source tags.
	require.NoError(t, ActivateMerchantStoreRefunds(db, 3))
	require.Equal(t, before, storeWriterSnapshot(t))
	require.NoError(t, ActivateMerchantStoreRefunds(db, 4))
	require.NoError(t, db.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("max_quantity_per_order", 1).Error)
	require.NoError(t, db.Exec("ALTER TABLE merchant_store_products ALTER COLUMN max_quantity_per_order SET NOT NULL").Error)
	require.ErrorIs(t, ActivateMerchantStoreRefunds(db, 4), ErrMerchantStoreWriterFrozen, "legacy NULL means unlimited and must remain nullable")
	require.NoError(t, db.Exec("ALTER TABLE merchant_store_products ALTER COLUMN max_quantity_per_order DROP NOT NULL").Error)
	require.NoError(t, db.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("max_quantity_per_order", nil).Error)
	require.NoError(t, db.Exec("ALTER TABLE merchant_store_orders ALTER COLUMN promotion_code DROP DEFAULT").Error)
	require.ErrorIs(t, ActivateMerchantStoreRefunds(db, 4), ErrMerchantStoreWriterFrozen)
	require.NoError(t, db.Exec("ALTER TABLE merchant_store_orders ALTER COLUMN promotion_code SET DEFAULT ''").Error)
	r, err := ProactivelyRefundMerchantStoreOrder(f.seller.Id, o.ID, refundFull("terminal-refund"))
	require.NoError(t, err)
	require.Equal(t, "awaiting_provider", r.Status)
	// Native original-channel success is a trusted internal proof; no real
	// provider or email is contacted by this fixture.
	refundNativeBasis(t, o, 100)
	require.NoError(t, db.First(r, "id = ?", r.ID).Error)
	refundNativeComplete(t, r)
	paidSnapshot := storeWriterSnapshot(t)
	require.NoError(t, CompleteMerchantStorePayment(o.ID, "immutable-provider-receipt"))
	require.NoError(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, "immutable-provider-receipt", "settlement_unavailable"))
	require.Equal(t, paidSnapshot, storeWriterSnapshot(t), "a late original callback cannot resurrect a refunded order or ledger")
	_, err = ClaimMerchantStoreOrder(token, "", f.buyer.Id)
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
	require.Equal(t, fingerprint, merchantStorePGFingerprint(t, db, schemaName))
	status, err := GetMerchantStoreWriterGateStatus(db)
	require.NoError(t, err)
	require.Equal(t, 4, status.RequiredCapability)
}
