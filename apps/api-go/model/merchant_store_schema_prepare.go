package model

import (
	"fmt"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// These are the exact signed access/catalogue/email additions. The stage-four
// qualifier excludes them so an upgraded runtime can serve the old schema
// before the separate reviewed installation and activation.
func storeAccessTable(table string) bool {
	switch table {
	case "merchant_store_guests", "merchant_store_seller_terms", "merchant_store_terms_acceptances", "merchant_store_catalogue_metadata", "merchant_store_cart_items", "merchant_store_favorites", "merchant_store_guest_email_verifications":
		return true
	}
	return false
}

func storeAccessColumn(table, column string) bool {
	switch table {
	case "merchant_store_products":
		return column == "visibility" || column == "purchase_login_required"
	case "merchant_store_orders":
		return column == "guest_id" || column == "seller_terms_version" || column == "seller_terms_content" || column == "seller_terms_accepted_at"
	}
	return false
}

// PrepareMerchantStoreSchema is an explicit private operator DDL action. It
// never calls the whole-application migration, modifies a wallet or raises the
// writer floor. A lost deployment session's durable owner still blocks it.
func PrepareMerchantStoreSchema(db *gorm.DB, expected int) error {
	if expected >= 5 {
		return PrepareMerchantStorePhaseSix(db, expected)
	}
	if db == nil || expected < 1 || expected > MerchantStoreWriterCapability || (db.Dialector.Name() != "postgres" && db.Dialector.Name() != "sqlite") {
		return ErrMerchantStoreWriterFrozen
	}
	models, err := storePhaseFivePreparationModels(db)
	if err != nil {
		return err
	}
	cache, seen := &sync.Map{}, map[string]bool{}
	for _, item := range models {
		parsed, err := schema.Parse(item, cache, db.NamingStrategy)
		if err != nil || !strings.HasPrefix(parsed.Table, "merchant_store_") || seen[parsed.Table] {
			return ErrMerchantStoreWriterFrozen
		}
		seen[parsed.Table] = true
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			required, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil || required != expected {
				return ErrMerchantStoreWriterFrozen
			}
			// Only the enumerated shop models may participate in this migration.
			// No root startup migration, user table, top-up migration or options
			// migration is called, and no missing gate is silently bootstrapped.
			if err := tx.AutoMigrate(models...); err != nil {
				return fmt.Errorf("%w: merchant schema preparation failed", ErrMerchantStoreWriterFrozen)
			}
			if err := BackfillMerchantStoreCatalogueMappings(tx); err != nil {
				return fmt.Errorf("%w: catalogue identity preparation failed", ErrMerchantStoreWriterFrozen)
			}
			if err := storeCheckMerchantStoreSchema(tx, 5); err != nil {
				return err
			}
			if err := storeCheckAccessDefaults(tx); err != nil {
				return err
			}
			current, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil || current != expected {
				return ErrMerchantStoreWriterFrozen
			}
			return nil
		})
	})
}

// ActivateMerchantStoreAccess performs no DDL: installation and serving-writer
// qualification must already be complete. Repeating it still checks the real
// schema and metadata mappings, never repairing damaged rows or lowering gates.
func ActivateMerchantStoreAccess(db *gorm.DB, expected int) error {
	if db == nil || MerchantStoreWriterCapability < 5 || (expected != 4 && expected != 5) {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			required, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil || (required != 5 && (required != 4 || required != expected)) {
				return ErrMerchantStoreWriterFrozen
			}
			if err := storeCheckMerchantStoreSchema(tx, 5); err != nil {
				return err
			}
			if err := storeCheckAccessDefaults(tx); err != nil {
				return err
			}
			var last string
			for {
				var products []MerchantStoreProduct
				if err := tx.Select("id").Where("id > ?", last).Order("id ASC").Limit(100).Find(&products).Error; err != nil {
					return ErrMerchantStoreWriterFrozen
				}
				if len(products) == 0 {
					break
				}
				for _, p := range products {
					var row MerchantStoreCatalogueMetadata
					if err := tx.First(&row, "product_id = ?", p.ID).Error; err != nil || row.DefaultVariantID != MerchantStoreDefaultVariantID(p.ID) {
						return fmt.Errorf("%w: catalogue identity is unprepared", ErrMerchantStoreWriterFrozen)
					}
					last = p.ID
				}
			}
			if required == 5 {
				return nil
			}
			result := tx.Model(&Option{}).Where("key = ? AND value = ?", MerchantStoreWriterCapabilityOption, "4").Update("value", "5")
			if result.Error != nil || result.RowsAffected != 1 {
				return ErrMerchantStoreWriterFrozen
			}
			return nil
		})
	})
}

func storeCheckAccessDefaults(tx *gorm.DB) error {
	for _, check := range []struct {
		model          any
		name, expected string
	}{
		{&MerchantStoreProduct{}, "visibility", ""},
		{&MerchantStoreProduct{}, "purchase_login_required", "true"},
		{&MerchantStoreOrder{}, "guest_id", ""},
		{&MerchantStoreOrder{}, "seller_terms_version", ""},
		{&MerchantStoreOrder{}, "seller_terms_content", ""},
		{&MerchantStoreOrder{}, "seller_terms_accepted_at", "0"},
	} {
		types, err := tx.Migrator().ColumnTypes(check.model)
		if err != nil {
			return ErrMerchantStoreWriterFrozen
		}
		valid := false
		for _, column := range types {
			if column.Name() != check.name {
				continue
			}
			value, known := column.DefaultValue()
			value = strings.Trim(strings.TrimSpace(strings.SplitN(value, "::", 2)[0]), "()'")
			if check.expected == "true" {
				value = strings.ReplaceAll(value, "1", "true")
			}
			valid = known && value == check.expected
		}
		if !valid {
			return fmt.Errorf("%w: invalid access default %s", ErrMerchantStoreWriterFrozen, check.name)
		}
	}
	return nil
}
