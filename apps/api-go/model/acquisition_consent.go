package model

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This is a privacy preference, retained until changed or the account is
// removed. It prevents a stale browser preference from undoing withdrawal.
type AcquisitionConsent struct {
	UserID    int   `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	Allowed   bool  `json:"allowed"`
	Version   int   `json:"version"`
	UpdatedAt int64 `json:"updated_at"`
}

func setAcquisitionConsent(tx *gorm.DB, userID int, allowed bool) error {
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoUpdates: clause.AssignmentColumns([]string{"allowed", "version", "updated_at"})}).Create(&AcquisitionConsent{UserID: userID, Allowed: allowed, Version: 2, UpdatedAt: time.Now().Unix()}).Error
}
func GrantAcquisitionConsent(ctx context.Context, userID int) error {
	if userID <= 0 || DB == nil {
		return gorm.ErrInvalidData
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Select("id", "created_at").First(&user, userID).Error; err != nil {
			return err
		}
		config, err := acquisitionConfig(tx)
		if err != nil {
			return err
		}
		source := "unknown"
		if user.CreatedAt < config.StartedAt {
			source = "historical_unrecorded"
		}
		account := AcquisitionAccount{UserID: userID, ConsentVersion: 2, RegistrationAt: user.CreatedAt, RegistrationSource: source, RegistrationEvidence: "unavailable", FirstSource: source, FirstEvidence: "unavailable", CreatedAt: time.Now().Unix(), LookbackDays: config.LookbackDays}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoUpdates: clause.Assignments(map[string]any{"consent_version": 2})}).Create(&account).Error; err != nil {
			return err
		}
		if err := setAcquisitionConsent(tx, userID, true); err != nil {
			return err
		}
		// Self-service consent must not rewind the shared observer. Historical
		// replay is a separately authorized administrator operation.
		return nil
	})
}

func revokeAcquisitionAccountTx(tx *gorm.DB, userID int) error {
	// All consent mutations lock the analytics account before the preference;
	// no lock is taken on the billing/authentication user row.
	stub := AcquisitionAccount{UserID: userID, RegistrationSource: "unknown", CreatedAt: time.Now().Unix()}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&stub).Error; err != nil {
		return err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("user_id").First(&stub, "user_id = ?", userID).Error; err != nil {
		return err
	}
	if err := setAcquisitionConsent(tx, userID, false); err != nil {
		return err
	}
	if err := tx.Where("user_id = ?", userID).Delete(&AcquisitionActivity{}).Error; err != nil {
		return err
	}
	return tx.Where("user_id = ?", userID).Delete(&AcquisitionAccount{}).Error
}
