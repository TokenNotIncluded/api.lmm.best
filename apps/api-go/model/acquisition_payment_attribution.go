package model

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AcquisitionAttributionPolicy struct {
	ID           int64 `gorm:"primaryKey"`
	EffectiveAt  int64 `gorm:"not null;index"`
	LookbackDays int   `gorm:"not null"`
}
type AcquisitionFirstPayment struct {
	UserID            int    `gorm:"primaryKey;autoIncrement:false"`
	FirstPaidAt       int64  `gorm:"index"`
	Source            string `gorm:"type:varchar(80)"`
	Evidence          string `gorm:"type:varchar(40)"`
	LinkID            string `gorm:"type:varchar(32)"`
	Campaign          string `gorm:"type:varchar(80)"`
	Medium            string `gorm:"type:varchar(80)"`
	Content           string `gorm:"type:varchar(80)"`
	ReferrerHost      string `gorm:"type:varchar(253)"`
	VisitID           int64
	ObservedAt        int64
	Inferred          bool
	LookbackDays      int
	PolicyEffectiveAt int64
	Rule              string `gorm:"type:varchar(48)"`
	HistoryConflictAt int64  `json:"history_conflict_at"`
	ConflictingPaidAt int64  `json:"conflicting_paid_at"`
	SnapshottedAt     int64  `gorm:"index"`
}
type acquisitionCashEvent struct {
	UserID     int
	Paid       int64
	Currency   string
	Method     string
	Provider   string
	OccurredAt int64
	CreatedAt  int64
	EventID    int64
	Kind       string
}

// Identical financial evidence to the cash report: mirrors and parent orders
// with immutable cycle events are excluded. This never reads gift/API usage as cash.
func acquisitionSuccessfulPayments(db *gorm.DB, from, until int64, userIDs []int) ([]acquisitionCashEvent, error) {
	var rows []acquisitionCashEvent
	base := db.Raw(`
 SELECT t.user_id, t.settled_amount_micros AS paid,
 t.settlement_currency AS currency, t.payment_method AS method, t.payment_provider AS provider,
 t.complete_time AS occurred_at, t.create_time AS created_at, t.id AS event_id, 'topup' AS kind
 FROM top_ups t WHERE t.status = ? AND NOT EXISTS (SELECT 1 FROM subscription_orders s WHERE s.trade_no = t.trade_no AND s.status = ?)
 UNION ALL
 SELECT s.user_id, 0, s.settlement_currency, s.payment_method, s.payment_provider,
 s.complete_time, s.create_time, s.id, 'subscription'
 FROM subscription_orders s WHERE s.status = ? AND NOT EXISTS (SELECT 1 FROM subscription_payment_events e WHERE e.subscription_order_id = s.id)
 UNION ALL
 SELECT s.user_id, e.settlement_amount_micros, e.settlement_currency, s.payment_method, e.payment_provider, e.created_time, e.created_time, e.id, 'cycle'
 FROM subscription_payment_events e JOIN subscription_orders s ON s.id = e.subscription_order_id WHERE s.status = ?
 `, common.TopUpStatusSuccess, common.TopUpStatusSuccess, common.TopUpStatusSuccess, common.TopUpStatusSuccess)
	err := db.Table("(?) AS cash", base).Where("user_id IN ?", userIDs).Where("(occurred_at >= ? AND occurred_at <= ?) OR (occurred_at <= 0 AND created_at <= ?)", from, until, until).Order("occurred_at, kind, event_id").Limit(50001).Scan(&rows).Error
	if len(rows) > 50000 {
		return nil, fmt.Errorf("acquisition payment observation limit exceeded; narrow the cohort")
	}
	return rows, err
}

func acquisitionPaymentClass(row acquisitionCashEvent) (cash, unknown bool) {
	if strings.TrimSpace(row.Method) == "" && strings.TrimSpace(row.Provider) == "" {
		return false, true
	}
	if !IsFinancialPaymentSource(row.Method, row.Provider) {
		return false, false
	}
	if row.Paid <= 0 || row.OccurredAt <= 0 {
		return false, true
	}
	return true, false
}

func ensureAcquisitionPolicy(db *gorm.DB, _ AcquisitionConfig, now int64) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var config AcquisitionConfig
		if err := lockForUpdate(tx).First(&config, 1).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&AcquisitionAttributionPolicy{}).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		// No retrospective policy is invented for pre-rollout payments.
		return tx.Create(&AcquisitionAttributionPolicy{EffectiveAt: now, LookbackDays: config.LookbackDays}).Error
	})
}

// ReconcileAcquisitionFirstPayments runs off the payment path. Snapshot inserts
// share the consent/account lock with withdrawal, and never revise old attribution.
func ReconcileAcquisitionFirstPayments(ctx context.Context, now int64) error {
	if DB == nil {
		return gorm.ErrInvalidDB
	}
	db := DB.WithContext(ctx)
	config, err := acquisitionConfig(db)
	if err != nil {
		return err
	}
	if err := ensureAcquisitionPolicy(db, config, now); err != nil {
		return err
	}
	var pending []int
	if err := db.Table("acquisition_accounts a").Joins("JOIN acquisition_consents c ON c.user_id = a.user_id AND c.allowed = ? AND c.version >= 2", true).Where("a.consent_version >= 2 AND a.created_at >= ?", now-AcquisitionAccountDays*86400).Pluck("a.user_id", &pending).Error; err != nil {
		return err
	}
	events, err := acquisitionSuccessfulPayments(db, now-AcquisitionAccountDays*86400, now, pending)
	if err != nil {
		return err
	}
	first := map[int]acquisitionCashEvent{}
	ambiguous := map[int]bool{}
	for _, event := range events {
		isCash, missing := acquisitionPaymentClass(event)
		if _, ok := first[event.UserID]; ok {
			continue
		}
		_ = missing
		if isCash {
			first[event.UserID] = event
		}
	}
	for _, event := range events {
		_, missing := acquisitionPaymentClass(event)
		if known, ok := first[event.UserID]; missing && ok && (event.CreatedAt <= 0 || event.CreatedAt <= known.OccurredAt) {
			ambiguous[event.UserID] = true
		}
	}
	ids := make([]int, 0, len(first))
	for id := range first {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	historyConflict := false
	for _, userID := range ids {
		event := first[userID]
		err := db.Transaction(func(tx *gorm.DB) error {
			var account AcquisitionAccount
			err := lockForUpdate(tx).Where("user_id = ? AND consent_version >= 2 AND created_at >= ?", userID, now-AcquisitionAccountDays*86400).First(&account).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			var consent AcquisitionConsent
			if err := tx.Where("user_id = ? AND allowed = ? AND version >= 2", userID, true).First(&consent).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			var user User
			if err := tx.Select("id", "created_at", "role").Where("id = ? AND role < ?", userID, common.RoleAdminUser).First(&user).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			var existing AcquisitionFirstPayment
			existingErr := tx.First(&existing, "user_id = ?", userID).Error
			if existingErr == nil {
				if existing.HistoryConflictAt > 0 {
					historyConflict = true
				}
				if existing.FirstPaidAt > 0 && (event.OccurredAt < existing.FirstPaidAt || ambiguous[userID]) {
					historyConflict = true
					return tx.Model(&AcquisitionFirstPayment{}).Where("user_id = ? AND history_conflict_at = 0", userID).Updates(map[string]any{"history_conflict_at": now, "conflicting_paid_at": event.OccurredAt}).Error
				}
				return nil
			}
			if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
				return existingErr
			}
			snapshot := AcquisitionFirstPayment{UserID: userID, FirstPaidAt: event.OccurredAt, Source: "unknown", Evidence: "unavailable", Rule: "last_external_before_payment_v1", SnapshottedAt: now}
			var policy AcquisitionAttributionPolicy
			err = tx.Where("effective_at <= ?", event.OccurredAt).Order("effective_at DESC, id DESC").First(&policy).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			switch {
			case ambiguous[userID]:
				snapshot.Evidence = "payment_history_incomplete"
				snapshot.FirstPaidAt = 0
			case user.CreatedAt < now-AcquisitionAccountDays*86400 || event.OccurredAt < user.CreatedAt:
				snapshot.Evidence = "historical_unrecorded"
				snapshot.FirstPaidAt = 0
			case err != nil:
				snapshot.Evidence = "rule_unrecorded"
			default:
				snapshot.LookbackDays, snapshot.PolicyEffectiveAt = policy.LookbackDays, policy.EffectiveAt
				lower := event.OccurredAt - int64(policy.LookbackDays)*86400
				if lower < now-AcquisitionRawDays*86400 {
					snapshot.Evidence = "source_history_expired"
					break
				}
				var visits []AcquisitionVisit
				if err := tx.Model(&AcquisitionVisit{}).Joins("JOIN acquisition_visitors v ON v.id = acquisition_visits.visitor_id").Where("v.user_id = ? AND acquisition_visits.consent_version >= 2 AND acquisition_visits.created_at >= ? AND acquisition_visits.created_at <= ?", userID, lower, event.OccurredAt).Order("acquisition_visits.created_at DESC, acquisition_visits.id DESC").Limit(1001).Find(&visits).Error; err != nil {
					return err
				}
				if len(visits) > 1000 {
					snapshot.Evidence = "source_observation_limit"
					break
				}
				for _, visit := range visits {
					if visit.Source == "" || visit.Source == "unknown" || visit.Source == "historical_unrecorded" {
						continue
					}
					snapshot.Source, snapshot.Evidence, snapshot.LinkID, snapshot.Campaign = visit.Source, visit.Evidence, visit.LinkID, visit.Campaign
					snapshot.VisitID, snapshot.ObservedAt, snapshot.Inferred = visit.ID, visit.CreatedAt, true
					snapshot.Medium, snapshot.Content, snapshot.ReferrerHost = visit.Medium, visit.Content, visit.ReferrerHost
					break
				}
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&snapshot).Error
		})
		if err != nil {
			return err
		}
	}
	status := "ready"
	if historyConflict {
		status = "history_conflict"
	}
	return db.Model(&AcquisitionConfig{}).Where("id = 1").Updates(map[string]any{"payment_snapshot_updated_at": now, "payment_snapshot_status": status}).Error
}
