package model

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A cost belongs to one immutable promotion link and registration cohort.
// Different windows/currencies are separate comparisons, never additive totals.
type AcquisitionCost struct {
	ID              int64  `json:"id"`
	LinkID          string `json:"link_id" gorm:"size:32;uniqueIndex:idx_acquisition_cost_cohort,priority:1"`
	FromAt          int64  `json:"from" gorm:"uniqueIndex:idx_acquisition_cost_cohort,priority:2"`
	ToAt            int64  `json:"to" gorm:"uniqueIndex:idx_acquisition_cost_cohort,priority:3"`
	ObservationDays int    `json:"observation_days" gorm:"uniqueIndex:idx_acquisition_cost_cohort,priority:4"`
	Currency        string `json:"currency" gorm:"size:3;uniqueIndex:idx_acquisition_cost_cohort,priority:5"`
	AmountMicros    int64  `json:"amount_micros"`
	UpdatedBy       int    `json:"updated_by"`
	UpdatedAt       int64  `json:"updated_at"`
}
type AcquisitionCostScope struct {
	LinkID          string `json:"link_id"`
	FromAt          int64  `json:"from"`
	ToAt            int64  `json:"to"`
	ObservationDays int    `json:"observation_days"`
	Currency        string `json:"currency"`
}
type AcquisitionCostReport struct {
	Scope                     AcquisitionCostScope `json:"scope"`
	Spend                     *AcquisitionCost     `json:"spend"`
	Registrations             int64                `json:"registrations"`
	FirstPayingAccounts       int64                `json:"first_paying_accounts"`
	PaymentRowsUnclassified   int64                `json:"payment_rows_unclassified"`
	CostPerRegistrationMicros *float64             `json:"cost_per_registration_micros"`
	CostPerFirstPayerMicros   *float64             `json:"cost_per_first_payer_micros"`
	Observing                 bool                 `json:"observing"`
	ObservedUntil             int64                `json:"observed_until"`
	CompleteAfter             int64                `json:"complete_after"`
}

func validateAcquisitionCostScope(scope AcquisitionCostScope, now int64) error {
	if len(scope.LinkID) != 32 || scope.FromAt <= 0 || scope.ToAt <= scope.FromAt || scope.ToAt > now || scope.ToAt-scope.FromAt > 366*86400 || scope.FromAt < now-AcquisitionAccountDays*86400 || scope.ObservationDays < 1 || scope.ObservationDays > 90 || len(scope.Currency) != 3 {
		return ErrAcquisitionInvalid
	}
	for _, c := range scope.Currency {
		if c < 'A' || c > 'Z' {
			return ErrAcquisitionInvalid
		}
	}
	return nil
}
func acquisitionCostQuery(db *gorm.DB, scope AcquisitionCostScope) *gorm.DB {
	return db.Where("link_id = ? AND from_at = ? AND to_at = ? AND observation_days = ? AND currency = ?", scope.LinkID, scope.FromAt, scope.ToAt, scope.ObservationDays, scope.Currency)
}
func SaveAcquisitionCost(ctx context.Context, scope AcquisitionCostScope, amount int64, actor int) (AcquisitionCost, error) {
	result := AcquisitionCost{}
	if err := validateAcquisitionCostScope(scope, time.Now().Unix()); err != nil {
		return result, err
	}
	if amount < 0 || amount > 1_000_000_000_000_000 || actor <= 0 {
		return result, ErrAcquisitionInvalid
	}
	if DB == nil {
		return result, gorm.ErrInvalidDB
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	db := DB.WithContext(ctx)
	var link AcquisitionLink
	if err := db.First(&link, "id = ?", scope.LinkID).Error; err != nil {
		return result, err
	}
	result = AcquisitionCost{LinkID: scope.LinkID, FromAt: scope.FromAt, ToAt: scope.ToAt, ObservationDays: scope.ObservationDays, Currency: scope.Currency, AmountMicros: amount, UpdatedBy: actor, UpdatedAt: time.Now().Unix()}
	err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "link_id"}, {Name: "from_at"}, {Name: "to_at"}, {Name: "observation_days"}, {Name: "currency"}}, DoUpdates: clause.AssignmentColumns([]string{"amount_micros", "updated_by", "updated_at"})}).Create(&result).Error
	if err == nil {
		err = acquisitionCostQuery(db, scope).First(&result).Error
	}
	return result, err
}
func GetAcquisitionCostReport(ctx context.Context, scope AcquisitionCostScope) (AcquisitionCostReport, error) {
	now := time.Now().Unix()
	result := AcquisitionCostReport{Scope: scope, ObservedUntil: now, CompleteAfter: scope.ToAt + int64(scope.ObservationDays)*86400}
	result.Observing = result.CompleteAfter > now
	if err := validateAcquisitionCostScope(scope, now); err != nil {
		return result, err
	}
	if DB == nil {
		return result, gorm.ErrInvalidDB
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	db := DB.WithContext(ctx)
	var link AcquisitionLink
	if err := db.First(&link, "id = ?", scope.LinkID).Error; err != nil {
		return result, err
	}
	var spend AcquisitionCost
	err := acquisitionCostQuery(db, scope).First(&spend).Error
	if err == nil {
		result.Spend = &spend
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	// Only explicitly attributed registrations in this link's retained cohort.
	cohort := func() *gorm.DB {
		return db.Table("users AS u").Joins("JOIN acquisition_accounts AS a ON a.user_id = u.id").Where("a.registration_link_id = ? AND u.created_at >= ? AND u.created_at < ? AND a.created_at >= ? AND u.role < ? AND u.deleted_at IS NULL", scope.LinkID, scope.FromAt, scope.ToAt, now-AcquisitionAccountDays*86400, common.RoleAdminUser)
	}
	if err = cohort().Count(&result.Registrations).Error; err != nil {
		return result, err
	}
	// Operational settlements, not API usage or gifted balances. Parent orders
	// replace top-up mirrors, and cycle receipts replace subscription orders.
	events := db.Raw(`SELECT t.user_id, t.settled_amount_micros AS amount, t.payment_method AS method, t.payment_provider AS provider, t.settlement_currency AS currency, t.complete_time AS occurred_at
 FROM top_ups t WHERE t.status = ? AND (t.complete_time >= ? OR t.complete_time = 0) AND NOT EXISTS (SELECT 1 FROM subscription_orders s WHERE s.trade_no=t.trade_no AND s.status=?)
 UNION ALL SELECT s.user_id, s.expected_amount_micros, s.payment_method, s.payment_provider, s.settlement_currency, s.complete_time FROM subscription_orders s WHERE s.status=? AND (s.complete_time>=? OR s.complete_time=0) AND NOT EXISTS(SELECT 1 FROM subscription_payment_events e WHERE e.subscription_order_id=s.id)
 UNION ALL SELECT s.user_id,e.settlement_amount_micros,s.payment_method,e.payment_provider,e.settlement_currency,e.created_time FROM subscription_payment_events e JOIN subscription_orders s ON s.id=e.subscription_order_id WHERE s.status=? AND e.created_time>=?`, common.TopUpStatusSuccess, scope.FromAt, common.TopUpStatusSuccess, common.TopUpStatusSuccess, scope.FromAt, common.TopUpStatusSuccess, scope.FromAt)
	var payments []struct {
		UserID       int
		Amount       int64
		Method       string
		Provider     string
		Currency     string
		OccurredAt   int64
		RegisteredAt int64
	}
	err = cohort().Joins("JOIN (?) AS p ON p.user_id=u.id AND (p.occurred_at = 0 OR (p.occurred_at>=u.created_at AND p.occurred_at <= ? AND p.occurred_at < u.created_at + ?))", events, now, int64(scope.ObservationDays)*86400).Select("p.*,u.created_at AS registered_at").Limit(10001).Scan(&payments).Error
	if err != nil {
		return result, err
	}
	if len(payments) > 10000 {
		return result, ErrAcquisitionInvalid
	}
	payers := map[int]bool{}
	for _, p := range payments {
		if strings.TrimSpace(p.Method) == "" && strings.TrimSpace(p.Provider) == "" {
			result.PaymentRowsUnclassified++
			continue
		}
		if !IsFinancialPaymentSource(p.Method, p.Provider) {
			continue
		}
		if p.Amount <= 0 || len(p.Currency) != 3 || p.OccurredAt <= 0 {
			result.PaymentRowsUnclassified++
			continue
		}
		payers[p.UserID] = true
	}
	result.FirstPayingAccounts = int64(len(payers))
	if result.Spend != nil && result.Registrations > 0 {
		v := float64(result.Spend.AmountMicros) / float64(result.Registrations)
		result.CostPerRegistrationMicros = &v
	}
	if result.Spend != nil && result.FirstPayingAccounts > 0 && !result.Observing && result.PaymentRowsUnclassified == 0 {
		v := float64(result.Spend.AmountMicros) / float64(result.FirstPayingAccounts)
		result.CostPerFirstPayerMicros = &v
	}
	return result, nil
}
