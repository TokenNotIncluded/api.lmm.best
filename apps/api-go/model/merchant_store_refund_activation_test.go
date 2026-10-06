package model

import (
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

func TestMerchantStoreRefundActivationChecksCompleteRegistryAndPreservesPaidOrders(t *testing.T) {
	f := newStoreFixture(t, "balance")
	o, _, err := CreateMerchantStoreOrder(f.checkout("legacy-paid", "balance"))
	require.NoError(t, err)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, err)
	before := storeWriterSnapshot(t)
	storeWriterGateForTest(t, "3")
	require.ErrorIs(t, DB.Transaction(MerchantStoreRefundRequiresWriter), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, ActivateMerchantStoreRefunds(DB, 4), ErrMerchantStoreWriterFrozen)
	require.NoError(t, ActivateMerchantStoreRefunds(DB, 3))
	status, err := GetMerchantStoreWriterGateStatus(DB)
	require.NoError(t, err)
	require.Equal(t, 4, status.RequiredCapability)
	require.True(t, status.NewWritesAllowed)
	require.NoError(t, DB.Transaction(MerchantStoreRefundRequiresWriter))
	require.NoError(t, DB.Transaction(storeRequireLifecycleWriter), "retirement remains supported at the higher floor")
	require.NoError(t, DB.Transaction(storeRequireVariantWriter))
	require.NoError(t, ActivateMerchantStoreRefunds(DB, 3))
	require.NoError(t, ActivateMerchantStoreRefunds(DB, 4))
	require.ErrorIs(t, ActivateMerchantStoreVariants(DB, 2), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, ActivateMerchantStoreProductLifecycle(DB, 3), ErrMerchantStoreWriterFrozen)
	require.NoError(t, BootstrapMerchantStoreWriterGate(DB))
	require.Equal(t, before, storeWriterSnapshot(t), "activation does not change wallets, orders, stock or existing history")
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
}

func TestMerchantStoreRefundActivationRejectsIncompleteOrWeakenedSchema(t *testing.T) {
	for _, table := range []string{"merchant_store_refunds", "merchant_store_refund_items", "merchant_store_refund_payment_bases", "merchant_store_refund_provider_attempts", "merchant_store_discount_codes"} {
		t.Run(table, func(t *testing.T) {
			newStoreFixture(t, "balance")
			storeWriterGateForTest(t, "3")
			require.NoError(t, DB.Migrator().DropTable(table))
			require.ErrorIs(t, ActivateMerchantStoreRefunds(DB, 3), ErrMerchantStoreWriterFrozen)
			require.False(t, DB.Migrator().HasTable(table), "qualification never repairs missing tables")
			status, err := GetMerchantStoreWriterGateStatus(DB)
			require.NoError(t, err)
			require.Equal(t, 3, status.RequiredCapability)
		})
	}
	for _, entry := range []struct{ table, column string }{
		{"merchant_store_orders", "original_price_quota"}, {"merchant_store_orders", "promotion_code"},
		{"merchant_store_products", "max_quantity_per_order"}, {"merchant_store_products", "max_quantity_per_buyer"},
		{"merchant_store_refund_provider_attempts", "submit_count"},
	} {
		t.Run(entry.table+"/"+entry.column, func(t *testing.T) {
			newStoreFixture(t, "balance")
			storeWriterGateForTest(t, "3")
			require.NoError(t, DB.Exec("ALTER TABLE "+entry.table+" DROP COLUMN "+entry.column).Error)
			require.ErrorIs(t, ActivateMerchantStoreRefunds(DB, 3), ErrMerchantStoreWriterFrozen)
			require.False(t, DB.Migrator().HasColumn(entry.table, entry.column))
		})
	}
	t.Run("receipt-uniqueness", func(t *testing.T) {
		newStoreFixture(t, "balance")
		storeWriterGateForTest(t, "3")
		parsed, err := schema.Parse(&MerchantStoreRefund{}, &sync.Map{}, schema.NamingStrategy{})
		require.NoError(t, err)
		var name string
		for key, index := range parsed.ParseIndexes() {
			if len(index.Fields) == 1 && index.Fields[0].DBName == "provider_refund_reference" {
				name = key
			}
		}
		require.NotEmpty(t, name)
		require.NoError(t, DB.Migrator().DropIndex(&MerchantStoreRefund{}, name))
		require.NoError(t, DB.Exec("CREATE INDEX "+name+" ON merchant_store_refunds(provider_refund_reference)").Error)
		require.ErrorIs(t, ActivateMerchantStoreRefunds(DB, 3), ErrMerchantStoreWriterFrozen, "same index name without UNIQUE is unsafe")
	})
}

func TestMerchantStoreRefundActivationRejectsSkippedStageAndDurableOwner(t *testing.T) {
	newStoreFixture(t, "balance")
	for _, floor := range []string{"1", "2", "5"} {
		storeWriterGateForTest(t, floor)
		require.ErrorIs(t, ActivateMerchantStoreRefunds(DB, 3), ErrMerchantStoreWriterFrozen)
	}
	storeWriterGateForTest(t, "3")
	row := Option{Key: deploymentfence.OptionPrefix + "unknown:host", Value: "malformed"}
	require.NoError(t, DB.Create(&row).Error)
	require.ErrorIs(t, ActivateMerchantStoreRefunds(DB, 3), ErrMerchantStoreWriterFrozen)
	require.NoError(t, DB.Where("key = ?", row.Key).Delete(&Option{}).Error)
	require.NoError(t, ActivateMerchantStoreRefunds(DB, 3))
	// Even an idempotent retry must not treat a corrupted schema as ready.
	require.NoError(t, DB.Migrator().DropTable(&MerchantStoreRefundProviderAttempt{}))
	require.ErrorIs(t, ActivateMerchantStoreRefunds(DB, 4), ErrMerchantStoreWriterFrozen)
}
