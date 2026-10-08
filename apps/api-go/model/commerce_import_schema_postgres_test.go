package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var commerceImportPostgresExpectedTables = []string{
	"merchant_store_commerce_card_batches",
	"merchant_store_commerce_connections",
	"merchant_store_commerce_product_mappings",
	"merchant_store_commerce_restock_requests",
	"merchant_store_commerce_sessions",
	"merchant_store_commerce_variant_mappings",
}

// Compare the exact pre-installation table set, rather than dropping arbitrary
// catalogue lines merely because their names resemble a new import object.
func commerceImportPostgresOldFacts(t *testing.T, db *gorm.DB, tables []string) map[string]string {
	t.Helper()
	all := runtimeVerificationPostgresFacts(t, db)
	facts := map[string]string{"sequences": all["sequences"]}
	for _, table := range tables {
		facts["rows:"+table] = all["rows:"+table]
	}
	for name, query := range map[string]string{
		"columns":     `SELECT COALESCE(pg_catalog.string_agg(pg_catalog.row_to_json(c)::text,E'\n' ORDER BY table_name,ordinal_position),'') FROM information_schema.columns AS c WHERE table_schema=pg_catalog.current_schema() AND table_name IN ?`,
		"indexes":     `SELECT COALESCE(pg_catalog.string_agg(indexdef,E'\n' ORDER BY tablename,indexname),'') FROM pg_catalog.pg_indexes WHERE schemaname=pg_catalog.current_schema() AND tablename IN ?`,
		"constraints": `SELECT COALESCE(pg_catalog.string_agg(pg_catalog.format('%s:%s:%s',c.relname,k.conname,pg_catalog.pg_get_constraintdef(k.oid)),E'\n' ORDER BY c.relname,k.conname),'') FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_class c ON c.oid=k.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=pg_catalog.current_schema() AND c.relname IN ?`,
	} {
		var value string
		require.NoError(t, db.Raw(query, tables).Scan(&value).Error)
		facts[name] = value
	}
	return facts
}

func TestCommerceImportSchemaPostgresPreparationPreservesFactsAndSeparatesActivation(t *testing.T) {
	db, name, _, legacy := merchantStorePGDB(t)
	f := merchantStorePGFixture(t, db, "balance")
	// Preserve a genuinely historical paid order before phase-five terms rules.
	_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"paid-before-commerce-import", "retained-before-commerce-import"})
	require.NoError(t, err)
	in := f.checkout("paid-before-commerce-schema", "balance")
	in.PickupCode, in.PickupEmail = "", ""
	order, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.Equal(t, "paid", order.Status)
	commerceImportPhaseSevenTest(t, db)
	commerceImportRemoveSchema(t, db)
	require.NoError(t, storeCheckMerchantStoreSchema(db, 7))
	before := storePhaseSixOldFacts(t, db)
	oldTables := merchantStorePGTables(t, db, name)
	oldFacts := commerceImportPostgresOldFacts(t, db, oldTables)

	require.ErrorIs(t, VerifyMerchantStoreCommerceImport(db), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, ActivateMerchantStoreCommerceImport(db, 7), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, PrepareMerchantStoreCommerceImport(db, 6), ErrMerchantStoreWriterFrozen)
	require.Equal(t, oldFacts, commerceImportPostgresOldFacts(t, db, oldTables))
	require.NoError(t, PrepareMerchantStoreCommerceImport(db, 7))
	require.Equal(t, before.rows, storePreparationFacts(t, db, before).rows)
	require.Equal(t, oldFacts, commerceImportPostgresOldFacts(t, db, oldTables), "old rows, column types/defaults, indexes, constraints and sequences remain unchanged")
	require.Equal(t, legacy, merchantStorePGFingerprint(t, db, name))
	var added []string
	for _, table := range merchantStorePGTables(t, db, name) {
		found := false
		for _, old := range oldTables {
			found = found || old == table
		}
		if !found {
			added = append(added, table)
		}
	}
	require.Equal(t, commerceImportPostgresExpectedTables, added, "preparation installs exactly the six declared import tables")
	floor, err := storeWriterGateRow(db, "")
	require.NoError(t, err)
	require.Equal(t, 7, floor, "installation never activates import")
	require.False(t, CommerceImportSupported())
	require.ErrorIs(t, db.Transaction(storeRequireCommerceImportWriter), ErrMerchantStoreWriterFrozen)
	require.NoError(t, db.Transaction(storeRequireWriter), "ordinary old shop writes remain available")
	require.NoError(t, db.Transaction(storeRequireFixedContentWriter), "phase-seven obligations remain available")
	installed := runtimeVerificationPostgresFacts(t, db)
	trigger := "commerce_no_ddl_" + strings.TrimPrefix(name, "lmm_merchant_store_test_")
	function := trigger + "_reject"
	require.NoError(t, db.Exec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS event_trigger LANGUAGE plpgsql AS $$ BEGIN IF pg_catalog.current_schema() = '%s' THEN RAISE EXCEPTION 'commerce-import retry issued DDL'; END IF; END $$`, function, name)).Error)
	require.NoError(t, db.Exec("CREATE EVENT TRIGGER "+trigger+" ON ddl_command_start WHEN TAG IN ('CREATE TABLE','ALTER TABLE','CREATE INDEX','ALTER INDEX') EXECUTE FUNCTION "+name+"."+function+"()").Error)
	t.Cleanup(func() {
		require.NoError(t, db.Exec("DROP EVENT TRIGGER IF EXISTS "+trigger).Error)
		require.NoError(t, db.Exec("DROP FUNCTION IF EXISTS "+function+"()").Error)
	})
	require.NoError(t, VerifyMerchantStoreCommerceImport(db))
	require.NoError(t, PrepareMerchantStoreCommerceImport(db, 7))
	require.Equal(t, installed, runtimeVerificationPostgresFacts(t, db), "verification and preparation retry preserve the entire installed catalogue without issuing DDL")
	require.ErrorIs(t, ActivateMerchantStoreCommerceImport(db, 8), ErrMerchantStoreWriterFrozen, "expected-current must match before raising the floor")
	var otherOptions []Option
	require.NoError(t, db.Where("key <> ?", MerchantStoreWriterCapabilityOption).Order("key").Find(&otherOptions).Error)
	require.NoError(t, ActivateMerchantStoreCommerceImport(db, 7))
	require.True(t, CommerceImportSupported())
	require.NoError(t, db.Transaction(storeRequireCommerceImportWriter))
	activated := runtimeVerificationPostgresFacts(t, db)
	for key, value := range installed {
		if key != "rows:options" {
			require.Equal(t, value, activated[key], "activation changes only the durable writer floor: %s", key)
		}
	}
	var currentOtherOptions []Option
	require.NoError(t, db.Where("key <> ?", MerchantStoreWriterCapabilityOption).Order("key").Find(&currentOtherOptions).Error)
	require.Equal(t, otherOptions, currentOtherOptions)
	require.NoError(t, ActivateMerchantStoreCommerceImport(db, 8))
	require.NoError(t, VerifyMerchantStoreCommerceImport(db))
	require.Equal(t, activated, runtimeVerificationPostgresFacts(t, db), "activation retry and verification perform no writes")
}

func TestCommerceImportSchemaPostgresRuntimeFollowsFloorAndRejectsDamage(t *testing.T) {
	db, _, _, _ := merchantStorePGDB(t)
	// Native verification needs the application registry, not only shop fixtures.
	require.NoError(t, db.AutoMigrate(append(mainMigrationModels(), &SubscriptionPlan{})...))
	require.NoError(t, ensureCompanyBillingProfilePostgresContract(db))
	require.NoError(t, db.Create(&Option{Key: "theme.frontend", Value: "default"}).Error)
	merchantStorePGFixture(t, db, "balance")
	commerceImportPhaseSevenTest(t, db)
	commerceImportRemoveSchema(t, db)
	old := runtimeVerificationPostgresFacts(t, db)
	require.NoError(t, storeCheckMerchantStoreSchema(db, 7))
	require.NoError(t, verifyPostgresRuntimeAndSchema(db), "a real phase-seven floor does not require import tables")
	require.ErrorIs(t, storeCheckMerchantStoreSchema(db, 8), ErrMerchantStoreWriterFrozen)
	require.Equal(t, old, runtimeVerificationPostgresFacts(t, db), "old-floor qualification never installs import")
	require.NoError(t, PrepareMerchantStoreCommerceImport(db, 7))
	require.NoError(t, ActivateMerchantStoreCommerceImport(db, 7))
	require.NoError(t, verifyPostgresRuntimeAndSchema(db), "the real phase-eight floor checks the full registry")

	for _, damage := range []struct {
		name, ddl, required string
		model               any
		prepareRejects      bool
	}{
		{"missing-session-table", "DROP TABLE merchant_store_commerce_sessions", "merchant_store_commerce_sessions", &MerchantStoreCommerceSession{}, false},
		{"missing-identity-unique-index", "DROP INDEX commerce_external_product", "commerce_external_product", &MerchantStoreCommerceProductMapping{}, true},
		{"missing-encrypted-token-column", "ALTER TABLE merchant_store_commerce_connections DROP COLUMN tokens_ciphertext", "tokens_ciphertext", &MerchantStoreCommerceConnection{}, true},
	} {
		t.Run(damage.name, func(t *testing.T) {
			require.NoError(t, db.Exec(damage.ddl).Error)
			missing := runtimeVerificationPostgresFacts(t, db)
			require.NoError(t, storeCheckMerchantStoreSchema(db, 7), "the historical qualifier excludes damaged import objects")
			require.ErrorIs(t, VerifyMerchantStoreCommerceImport(db), ErrMerchantStoreWriterFrozen)
			require.ErrorIs(t, ActivateMerchantStoreCommerceImport(db, 8), ErrMerchantStoreWriterFrozen, "activation retry checks the actual schema")
			require.ErrorContains(t, verifyPostgresRuntimeAndSchema(db), damage.required)
			if damage.prepareRejects {
				require.ErrorIs(t, PrepareMerchantStoreCommerceImport(db, 8), ErrMerchantStoreWriterFrozen, "preparation cannot repair a damaged existing import table")
			}
			require.Equal(t, missing, runtimeVerificationPostgresFacts(t, db), "failed qualification neither repairs the schema nor changes rows or the floor")
			require.NoError(t, db.AutoMigrate(damage.model), "only test restoration performs DDL")
			require.NoError(t, VerifyMerchantStoreCommerceImport(db))
			require.NoError(t, verifyPostgresRuntimeAndSchema(db))
		})
	}
	var floor Option
	require.NoError(t, db.First(&floor, "key = ?", MerchantStoreWriterCapabilityOption).Error)
	require.NoError(t, db.Where("key = ?", floor.Key).Delete(&Option{}).Error)
	commerceImportRemoveSchema(t, db)
	missing := runtimeVerificationPostgresFacts(t, db)
	require.ErrorContains(t, verifyPostgresRuntimeAndSchema(db), "merchant_store_commerce_", "missing gate still requires the complete newest catalogue")
	require.ErrorIs(t, storeRequireWriter(db), ErrMerchantStoreWriterFrozen)
	require.Equal(t, missing, runtimeVerificationPostgresFacts(t, db), "missing-gate verification never bootstraps or repairs the catalogue")
}

func TestCommerceImportSchemaPostgresPreparationRollsBackPartialDDL(t *testing.T) {
	for _, failure := range []struct{ name, table string }{
		{"second-table", "merchant_store_commerce_sessions"},
		{"sixth-table", "merchant_store_commerce_card_batches"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			db, name, _, _ := merchantStorePGDB(t)
			merchantStorePGFixture(t, db, "balance")
			commerceImportPhaseSevenTest(t, db)
			commerceImportRemoveSchema(t, db)
			require.NoError(t, storeCheckMerchantStoreSchema(db, 7))
			before := runtimeVerificationPostgresFacts(t, db)
			oldTables := merchantStorePGTables(t, db, name)
			trigger := "commerce_eight_" + strings.TrimPrefix(name, "lmm_merchant_store_test_")
			function := trigger + "_reject"
			sql := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS event_trigger LANGUAGE plpgsql AS $$ BEGIN IF EXISTS (SELECT 1 FROM pg_event_trigger_ddl_commands() WHERE schema_name = '%s' AND object_identity LIKE '%%%s') THEN RAISE EXCEPTION 'private-commerce-import-DDL-failure'; END IF; END $$`, function, name, failure.table)
			require.NoError(t, db.Exec(sql).Error)
			require.NoError(t, db.Exec("CREATE EVENT TRIGGER "+trigger+" ON ddl_command_end WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION "+name+"."+function+"()").Error)
			t.Cleanup(func() {
				require.NoError(t, db.Exec("DROP EVENT TRIGGER IF EXISTS "+trigger).Error)
				require.NoError(t, db.Exec("DROP FUNCTION IF EXISTS "+function+"()").Error)
			})
			err := PrepareMerchantStoreCommerceImport(db, 7)
			require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
			require.NotContains(t, err.Error(), "private-commerce-import-DDL-failure", "operator errors do not echo raw database diagnostics")
			for _, model := range CommerceImportModels() {
				require.False(t, db.Migrator().HasTable(model), "rollback removes earlier successfully created import tables too")
			}
			require.Equal(t, oldTables, merchantStorePGTables(t, db, name))
			require.Equal(t, before, runtimeVerificationPostgresFacts(t, db), "failed preparation rolls back DDL and preserves every old fact")
		})
	}
}

func TestCommerceImportSchemaPostgresActivationWaitsForOrdinaryWriterShare(t *testing.T) {
	db, name, observer, _ := merchantStorePGDB(t)
	merchantStorePGFixture(t, db, "balance")
	commerceImportPhaseSevenTest(t, db)
	commerceImportRemoveSchema(t, db)
	require.NoError(t, PrepareMerchantStoreCommerceImport(db, 7))
	tx := db.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()
	// Ordinary old writers hold only the floor row, not the newer shared fence.
	require.NoError(t, storeRequireWriter(tx))
	activated := make(chan error, 1)
	go func() { activated <- ActivateMerchantStoreCommerceImport(db, 7) }()
	require.Eventually(t, func() bool {
		var held int64
		err := observer.Raw("SELECT COUNT(*) FROM pg_catalog.pg_stat_activity WHERE application_name = ? AND wait_event_type = 'Lock' AND query LIKE '%FOR UPDATE%'", name).Scan(&held).Error
		return err == nil && held > 0
	}, 5*time.Second, 20*time.Millisecond, "activation waits on the old writer's real floor SHARE lock")
	select {
	case err := <-activated:
		t.Fatalf("activation bypassed the ordinary old writer: %v", err)
	default:
	}
	floor, err := storeWriterGateRow(db, "")
	require.NoError(t, err)
	require.Equal(t, 7, floor)
	require.NoError(t, tx.Commit().Error)
	select {
	case err := <-activated:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("activation did not resume after the ordinary writer committed")
	}
	floor, err = storeWriterGateRow(db, "")
	require.NoError(t, err)
	require.Equal(t, 8, floor)
}

func TestCommerceImportSchemaPostgresDeploymentFencesAndUnknownOwners(t *testing.T) {
	t.Run("exclusive-deployment-fence", func(t *testing.T) {
		db, _, _, _ := merchantStorePGDB(t)
		merchantStorePGFixture(t, db, "balance")
		commerceImportPhaseSevenTest(t, db)
		commerceImportRemoveSchema(t, db)
		require.NoError(t, PrepareMerchantStoreCommerceImport(db, 7))
		require.NoError(t, ActivateMerchantStoreCommerceImport(db, 7))
		before := runtimeVerificationPostgresFacts(t, db)
		holder := db.Begin()
		require.NoError(t, holder.Error)
		defer holder.Rollback()
		var acquired bool
		require.NoError(t, holder.Raw("SELECT pg_catalog.pg_try_advisory_xact_lock(?)", deploymentfence.AdvisoryKey).Scan(&acquired).Error)
		require.True(t, acquired)
		require.ErrorIs(t, PrepareMerchantStoreCommerceImport(db, 8), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, VerifyMerchantStoreCommerceImport(db), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, ActivateMerchantStoreCommerceImport(db, 8), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, db.Transaction(storeRequireCommerceImportWriter), ErrMerchantStoreWriterFrozen)
		require.Equal(t, before, runtimeVerificationPostgresFacts(t, db))
		require.NoError(t, holder.Rollback().Error)
		require.NoError(t, VerifyMerchantStoreCommerceImport(db), "the test-owned live fence disappears only after its transaction ends")
		require.NoError(t, db.Transaction(storeRequireCommerceImportWriter))
	})
	t.Run("unknown-durable-owner-before-and-after-installation", func(t *testing.T) {
		db, _, _, _ := merchantStorePGDB(t)
		merchantStorePGFixture(t, db, "balance")
		commerceImportPhaseSevenTest(t, db)
		commerceImportRemoveSchema(t, db)
		owner := Option{Key: deploymentfence.OptionPrefix + "commerce-import:unknown-owner", Value: "malformed"}
		require.NoError(t, db.Create(&owner).Error)
		before := runtimeVerificationPostgresFacts(t, db)
		require.ErrorIs(t, PrepareMerchantStoreCommerceImport(db, 7), ErrMerchantStoreWriterFrozen)
		for _, model := range CommerceImportModels() {
			require.False(t, db.Migrator().HasTable(model), "a malformed retained owner cannot authorize installation")
		}
		require.Equal(t, before, runtimeVerificationPostgresFacts(t, db))
		require.NoError(t, db.Where("key = ?", owner.Key).Delete(&Option{}).Error)
		require.NoError(t, PrepareMerchantStoreCommerceImport(db, 7))
		require.NoError(t, ActivateMerchantStoreCommerceImport(db, 7))
		require.NoError(t, VerifyMerchantStoreCommerceImport(db))
		require.NoError(t, db.Create(&owner).Error)
		installed := runtimeVerificationPostgresFacts(t, db)
		require.ErrorIs(t, PrepareMerchantStoreCommerceImport(db, 8), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, VerifyMerchantStoreCommerceImport(db), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, ActivateMerchantStoreCommerceImport(db, 8), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, storeRequireCommerceImportReadable(db), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, db.Transaction(storeRequireCommerceImportWriter), ErrMerchantStoreWriterFrozen)
		require.False(t, CommerceImportSupported())
		require.Equal(t, installed, runtimeVerificationPostgresFacts(t, db), "owner presence never expires, repairs itself or changes the floor")
	})
}
