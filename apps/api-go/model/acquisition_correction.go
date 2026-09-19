package model

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrAcquisitionCorrectionConflict = errors.New("source correction changed; reload before saving")

type AcquisitionCorrectionHead struct {
	UserID    int    `json:"user_id" gorm:"primaryKey"`
	Revision  int64  `json:"revision"`
	Source    string `json:"source" gorm:"size:80"`
	UpdatedAt int64  `json:"updated_at" gorm:"index"`
}
type AcquisitionCorrection struct {
	ID               int64  `json:"id"`
	UserID           int    `json:"user_id" gorm:"index"`
	PreviousRevision int64  `json:"previous_revision"`
	PreviousSource   string `json:"previous_source" gorm:"size:253"`
	Source           string `json:"source" gorm:"size:80"`
	Reason           string `json:"reason" gorm:"size:300"`
	ActorID          int    `json:"actor_id"`
	CreatedAt        int64  `json:"created_at" gorm:"index"`
}

func (*AcquisitionCorrection) BeforeUpdate(*gorm.DB) error {
	return errors.New("source correction audit is append-only")
}
func lockAcquisitionCorrection(tx *gorm.DB, userID int) (AcquisitionCorrectionHead, error) {
	head := AcquisitionCorrectionHead{UserID: userID}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&head).Error; err != nil {
		return head, err
	}
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&head, "user_id = ?", userID).Error
	return head, err
}
func SaveAcquisitionCorrection(ctx context.Context, userID, actorID int, expectedRevision int64, source, reason string) (AcquisitionCorrection, error) {
	result := AcquisitionCorrection{}
	reason = strings.TrimSpace(reason)
	if userID <= 0 || actorID <= 0 || expectedRevision < 0 || AcquisitionLabel(source) != source || source == "" || utf8.RuneCountInString(reason) < 3 || utf8.RuneCountInString(reason) > 300 || strings.ContainsAny(reason, "\r\n\x00@") || acquisitionSelfReportSecret.MatchString(reason) {
		return result, ErrAcquisitionInvalid
	}
	if DB == nil {
		return result, gorm.ErrInvalidDB
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Select("id", "role", "created_at").First(&user, userID).Error; err != nil {
			return err
		}
		if user.Role >= common.RoleAdminUser {
			return ErrAcquisitionInvalid
		}
		head, err := lockAcquisitionCorrection(tx, userID)
		if err != nil {
			return err
		}
		var consent AcquisitionConsent
		err = tx.First(&consent, "user_id = ?", userID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && !consent.Allowed {
			return ErrAcquisitionInvalid
		}
		if head.Revision != expectedRevision {
			return ErrAcquisitionCorrectionConflict
		}
		previous := head.Source
		if head.Revision == 0 {
			previous = "unknown"
			var account AcquisitionAccount
			err = tx.Where("user_id = ? AND created_at >= ?", userID, time.Now().Unix()-AcquisitionAccountDays*86400).First(&account).Error
			if err == nil {
				previous = account.RegistrationSource
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			} else {
				var config AcquisitionConfig
				err = tx.First(&config, 1).Error
				if err == nil && user.CreatedAt < config.StartedAt {
					previous = "historical_unrecorded"
				} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
		}
		result = AcquisitionCorrection{UserID: userID, PreviousRevision: head.Revision, PreviousSource: previous, Source: source, Reason: reason, ActorID: actorID, CreatedAt: time.Now().Unix()}
		if err = tx.Create(&result).Error; err != nil {
			return err
		}
		return tx.Model(&head).Updates(map[string]any{"revision": result.ID, "source": source, "updated_at": result.CreatedAt}).Error
	})
	return result, err
}

type AcquisitionCorrections struct {
	Head    *AcquisitionCorrectionHead `json:"head"`
	Items   []AcquisitionCorrection    `json:"items"`
	HasMore bool                       `json:"has_more"`
}

func ReadAcquisitionCorrections(ctx context.Context, userID int) (AcquisitionCorrections, error) {
	result := AcquisitionCorrections{Items: []AcquisitionCorrection{}}
	if DB == nil {
		return result, gorm.ErrInvalidDB
	}
	db := DB.WithContext(ctx)
	var head AcquisitionCorrectionHead
	err := db.Where("user_id = ? AND revision > 0 AND updated_at >= ?", userID, time.Now().Unix()-AcquisitionAccountDays*86400).First(&head).Error
	if err == nil {
		result.Head = &head
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	err = db.Where("user_id = ? AND created_at >= ?", userID, time.Now().Unix()-AcquisitionAccountDays*86400).Order("id DESC").Limit(101).Find(&result.Items).Error
	if len(result.Items) > 100 {
		result.HasMore = true
		result.Items = result.Items[:100]
	}
	return result, err
}
func PurgeAcquisitionCorrections(ctx context.Context) error {
	db := DB.WithContext(ctx)
	cutoff := time.Now().Unix() - AcquisitionAccountDays*86400
	if err := db.Where("created_at < ? OR user_id NOT IN (SELECT id FROM users WHERE deleted_at IS NULL)", cutoff).Delete(&AcquisitionCorrection{}).Error; err != nil {
		return err
	}
	return db.Where("updated_at < ? OR user_id NOT IN (SELECT id FROM users WHERE deleted_at IS NULL)", cutoff).Delete(&AcquisitionCorrectionHead{}).Error
}
