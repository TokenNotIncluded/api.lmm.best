package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// These server-only primitives accept evidence only after the adapter verifies
// the original provider account, paid payment identity, amount and currency.
// There is deliberately no browser route for either operation.
type VerifiedMerchantStoreRefundPaymentBasis struct {
	ReceiptReference string
	PaymentReference string
	AmountMinor      int64
	Currency         string
	EvidenceHash     string
}

func RecordMerchantStoreRefundPaymentBasis(id string, in VerifiedMerchantStoreRefundPaymentBasis) error {
	if in.AmountMinor <= 0 || in.AmountMinor > int64(common.MaxWalletQuota) || in.PaymentReference == "" || len(in.PaymentReference) > 128 || len(in.EvidenceHash) != 64 {
		return ErrMerchantStoreInput
	}
	return storeOrderTx(id, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if e := MerchantStoreRefundRequiresWriter(tx); e != nil {
			return e
		}
		if !storeRefundPaid(o) || o.PaymentMethod == "balance" || in.ReceiptReference != o.ProviderTradeID || in.Currency != o.Currency {
			return ErrMerchantStoreConflict
		}
		b, e := storeRefundBasis(tx, o)
		if e != nil {
			return e
		}
		if b != nil {
			if b.PaymentReference == in.PaymentReference && b.AmountMinor == in.AmountMinor && b.EvidenceHash == in.EvidenceHash {
				return nil
			}
			return ErrMerchantStoreConflict
		}
		b = &MerchantStoreRefundPaymentBasis{OrderID: id, ReceiptReference: in.ReceiptReference, PaymentReference: in.PaymentReference, AmountMinor: in.AmountMinor, Currency: in.Currency, EvidenceHash: in.EvidenceHash, CreatedAt: common.GetTimestamp()}
		if e := tx.Create(b).Error; e != nil {
			return e
		}
		rows, e := storeRefundRows(tx, o)
		if e != nil {
			return e
		}
		for _, r := range rows {
			if storeRefundActive(r.Status) && r.AmountMinor == 0 {
				if r.Mode != "full" || r.PrincipalQuota != o.PriceQuota {
					return ErrMerchantStoreConflict
				}
				if e := tx.Model(&r).Update("amount_minor", in.AmountMinor).Error; e != nil {
					return e
				}
			}
		}
		return nil
	})
}

type MerchantStoreVerifiedRefundEvidence struct {
	PaymentReference string
	RefundReference  string
	AmountMinor      int64
	Currency         string
	EvidenceHash     string
}

func CompleteMerchantStoreVerifiedRefund(refundID string, in MerchantStoreVerifiedRefundEvidence) error {
	if in.RefundReference == "" || len(in.RefundReference) > 128 || in.AmountMinor <= 0 || len(in.EvidenceHash) != 64 {
		return ErrMerchantStoreInput
	}
	var lookup MerchantStoreRefund
	if e := DB.First(&lookup, "id = ?", refundID).Error; e != nil {
		return e
	}
	e := storeOrderTx(lookup.OrderID, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if e := MerchantStoreRefundRequiresWriter(tx); e != nil {
			return e
		}
		r, e := storeRefundLoad(tx, o, refundID)
		if e != nil {
			return e
		}
		b, e := storeRefundBasis(tx, o)
		if e != nil {
			return e
		}
		if b == nil || o.PaymentMethod == "balance" || in.PaymentReference != b.PaymentReference || in.Currency != b.Currency || in.AmountMinor != r.AmountMinor {
			return ErrMerchantStoreConflict
		}
		if r.ProviderRefundReference != nil {
			if *r.ProviderRefundReference != in.RefundReference || r.ProviderEvidenceHash != in.EvidenceHash {
				return ErrMerchantStoreConflict
			}
			return nil
		}
		if r.Status != "awaiting_provider" || !storeRefundPaid(o) {
			return ErrMerchantStoreConflict
		}
		if e := tx.Model(r).Updates(map[string]any{"provider_refund_reference": in.RefundReference, "provider_evidence_hash": in.EvidenceHash, "status": "reconciliation_required"}).Error; e != nil {
			return e
		}
		return storeEvent(tx, 0, o.ID, "refund_provider_received")
	})
	if e != nil {
		return e
	}
	var wallets []int
	e = storeOrderTx(lookup.OrderID, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if e := MerchantStoreRefundRequiresWriter(tx); e != nil {
			return e
		}
		r, e := storeRefundLoad(tx, o, refundID)
		if e != nil {
			return e
		}
		if r.Status == "completed" {
			return nil
		}
		if r.Status != "reconciliation_required" {
			return ErrMerchantStoreConflict
		}
		b, e := storeRefundBasis(tx, o)
		if e != nil {
			return e
		}
		rows, e := storeRefundRows(tx, o)
		if e != nil {
			return e
		}
		u, e := storeRefundTotals(rows, o, b)
		if e != nil {
			return e
		}
		quota := storeRefundFloor(o.PriceQuota, u.nativeCompleted+in.AmountMinor, b.AmountMinor) - u.completed
		if e := storeRefundComplete(tx, o, r, quota); e != nil {
			return e
		}
		wallets = []int{o.SellerID}
		return storeEvent(tx, 0, o.ID, "refund_verified")
	})
	if e == nil {
		marketInvalidate(wallets...)
	}
	return e
}

// A failed request releases its reservation only after the trusted adapter
// proves a terminal provider failure for this exact request. Timeouts, missing
// rows and rejected ticket proposals are not proof that an executed refund
// failed, and must remain awaiting_provider.
type MerchantStoreVerifiedRefundFailure struct {
	PaymentReference     string
	RefundReference      string
	RequestedAmountMinor int64
	Currency             string
	EvidenceHash         string
}

func RejectMerchantStoreVerifiedRefund(refundID string, in MerchantStoreVerifiedRefundFailure) error {
	if in.RefundReference == "" || len(in.RefundReference) > 128 || in.RequestedAmountMinor <= 0 || len(in.EvidenceHash) != 64 {
		return ErrMerchantStoreInput
	}
	var lookup MerchantStoreRefund
	if e := DB.First(&lookup, "id = ?", refundID).Error; e != nil {
		return e
	}
	return storeOrderTx(lookup.OrderID, func(tx *gorm.DB, o *MerchantStoreOrder) error {
		if e := MerchantStoreRefundRequiresWriter(tx); e != nil {
			return e
		}
		r, e := storeRefundLoad(tx, o, refundID)
		if e != nil {
			return e
		}
		b, e := storeRefundBasis(tx, o)
		if e != nil {
			return e
		}
		if b == nil || o.PaymentMethod == "balance" || in.PaymentReference != b.PaymentReference || in.Currency != b.Currency || in.RequestedAmountMinor != r.AmountMinor {
			return ErrMerchantStoreConflict
		}
		if r.Status == "rejected" && r.ProviderRefundReference != nil && *r.ProviderRefundReference == in.RefundReference && r.ProviderEvidenceHash == in.EvidenceHash {
			return nil
		}
		if r.Status != "awaiting_provider" || r.ProviderRefundReference != nil {
			return ErrMerchantStoreConflict
		}
		if e := tx.Model(r).Updates(map[string]any{"status": "rejected", "decision_reason": "provider_refund_failed", "provider_refund_reference": in.RefundReference, "provider_evidence_hash": in.EvidenceHash}).Error; e != nil {
			return e
		}
		if e := storeRefundOrderStatus(tx, o); e != nil {
			return e
		}
		return storeEvent(tx, 0, o.ID, "refund_provider_failed")
	})
}
