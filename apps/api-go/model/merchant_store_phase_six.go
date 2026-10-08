package model

import (
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/internal/deploymentfence"
	"gorm.io/gorm"
)

// Phase six is additive to the signed phase-five catalogue. Retirement of a
// category retains every product and order; social rows contain account/product
// references only. These additions never participate in an earlier qualifier.
func storePhaseSixTable(table string) bool {
	return table == "merchant_store_categories" || table == "merchant_store_product_likes"
}

func storePhaseSixColumn(table, column string) bool {
	return table == "merchant_store_products" && column == "category_id"
}

func storePhaseSixFence(tx *gorm.DB, writer bool) error {
	if tx == nil {
		return ErrMerchantStoreWriterFrozen
	}
	q := tx.Session(&gorm.Session{NewDB: true})
	if tx.Dialector.Name() == "postgres" {
		if writer {
			var acquired bool
			if err := q.Raw("SELECT pg_catalog.pg_try_advisory_xact_lock_shared(?)", deploymentfence.AdvisoryKey).Scan(&acquired).Error; err != nil || !acquired {
				return ErrMerchantStoreWriterFrozen
			}
		}
		var owner bool
		if err := q.Raw("SELECT EXISTS (SELECT 1 FROM options WHERE " + deploymentfence.PostgreSQLPresencePredicate + ")").Scan(&owner).Error; err != nil || owner {
			return ErrMerchantStoreWriterFrozen
		}
		return nil
	}
	var keys []string
	if err := q.Model(&Option{}).Pluck("key", &keys).Error; err != nil {
		return ErrMerchantStoreWriterFrozen
	}
	for _, key := range keys {
		if deploymentfence.ReservedOptionKey(key) {
			return ErrMerchantStoreWriterFrozen
		}
	}
	return nil
}

func storeRequirePhaseSixReadable(tx *gorm.DB) error {
	if err := storePhaseSixFence(tx, false); err != nil {
		return err
	}
	floor, err := storeWriterGateRow(tx.Session(&gorm.Session{NewDB: true}), "")
	if err != nil || (floor < 6 || floor > MerchantStoreWriterCapability) || MerchantStoreWriterCapability < 6 {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func storeRequirePhaseSixWriter(tx *gorm.DB) error {
	// Deployment takes this fence before locking the writer floor. Preserve that
	// order and retain both shared locks through the business transaction.
	if err := storePhaseSixFence(tx, true); err != nil {
		return err
	}
	floor, err := storeWriterGateRow(tx, "SHARE")
	if err != nil || (floor < 6 || floor > MerchantStoreWriterCapability) || MerchantStoreWriterCapability < 6 {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func storeRequireCategoriesWriter(tx *gorm.DB) error { return storeRequirePhaseSixWriter(tx) }
func storeRequireSocialWriter(tx *gorm.DB) error     { return storeRequirePhaseSixWriter(tx) }

// Old preparation remains a phase-five operation even in a newer binary. The
// frozen product shape prevents AutoMigrate from silently installing category_id
// during a repeat of the historical access preparation.
func storePhaseFivePreparationModels(db *gorm.DB) ([]interface{}, error) {
	models := make([]interface{}, 0, len(MerchantStoreModels()))
	for _, item := range MerchantStoreModels() {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(item); err != nil {
			return nil, ErrMerchantStoreWriterFrozen
		}
		if storePhaseSixTable(stmt.Schema.Table) || storePhaseSevenTable(stmt.Schema.Table) || storeCommerceImportTable(stmt.Schema.Table) {
			continue
		}
		if stmt.Schema.Table == "merchant_store_products" {
			models = append(models, &merchantStoreProductSchemaFive{})
		} else {
			models = append(models, item)
		}
	}
	return models, nil
}

// Installation is a private, explicit NEXT 5->6 action. Only the two declared
// tables and one empty-default product reference are migrated; old shop tables,
// mappings, wallets and orders are not rewritten by this phase.
func PrepareMerchantStorePhaseSix(db *gorm.DB, expected int) error {
	if db == nil || MerchantStoreWriterCapability < 6 || (expected != 5 && expected != 6) || (db.Dialector.Name() != "postgres" && db.Dialector.Name() != "sqlite") {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			floor, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil || floor != expected {
				return ErrMerchantStoreWriterFrozen
			}
			if err := storeCheckMerchantStoreSchema(tx, 5); err != nil {
				return err
			}
			for _, item := range MerchantStoreModels() {
				stmt := &gorm.Statement{DB: tx}
				if err := stmt.Parse(item); err != nil {
					return ErrMerchantStoreWriterFrozen
				}
				if storePhaseSixTable(stmt.Schema.Table) {
					if err := tx.AutoMigrate(item); err != nil {
						return fmt.Errorf("%w: phase-six table preparation failed", ErrMerchantStoreWriterFrozen)
					}
				}
			}
			if !tx.Migrator().HasColumn(&MerchantStoreProduct{}, "category_id") {
				if err := tx.Migrator().AddColumn(&MerchantStoreProduct{}, "CategoryID"); err != nil {
					return fmt.Errorf("%w: category reference preparation failed", ErrMerchantStoreWriterFrozen)
				}
			}
			stmt := &gorm.Statement{DB: tx}
			if err := stmt.Parse(&MerchantStoreProduct{}); err != nil {
				return ErrMerchantStoreWriterFrozen
			}
			for name, index := range stmt.Schema.ParseIndexes() {
				for _, field := range index.Fields {
					if storePhaseSixColumn(stmt.Schema.Table, field.DBName) && !tx.Migrator().HasIndex(&MerchantStoreProduct{}, name) {
						if err := tx.Migrator().CreateIndex(&MerchantStoreProduct{}, name); err != nil {
							return ErrMerchantStoreWriterFrozen
						}
					}
				}
			}
			return storeCheckPhaseSixSchema(tx)
		})
	})
}

func storeCheckPhaseSixSchema(tx *gorm.DB) error {
	if err := storeCheckMerchantStoreSchema(tx, 6); err != nil {
		return err
	}
	columns, err := tx.Migrator().ColumnTypes(&MerchantStoreProduct{})
	if err != nil {
		return ErrMerchantStoreWriterFrozen
	}
	for _, column := range columns {
		if column.Name() == "category_id" {
			value, known := column.DefaultValue()
			value = strings.Trim(strings.TrimSpace(strings.SplitN(value, "::", 2)[0]), "()'")
			if known && value == "" {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: invalid category reference default", ErrMerchantStoreWriterFrozen)
}

// Activation contains no DDL or repair. The actual next-stage schema must
// already be installed; repeating at six verifies it without changing the floor.
func ActivateMerchantStorePhaseSix(db *gorm.DB, expected int) error {
	if db == nil || MerchantStoreWriterCapability < 6 || (expected != 5 && expected != 6) {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			floor, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil || (floor != 6 && floor != expected) || floor < 5 {
				return ErrMerchantStoreWriterFrozen
			}
			if err := storeCheckPhaseSixSchema(tx); err != nil {
				return err
			}
			if floor == 6 {
				return nil
			}
			result := tx.Model(&Option{}).Where("key = ? AND value = ?", MerchantStoreWriterCapabilityOption, "5").Update("value", "6")
			if result.Error != nil || result.RowsAffected != 1 {
				return ErrMerchantStoreWriterFrozen
			}
			return nil
		})
	})
}
