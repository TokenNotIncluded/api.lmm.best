package model

import (
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type AcquisitionConfig struct {
	ID                       int    `json:"id" gorm:"primaryKey"`
	StartedAt                int64  `json:"started_at"`
	LookbackDays             int    `json:"lookback_days"`
	LastCleanupAt            int64  `json:"last_cleanup_at"`
	PaymentSnapshotUpdatedAt int64  `json:"payment_snapshot_updated_at"`
	PaymentSnapshotStatus    string `json:"payment_snapshot_status" gorm:"type:varchar(32)"`
}

func acquisitionConfig(db *gorm.DB) (AcquisitionConfig, error) {
	value := AcquisitionConfig{ID: 1, StartedAt: time.Now().Unix(), LookbackDays: 30}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&value).Error; err != nil {
		return value, err
	}
	err := db.First(&value, 1).Error
	return value, err
}

// RunAcquisitionRetention is owned by the server runtime, never migration/verify commands.
func RunAcquisitionRetention(parent context.Context) {
	run := func() {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		if DB == nil {
			return
		}
		if _, err := acquisitionConfig(DB.WithContext(ctx)); err != nil {
			return
		}
		if PurgeAcquisition(ctx) == nil {
			_ = DB.WithContext(ctx).Model(&AcquisitionConfig{}).Where("id = 1").Update("last_cleanup_at", time.Now().Unix()).Error
		}
	}
	run()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-parent.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func SetAcquisitionLookback(ctx context.Context, days int) error {
	if days < 1 || days > AcquisitionRawDays {
		return ErrAcquisitionInvalid
	}
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	if _, err := acquisitionConfig(DB.WithContext(ctx)); err != nil {
		return err
	}
	// Existing account attributions keep their recorded rule and window.
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var config AcquisitionConfig
		if err := lockForUpdate(tx).First(&config, 1).Error; err != nil {
			return err
		}
		now := time.Now().Unix()
		if err := ensureAcquisitionPolicy(tx, config, now); err != nil {
			return err
		}
		if config.LookbackDays == days {
			return nil
		}
		if err := tx.Create(&AcquisitionAttributionPolicy{EffectiveAt: now, LookbackDays: days}).Error; err != nil {
			return err
		}
		return tx.Model(&AcquisitionConfig{}).Where("id = 1").Update("lookback_days", days).Error
	})
}
