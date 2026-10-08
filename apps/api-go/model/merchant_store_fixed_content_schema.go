package model

import "gorm.io/gorm"

func storePhaseSevenModels() []interface{} {
	return append(storeFixedContentModels(), storeAnalyticsModels()...)
}

func storePhaseSevenTable(table string) bool {
	return storeFixedContentTable(table) || table == "merchant_store_product_traffic_days" || table == "merchant_store_product_traffic_receipts"
}

// The signed phase-six catalogue remains unchanged. This explicit shop-only
// preparation adds two private payload and two traffic tables without raising
// the writer floor. All four additions belong to the same phase-seven catalogue.
func PrepareMerchantStoreFixedContent(db *gorm.DB, expected int) error {
	if db == nil || MerchantStoreWriterCapability < 7 || (expected != 6 && expected != 7) || (db.Dialector.Name() != "postgres" && db.Dialector.Name() != "sqlite") {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			floor, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil || floor != expected {
				return ErrMerchantStoreWriterFrozen
			}
			if err := storeCheckPhaseSixSchema(tx); err != nil {
				return err
			}
			if err := tx.AutoMigrate(storePhaseSevenModels()...); err != nil {
				return err
			}
			return storeCheckMerchantStoreSchema(tx, 7)
		})
	})
}

// Activation verifies installed schema then performs a compare-and-swap only.
// Old writers must be retired before the operator makes this explicit action.
func ActivateMerchantStoreFixedContent(db *gorm.DB, expected int) error {
	if db == nil || MerchantStoreWriterCapability < 7 || (expected != 6 && expected != 7) {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			floor, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil || (floor != 7 && floor != expected) || floor < 6 {
				return ErrMerchantStoreWriterFrozen
			}
			if err := storeCheckMerchantStoreSchema(tx, 7); err != nil {
				return err
			}
			if floor == 7 {
				return nil
			}
			result := tx.Model(&Option{}).Where("key = ? AND value = ?", MerchantStoreWriterCapabilityOption, "6").Update("value", "7")
			if result.Error != nil || result.RowsAffected != 1 {
				return ErrMerchantStoreWriterFrozen
			}
			return nil
		})
	})
}

// Verification is read-only and can qualify installation before activation.
func VerifyMerchantStoreFixedContent(db *gorm.DB) error {
	if db == nil || MerchantStoreWriterCapability < 7 {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			floor, err := storeWriterGateRow(tx, "SHARE")
			if err != nil || (floor != 6 && floor != 7) {
				return ErrMerchantStoreWriterFrozen
			}
			if err := storeCheckPhaseSixSchema(tx); err != nil {
				return err
			}
			return storeCheckMerchantStoreSchema(tx, 7)
		})
	})
}
