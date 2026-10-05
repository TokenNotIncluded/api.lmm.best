package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"gorm.io/gorm"
)

// WalletTopUpCreditRebase is an explicit migration audit record. It is never
// auto-migrated at application startup. Historical top-up counters retain their
// original credit unit; this row tracks actual wallet reversals after migration.
type WalletTopUpCreditRebase struct {
	TopUpID                      int `gorm:"column:top_up_id;primaryKey;autoIncrement:false"`
	UserID                       int `gorm:"column:user_id"`
	MigrationID                  string
	OriginalCreditedQuota        int64
	OriginalRefundedQuota        int64
	OriginalRefundedAmountMicros int64
	OriginalPaidAmountMicros     int64
	RefundableQuota              int64
	RebasedDebitedQuota          int64
}

func (WalletTopUpCreditRebase) TableName() string { return "wallet_topup_credit_rebases" }

// Explicit catalog reads preserve compatibility before the optional migration,
// while returning catalog/connection failures instead of treating them as absence.
func walletCreditAuditTableExists(tx *gorm.DB, table string) (bool, error) {
	var count int64
	var err error
	switch tx.Dialector.Name() {
	case "postgres":
		err = tx.Raw("SELECT CASE WHEN to_regclass(?) IS NULL THEN 0 ELSE 1 END", table).Scan(&count).Error
	case "sqlite":
		err = tx.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count).Error
	case "mysql":
		err = tx.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table).Scan(&count).Error
	default:
		return false, fmt.Errorf("unsupported refund rebase database: %s", tx.Dialector.Name())
	}
	return count > 0, err
}

func rebasedTopUpRefundDeltaTx(tx *gorm.DB, topUp *TopUp, creditedQuota, paidMicros, refundMicros, originalDelta int64) (int64, error) {
	exists, err := walletCreditAuditTableExists(tx, "wallet_topup_credit_rebases")
	if err != nil {
		return 0, err
	}
	if !exists {
		return unrebasedTopUpRefundDeltaTx(tx, topUp, originalDelta)
	}
	var base WalletTopUpCreditRebase
	err = lockForUpdate(tx).Where("top_up_id = ?", topUp.Id).First(&base).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return unrebasedTopUpRefundDeltaTx(tx, topUp, originalDelta)
	}
	if err != nil {
		return 0, err
	}
	if base.UserID != topUp.UserId || base.MigrationID == "" || base.OriginalCreditedQuota != creditedQuota || base.OriginalPaidAmountMicros != paidMicros ||
		base.OriginalRefundedQuota < 0 || base.OriginalRefundedQuota > creditedQuota || base.OriginalRefundedQuota > topUp.RefundedQuota ||
		base.OriginalRefundedAmountMicros < 0 || base.OriginalRefundedAmountMicros >= paidMicros || base.OriginalRefundedAmountMicros > topUp.RefundedAmountMicros ||
		base.RefundableQuota < 0 || base.RebasedDebitedQuota < 0 || base.RebasedDebitedQuota > base.RefundableQuota {
		return 0, fmt.Errorf("%w: invalid wallet refund rebase baseline", ErrRefundAmountInvalid)
	}
	remainingPaid := paidMicros - base.OriginalRefundedAmountMicros
	alreadyRefunded := topUp.RefundedAmountMicros - base.OriginalRefundedAmountMicros
	expectedDebited := rebasedRefundTarget(base.RefundableQuota, remainingPaid, alreadyRefunded)
	if expectedDebited != base.RebasedDebitedQuota {
		return 0, fmt.Errorf("%w: wallet refund rebase counter mismatch", ErrRefundAmountInvalid)
	}
	target := rebasedRefundTarget(base.RefundableQuota, remainingPaid, alreadyRefunded+refundMicros)
	delta := target - base.RebasedDebitedQuota
	if err := tx.Model(&WalletTopUpCreditRebase{}).Where("top_up_id = ?", topUp.Id).Update("rebased_debited_quota", target).Error; err != nil {
		return 0, err
	}
	return delta, nil
}

// WalletTopUpCreditRebaseFacts exposes the existing payment authority for a
// read-only migration snapshot. Neither value comes from the user's displayed
// fiat balance. The caller must snapshot all raw order facts in the same read.
type WalletTopUpCreditRebaseFacts struct {
	EffectiveCreditedQuota int64 `json:"effective_credited_quota"`
	PaidAmountMicros       int64 `json:"paid_amount_micros"`
}

func ExportWalletTopUpCreditRebaseFacts(topUp *TopUp) WalletTopUpCreditRebaseFacts {
	if topUp == nil {
		return WalletTopUpCreditRebaseFacts{}
	}
	paid := topUp.SettledAmountMicros
	if paid <= 0 {
		paid = expectedTopUpAmountMicros(topUp)
	}
	return WalletTopUpCreditRebaseFacts{EffectiveCreditedQuota: normalizedTopUpCreditedQuota(topUp), PaidAmountMicros: paid}
}

// Integer arithmetic avoids decimal division precision near a half-credit tie.
func rebasedRefundTarget(quota, paid, refunded int64) int64 {
	if quota <= 0 || paid <= 0 || refunded <= 0 {
		return 0
	}
	if refunded >= paid {
		return quota
	}
	numerator := new(big.Int).Mul(big.NewInt(quota), big.NewInt(refunded))
	denominator := big.NewInt(paid)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if remainder.Lsh(remainder, 1).Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient.Int64()
}

// A missing child table/row is only a legacy/new-payment case when the durable
// migration plan also confirms this order was not included. Lost child audits
// must never silently re-enable a refund in the old credit unit.
func unrebasedTopUpRefundDeltaTx(tx *gorm.DB, topUp *TopUp, originalDelta int64) (int64, error) {
	exists, err := walletCreditAuditTableExists(tx, "wallet_credit_rebases")
	if err != nil {
		return 0, err
	}
	if !exists {
		return originalDelta, nil
	}
	var audits []struct{ Plan string }
	if err := tx.Table("wallet_credit_rebases").Select("plan").Find(&audits).Error; err != nil {
		return 0, err
	}
	for _, audit := range audits {
		var plan struct {
			UserIDs     []int           `json:"user_ids"`
			RefundBases json.RawMessage `json:"refund_bases"`
		}
		if err := json.Unmarshal([]byte(audit.Plan), &plan); err != nil {
			return 0, fmt.Errorf("%w: invalid wallet rebase audit plan", ErrRefundAmountInvalid)
		}
		affected := false
		for _, userID := range plan.UserIDs {
			if userID == topUp.UserId {
				affected = true
			}
		}
		if affected && (len(plan.RefundBases) == 0 || string(plan.RefundBases) == "null") {
			return 0, fmt.Errorf("%w: wallet rebase audit lacks refund baselines", ErrRefundAmountInvalid)
		}
		var bases []struct {
			TopUpID int `json:"top_up_id"`
		}
		if len(plan.RefundBases) > 0 && json.Unmarshal(plan.RefundBases, &bases) != nil {
			return 0, fmt.Errorf("%w: invalid wallet rebase refund plan", ErrRefundAmountInvalid)
		}
		for _, base := range bases {
			if base.TopUpID <= 0 {
				return 0, fmt.Errorf("%w: invalid wallet rebase refund order id", ErrRefundAmountInvalid)
			}
			if base.TopUpID == topUp.Id {
				return 0, fmt.Errorf("%w: wallet rebase refund baseline missing", ErrRefundAmountInvalid)
			}
		}
	}
	return originalDelta, nil
}
