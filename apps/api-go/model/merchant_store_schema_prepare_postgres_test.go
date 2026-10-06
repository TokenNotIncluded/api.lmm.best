package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This is a disposable-schema migration proof, not an official production
// clone, retained artifact, or permission to activate a serving deployment.
func TestMerchantStoreSchemaPreparationPostgresPreservesFactsAndRollsBackDDL(t *testing.T) {
	t.Run("installation preserves paid facts and separates activation", func(t *testing.T) {
		db, name, _, legacy := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "balance")
		_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"old-paid-card", "unreserved-card"})
		require.NoError(t, err)
		o, _, err := CreateMerchantStoreOrder(f.checkout("paid-before-schema-preparation", "balance"))
		require.NoError(t, err)
		token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
		require.NoError(t, err)
		storeWriterGateForTest(t, "4")
		storeRemovePhase5Schema(t, db)
		require.NoError(t, ActivateMerchantStoreRefunds(db, 4))
		before := storePreparationFacts(t, db, nil)
		oldTables := merchantStorePGTables(t, db, name)
		require.ErrorIs(t, ActivateMerchantStoreAccess(db, 4), ErrMerchantStoreWriterFrozen)
		require.NoError(t, PrepareMerchantStoreSchema(db, 4))
		require.Equal(t, before.rows, storePreparationFacts(t, db, before).rows, "every original column retains its original rows")
		require.Equal(t, legacy, merchantStorePGFingerprint(t, db, name))
		currentTables := merchantStorePGTables(t, db, name)
		added := make([]string, 0, 7)
		for _, table := range currentTables {
			found := false
			for _, original := range oldTables {
				found = found || original == table
			}
			if !found {
				require.True(t, storeAccessTable(table), "only declared shop additions are installed")
				added = append(added, table)
			}
		}
		require.Len(t, added, 7)
		status, err := GetMerchantStoreWriterGateStatus(db)
		require.NoError(t, err)
		require.Equal(t, 4, status.RequiredCapability)
		require.False(t, MerchantStoreAccessSupported())
		require.NoError(t, PrepareMerchantStoreSchema(db, 4), "retry does not mutate existing facts")
		require.Equal(t, before.rows, storePreparationFacts(t, db, before).rows)
		require.NoError(t, ActivateMerchantStoreAccess(db, 4))
		require.NoError(t, ActivateMerchantStoreAccess(db, 5))
		require.True(t, MerchantStoreCatalogueSupported())
		claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
		require.NoError(t, err)
		require.Equal(t, []string{"old-paid-card"}, claim.Items)
		require.NoError(t, db.Transaction(MerchantStoreRefundRequiresWriter))
	})
	t.Run("backfill failure rolls back already executed PostgreSQL DDL", func(t *testing.T) {
		db, name, _, legacy := merchantStorePGDB(t)
		merchantStorePGFixture(t, db, "balance")
		storeWriterGateForTest(t, "4")
		storeRemovePhase5Schema(t, db)
		require.NoError(t, db.Migrator().CreateTable(&MerchantStoreCatalogueMetadata{}))
		require.NoError(t, db.Exec(`CREATE FUNCTION reject_store_mapping_prepare() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected-private-diagnostic'; END $$`).Error)
		require.NoError(t, db.Exec(`CREATE TRIGGER reject_store_mapping_prepare BEFORE INSERT ON merchant_store_catalogue_metadata FOR EACH ROW EXECUTE FUNCTION reject_store_mapping_prepare()`).Error)
		before := storePreparationFacts(t, db, nil)
		beforeTables := merchantStorePGTables(t, db, name)
		err := PrepareMerchantStoreSchema(db, 4)
		require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
		require.NotContains(t, err.Error(), "injected-private-diagnostic")
		require.Equal(t, beforeTables, merchantStorePGTables(t, db, name))
		require.Equal(t, before.rows, storePreparationFacts(t, db, before).rows)
		require.False(t, db.Migrator().HasColumn(&MerchantStoreProduct{}, "visibility"))
		require.False(t, db.Migrator().HasColumn(&MerchantStoreOrder{}, "guest_id"))
		require.Equal(t, legacy, merchantStorePGFingerprint(t, db, name))
		status, err := GetMerchantStoreWriterGateStatus(db)
		require.NoError(t, err)
		require.Equal(t, 4, status.RequiredCapability)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return storeRequireWriter(tx) }))
	})
}
