// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.

package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMerchantStoreRuntimeVerificationCatalogFollowsDurableFloor(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))
	require.NoError(t, BootstrapMerchantStoreWriterGate(db))
	full, err := buildPostgresSchemaInventory(db, "app_test", append(mainMigrationModels(), &SubscriptionPlan{}))
	require.NoError(t, err)
	for _, floor := range []int{1, 2, 3, 4, 5, 6, 7} {
		t.Run(fmt.Sprint(floor), func(t *testing.T) {
			// This tests model selection, not activation: real activation is
			// exercised separately on the complete PostgreSQL catalogue.
			require.NoError(t, db.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", fmt.Sprint(floor)).Error)
			models, err := runtimeVerificationModels(db)
			require.NoError(t, err)
			inventory, err := buildPostgresSchemaInventory(db, "app_test", models)
			require.NoError(t, err)
			require.NoError(t, verifyPostgresCatalogSnapshot(inventory, catalogSnapshotForInventory(inventory)))
			for _, table := range []string{"merchant_store_fixed_contents", "merchant_store_order_fixed_deliveries", "merchant_store_product_traffic_days", "merchant_store_product_traffic_receipts"} {
				present := false
				for _, object := range inventory.Objects {
					present = present || object.table == table
				}
				require.Equal(t, floor == 7, present, "phase-seven table %s follows the durable floor", table)
			}
			if floor == 7 {
				require.Equal(t, full, inventory)
				return
			}
			// Phase-seven's four tables are absent below seven. Below six,
			// only the two phase-six tables, their constraints/indexes and
			// category_id's column/index also disappear. Every older global
			// and shop column, index and constraint remains authoritative.
			expectedObjects := []string{}
			actualObjects := []string{}
			for _, object := range full.Objects {
				if !storePhaseSevenTable(object.table) && (floor >= 6 || (!storePhaseSixTable(object.table) && !storePhaseSixColumn(object.table, object.column))) {
					expectedObjects = append(expectedObjects, object.table+"."+object.column)
				}
			}
			for _, object := range inventory.Objects {
				actualObjects = append(actualObjects, object.table+"."+object.column)
			}
			require.Equal(t, expectedObjects, actualObjects)
			expectedIndexes := []postgresIndexSpec{}
			for _, index := range full.Indexes {
				if !storePhaseSevenTable(index.Table) && (floor >= 6 || (!storePhaseSixTable(index.Table) && index.Name != "idx_merchant_store_products_category_id")) {
					expectedIndexes = append(expectedIndexes, index)
				}
			}
			require.Equal(t, expectedIndexes, inventory.Indexes)
			expectedConstraints := []postgresConstraintSpec{}
			for _, constraint := range full.Constraints {
				if !storePhaseSevenTable(constraint.Table) && (floor >= 6 || !storePhaseSixTable(constraint.Table)) {
					expectedConstraints = append(expectedConstraints, constraint)
				}
			}
			require.Equal(t, expectedConstraints, inventory.Constraints)
			for _, table := range []string{"users", "tokens", "wallet_transfers", "merchant_store_products"} {
				snapshot := catalogSnapshotForInventory(inventory)
				removed := false
				for key, constraint := range snapshot.Constraints {
					if key.Table == table && constraint.Kind == postgresPrimaryConstraint {
						delete(snapshot.Constraints, key)
						removed = true
					}
				}
				require.True(t, removed, table)
				require.Error(t, verifyPostgresCatalogSnapshot(inventory, snapshot), "old primary keys remain required: %s", table)
			}
		})
	}
}

func TestMerchantStoreRuntimeVerificationMissingFloorRequiresFullCatalog(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	_, err = runtimeVerificationModels(db)
	require.Error(t, err, "a missing options table is not a fresh installed catalogue")
	require.NoError(t, db.AutoMigrate(&Option{}))
	require.NoError(t, checkMerchantStoreWriterMigration(db), "an explicit first apply still supports a fresh database")
	models, err := runtimeVerificationModels(db)
	require.NoError(t, err)
	full, err := buildPostgresSchemaInventory(db, "app_test", append(mainMigrationModels(), &SubscriptionPlan{}))
	require.NoError(t, err)
	actual, err := buildPostgresSchemaInventory(db, "app_test", models)
	require.NoError(t, err)
	require.Equal(t, full, actual)
	require.ErrorIs(t, storeRequireWriter(db), ErrMerchantStoreWriterFrozen, "verification does not activate missing-gate business writers")
	var count int64
	require.NoError(t, db.Model(&Option{}).Count(&count).Error)
	require.Zero(t, count, "verification never creates an activation marker")
	for _, value := range []string{"", "0", "8", "06", " 6", "6 ", "unknown"} {
		t.Run(fmt.Sprintf("invalid-%q", value), func(t *testing.T) {
			require.NoError(t, db.Where("key = ?", MerchantStoreWriterCapabilityOption).Delete(&Option{}).Error)
			require.NoError(t, db.Create(&Option{Key: MerchantStoreWriterCapabilityOption, Value: value}).Error)
			_, err := runtimeVerificationModels(db)
			require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
			var preserved Option
			require.NoError(t, db.First(&preserved, "key = ?", MerchantStoreWriterCapabilityOption).Error)
			require.Equal(t, value, preserved.Value, "verification never repairs or activates the floor")
		})
	}
}

func TestMerchantStoreRuntimeVerificationPostgresPreparedFloorAndStrictActivation(t *testing.T) {
	db, schemaName, _, _ := merchantStorePGDB(t)
	// Install the actual complete application catalogue in a unique disposable
	// schema, then reproduce native phase-five preparation without phase six.
	require.NoError(t, db.AutoMigrate(append(mainMigrationModels(), &SubscriptionPlan{})...))
	require.NoError(t, ensureCompanyBillingProfilePostgresContract(db))
	require.NoError(t, db.Create(&Option{Key: "theme.frontend", Value: "default"}).Error)
	// The shop fixture bootstraps floor one; a standalone application apply
	// does not. Remove only that fixture marker to reproduce first installation.
	var originalFloor Option
	require.NoError(t, db.First(&originalFloor, "key = ?", MerchantStoreWriterCapabilityOption).Error)
	require.NoError(t, db.Where("key = ?", MerchantStoreWriterCapabilityOption).Delete(&Option{}).Error)
	installed := runtimeVerificationPostgresFacts(t, db)
	require.NoError(t, verifyPostgresRuntimeAndSchema(db), "fresh standalone apply without a writer gate verifies the full catalogue")
	require.ErrorIs(t, storeRequireWriter(db), ErrMerchantStoreWriterFrozen)
	require.Equal(t, installed, runtimeVerificationPostgresFacts(t, db), "fresh verification performs no writes")
	storeRemovePhaseSixSchema(t, db)
	require.ErrorContains(t, verifyPostgresRuntimeAndSchema(db), "merchant_store_products.category_id", "a missing gate cannot hide missing phase-six schema")
	// Restore the exact synthetic fixture marker, not a bootstrap of a latest
	// fresh installation (which deliberately cannot activate old writers).
	require.NoError(t, db.Create(&originalFloor).Error)
	require.NoError(t, PrepareMerchantStoreSchema(db, 1))
	require.False(t, db.Migrator().HasColumn(&MerchantStoreProduct{}, "category_id"))
	require.False(t, db.Migrator().HasTable(&MerchantStoreCategory{}))
	require.False(t, db.Migrator().HasTable(&MerchantStoreProductLike{}))
	before := runtimeVerificationPostgresFacts(t, db)
	original, err := buildPostgresSchemaInventory(db, schemaName, append(mainMigrationModels(), &SubscriptionPlan{}))
	require.NoError(t, err)
	require.ErrorContains(t, verifyPostgresSchemaInventory(db, original), "merchant_store_products.category_id", "reproduce the actual unfiltered Go92 native failure")
	require.NoError(t, verifyPostgresRuntimeAndSchema(db), "prepared floor one must pass the complete native verifier")
	require.Equal(t, before, runtimeVerificationPostgresFacts(t, db), "verification performs no schema or row writes")
	storeActivatePhaseFiveForSixTest(t, db)
	require.NoError(t, verifyPostgresRuntimeAndSchema(db), "real floor five still uses the frozen catalogue")
	before = runtimeVerificationPostgresFacts(t, db)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Exec("ALTER TABLE users DROP COLUMN username CASCADE").Error)
		require.ErrorContains(t, verifyPostgresRuntimeAndSchema(tx), "users.username", "unrelated requirements are never skipped")
		return errors.New("rollback intentional negative fixture")
	}))
	require.Equal(t, before, runtimeVerificationPostgresFacts(t, db))
	require.NoError(t, PrepareMerchantStorePhaseSix(db, 5))
	require.NoError(t, ActivateMerchantStorePhaseSix(db, 5))
	require.NoError(t, verifyPostgresRuntimeAndSchema(db), "real floor six checks the complete new catalogue")
	for name, ddl := range map[string]string{
		"category-column": "ALTER TABLE merchant_store_products DROP COLUMN category_id",
		"category-table":  "DROP TABLE merchant_store_categories",
		"likes-table":     "DROP TABLE merchant_store_product_likes",
		"category-index":  "DROP INDEX idx_merchant_store_products_category_id",
		"old-shop-index":  "DROP INDEX idx_merchant_store_products_visibility",
	} {
		t.Run(name, func(t *testing.T) {
			before := runtimeVerificationPostgresFacts(t, db)
			// Catalogue reads use the SQL pool, so make the deliberate missing
			// object visible to every connection rather than an uncommitted DDL.
			require.NoError(t, db.Exec(ddl).Error)
			missing := runtimeVerificationPostgresFacts(t, db)
			err := verifyPostgresRuntimeAndSchema(db)
			require.Error(t, err, "floor six must reject missing %s", name)
			require.NotContains(t, strings.ToLower(err.Error()), "writer floor", "failure must come from the required schema, not a fake floor")
			require.Contains(t, err.Error(), "is missing")
			require.Equal(t, missing, runtimeVerificationPostgresFacts(t, db), "failed verification cannot repair the missing object")
			require.NoError(t, db.AutoMigrate(&MerchantStoreProduct{}, &MerchantStoreCategory{}, &MerchantStoreProductLike{}))
			restored := runtimeVerificationPostgresFacts(t, db)
			for key, rows := range before {
				if strings.HasPrefix(key, "rows:") {
					require.Equal(t, rows, restored[key], "test-only DDL restoration preserves %s", key)
				}
			}
		})
	}
	require.NoError(t, verifyPostgresRuntimeAndSchema(db))
}

// Full application tables include PostgreSQL json (not orderable). Compare
// canonical row text rather than ORDER BY every column as a shop-only fixture
// does. Columns/defaults, indexes/constraints and sequence values are sealed too.
func runtimeVerificationPostgresFacts(t *testing.T, db *gorm.DB) map[string]string {
	t.Helper()
	facts := map[string]string{}
	var tables []string
	require.NoError(t, db.Raw("SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname=pg_catalog.current_schema() ORDER BY tablename").Scan(&tables).Error)
	for _, table := range tables {
		quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		var rows string
		require.NoError(t, db.Raw("SELECT COALESCE(pg_catalog.string_agg(row_data::text, E'\\n' ORDER BY row_data::text),'') FROM (SELECT pg_catalog.row_to_json(t) AS row_data FROM "+quoted+" AS t) AS row_snapshot").Scan(&rows).Error)
		facts["rows:"+table] = rows
	}
	for name, query := range map[string]string{
		"columns":     `SELECT COALESCE(pg_catalog.string_agg(pg_catalog.row_to_json(c)::text,E'\n' ORDER BY table_name,ordinal_position),'') FROM information_schema.columns AS c WHERE table_schema=pg_catalog.current_schema()`,
		"indexes":     `SELECT COALESCE(pg_catalog.string_agg(indexdef,E'\n' ORDER BY tablename,indexname),'') FROM pg_catalog.pg_indexes WHERE schemaname=pg_catalog.current_schema()`,
		"constraints": `SELECT COALESCE(pg_catalog.string_agg(pg_catalog.format('%s:%s:%s',c.relname,k.conname,pg_catalog.pg_get_constraintdef(k.oid)),E'\n' ORDER BY c.relname,k.conname),'') FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_class c ON c.oid=k.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=pg_catalog.current_schema()`,
		"sequences":   `SELECT COALESCE(pg_catalog.string_agg(pg_catalog.row_to_json(s)::text,E'\n' ORDER BY sequencename),'') FROM pg_catalog.pg_sequences s WHERE schemaname=pg_catalog.current_schema()`,
	} {
		var value string
		require.NoError(t, db.Raw(query).Scan(&value).Error)
		facts[name] = value
	}
	return facts
}
