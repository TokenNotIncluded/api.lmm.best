package model

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func storeActivatePhaseFiveForSixTest(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, PrepareMerchantStoreSchema(db, 1))
	require.NoError(t, ActivateMerchantStoreVariants(db, 1))
	require.NoError(t, ActivateMerchantStoreProductLifecycle(db, 2))
	require.NoError(t, ActivateMerchantStoreRefunds(db, 3))
	require.NoError(t, ActivateMerchantStoreAccess(db, 4))
}

func storeRemovePhaseSixSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, item := range MerchantStoreModels() {
		stmt := &gorm.Statement{DB: db}
		require.NoError(t, stmt.Parse(item))
		if storePhaseSixTable(stmt.Schema.Table) {
			require.NoError(t, db.Migrator().DropTable(item))
		}
	}
	require.NoError(t, db.Migrator().DropIndex(&MerchantStoreProduct{}, "idx_merchant_store_products_category_id"))
	require.NoError(t, db.Exec("ALTER TABLE merchant_store_products DROP COLUMN category_id").Error)
}

func storePhaseSixOldFacts(t *testing.T, db *gorm.DB) *storePreparationSnapshot {
	t.Helper()
	before := &storePreparationSnapshot{columns: map[string][]string{}, rows: map[string]string{}}
	var tables []string
	if db.Dialector.Name() == "postgres" {
		require.NoError(t, db.Raw("SELECT tablename FROM pg_tables WHERE schemaname=current_schema() ORDER BY tablename").Scan(&tables).Error)
	} else {
		require.NoError(t, db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name").Scan(&tables).Error)
	}
	for _, table := range tables {
		var columns []struct{ Name string }
		if db.Dialector.Name() == "postgres" {
			require.NoError(t, db.Raw("SELECT column_name AS name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? ORDER BY ordinal_position", table).Scan(&columns).Error)
		} else {
			require.NoError(t, db.Raw("SELECT name FROM pragma_table_info(?) ORDER BY cid", table).Scan(&columns).Error)
		}
		for _, column := range columns {
			before.columns[table] = append(before.columns[table], column.Name)
		}
	}
	return storePreparationFacts(t, db, before)
}

func TestMerchantStorePhaseSixFrozenPhaseFiveShape(t *testing.T) {
	old, err := schema.Parse(&merchantStoreProductSchemaFive{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	current, err := schema.Parse(&MerchantStoreProduct{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	require.Equal(t, current.Table, old.Table)
	for _, field := range current.Fields {
		if field.DBName == "" || storePhaseSixColumn(current.Table, field.DBName) {
			continue
		}
		frozen := old.LookUpField(field.DBName)
		require.NotNil(t, frozen)
		require.Equal(t, field.TagSettings, frozen.TagSettings, field.DBName)
	}
	require.Nil(t, old.LookUpField("category_id"))
	for name, index := range old.ParseIndexes() {
		require.Equal(t, current.ParseIndexes()[name].Class, index.Class)
	}
}

func TestMerchantStorePhaseSixPreparationPreservesPhaseFiveFactsAndActivation(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storeActivatePhaseFiveForSixTest(t, DB)
	storeRemovePhaseSixSchema(t, DB)
	before := storePhaseSixOldFacts(t, DB)
	require.ErrorIs(t, ActivateMerchantStorePhaseSix(DB, 5), ErrMerchantStoreWriterFrozen)
	require.NoError(t, DB.Transaction(storeRequireWriter), "cap6 remains a real phase-five writer before installation")
	require.False(t, MerchantStoreCategoriesSupported())
	require.NoError(t, PrepareMerchantStoreSchema(DB, 5))
	require.Equal(t, before.rows, storePreparationFacts(t, DB, before).rows)
	require.NoError(t, PrepareMerchantStoreSchema(DB, 5))
	require.Equal(t, before.rows, storePreparationFacts(t, DB, before).rows)
	require.False(t, MerchantStoreCategoriesSupported(), "installation alone cannot activate category writes")
	require.ErrorIs(t, DB.Transaction(storeRequireCategoriesWriter), ErrMerchantStoreWriterFrozen)
	require.NoError(t, ActivateMerchantStorePhaseSix(DB, 5))
	require.NoError(t, ActivateMerchantStorePhaseSix(DB, 6))
	require.True(t, MerchantStoreCategoriesSupported())
	require.True(t, MerchantStoreLikesSupported())
	require.NoError(t, DB.Transaction(storeRequireCategoriesWriter))
	var p MerchantStoreProduct
	require.NoError(t, DB.First(&p, "id = ?", f.product.ID).Error)
	require.Empty(t, p.CategoryID, "migration never invents a category for older products")
	owner := Option{Key: deploymentfence.OptionPrefix + "unknown:host", Value: "malformed"}
	require.NoError(t, DB.Create(&owner).Error)
	require.False(t, MerchantStoreCategoriesSupported())
	require.ErrorIs(t, DB.Transaction(storeRequireSocialWriter), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, PrepareMerchantStoreSchema(DB, 6), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, ActivateMerchantStorePhaseSix(DB, 6), ErrMerchantStoreWriterFrozen)
}

func TestMerchantStorePhaseSixOldPreparationDoesNotInstallNewSchema(t *testing.T) {
	newStoreFixture(t, "balance")
	storeRemovePhaseSixSchema(t, DB)
	require.NoError(t, PrepareMerchantStoreSchema(DB, 1))
	require.False(t, DB.Migrator().HasTable(&MerchantStoreCategory{}))
	require.False(t, DB.Migrator().HasTable(&MerchantStoreProductLike{}))
	require.False(t, DB.Migrator().HasColumn(&MerchantStoreProduct{}, "category_id"))
}

func storePhaseSixRollbackTrigger(t *testing.T, db *gorm.DB) {
	t.Helper()
	// An event trigger forces a failure after at least one phase-six table DDL.
	// The fresh isolated PostgreSQL test role may create it; scope it narrowly to
	// this test schema and clean it up even if the migration transaction fails.
	var name string
	require.NoError(t, db.Raw("SELECT current_schema()").Scan(&name).Error)
	trigger := "store_phase_six_" + strings.TrimPrefix(name, "lmm_merchant_store_test_")
	function := trigger + "_reject"
	sql := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS event_trigger LANGUAGE plpgsql AS $$ BEGIN IF EXISTS (SELECT 1 FROM pg_event_trigger_ddl_commands() WHERE schema_name = '%s' AND object_identity LIKE '%%merchant_store_product_likes') THEN RAISE EXCEPTION 'private-phase-six-DDL-diagnostic'; END IF; END $$`, function, name)
	require.NoError(t, db.Exec(sql).Error)
	require.NoError(t, db.Exec("CREATE EVENT TRIGGER "+trigger+" ON ddl_command_end WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION "+name+"."+function+"()").Error)
	t.Cleanup(func() {
		require.NoError(t, db.Exec("DROP EVENT TRIGGER IF EXISTS "+trigger).Error)
		require.NoError(t, db.Exec("DROP FUNCTION IF EXISTS "+function+"()").Error)
	})
}

// True disposable PostgreSQL proof: the existing fixture rejects production
// endpoints and creates a fresh, uniquely owned schema in the selected cluster.
func TestMerchantStorePhaseSixPreparationPostgresParents(t *testing.T) {
	t.Run("preserves_actual_phase_five_facts_and_separates_activation", func(t *testing.T) {
		db, name, _, legacy := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "balance")
		_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"phase-six-paid-fixture", "phase-six-retained-fixture"})
		require.NoError(t, err)
		_, _, err = CreateMerchantStoreOrder(f.checkout("paid-before-phase-six", "balance"))
		require.NoError(t, err)
		storeActivatePhaseFiveForSixTest(t, db)
		storeRemovePhaseSixSchema(t, db)
		before := storePhaseSixOldFacts(t, db)
		oldTables := merchantStorePGTables(t, db, name)
		require.ErrorIs(t, ActivateMerchantStorePhaseSix(db, 5), ErrMerchantStoreWriterFrozen)
		require.NoError(t, PrepareMerchantStoreSchema(db, 5))
		require.Equal(t, before.rows, storePreparationFacts(t, db, before).rows)
		var added []string
		for _, table := range merchantStorePGTables(t, db, name) {
			found := false
			for _, old := range oldTables {
				found = found || table == old
			}
			if !found {
				require.True(t, storePhaseSixTable(table))
				added = append(added, table)
			}
		}
		require.ElementsMatch(t, []string{"merchant_store_categories", "merchant_store_product_likes"}, added)
		require.Equal(t, legacy, merchantStorePGFingerprint(t, db, name))
		require.NoError(t, PrepareMerchantStoreSchema(db, 5))
		require.Equal(t, before.rows, storePreparationFacts(t, db, before).rows)
		require.NoError(t, ActivateMerchantStorePhaseSix(db, 5))
		require.NoError(t, ActivateMerchantStorePhaseSix(db, 6))
		require.True(t, MerchantStoreCategoriesSupported())
		require.True(t, MerchantStoreLikesSupported())
		category, err := SaveMerchantStoreCategory(f.root.Id, "", MerchantStoreCategoryInput{Name: "Actual PG category", SortOrder: 8})
		require.NoError(t, err)
		assigned, err := SetMerchantStoreProductCategory(f.seller.Id, f.product.ID, category.ID)
		require.NoError(t, err)
		require.Equal(t, "published", assigned.Status)
		visible, err := GetPublicMerchantStoreProduct(f.product.ID)
		require.NoError(t, err)
		require.Equal(t, category.Name, visible.Category.Name)
		_, err = SetMerchantStoreProductCategory(f.buyer.Id, f.product.ID, category.ID)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		inactive := false
		_, err = SaveMerchantStoreCategory(f.root.Id, category.ID, MerchantStoreCategoryInput{Name: category.Name, SortOrder: 8, Active: &inactive})
		require.NoError(t, err)
		visible, err = GetPublicMerchantStoreProduct(f.product.ID)
		require.NoError(t, err)
		require.Equal(t, category.Name, visible.Category.Name, "disabled categories preserve real product labels")
		filtered, err := ListMerchantStoreCatalogue(0, "", 0, 0, 24, MerchantStoreCatalogueQuery{CategoryID: category.ID})
		require.NoError(t, err)
		require.Empty(t, filtered)
		likes, err := SetMerchantStoreProductLike(f.buyer.Id, f.product.ID, true)
		require.NoError(t, err)
		require.True(t, likes.Supported)
		require.True(t, likes.Liked)
		require.EqualValues(t, 1, *likes.Count)
		t.Logf("phase6 schema=%s exact_additions=%v legacy_fingerprint=%s", name, added, legacy)
	})
	t.Run("DDL_failure_rolls_back_every_phase_six_addition", func(t *testing.T) {
		db, name, _, _ := merchantStorePGDB(t)
		merchantStorePGFixture(t, db, "balance")
		storeActivatePhaseFiveForSixTest(t, db)
		storeRemovePhaseSixSchema(t, db)
		before := storePhaseSixOldFacts(t, db)
		oldTables := merchantStorePGTables(t, db, name)
		storePhaseSixRollbackTrigger(t, db)
		err := PrepareMerchantStoreSchema(db, 5)
		require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
		require.NotContains(t, err.Error(), "private-phase-six-DDL-diagnostic")
		require.Equal(t, oldTables, merchantStorePGTables(t, db, name))
		require.False(t, db.Migrator().HasColumn(&MerchantStoreProduct{}, "category_id"))
		require.Equal(t, before.rows, storePreparationFacts(t, db, before).rows)
	})
	t.Run("phase_six_business_transaction_retains_real_fence_and_floor_SHARE", func(t *testing.T) {
		db, _, _, _ := merchantStorePGDB(t)
		merchantStorePGFixture(t, db, "balance")
		storeActivatePhaseFiveForSixTest(t, db)
		require.NoError(t, PrepareMerchantStoreSchema(db, 5))
		require.NoError(t, ActivateMerchantStorePhaseSix(db, 5))
		tx := db.Begin()
		require.NoError(t, tx.Error)
		t.Cleanup(func() { _ = tx.Rollback().Error })
		require.NoError(t, storeRequireSocialWriter(tx))
		require.ErrorIs(t, ActivateMerchantStorePhaseSix(db, 6), ErrMerchantStoreWriterFrozen, "activation cannot cross an in-flight business fence")
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		err := db.WithContext(ctx).Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "5").Error
		cancel()
		require.Error(t, err, "the guard's physical SHARE lock blocks a concurrent floor rewrite")
		require.NoError(t, tx.Rollback().Error)
		require.NoError(t, ActivateMerchantStorePhaseSix(db, 6))
		owner := Option{Key: deploymentfence.OptionPrefix + "crashed:phase-six", Value: "malformed"}
		require.NoError(t, db.Create(&owner).Error)
		require.ErrorIs(t, db.Transaction(storeRequireCategoriesWriter), ErrMerchantStoreWriterFrozen)
		require.ErrorIs(t, PrepareMerchantStoreSchema(db, 6), ErrMerchantStoreWriterFrozen)
		require.False(t, MerchantStoreLikesSupported())
	})
}
