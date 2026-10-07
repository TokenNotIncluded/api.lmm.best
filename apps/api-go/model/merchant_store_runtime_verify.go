// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.

package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// Verification follows the durable writer floor, not the newest binary's
// optional catalogue. Explicit phase-five preparation installs the complete
// frozen catalogue before activation, including while the writer floor is one.
// Application migrations retain their original, independent model registry.
func runtimeVerificationModels(db *gorm.DB) ([]interface{}, error) {
	if db == nil {
		return nil, fmt.Errorf("verify merchant-store writer floor: %w", ErrMerchantStoreWriterFrozen)
	}
	// Standalone apply creates the latest catalogue without activating shop
	// writers. A missing gate therefore requires the complete catalogue; it
	// must never imply an older floor or permission to write.
	var option Option
	err := db.Session(&gorm.Session{NewDB: true}).Where("key = ?", MerchantStoreWriterCapabilityOption).First(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return append(mainMigrationModels(), &SubscriptionPlan{}), nil
	}
	if err != nil {
		return nil, fmt.Errorf("verify merchant-store writer floor: %w", err)
	}
	floor, err := storeWriterGateRow(db, "")
	if err != nil {
		return nil, fmt.Errorf("verify merchant-store writer floor: %w", err)
	}
	models := mainMigrationModels()
	if floor < 6 {
		frozen := make([]interface{}, 0, len(models))
		for _, item := range models {
			stmt := &gorm.Statement{DB: db}
			if err := stmt.Parse(item); err != nil {
				return nil, fmt.Errorf("verify migration model: %w", err)
			}
			if storePhaseSixTable(stmt.Schema.Table) {
				continue
			}
			if stmt.Schema.Table == "merchant_store_products" {
				item = &merchantStoreProductSchemaFive{}
			}
			frozen = append(frozen, item)
		}
		models = frozen
	}
	return append(models, &SubscriptionPlan{}), nil
}
