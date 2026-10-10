package model

import (
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// No new tables. Capability eight fences older writers before provider-origin
// receipts can use provider_review. Neither startup nor a webhook activates it.
var ErrMerchantStoreRefundReconciliation = errors.New("provider refund needs local reconciliation")

func storeRequireRefundSync(tx *gorm.DB) error {
	floor, err := storeWriterGateRow(tx, "SHARE")
	if err != nil || floor != 8 || MerchantStoreWriterCapability < 8 {
		return ErrMerchantStoreWriterFrozen
	}
	return nil
}

func ActivateMerchantStoreRefundSync(db *gorm.DB, expected int) error {
	if db == nil || (expected != 7 && expected != 8) || MerchantStoreWriterCapability < 8 {
		return ErrMerchantStoreWriterFrozen
	}
	return withMerchantStoreActivationDB(db, func(bound *gorm.DB) error {
		return bound.Transaction(func(tx *gorm.DB) error {
			floor, err := storeWriterGateRow(tx, "UPDATE")
			if err != nil || (floor != expected && floor != 8) || floor < 7 {
				return ErrMerchantStoreWriterFrozen
			}
			if err := storeCheckMerchantStoreSchema(tx, 7); err != nil {
				return err
			}
			if floor == 8 {
				return nil
			}
			result := tx.Model(&Option{}).Where("key = ? AND value = ?", MerchantStoreWriterCapabilityOption, "7").Update("value", "8")
			if result.Error != nil || result.RowsAffected != 1 {
				return ErrMerchantStoreWriterFrozen
			}
			return nil
		})
	})
}

func storeRefundExternalReview(tx *gorm.DB, orderID string) (bool, error) {
	floor, err := storeWriterGateRow(tx, "")
	if err != nil {
		return false, err
	}
	if floor < 8 {
		return false, nil
	}
	var count int64
	err = tx.Model(&MerchantStoreRefund{}).Where("order_id = ? AND status = ?", orderID, "provider_review").Count(&count).Error
	return count > 0, err
}

func storeRefundRequireNoExternalReview(tx *gorm.DB, orderID string) error {
	held, err := storeRefundExternalReview(tx, orderID)
	if err != nil {
		return err
	}
	if held {
		return ErrMerchantStoreRefundReconciliation
	}
	return nil
}

// Server-only snapshot. Browser callers must first pass the separate current
// seller/root check; signed callbacks instead prove the original provider scope.
func GetMerchantStoreRefundSyncSnapshot(orderID string) (*MerchantStoreRefundDispatch, []MerchantStoreRefund, error) {
	var snapshot *MerchantStoreRefundDispatch
	var rows []MerchantStoreRefund
	err := storeOrderTx(orderID, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if err := storeRequireRefundSync(tx); err != nil {
			return err
		}
		if !storeRefundPaid(o) || (o.PaymentMethod != "platform:waffo_pancake" && o.PaymentMethod != "external:waffo_pancake") {
			return ErrMerchantStoreRefundUnsupported
		}
		basis, err := storeRefundBasis(tx, o)
		if err != nil {
			return err
		}
		if basis == nil {
			return ErrMerchantStoreRefundUnsupported
		}
		snapshot = &MerchantStoreRefundDispatch{Order: *o, Basis: *basis}
		rows, err = storeRefundRows(tx, o)
		return err
	})
	return snapshot, rows, err
}

func AuthorizeMerchantStoreRefundSync(actor int, orderID string) error {
	return storeOrderTx(orderID, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		_, err := storeRefundAccess(tx, o, actor, true)
		return err
	})
}

// Record provider success before touching local balances. A stable execution ID,
// not a delivery ID or a ticket, owns one immutable native receipt. The pending
// review state also blocks new sends if an independent refund overlaps a local
// request already in flight. There is intentionally no public evidence input.
func RecordMerchantStoreExternalRefund(orderID string, in MerchantStoreVerifiedRefundEvidence) (string, error) {
	if in.RefundReference == "" || len(in.RefundReference) > 128 || strings.ContainsAny(in.RefundReference, "\x00\r\n") || in.AmountMinor <= 0 || len(in.EvidenceHash) != 64 {
		return "", ErrMerchantStoreInput
	}
	id := storeHash("provider-refund-v1:" + orderID + ":" + in.RefundReference)
	err := storeOrderTx(orderID, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if err := storeRequireRefundSync(tx); err != nil {
			return err
		}
		if !storeRefundPaid(o) || (o.PaymentMethod != "platform:waffo_pancake" && o.PaymentMethod != "external:waffo_pancake") {
			return ErrMerchantStoreRefundUnsupported
		}
		b, err := storeRefundBasis(tx, o)
		if err != nil {
			return err
		}
		if b == nil || in.PaymentReference != b.PaymentReference || in.Currency != b.Currency || in.AmountMinor > b.AmountMinor {
			return ErrMerchantStoreConflict
		}
		var old MerchantStoreRefund
		err = tx.First(&old, "id = ? OR provider_refund_reference = ?", id, in.RefundReference).Error
		if err == nil {
			if old.ID != id || old.OrderID != orderID || old.RequestedRole != "provider" || old.AmountMinor != in.AmountMinor || old.Currency != in.Currency || old.ProviderEvidenceHash != in.EvidenceHash || old.ProviderRefundReference == nil || *old.ProviderRefundReference != in.RefundReference {
				return ErrMerchantStoreConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		r := MerchantStoreRefund{ID: id, OrderID: orderID, RequestKey: id, InputDigest: in.EvidenceHash,
			Mode: "amount", AmountMinor: in.AmountMinor, Currency: in.Currency,
			Reason: "Refund issued by the payment provider", Status: "provider_review", RequestedRole: "provider",
			RetainedFeeQuota: o.FeeQuota, ProviderRefundReference: &in.RefundReference, ProviderEvidenceHash: in.EvidenceHash,
			CreatedAt: common.GetTimestamp(), DecidedAt: common.GetTimestamp()}
		if err := tx.Create(&r).Error; err != nil {
			return err
		}
		if err := tx.Model(o).Update("status", "refund_pending").Error; err != nil {
			return err
		}
		return storeEvent(tx, 0, orderID, "refund_provider_imported")
	})
	return id, err
}

// Resolve an imported receipt without any provider POST. Unsent local requests
// may be superseded when their reservation conflicts with money already returned.
// Submitted/unknown requests remain fenced and require authoritative resolution;
// their reservations are never released from an empty query or a timeout.
func ReconcileMerchantStoreExternalRefund(id string) error {
	var lookup MerchantStoreRefund
	if err := DB.First(&lookup, "id = ?", id).Error; err != nil {
		return err
	}
	var proof MerchantStoreVerifiedRefundEvidence
	blocked := false
	err := storeOrderTx(lookup.OrderID, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if err := storeRequireRefundSync(tx); err != nil {
			return err
		}
		r, err := storeRefundLoad(tx, o, id)
		if err != nil {
			return err
		}
		b, err := storeRefundBasis(tx, o)
		if err != nil {
			return err
		}
		if b == nil || r.RequestedRole != "provider" || r.ProviderRefundReference == nil || len(r.ProviderEvidenceHash) != 64 || r.AmountMinor <= 0 || r.Currency != b.Currency {
			return ErrMerchantStoreConflict
		}
		proof = MerchantStoreVerifiedRefundEvidence{PaymentReference: b.PaymentReference, RefundReference: *r.ProviderRefundReference, AmountMinor: r.AmountMinor, Currency: r.Currency, EvidenceHash: r.ProviderEvidenceHash}
		if r.Status == "completed" || r.Status == "reconciliation_required" {
			return nil
		}
		if r.Status != "provider_review" {
			return ErrMerchantStoreConflict
		}
		rows, err := storeRefundRows(tx, o)
		if err != nil {
			return err
		}
		u, err := storeRefundTotals(rows, o, b)
		if err != nil {
			return err
		}
		if r.AmountMinor > b.AmountMinor-u.nativeCompleted-u.nativeReserved {
			for _, pending := range rows {
				if pending.Status != "requested" && pending.Status != "awaiting_provider" {
					continue
				}
				var attempt MerchantStoreRefundProviderAttempt
				e := tx.First(&attempt, "refund_id = ?", pending.ID).Error
				if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
					return e
				}
				if e == nil && (attempt.SubmitCount != 0 || attempt.State != "ready") {
					continue
				}
				if err := tx.Model(&pending).Updates(map[string]any{"status": "rejected", "decision_reason": "A provider refund changed the remaining amount before this request was sent.", "decided_at": common.GetTimestamp()}).Error; err != nil {
					return err
				}
				if err := storeEvent(tx, 0, o.ID, "refund_unsent_superseded"); err != nil {
					return err
				}
			}
			if err := storeRefundNormalizeNativeReservations(tx, o); err != nil {
				return err
			}
			rows, err = storeRefundRows(tx, o)
			if err != nil {
				return err
			}
			u, err = storeRefundTotals(rows, o, b)
			if err != nil {
				return err
			}
		}
		if r.AmountMinor > b.AmountMinor-u.nativeCompleted-u.nativeReserved {
			blocked = true
			return storeRefundOrderStatus(tx, o)
		}
		quota := storeRefundFloor(o.PriceQuota, u.nativeCompleted+u.nativeReserved+r.AmountMinor, b.AmountMinor) - u.completed - u.reserved
		if err := tx.Model(r).Updates(map[string]any{"principal_quota": quota, "status": "reconciliation_required"}).Error; err != nil {
			return err
		}
		return storeRefundOrderStatus(tx, o)
	})
	if err != nil {
		return err
	}
	if blocked {
		return ErrMerchantStoreRefundReconciliation
	}
	return CompleteMerchantStoreVerifiedRefund(id, proof)
}
