package model

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

func storeCommerceImportTable(table string) bool {
	return strings.HasPrefix(table, "merchant_store_commerce_")
}

func storeRequireCommerceImportReadable(tx *gorm.DB) error {
	if err := storePhaseSixFence(tx, false); err != nil {
		return err
	}
	floor, err := storeWriterGateRow(tx.Session(&gorm.Session{NewDB: true}), "")
	if err != nil || floor < 8 || floor > MerchantStoreWriterCapability || MerchantStoreWriterCapability < 8 {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func storeRequireCommerceImportWriter(tx *gorm.DB) error {
	// Deployment holds its fence before locking the writer floor. Keep the
	// shared fence and floor locks through the complete business transaction.
	if err := storePhaseSixFence(tx, true); err != nil {
		return err
	}
	floor, err := storeWriterGateRow(tx, "SHARE")
	if err != nil || floor < 8 || floor > MerchantStoreWriterCapability || MerchantStoreWriterCapability < 8 {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func storeCommerceImportSupported(tx *gorm.DB) bool {
	if storeRequireCommerceImportReadable(tx) != nil {
		return false
	}
	for _, model := range CommerceImportModels() {
		if !tx.Migrator().HasTable(model) {
			return false
		}
	}
	return true
}

func MerchantStoreCommerceImportSupported() bool { return storeCommerceImportSupported(DB) }
func CommerceImportSupported() bool              { return storeCommerceImportSupported(DB) }

// Preparation installs only the independent commerce-import tables. It never
// migrates existing shop models, backfills orders or raises the writer floor.
func PrepareMerchantStoreCommerceImport(db *gorm.DB, expected int) error {
	if db == nil || MerchantStoreWriterCapability < 8 || (expected != 7 && expected != 8) || (db.Dialector.Name() != "postgres" && db.Dialector.Name() != "sqlite") {
		return ErrMerchantStoreWriterFrozen
	}
	models := CommerceImportModels()
	seen := make(map[string]bool, len(models))
	for _, item := range models {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(item); err != nil || !storeCommerceImportTable(stmt.Schema.Table) || seen[stmt.Schema.Table] {
			return ErrMerchantStoreWriterFrozen
		}
		seen[stmt.Schema.Table] = true
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			floor, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil || floor != expected {
				return ErrMerchantStoreWriterFrozen
			}
			if err := storeCheckMerchantStoreSchema(tx, 7); err != nil {
				return err
			}
			for _, item := range models {
				// A retry verifies installed tables instead of rewriting them.
				// Partial or damaged existing tables must fail qualification.
				if tx.Migrator().HasTable(item) {
					continue
				}
				if err := tx.AutoMigrate(item); err != nil {
					return fmt.Errorf("%w: commerce-import table preparation failed", ErrMerchantStoreWriterFrozen)
				}
			}
			return storeCheckMerchantStoreSchema(tx, 8)
		})
	})
}

// Activation checks installed schema and changes only the durable floor. A
// retry at eight rechecks readiness without applying DDL or lowering the floor.
func ActivateMerchantStoreCommerceImport(db *gorm.DB, expected int) error {
	if db == nil || MerchantStoreWriterCapability < 8 || (expected != 7 && expected != 8) {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			floor, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil || (floor != 8 && floor != expected) || floor < 7 {
				return ErrMerchantStoreWriterFrozen
			}
			if err := storeCheckMerchantStoreSchema(tx, 8); err != nil {
				return err
			}
			if floor == 8 {
				return nil
			}
			result := tx.Model(&Option{}).Where("key = ? AND value = ?", MerchantStoreWriterCapabilityOption, "7").Update("value", "8")
			if result.Error != nil || result.RowsAffected != 1 {
				return ErrMerchantStoreWriterFrozen
			}
			return nil
		})
	})
}

// Verification can qualify preparation at seven and performs no repair or
// activation, including when a required table, column or index is missing.
func VerifyMerchantStoreCommerceImport(db *gorm.DB) error {
	if db == nil || MerchantStoreWriterCapability < 8 {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			floor, err := storeWriterGateRow(tx, "SHARE")
			if err != nil || (floor != 7 && floor != 8) {
				return ErrMerchantStoreWriterFrozen
			}
			if err := storeCheckPhaseSixSchema(tx); err != nil {
				return err
			}
			return storeCheckMerchantStoreSchema(tx, 8)
		})
	})
}
