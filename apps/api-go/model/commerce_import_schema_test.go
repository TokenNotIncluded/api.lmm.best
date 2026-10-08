package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func commerceImportPhaseSevenTest(t *testing.T, db *gorm.DB) {
	t.Helper()
	storeActivateFixedTest(t, db)
}

func commerceImportRemoveSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, item := range CommerceImportModels() {
		require.NoError(t, db.Migrator().DropTable(item))
	}
}

func commerceImportTableNames(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var tables []string
	if db.Dialector.Name() == "postgres" {
		require.NoError(t, db.Raw("SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename LIKE 'merchant_store_commerce_%' ORDER BY tablename").Scan(&tables).Error)
	} else {
		require.NoError(t, db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'merchant_store_commerce_%' ORDER BY name").Scan(&tables).Error)
	}
	return tables
}

func commerceImportSQLiteObjects(t *testing.T, db *gorm.DB) map[string]string {
	t.Helper()
	var objects []struct{ Name, SQL string }
	require.NoError(t, db.Raw("SELECT name, sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY name").Scan(&objects).Error)
	result := make(map[string]string, len(objects))
	for _, object := range objects {
		result[object.Name] = object.SQL
	}
	return result
}

func TestCommerceImportPreparationOnlyAddsSixTablesAndSeparatesActivation(t *testing.T) {
	f := newStoreFixture(t, "balance")
	_, _, err := CreateMerchantStoreOrder(f.checkout("paid-before-commerce", "balance"))
	require.NoError(t, err)
	commerceImportPhaseSevenTest(t, DB)
	commerceImportRemoveSchema(t, DB)
	before := storePhaseSixOldFacts(t, DB)
	oldObjects := commerceImportSQLiteObjects(t, DB)
	require.NoError(t, storeCheckMerchantStoreSchema(DB, 7))
	require.ErrorIs(t, VerifyMerchantStoreCommerceImport(DB), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, ActivateMerchantStoreCommerceImport(DB, 7), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, DB.Transaction(storeRequireCommerceImportWriter), ErrMerchantStoreWriterFrozen)
	require.False(t, CommerceImportSupported())
	require.NoError(t, PrepareMerchantStoreCommerceImport(DB, 7))
	require.Equal(t, before.rows, storePreparationFacts(t, DB, before).rows, "preparation retains every existing wallet, stock, order and ledger row")
	for name, object := range oldObjects {
		require.Equal(t, object, commerceImportSQLiteObjects(t, DB)[name], "preparation cannot alter an old schema object: %s", name)
	}
	require.Equal(t, []string{
		"merchant_store_commerce_card_batches", "merchant_store_commerce_connections", "merchant_store_commerce_product_mappings",
		"merchant_store_commerce_restock_requests", "merchant_store_commerce_sessions", "merchant_store_commerce_variant_mappings",
	}, commerceImportTableNames(t, DB))
	floor, err := storeWriterGateRow(DB, "")
	require.NoError(t, err)
	require.Equal(t, 7, floor, "table installation never activates import")
	require.False(t, CommerceImportSupported())
	require.ErrorIs(t, DB.Transaction(storeRequireCommerceImportWriter), ErrMerchantStoreWriterFrozen)
	installed := storePhaseSixOldFacts(t, DB)
	installedObjects := commerceImportSQLiteObjects(t, DB)
	require.NoError(t, PrepareMerchantStoreCommerceImport(DB, 7))
	require.NoError(t, VerifyMerchantStoreCommerceImport(DB))
	require.Equal(t, installed.rows, storePreparationFacts(t, DB, installed).rows)
	require.Equal(t, installedObjects, commerceImportSQLiteObjects(t, DB), "prepare retry and verification leave schema unchanged")
	require.NoError(t, ActivateMerchantStoreCommerceImport(DB, 7))
	require.Equal(t, installedObjects, commerceImportSQLiteObjects(t, DB), "activation performs no DDL")
	activated := storePreparationFacts(t, DB, installed)
	for table, rows := range installed.rows {
		if table != "options" {
			require.Equal(t, rows, activated.rows[table], table)
		}
	}
	floor, err = storeWriterGateRow(DB, "")
	require.NoError(t, err)
	require.Equal(t, 8, floor)
	require.True(t, CommerceImportSupported())
	require.True(t, MerchantStoreCommerceImportSupported())
	require.NoError(t, DB.Transaction(storeRequireCommerceImportWriter))
	require.NoError(t, storeRequireCommerceImportReadable(DB))
	require.NoError(t, ActivateMerchantStoreCommerceImport(DB, 7))
	require.NoError(t, ActivateMerchantStoreCommerceImport(DB, 8))
	require.NoError(t, PrepareMerchantStoreCommerceImport(DB, 8))
	require.NoError(t, VerifyMerchantStoreCommerceImport(DB))
	require.Equal(t, activated.rows, storePreparationFacts(t, DB, installed).rows, "all activated retries retain old and new rows")
	require.Equal(t, installedObjects, commerceImportSQLiteObjects(t, DB))
}

func TestCommerceImportHistoricalPreparationAndQualificationExcludeNewTables(t *testing.T) {
	newStoreFixture(t, "balance")
	commerceImportRemoveSchema(t, DB)
	require.NoError(t, PrepareMerchantStoreSchema(DB, 1))
	require.Empty(t, commerceImportTableNames(t, DB), "historical phase-five preparation never installs import")
	storeActivatePhaseFiveForSixTest(t, DB)
	require.NoError(t, PrepareMerchantStorePhaseSix(DB, 5))
	require.NoError(t, ActivateMerchantStorePhaseSix(DB, 5))
	require.Empty(t, commerceImportTableNames(t, DB), "historical phase-six preparation never installs import")
	require.NoError(t, PrepareMerchantStoreFixedContent(DB, 6))
	require.NoError(t, VerifyMerchantStoreFixedContent(DB))
	require.NoError(t, ActivateMerchantStoreFixedContent(DB, 6))
	require.Empty(t, commerceImportTableNames(t, DB), "historical phase-seven preparation never installs import")
	for _, capability := range []int{4, 5, 6, 7} {
		require.NoError(t, storeCheckMerchantStoreSchema(DB, capability), "old qualifier %d excludes capability-eight tables", capability)
	}
	require.ErrorIs(t, storeCheckMerchantStoreSchema(DB, 8), ErrMerchantStoreWriterFrozen)
	require.NoError(t, VerifyMerchantStoreFixedContent(DB))
}

func TestCommerceImportGateRejectsInvalidStagesAndDeploymentOwnerWithoutMutation(t *testing.T) {
	newStoreFixture(t, "balance")
	commerceImportPhaseSevenTest(t, DB)
	for _, value := range []string{"1", "2", "3", "4", "5", "6", "9", "08", " 8", "8 "} {
		t.Run("floor-"+value, func(t *testing.T) {
			storeWriterGateForTest(t, value)
			before := storePhaseSixOldFacts(t, DB)
			objects := commerceImportSQLiteObjects(t, DB)
			require.ErrorIs(t, PrepareMerchantStoreCommerceImport(DB, 7), ErrMerchantStoreWriterFrozen)
			require.ErrorIs(t, ActivateMerchantStoreCommerceImport(DB, 7), ErrMerchantStoreWriterFrozen)
			require.ErrorIs(t, VerifyMerchantStoreCommerceImport(DB), ErrMerchantStoreWriterFrozen)
			require.ErrorIs(t, DB.Transaction(storeRequireCommerceImportWriter), ErrMerchantStoreWriterFrozen)
			require.ErrorIs(t, storeRequireCommerceImportReadable(DB), ErrMerchantStoreWriterFrozen)
			require.False(t, CommerceImportSupported())
			require.Equal(t, before.rows, storePreparationFacts(t, DB, before).rows)
			require.Equal(t, objects, commerceImportSQLiteObjects(t, DB))
		})
	}
	storeWriterGateForTest(t, "7")
	for _, expected := range []int{0, 1, 6, 8, 9} {
		require.ErrorIs(t, PrepareMerchantStoreCommerceImport(DB, expected), ErrMerchantStoreWriterFrozen, fmt.Sprint(expected))
		require.ErrorIs(t, ActivateMerchantStoreCommerceImport(DB, expected), ErrMerchantStoreWriterFrozen, fmt.Sprint(expected))
	}
	require.NoError(t, DB.Where("key = ?", MerchantStoreWriterCapabilityOption).Delete(&Option{}).Error)
	before := storePhaseSixOldFacts(t, DB)
	require.ErrorIs(t, PrepareMerchantStoreCommerceImport(DB, 7), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, ActivateMerchantStoreCommerceImport(DB, 7), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, VerifyMerchantStoreCommerceImport(DB), ErrMerchantStoreWriterFrozen)
	require.Equal(t, before.rows, storePreparationFacts(t, DB, before).rows, "missing gate is never bootstrapped")
	require.NoError(t, DB.Create(&Option{Key: MerchantStoreWriterCapabilityOption, Value: "8"}).Error)
	require.NoError(t, DB.Create(&Option{Key: deploymentfence.OptionPrefix + "commerce:unknown-owner", Value: "malformed"}).Error)
	before = storePhaseSixOldFacts(t, DB)
	require.ErrorIs(t, PrepareMerchantStoreCommerceImport(DB, 8), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, ActivateMerchantStoreCommerceImport(DB, 8), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, VerifyMerchantStoreCommerceImport(DB), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, DB.Transaction(storeRequireCommerceImportWriter), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, storeRequireCommerceImportReadable(DB), ErrMerchantStoreWriterFrozen)
	require.False(t, CommerceImportSupported())
	require.Equal(t, before.rows, storePreparationFacts(t, DB, before).rows)
	require.ErrorIs(t, PrepareMerchantStoreCommerceImport(nil, 7), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, ActivateMerchantStoreCommerceImport(nil, 7), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, VerifyMerchantStoreCommerceImport(nil), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, storeRequireCommerceImportReadable(nil), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, storeRequireCommerceImportWriter(nil), ErrMerchantStoreWriterFrozen)
	require.False(t, storeCommerceImportSupported(nil))
}

func TestCommerceImportActivationAndVerificationRejectMissingTablesAndUniqueIndexes(t *testing.T) {
	newStoreFixture(t, "balance")
	commerceImportPhaseSevenTest(t, DB)
	require.NoError(t, PrepareMerchantStoreCommerceImport(DB, 7))
	require.NoError(t, ActivateMerchantStoreCommerceImport(DB, 7))
	for _, model := range CommerceImportModels() {
		stmt := &gorm.Statement{DB: DB}
		require.NoError(t, stmt.Parse(model))
		t.Run(stmt.Schema.Table, func(t *testing.T) {
			require.NoError(t, DB.Migrator().DropTable(model))
			before := commerceImportSQLiteObjects(t, DB)
			require.NoError(t, storeCheckMerchantStoreSchema(DB, 7))
			require.ErrorIs(t, VerifyMerchantStoreCommerceImport(DB), ErrMerchantStoreWriterFrozen)
			require.ErrorIs(t, ActivateMerchantStoreCommerceImport(DB, 8), ErrMerchantStoreWriterFrozen)
			require.False(t, CommerceImportSupported())
			require.Equal(t, before, commerceImportSQLiteObjects(t, DB), "verify and activation cannot repair missing tables")
			require.False(t, DB.Migrator().HasTable(model))
			require.NoError(t, DB.AutoMigrate(model), "restore only the intentionally removed test table")
		})
	}
	for _, index := range []string{"commerce_request_key", "commerce_batch_identity", "commerce_external_product", "commerce_external_variant"} {
		t.Run(index, func(t *testing.T) {
			require.NoError(t, DB.Exec("DROP INDEX "+index).Error)
			before := commerceImportSQLiteObjects(t, DB)
			require.NoError(t, storeCheckMerchantStoreSchema(DB, 7))
			require.ErrorIs(t, VerifyMerchantStoreCommerceImport(DB), ErrMerchantStoreWriterFrozen)
			require.ErrorIs(t, ActivateMerchantStoreCommerceImport(DB, 8), ErrMerchantStoreWriterFrozen)
			require.ErrorIs(t, PrepareMerchantStoreCommerceImport(DB, 8), ErrMerchantStoreWriterFrozen, "preparation never repairs an already installed malformed table")
			require.Equal(t, before, commerceImportSQLiteObjects(t, DB), "verification cannot repair missing unique indexes")
			require.NoError(t, DB.AutoMigrate(CommerceImportModels()...))
		})
	}
	for name, statement := range map[string]string{
		"nonunique":   "CREATE INDEX commerce_request_key ON merchant_store_commerce_restock_requests(connection_id,grant_id,key_hash)",
		"wrong-order": "CREATE UNIQUE INDEX commerce_request_key ON merchant_store_commerce_restock_requests(grant_id,connection_id,key_hash)",
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, DB.Exec("DROP INDEX commerce_request_key").Error)
			require.NoError(t, DB.Exec(statement).Error)
			require.ErrorIs(t, VerifyMerchantStoreCommerceImport(DB), ErrMerchantStoreWriterFrozen)
			require.ErrorIs(t, ActivateMerchantStoreCommerceImport(DB, 8), ErrMerchantStoreWriterFrozen)
			require.NoError(t, DB.Exec("DROP INDEX commerce_request_key").Error)
			require.NoError(t, DB.AutoMigrate(&MerchantStoreCommerceRestockRequest{}))
		})
	}
	require.NoError(t, VerifyMerchantStoreCommerceImport(DB))
	for _, table := range commerceImportTableNames(t, DB) {
		require.True(t, storeCommerceImportTable(table))
	}
	require.False(t, storeCommerceImportTable("merchant_store_orders"))
	require.False(t, storeCommerceImportTable(strings.TrimSuffix("merchant_store_commerce_", "_")))
}
