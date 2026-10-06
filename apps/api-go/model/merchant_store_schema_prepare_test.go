package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type storePreparationSnapshot struct {
	columns map[string][]string
	rows    map[string]string
}

// Freeze the actual pre-installation columns, then compare those same facts
// after the allowed additions. New defaults are checked separately and cannot
// hide a change to any original wallet, stock, order, or ledger column.
func storePreparationFacts(t *testing.T, db *gorm.DB, before *storePreparationSnapshot) *storePreparationSnapshot {
	t.Helper()
	if before == nil {
		before = &storePreparationSnapshot{columns: map[string][]string{}, rows: map[string]string{}}
		var tables []string
		require.NoError(t, db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name").Scan(&tables).Error)
		for _, table := range tables {
			if storeAccessTable(table) {
				continue
			}
			var cols []struct{ Name string }
			require.NoError(t, db.Raw("SELECT name FROM pragma_table_info(?) ORDER BY cid", table).Scan(&cols).Error)
			for _, col := range cols {
				before.columns[table] = append(before.columns[table], col.Name)
			}
		}
	}
	result := &storePreparationSnapshot{columns: before.columns, rows: map[string]string{}}
	for table, columns := range before.columns {
		quoted := make([]string, len(columns))
		for i, column := range columns {
			quoted[i] = `"` + strings.ReplaceAll(column, `"`, `""`) + `"`
		}
		var rows []map[string]any
		require.NoError(t, db.Table(table).Select(strings.Join(quoted, ",")).Order(strings.Join(quoted, ",")).Find(&rows).Error)
		data, err := json.Marshal(rows)
		require.NoError(t, err)
		result.rows[table] = string(data)
	}
	return result
}

func storeRemovePhase5Schema(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, item := range MerchantStoreModels() {
		stmt := &gorm.Statement{DB: db}
		require.NoError(t, stmt.Parse(item))
		if storeAccessTable(stmt.Schema.Table) {
			require.NoError(t, db.Migrator().DropTable(item))
		}
	}
	require.NoError(t, db.Migrator().DropIndex(&MerchantStoreProduct{}, "idx_merchant_store_products_visibility"))
	require.NoError(t, db.Migrator().DropIndex(&MerchantStoreOrder{}, "idx_merchant_store_orders_guest_id"))
	for table, cols := range map[string][]string{
		"merchant_store_products": {"visibility", "purchase_login_required"},
		"merchant_store_orders":   {"guest_id", "seller_terms_version", "seller_terms_content", "seller_terms_accepted_at"},
	} {
		for _, column := range cols {
			require.NoError(t, db.Exec(fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, column)).Error)
		}
	}
}

func TestMerchantStoreSchemaPreparationPreservesOldFactsAndSeparatesActivation(t *testing.T) {
	f := newStoreFixture(t, "balance")
	o, _, err := CreateMerchantStoreOrder(f.checkout("paid-before-access", "balance"))
	require.NoError(t, err)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, err)
	storeWriterGateForTest(t, "4")
	storeRemovePhase5Schema(t, DB)
	// Capability five genuinely runs on stage four before its new columns,
	// tables and defaults are installed. No invented version override is used.
	require.NoError(t, ActivateMerchantStoreRefunds(DB, 4))
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
	require.ErrorIs(t, ActivateMerchantStoreAccess(DB, 4), ErrMerchantStoreWriterFrozen)
	before := storePreparationFacts(t, DB, nil)
	require.NoError(t, PrepareMerchantStoreSchema(DB, 4))
	require.Equal(t, before.rows, storePreparationFacts(t, DB, before).rows)
	status, err := GetMerchantStoreWriterGateStatus(DB)
	require.NoError(t, err)
	require.Equal(t, 4, status.RequiredCapability, "DDL does not activate access")
	require.False(t, MerchantStoreAccessSupported())
	require.False(t, MerchantStoreCatalogueSupported())
	var metadata MerchantStoreCatalogueMetadata
	require.NoError(t, DB.First(&metadata, "product_id = ?", f.product.ID).Error)
	require.Equal(t, MerchantStoreDefaultVariantID(f.product.ID), metadata.DefaultVariantID)
	require.NoError(t, PrepareMerchantStoreSchema(DB, 4), "installation retry is idempotent")
	require.Equal(t, before.rows, storePreparationFacts(t, DB, before).rows)
	require.NoError(t, ActivateMerchantStoreAccess(DB, 4))
	require.NoError(t, ActivateMerchantStoreAccess(DB, 5))
	require.True(t, MerchantStoreAccessSupported())
	require.True(t, MerchantStoreCatalogueSupported())
	require.NoError(t, DB.Transaction(MerchantStoreRefundRequiresWriter), "higher floor retains refund obligations")
	require.NoError(t, DB.Transaction(storeRequireLifecycleWriter))
	claim, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
}

func TestMerchantStoreSchemaPreparationRejectsWrongFloorAndDurableOwners(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storeWriterGateForTest(t, "4")
	require.NoError(t, DB.Migrator().DropTable(&MerchantStoreFavorite{}))
	require.ErrorIs(t, PrepareMerchantStoreSchema(DB, 3), ErrMerchantStoreWriterFrozen)
	require.False(t, DB.Migrator().HasTable(&MerchantStoreFavorite{}))
	owner := Option{Key: deploymentfence.OptionPrefix + "orphan:host", Value: "malformed"}
	require.NoError(t, DB.Create(&owner).Error)
	require.ErrorIs(t, PrepareMerchantStoreSchema(DB, 4), ErrMerchantStoreWriterFrozen)
	require.False(t, DB.Migrator().HasTable(&MerchantStoreFavorite{}), "unknown owner cannot authorize repair")
	require.ErrorIs(t, ActivateMerchantStoreAccess(DB, 4), ErrMerchantStoreWriterFrozen)
	require.NoError(t, DB.Where("key = ?", owner.Key).Delete(&Option{}).Error)
	require.ErrorIs(t, PrepareMerchantStoreSchema(DB, 0), ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, PrepareMerchantStoreSchema(DB, MerchantStoreWriterCapability+1), ErrMerchantStoreWriterFrozen)
	require.NoError(t, PrepareMerchantStoreSchema(DB, 4))
	require.NoError(t, DB.Model(&MerchantStoreCatalogueMetadata{}).Where("product_id = ?", f.product.ID).Update("default_variant_id", "wrong-pool").Error)
	require.ErrorIs(t, ActivateMerchantStoreAccess(DB, 4), ErrMerchantStoreWriterFrozen, "mapping mismatch cannot silently select a different stock pool")
	var row MerchantStoreCatalogueMetadata
	require.NoError(t, DB.First(&row).Error)
	require.Equal(t, "wrong-pool", row.DefaultVariantID, "activation does not repair bad metadata")
}

func TestMerchantStoreSchemaPreparationRollsBackDDLWhenMappingBackfillFails(t *testing.T) {
	newStoreFixture(t, "balance")
	storeWriterGateForTest(t, "4")
	storeRemovePhase5Schema(t, DB)
	// A real database failure occurs after AutoMigrate has installed additions.
	// The preparation transaction must roll those additions back as well as DML.
	require.NoError(t, DB.Migrator().CreateTable(&MerchantStoreCatalogueMetadata{}))
	require.NoError(t, DB.Exec(`CREATE TRIGGER reject_store_mapping_prepare BEFORE INSERT ON merchant_store_catalogue_metadata BEGIN SELECT RAISE(ABORT, 'injected-private-diagnostic'); END`).Error)
	before := storePreparationFacts(t, DB, nil)
	err := PrepareMerchantStoreSchema(DB, 4)
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	require.NotContains(t, err.Error(), "injected-private-diagnostic", "operator error does not echo raw database diagnostics")
	require.False(t, DB.Migrator().HasTable(&MerchantStoreFavorite{}))
	require.False(t, DB.Migrator().HasColumn(&MerchantStoreProduct{}, "visibility"))
	require.False(t, DB.Migrator().HasColumn(&MerchantStoreOrder{}, "guest_id"))
	require.Equal(t, before.rows, storePreparationFacts(t, DB, before).rows)
	status, err := GetMerchantStoreWriterGateStatus(DB)
	require.NoError(t, err)
	require.Equal(t, 4, status.RequiredCapability)
}
