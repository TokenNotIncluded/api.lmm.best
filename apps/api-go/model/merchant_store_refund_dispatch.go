package model

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// One immutable operation per approved refund. SubmitCount is a one-way fence:
// once it is 1, a crashed or timed-out worker may only query/reconcile, never
// issue another money-moving request. Credentials and raw provider bodies are
// deliberately not stored here.
type MerchantStoreRefundProviderAttempt struct {
	RefundID        string `json:"-" gorm:"primaryKey;size:64"`
	OrderID         string `json:"-" gorm:"size:64;not null;index"`
	RequestHash     string `json:"-" gorm:"size:64;not null"`
	State           string `json:"-" gorm:"size:32;not null;index"`
	Code            string `json:"-" gorm:"size:64;not null"`
	SubmitCount     int    `json:"-" gorm:"not null"`
	TicketReference string `json:"-" gorm:"size:128"`
	RefundReference string `json:"-" gorm:"size:128"`
	EvidenceHash    string `json:"-" gorm:"size:64"`
	LeaseToken      string `json:"-" gorm:"size:64"`
	LeaseUntil      int64  `json:"-" gorm:"type:bigint;not null"`
	NextCheckAt     int64  `json:"-" gorm:"type:bigint;not null;index"`
	CreatedAt       int64  `json:"-"`
	UpdatedAt       int64  `json:"-"`
}

type MerchantStoreRefundDispatch struct {
	Order   MerchantStoreOrder                 `json:"-"`
	Refund  MerchantStoreRefund                `json:"-"`
	Basis   MerchantStoreRefundPaymentBasis    `json:"-"`
	Attempt MerchantStoreRefundProviderAttempt `json:"-"`
}

func storeRefundDispatchHash(o *MerchantStoreOrder, r *MerchantStoreRefund, b *MerchantStoreRefundPaymentBasis) string {
	values := []any{"merchant-store-refund-dispatch-v1", o.ID, r.ID, o.PaymentMethod, o.PaymentScopeHash, storeHash(o.GatewaySnapshot), o.BuyerID, o.TradeNo, r.Mode, r.AmountMinor, r.Currency, storeHash(r.Reason), b.ReceiptReference, b.PaymentReference, b.AmountMinor, b.Currency, b.EvidenceHash}
	// Preserve every old member request digest; only guest operations bind the
	// additional immutable subject, never the generic buyer_id=0.
	if o.GuestID != "" {
		values = append(values, []string{"guest", o.GuestID})
	}
	data, _ := json.Marshal(values)
	return storeHash(string(data))
}

func storeRefundDispatchTx(id string, fn func(*gorm.DB, *MerchantStoreOrder, *MerchantStoreRefund, *MerchantStoreRefundPaymentBasis) error) error {
	if len(id) != 64 {
		return ErrMerchantStoreInput
	}
	var lookup MerchantStoreRefund
	if e := DB.Select("order_id").First(&lookup, "id = ?", id).Error; e != nil {
		return e
	}
	return storeOrderTx(lookup.OrderID, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if e := MerchantStoreRefundRequiresWriter(tx); e != nil {
			return e
		}
		r, e := storeRefundLoad(tx, o, id)
		if e != nil {
			return e
		}
		b, e := storeRefundBasis(tx, o)
		if e != nil {
			return e
		}
		if b == nil || r.AmountMinor <= 0 || b.EvidenceHash == "" {
			return ErrMerchantStoreRefundUnsupported
		}
		if r.Status != "awaiting_provider" && r.Status != "reconciliation_required" && r.Status != "completed" && r.Status != "rejected" {
			return ErrMerchantStoreConflict
		}
		return fn(tx, o, r, b)
	})
}

// Prepare persists the operation and claims a short query-only lease. Retrying
// a ready operation is safe: ClaimSubmit below is the sole authority to POST.
func PrepareMerchantStoreRefundDispatch(id string, now int64) (*MerchantStoreRefundDispatch, error) {
	var result *MerchantStoreRefundDispatch
	e := storeRefundDispatchTx(id, func(tx *gorm.DB, o *MerchantStoreOrder, r *MerchantStoreRefund, b *MerchantStoreRefundPaymentBasis) error {
		var a MerchantStoreRefundProviderAttempt
		e := tx.First(&a, "refund_id = ?", id).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			if r.Status != "awaiting_provider" && r.Status != "reconciliation_required" {
				return ErrMerchantStoreConflict
			}
			a = MerchantStoreRefundProviderAttempt{RefundID: id, OrderID: o.ID, RequestHash: storeRefundDispatchHash(o, r, b), State: "ready", Code: "ready", CreatedAt: now, UpdatedAt: now}
			if r.Status == "reconciliation_required" {
				if r.ProviderRefundReference == nil || r.ProviderEvidenceHash == "" {
					return ErrMerchantStoreConflict
				}
				a.State, a.Code, a.SubmitCount, a.RefundReference, a.EvidenceHash = "succeeded", "local_reconciliation", 1, *r.ProviderRefundReference, r.ProviderEvidenceHash
			}
			if e = tx.Create(&a).Error; e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		if a.RequestHash != storeRefundDispatchHash(o, r, b) || a.OrderID != o.ID || a.SubmitCount < 0 || a.SubmitCount > 1 {
			return ErrMerchantStoreConflict
		}
		if a.LeaseUntil > now || a.NextCheckAt > now {
			return nil
		}
		if r.Status == "completed" || r.Status == "rejected" {
			return nil
		}
		a.LeaseToken, e = storeToken()
		if e != nil {
			return e
		}
		a.LeaseUntil = now + 90
		a.UpdatedAt = now
		if e = tx.Save(&a).Error; e != nil {
			return e
		}
		result = &MerchantStoreRefundDispatch{Order: *o, Refund: *r, Basis: *b, Attempt: a}
		return nil
	})
	return result, e
}

// ClaimSubmit commits unknown before the first money-moving POST. No reset,
// expiry or query result can lower SubmitCount. This also survives a crash
// between this commit and receiving/saving the provider response.
func ClaimMerchantStoreRefundSubmit(id, lease, hash string, now int64) (bool, error) {
	claimed := false
	e := storeRefundDispatchTx(id, func(tx *gorm.DB, o *MerchantStoreOrder, r *MerchantStoreRefund, b *MerchantStoreRefundPaymentBasis) error {
		var a MerchantStoreRefundProviderAttempt
		if e := tx.First(&a, "refund_id = ?", id).Error; e != nil {
			return e
		}
		if a.LeaseToken != lease || a.LeaseUntil <= now || a.RequestHash != hash || hash != storeRefundDispatchHash(o, r, b) {
			return ErrMerchantStoreConflict
		}
		if a.SubmitCount == 1 {
			return nil
		}
		if a.SubmitCount != 0 || a.State != "ready" || r.Status != "awaiting_provider" {
			return ErrMerchantStoreConflict
		}
		if e := tx.Model(&a).Updates(map[string]any{"submit_count": 1, "state": "unknown", "code": "submission_in_progress", "updated_at": now, "next_check_at": now + 30}).Error; e != nil {
			return e
		}
		claimed = true
		return storeEvent(tx, 0, o.ID, "refund_provider_dispatch")
	})
	return claimed, e
}

// Persist observations only through the private broker. Terminal evidence is
// saved before invoking the core's separately recoverable wallet reconciliation.
func RecordMerchantStoreRefundObservation(id, lease, hash, state, code, ticket, reference, evidence string, now int64) error {
	allowed := map[string]bool{"ready": true, "unknown": true, "refund_requested": true, "pending": true, "succeeded": true, "failed": true, "manual": true}
	if !allowed[state] || len(code) > 64 || len(ticket) > 128 || len(reference) > 128 || len(evidence) > 64 || strings.ContainsAny(code, "\r\n\x00") {
		return ErrMerchantStoreInput
	}
	if (state == "succeeded" || state == "failed") && (reference == "" || len(evidence) != 64) {
		return ErrMerchantStoreInput
	}
	return storeRefundDispatchTx(id, func(tx *gorm.DB, o *MerchantStoreOrder, r *MerchantStoreRefund, b *MerchantStoreRefundPaymentBasis) error {
		var a MerchantStoreRefundProviderAttempt
		if e := tx.First(&a, "refund_id = ?", id).Error; e != nil {
			return e
		}
		if a.LeaseToken != lease || a.RequestHash != hash || hash != storeRefundDispatchHash(o, r, b) {
			return ErrMerchantStoreConflict
		}
		if a.EvidenceHash != "" && (a.EvidenceHash != evidence || a.RefundReference != reference || a.State != state) {
			return ErrMerchantStoreConflict
		}
		if a.TicketReference != "" && ticket != "" && ticket != a.TicketReference {
			return ErrMerchantStoreConflict
		}
		if ticket == "" {
			ticket = a.TicketReference
		}
		next := now + 60
		if state == "manual" {
			next = now + 86400
		}
		return tx.Model(&a).Updates(map[string]any{"state": state, "code": code, "ticket_reference": ticket, "refund_reference": reference, "evidence_hash": evidence, "lease_until": 0, "lease_token": "", "next_check_at": next, "updated_at": now}).Error
	})
}

func DueMerchantStoreRefundDispatches(ctx context.Context, now int64, limit int) ([]string, error) {
	if ctx == nil || limit < 1 || limit > 20 {
		return nil, ErrMerchantStoreInput
	}
	ids := []string{}
	e := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := MerchantStoreRefundRequiresWriter(tx); e != nil {
			if errors.Is(e, ErrMerchantStoreWriterFrozen) {
				return nil
			}
			return e
		}
		return tx.Model(&MerchantStoreRefund{}).Select("merchant_store_refunds.id").Joins("JOIN merchant_store_refund_payment_bases AS basis ON basis.order_id = merchant_store_refunds.order_id").Joins("LEFT JOIN merchant_store_refund_provider_attempts AS attempt ON attempt.refund_id = merchant_store_refunds.id").Where("merchant_store_refunds.status IN ?", []string{"awaiting_provider", "reconciliation_required"}).Where("merchant_store_refunds.amount_minor > 0").Where("attempt.refund_id IS NULL OR (attempt.next_check_at <= ? AND attempt.lease_until <= ?)", now, now).Order("merchant_store_refunds.created_at ASC,merchant_store_refunds.id ASC").Limit(limit).Pluck("merchant_store_refunds.id", &ids).Error
	})
	return ids, e
}

// A signed notification may advance only the query schedule. It cannot grant a
// submit lease, clear the one-shot fence or settle/reject the refund itself.
func WakeMerchantStoreRefundDispatch(id string) error {
	return storeRefundDispatchTx(id, func(tx *gorm.DB, o *MerchantStoreOrder, r *MerchantStoreRefund, b *MerchantStoreRefundPaymentBasis) error {
		return tx.Model(&MerchantStoreRefundProviderAttempt{}).Where("refund_id = ?", id).Update("next_check_at", common.GetTimestamp()).Error
	})
}

// A verified refund notification already proves a provider operation may
// exist, even if no broker attempt was recorded (for example a merchant action
// at the provider). Persist the one-shot fence before scheduling a query.
func NoteMerchantStoreRefundNotification(id string) error {
	return storeRefundDispatchTx(id, func(tx *gorm.DB, o *MerchantStoreOrder, r *MerchantStoreRefund, b *MerchantStoreRefundPaymentBasis) error {
		now := common.GetTimestamp()
		var a MerchantStoreRefundProviderAttempt
		e := tx.First(&a, "refund_id = ?", id).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			a = MerchantStoreRefundProviderAttempt{RefundID: id, OrderID: o.ID, RequestHash: storeRefundDispatchHash(o, r, b), State: "unknown", Code: "verified_notification_query_required", SubmitCount: 1, NextCheckAt: now, CreatedAt: now, UpdatedAt: now}
			return tx.Create(&a).Error
		}
		if e != nil {
			return e
		}
		if a.RequestHash != storeRefundDispatchHash(o, r, b) {
			return ErrMerchantStoreConflict
		}
		return tx.Model(&a).Updates(map[string]any{"submit_count": 1, "next_check_at": now, "updated_at": now}).Error
	})
}

func GetMerchantStoreRefundDispatchSnapshot(id string) (*MerchantStoreRefundDispatch, error) {
	var d *MerchantStoreRefundDispatch
	e := storeRefundDispatchTx(id, func(tx *gorm.DB, o *MerchantStoreOrder, r *MerchantStoreRefund, b *MerchantStoreRefundPaymentBasis) error {
		d = &MerchantStoreRefundDispatch{Order: *o, Refund: *r, Basis: *b}
		e := tx.First(&d.Attempt, "refund_id = ?", id).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil
		}
		return e
	})
	return d, e
}

func AuthorizeMerchantStoreRefundReconciliation(actor int, orderID, id string) error {
	return storeOrderTx(orderID, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if _, e := storeRefundAccess(tx, o, actor, true); e != nil {
			return e
		}
		_, e := storeRefundLoad(tx, o, id)
		return e
	})
}
