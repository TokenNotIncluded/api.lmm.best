package model

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"sort"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

type AcquisitionChannel struct {
	Evidence                string `json:"evidence"`
	Source                  string `json:"source"`
	Registrations           int64  `json:"registrations"`
	IdentifiedRegistrations int64  `json:"identified_registrations"`
}
type AcquisitionPayment struct {
	Source         string `json:"source"`
	Currency       string `json:"currency"`
	PaidMicros     int64  `json:"paid_micros"`
	RefundMicros   int64  `json:"refund_micros"`
	NetMicros      int64  `json:"net_micros"`
	PayingAccounts int64  `json:"paying_accounts"`
}
type AcquisitionReport struct {
	AppliedLookbackDays     []int                        `json:"applied_lookback_days"`
	ActivityState           *AcquisitionActivityState    `json:"activity_state"`
	Activity                []AcquisitionActivitySummary `json:"activity"`
	StartedAt               int64                        `json:"started_at"`
	LookbackDays            int                          `json:"lookback_days"`
	Channels                []AcquisitionChannel         `json:"channels"`
	Payments                []AcquisitionPayment         `json:"payments"`
	From                    int64                        `json:"from"`
	To                      int64                        `json:"to"`
	ObservedUntil           int64                        `json:"observed_until"`
	RawRetentionDays        int                          `json:"raw_retention_days"`
	AccountRetentionDays    int                          `json:"account_retention_days"`
	Attribution             string                       `json:"attribution"`
	UnclassifiedPaymentRows int64                        `json:"unclassified_payment_rows"`
	Unavailable             []string                     `json:"unavailable"`
}

// Cash figures use successful topup/subscription settlements and refund records. Bonus quota and API consumption are never included as payments.
// This is a registration-cohort report: payment observations end at report time.
func GetAcquisitionReport(ctx context.Context, from, to int64) (AcquisitionReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	now := time.Now().Unix()
	result := AcquisitionReport{From: from, To: to, ObservedUntil: now, RawRetentionDays: AcquisitionRawDays, AccountRetentionDays: AcquisitionAccountDays, Attribution: "registration", Channels: []AcquisitionChannel{}, Payments: []AcquisitionPayment{}, Unavailable: []string{"successful_api_accounts", "retention", "first_payment_attribution", "visitor_estimates"}}
	if from <= 0 || to <= from || to-from > 366*86400 {
		return result, ErrAcquisitionInvalid
	}
	if DB == nil {
		return result, gorm.ErrInvalidDB
	}
	db := DB.WithContext(ctx)
	config, err := acquisitionConfig(db)
	if err != nil {
		return result, err
	}
	result.StartedAt = config.StartedAt
	result.LookbackDays = config.LookbackDays
	result.AppliedLookbackDays = []int{}
	if err := db.Table("acquisition_accounts AS a").Joins("JOIN users AS u ON u.id = a.user_id").Where("u.created_at >= ? AND u.created_at < ? AND a.lookback_days > 0 AND a.created_at >= ? AND u.role < ?", from, to, now-AcquisitionAccountDays*86400, common.RoleAdminUser).Distinct("a.lookback_days").Order("a.lookback_days").Pluck("a.lookback_days", &result.AppliedLookbackDays).Error; err != nil {
		return result, err
	}
	result.ActivityState, result.Activity, err = AcquisitionActivityReport(ctx, from, to)
	if err != nil {
		return result, err
	}
	if result.ActivityState != nil && result.ActivityState.Status == "ready" {
		result.Unavailable = []string{"first_payment_attribution", "visitor_estimates"}
		if result.ActivityState.Incomplete {
			result.Unavailable = append(result.Unavailable, "retention_coverage_gaps")
		}
	}
	sourceExpr := fmt.Sprintf("CASE WHEN a.registration_source IS NOT NULL THEN a.registration_source WHEN u.created_at < %d THEN 'historical_unrecorded' ELSE 'unknown' END", config.StartedAt)
	cohort := func() *gorm.DB {
		return db.Table("users AS u").Joins("LEFT JOIN acquisition_accounts AS a ON a.user_id = u.id AND a.created_at >= ?", now-AcquisitionAccountDays*86400).Where("u.created_at >= ? AND u.created_at < ? AND u.role < ?", from, to, common.RoleAdminUser)
	}
	evidenceExpr := "COALESCE(NULLIF(a.registration_evidence, ''), 'unavailable')"
	err = cohort().Select(sourceExpr + " AS source, " + evidenceExpr + " AS evidence, COUNT(*) AS registrations, SUM(CASE WHEN a.registration_source IS NOT NULL AND a.registration_source NOT IN ('unknown','historical_unrecorded','') THEN 1 ELSE 0 END) AS identified_registrations").Group(sourceExpr + ", " + evidenceExpr).Scan(&result.Channels).Error
	if err != nil {
		return result, err
	}

	// Match the financial dashboard: subscription orders replace top-up mirrors,
	// immutable cycle events replace their parent order, and refunds are separate.
	events := db.Raw(`
 SELECT t.user_id, CASE WHEN t.settled_amount_micros > 0 THEN t.settled_amount_micros ELSE t.expected_amount_micros END AS paid,
  0 AS refunded, t.settlement_currency AS currency, t.payment_method AS method, t.payment_provider AS provider,
  CASE WHEN t.complete_time > 0 THEN t.complete_time ELSE t.create_time END AS occurred_at
 FROM top_ups AS t WHERE t.status = ? AND (CASE WHEN t.complete_time > 0 THEN t.complete_time ELSE t.create_time END) BETWEEN ? AND ? AND NOT EXISTS (SELECT 1 FROM subscription_orders AS s WHERE s.trade_no = t.trade_no AND s.status = ?)
 UNION ALL
 SELECT s.user_id, s.expected_amount_micros, 0, s.settlement_currency, s.payment_method, s.payment_provider,
  CASE WHEN s.complete_time > 0 THEN s.complete_time ELSE s.create_time END
 FROM subscription_orders AS s WHERE s.status = ? AND (CASE WHEN s.complete_time > 0 THEN s.complete_time ELSE s.create_time END) BETWEEN ? AND ? AND NOT EXISTS (SELECT 1 FROM subscription_payment_events AS e WHERE e.subscription_order_id = s.id)
 UNION ALL
 SELECT s.user_id, e.settlement_amount_micros, 0, e.settlement_currency, s.payment_method, e.payment_provider, e.created_time
 FROM subscription_payment_events AS e JOIN subscription_orders AS s ON s.id = e.subscription_order_id WHERE s.status = ? AND e.created_time BETWEEN ? AND ?
 UNION ALL
 SELECT f.user_id, 0, -f.direction * f.amount_micros, f.currency, f.payment_method, f.payment_provider, f.occurred_at
 FROM finance_ledger_entries AS f WHERE f.entry_type = ? AND f.source_type = ? AND f.occurred_at BETWEEN ? AND ?
 `, common.TopUpStatusSuccess, from, now, common.TopUpStatusSuccess, common.TopUpStatusSuccess, from, now, common.TopUpStatusSuccess, from, now, FinanceEntryRevenue, FinanceSourceRefund, from, now)
	var cash []struct {
		Source   string
		Currency string
		Method   string
		Provider string
		UserID   int
		Paid     int64
		Refunded int64
		Records  int64
		Missing  int64
	}
	err = cohort().Joins("JOIN (?) AS f ON f.user_id = u.id AND f.occurred_at >= u.created_at AND f.occurred_at <= ?", events, now).
		Select(sourceExpr + " AS source, f.currency, f.method, f.provider, f.user_id, SUM(f.paid) AS paid, SUM(f.refunded) AS refunded, COUNT(*) AS records, SUM(CASE WHEN f.paid = 0 AND f.refunded = 0 THEN 1 ELSE 0 END) AS missing").
		Group(sourceExpr + ", f.currency, f.method, f.provider, f.user_id").Scan(&cash).Error
	if err != nil {
		return result, err
	}
	buckets := map[string]*AcquisitionPayment{}
	payers := map[string]map[int]bool{}
	for _, row := range cash {
		if strings.TrimSpace(row.Method) == "" && strings.TrimSpace(row.Provider) == "" {
			result.UnclassifiedPaymentRows += row.Records
			continue
		}
		if !IsFinancialPaymentSource(row.Method, row.Provider) {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(row.Currency))
		validCurrency := len(currency) == 3
		for _, letter := range currency {
			if letter < 'A' || letter > 'Z' {
				validCurrency = false
			}
		}
		if !validCurrency {
			result.UnclassifiedPaymentRows += row.Records
			continue
		}
		result.UnclassifiedPaymentRows += row.Missing
		if row.Paid == 0 && row.Refunded == 0 {
			continue
		}
		key := row.Source + ":" + currency
		if buckets[key] == nil {
			buckets[key] = &AcquisitionPayment{Source: row.Source, Currency: currency}
			payers[key] = map[int]bool{}
		}
		buckets[key].PaidMicros += row.Paid
		buckets[key].RefundMicros += row.Refunded
		if row.Paid > 0 {
			payers[key][row.UserID] = true
		}
	}
	for key, bucket := range buckets {
		bucket.NetMicros = bucket.PaidMicros - bucket.RefundMicros
		bucket.PayingAccounts = int64(len(payers[key]))
		result.Payments = append(result.Payments, *bucket)
	}
	sort.Slice(result.Payments, func(i, j int) bool {
		a, b := result.Payments[i], result.Payments[j]
		if a.Source == b.Source {
			return a.Currency < b.Currency
		}
		return a.Source < b.Source
	})
	if result.UnclassifiedPaymentRows > 0 {
		result.Unavailable = append(result.Unavailable, "unclassified_payment_records")
	}

	return result, err
}

func PurgeAcquisition(ctx context.Context) error {
	if err := DB.WithContext(ctx).Where("updated_at < ? OR user_id NOT IN (SELECT id FROM users WHERE deleted_at IS NULL)", time.Now().Unix()-AcquisitionAccountDays*86400).Delete(&AcquisitionSelfReport{}).Error; err != nil {
		return err
	}
	now := time.Now().Unix()
	db := DB.WithContext(ctx)
	if err := db.Where("created_at < ?", now-AcquisitionRawDays*86400).Delete(&AcquisitionVisit{}).Error; err != nil {
		return err
	}
	if err := db.Where("created_at < ? AND NOT EXISTS (SELECT 1 FROM acquisition_visits AS v WHERE v.visitor_id = acquisition_visitors.id)", now-AcquisitionRawDays*86400).Delete(&AcquisitionVisitor{}).Error; err != nil {
		return err
	}
	if err := db.Where("through_at < ?", now-AcquisitionAccountDays*86400).Delete(&AcquisitionActivityGap{}).Error; err != nil {
		return err
	}
	if err := db.Where("day < ?", now-AcquisitionAccountDays*86400).Delete(&AcquisitionActivity{}).Error; err != nil {
		return err
	}
	if err := db.Where("user_id NOT IN (SELECT id FROM users WHERE deleted_at IS NULL)").Delete(&AcquisitionConsent{}).Error; err != nil {
		return err
	}
	return db.Where("created_at < ?", now-AcquisitionAccountDays*86400).Delete(&AcquisitionAccount{}).Error
}
