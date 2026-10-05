package model

import (
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Email charges and their historical refund counter retain the original unit.
// Only new wallet credits and their new ledger entries use the corrected pool.
func heroSMSEmailRefundDeltaTx(tx *gorm.DB, order *HeroSMSEmailOrder, quota int) (int, error) {
	base, cutoff, err := walletFutureCreditBasisTx(tx, order.UserID, "email_refund_pool", order.ID)
	if err != nil {
		return 0, err
	}
	if base == nil {
		if cutoff > 0 && order.CreatedAt <= cutoff {
			return 0, errors.New("historical email refund credit basis missing")
		}
		return quota, nil
	}
	var source struct {
		ID                 string `json:"id"`
		UserID             int    `json:"user_id"`
		ChargeQuota        int    `json:"charge_quota"`
		RefundedQuota      int    `json:"refunded_quota"`
		CreatedAt          int64  `json:"created_at"`
		LastRefundLedgerID int64  `json:"last_refund_ledger_id"`
	}
	if json.Unmarshal(base.Source, &source) != nil || source.ID != order.ID || source.UserID != order.UserID ||
		source.CreatedAt != order.CreatedAt || source.ChargeQuota != order.ChargeQuota || source.RefundedQuota < 0 ||
		source.RefundedQuota >= source.ChargeQuota || order.RefundedQuota < source.RefundedQuota ||
		source.ChargeQuota-source.RefundedQuota != base.OriginalQuota || source.LastRefundLedgerID < 0 {
		return 0, errors.New("email refund credit source changed")
	}
	var original int64
	query := tx.Model(&HeroSMSEmailQuotaLedger{}).Where("order_id = ? AND entry_type = ?", order.ID, HeroSMSEmailLedgerRefund)
	if err := query.Where("id <= ?", source.LastRefundLedgerID).Select("COALESCE(SUM(amount_quota),0)").Scan(&original).Error; err != nil {
		return 0, err
	}
	var prior struct{ Actual, Original, Missing int64 }
	if err := tx.Model(&HeroSMSEmailQuotaLedger{}).Where("order_id = ? AND entry_type = ? AND id > ?", order.ID, HeroSMSEmailLedgerRefund, source.LastRefundLedgerID).
		Select("COALESCE(SUM(amount_quota),0) AS actual, COALESCE(SUM(original_amount_quota),0) AS original, COALESCE(SUM(CASE WHEN original_amount_quota IS NULL THEN 1 ELSE 0 END),0) AS missing").Scan(&prior).Error; err != nil {
		return 0, err
	}
	usedPool := int64(order.RefundedQuota - source.RefundedQuota)
	expected := rebasedRefundTarget(int64(base.RebasedQuota), int64(base.OriginalQuota), usedPool)
	if original != int64(source.RefundedQuota) || prior.Actual != expected || prior.Original != usedPool || prior.Missing != 0 {
		return 0, errors.New("email refund credit ledger mismatch")
	}
	target := rebasedRefundTarget(int64(base.RebasedQuota), int64(base.OriginalQuota), usedPool+int64(quota))
	return int(target - expected), nil
}

func heroSMSRefundQuotaTx(tx *gorm.DB, order *HeroSMSEmailOrder, activationID string, quota int, key string) error {
	if quota <= 0 {
		return nil
	}
	var fresh HeroSMSEmailOrder
	if err := lockForUpdate(tx).Where("id = ?", order.ID).First(&fresh).Error; err != nil {
		return err
	}
	if fresh.UserID != order.UserID {
		return errors.New("email refund owner changed")
	}
	var existing int64
	if err := tx.Model(&HeroSMSEmailQuotaLedger{}).Where("idempotency_key = ?", key).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	if fresh.RefundedQuota < 0 || fresh.RefundedQuota > fresh.ChargeQuota || quota > fresh.ChargeQuota-fresh.RefundedQuota {
		return errors.New("HeroSMS refund exceeds reserved quota")
	}
	delta, err := heroSMSEmailRefundDeltaTx(tx, &fresh, quota)
	if err != nil {
		return err
	}
	ledger := HeroSMSEmailQuotaLedger{UserID: fresh.UserID, OrderID: fresh.ID, ActivationID: activationID,
		EntryType: HeroSMSEmailLedgerRefund, AmountQuota: delta, OriginalAmountQuota: &quota, IdempotencyKey: key}
	insert := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "idempotency_key"}}, DoNothing: true}).Create(&ledger)
	if insert.Error != nil {
		return insert.Error
	}
	if insert.RowsAffected == 0 {
		return nil
	}
	update := tx.Model(&HeroSMSEmailOrder{}).Where("id = ? AND refunded_quota = ? AND charge_quota = ?", fresh.ID, fresh.RefundedQuota, fresh.ChargeQuota).
		UpdateColumn("refunded_quota", fresh.RefundedQuota+quota)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return errors.New("email refund counter changed")
	}
	return ApplyWalletQuotaDelta(tx, fresh.UserID, delta)
}
