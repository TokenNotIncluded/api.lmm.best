package model

import (
	"fmt"
	"gorm.io/gorm"
)

// Older SQLite tables retain NOT NULL despite AutoMigrate. Upgrade the order
// link explicitly before accepting unpaid advances. Do not rebuild on each boot.
func migrateReferralOrderNullability(db *gorm.DB) error {
	if !db.Migrator().HasTable(&ReferralReward{}) {
		return nil
	}
	columns, err := db.Migrator().ColumnTypes(&ReferralReward{})
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "top_up_id" {
			continue
		}
		if nullable, known := column.Nullable(); known && nullable {
			return nil
		}
		return db.Transaction(func(tx *gorm.DB) error {
			// SQLite rebuilds the table; retain its indexes and triggers too.
			var definitions []string
			if tx.Dialector.Name() == "sqlite" {
				if err := tx.Raw("SELECT sql FROM sqlite_master WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL", "referral_rewards").Scan(&definitions).Error; err != nil {
					return err
				}
			}
			if err := tx.Migrator().AlterColumn(&ReferralReward{}, "TopUpId"); err != nil {
				return err
			}
			for _, definition := range definitions {
				if err := tx.Exec(definition).Error; err != nil {
					return err
				}
			}
			return nil
		})
	}
	return fmt.Errorf("referral rewards order column is missing")
}
