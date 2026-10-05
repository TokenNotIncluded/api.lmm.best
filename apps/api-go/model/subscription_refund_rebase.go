package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"gorm.io/gorm"
)

// SubscriptionOrderCreditRebase is created only by the explicit balance
// migration. The order keeps its original credit counters; this audit records
// the corrected grant and the actual credits revoked in its original period.
type SubscriptionOrderCreditRebase struct {
	SubscriptionOrderID          int `gorm:"column:subscription_order_id;primaryKey;autoIncrement:false"`
	UserSubscriptionID           int `gorm:"column:user_subscription_id"`
	UserID                       int `gorm:"column:user_id"`
	MigrationID                  string
	PeriodStart                  int64
	PeriodEnd                    int64
	SubscriptionEndTime          int64
	OriginalQuotaVersion         int64
	OriginalCreditQuota          int64
	OriginalRefundedQuota        int64
	OriginalRefundedAmountMicros int64
	OriginalPaidAmountMicros     int64
	RefundableQuota              int64
	ResetQuota                   int64
	ResetReducedQuota            int64
	RebasedDebitedQuota          int64
}

func (SubscriptionOrderCreditRebase) TableName() string { return "subscription_order_credit_rebases" }

func subscriptionRefundRebaseTx(tx *gorm.DB, order *SubscriptionOrder) (*SubscriptionOrderCreditRebase, error) {
	exists, err := walletCreditAuditTableExists(tx, "subscription_order_credit_rebases")
	if err != nil {
		return nil, err
	}
	if exists {
		var base SubscriptionOrderCreditRebase
		err = lockForUpdate(tx).Where("subscription_order_id = ?", order.Id).First(&base).Error
		if err == nil {
			return &base, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	// An absent child cannot silently restore the old quota unit when the
	// durable migration plan identifies this order or subscription.
	exists, err = walletCreditAuditTableExists(tx, "wallet_credit_rebases")
	if err != nil || !exists {
		return nil, err
	}
	var audits []struct{ Plan string }
	if err := tx.Table("wallet_credit_rebases").Select("plan").Find(&audits).Error; err != nil {
		return nil, err
	}
	for _, audit := range audits {
		var plan struct {
			Subscriptions []struct {
				ID int `json:"id"`
			} `json:"subscriptions"`
			Bases []struct {
				OrderID int `json:"subscription_order_id"`
			} `json:"subscription_refund_bases"`
		}
		if json.Unmarshal([]byte(audit.Plan), &plan) != nil {
			return nil, ErrSubscriptionRefundReconciliationRequired
		}
		for _, sub := range plan.Subscriptions {
			if sub.ID <= 0 || sub.ID == order.UserSubscriptionId {
				return nil, ErrSubscriptionRefundReconciliationRequired
			}
		}
		for _, base := range plan.Bases {
			if base.OrderID <= 0 || base.OrderID == order.Id {
				return nil, ErrSubscriptionRefundReconciliationRequired
			}
		}
	}
	return nil, nil
}

func subscriptionRefundRebaseIsCurrent(base *SubscriptionOrderCreditRebase, order *SubscriptionOrder) bool {
	return base != nil && base.PeriodStart == order.CurrentPeriodStart && base.PeriodEnd == order.CurrentPeriodEnd
}

// A reset clears usage, but retains the reduced grant. Refunds therefore use a
// cumulative nominal grant reduction, independently of the actual balance
// removed when historical/current consumption has already exhausted it.
func applySubscriptionRefundQuotaTx(tx *gorm.DB, order *SubscriptionOrder, sub *UserSubscription, paidMicros, refundMicros, legacyGrant int64, paymentBound bool) (actualDebit, orderQuotaDelta int64, err error) {
	if paidMicros <= 0 || refundMicros <= 0 || order.RefundedAmountMicros < 0 || refundMicros > paidMicros-order.RefundedAmountMicros {
		return 0, 0, ErrSubscriptionRefundReconciliationRequired
	}
	base, err := subscriptionRefundRebaseTx(tx, order)
	if err != nil {
		return 0, 0, err
	}
	if base != nil && !subscriptionRefundRebaseIsCurrent(base, order) {
		if !paymentBound {
			// An order id alone cannot bind a late refund to its paid cycle.
			return 0, 0, ErrSubscriptionRefundPaymentRequired
		}
		if base.MigrationID == "" || base.UserID != order.UserId || base.UserSubscriptionID != sub.Id ||
			sub.UserId != order.UserId || base.PeriodStart <= 0 || base.PeriodEnd <= base.PeriodStart ||
			base.SubscriptionEndTime != base.PeriodEnd || order.CurrentPeriodStart < base.PeriodEnd ||
			order.CurrentPeriodEnd <= base.PeriodEnd || sub.EndTime != order.CurrentPeriodEnd ||
			base.OriginalQuotaVersion < 0 || sub.QuotaVersion <= base.OriginalQuotaVersion ||
			sub.RenewalAmount == nil || sub.ResetAmount == nil || *sub.RenewalAmount < 0 ||
			*sub.ResetAmount != *sub.RenewalAmount-rebasedRefundTarget(*sub.RenewalAmount, paidMicros, order.RefundedAmountMicros) {
			return 0, 0, ErrSubscriptionRefundReconciliationRequired
		}
		base = nil // payment-specific evidence already bound the new period
	} else if base == nil && (sub.ResetAmount != nil || sub.RenewalAmount != nil) {
		// Corrected fields identify a migrated contract even if its parent
		// audit was also lost. Only a validated older-period child proves that
		// this is a subsequent paid grant using the corrected credit unit.
		return 0, 0, ErrSubscriptionRefundReconciliationRequired
	}
	cumulative := order.RefundedAmountMicros + refundMicros
	nominalDelta := int64(0)
	var newReset *int64
	if base != nil {
		var soldPlan SubscriptionPlan
		if json.Unmarshal([]byte(order.PlanSnapshot), &soldPlan) != nil || soldPlan.TotalAmount != base.OriginalCreditQuota {
			return 0, 0, ErrSubscriptionRefundReconciliationRequired
		}
		if base.UserID != order.UserId || base.UserSubscriptionID != sub.Id || sub.UserId != order.UserId ||
			base.MigrationID == "" || base.SubscriptionEndTime != sub.EndTime || base.OriginalQuotaVersion < 0 || sub.QuotaVersion < base.OriginalQuotaVersion ||
			base.OriginalPaidAmountMicros != paidMicros || paidMicros <= 0 || base.OriginalCreditQuota < 0 ||
			base.OriginalRefundedQuota < 0 || base.OriginalRefundedQuota > base.OriginalCreditQuota ||
			base.OriginalRefundedAmountMicros < 0 || base.OriginalRefundedAmountMicros >= paidMicros ||
			base.OriginalRefundedAmountMicros > order.RefundedAmountMicros || base.ResetQuota < 0 ||
			base.RefundableQuota < 0 || base.RefundableQuota > base.ResetQuota || base.ResetReducedQuota < 0 ||
			base.RebasedDebitedQuota < 0 || base.RebasedDebitedQuota > base.ResetReducedQuota || sub.ResetAmount == nil {
			return 0, 0, ErrSubscriptionRefundReconciliationRequired
		}
		remainingPaid := paidMicros - base.OriginalRefundedAmountMicros
		alreadyRefunded := order.RefundedAmountMicros - base.OriginalRefundedAmountMicros
		priorTarget := rebasedRefundTarget(base.ResetQuota, remainingPaid, alreadyRefunded)
		priorHistorical := proportionalRefundDelta(base.OriginalCreditQuota, paidMicros, base.OriginalRefundedQuota,
			base.OriginalRefundedAmountMicros, alreadyRefunded)
		if base.ResetReducedQuota != priorTarget || *sub.ResetAmount != base.ResetQuota-priorTarget ||
			order.RefundedQuota != base.OriginalRefundedQuota+priorHistorical {
			return 0, 0, ErrSubscriptionRefundReconciliationRequired
		}
		target := rebasedRefundTarget(base.ResetQuota, remainingPaid, alreadyRefunded+refundMicros)
		nominalDelta = target - priorTarget
		grant := base.ResetQuota - target
		newReset = &grant
		orderQuotaDelta = proportionalRefundDelta(base.OriginalCreditQuota, paidMicros, order.RefundedQuota, order.RefundedAmountMicros, refundMicros)
		base.ResetReducedQuota = target
	} else {
		grant := legacyGrant
		targetForRefund := proportionalRefundTarget
		if sub.RenewalAmount != nil {
			grant = *sub.RenewalAmount
			targetForRefund = rebasedRefundTarget
		}
		if grant < 0 {
			return 0, 0, ErrSubscriptionRefundReconciliationRequired
		}
		target := targetForRefund(grant, paidMicros, cumulative)
		nominalDelta = target - targetForRefund(grant, paidMicros, order.RefundedAmountMicros)
		if sub.ResetAmount != nil {
			remainingGrant := grant - target
			newReset = &remainingGrant
		}
		orderQuotaDelta = nominalDelta
	}
	if sub.AmountUsed < 0 || sub.AmountTotal < sub.AmountUsed || nominalDelta < 0 {
		return 0, 0, ErrSubscriptionRefundReconciliationRequired
	}
	actualDebit = nominalDelta
	if remaining := sub.AmountTotal - sub.AmountUsed; actualDebit > remaining {
		actualDebit = remaining
	}
	sub.AmountTotal -= actualDebit
	if newReset != nil {
		sub.ResetAmount = newReset
	}
	if base != nil {
		if actualDebit > math.MaxInt64-base.RebasedDebitedQuota {
			return 0, 0, ErrSubscriptionRefundReconciliationRequired
		}
		base.RebasedDebitedQuota += actualDebit
		if err := tx.Model(base).Updates(map[string]interface{}{
			"reset_reduced_quota": base.ResetReducedQuota, "rebased_debited_quota": base.RebasedDebitedQuota,
		}).Error; err != nil {
			return 0, 0, err
		}
	} else if paymentBound {
		orderQuotaDelta = actualDebit
	}
	return actualDebit, orderQuotaDelta, nil
}

func subscriptionRefundCountersMatchTx(tx *gorm.DB, order *SubscriptionOrder, amountMicros, quotaRevoked int64) error {
	if order.RefundedAmountMicros != amountMicros {
		return ErrSubscriptionRefundReconciliationRequired
	}
	base, err := subscriptionRefundRebaseTx(tx, order)
	if err != nil {
		return err
	}
	expectedQuota := order.RefundedQuota
	if subscriptionRefundRebaseIsCurrent(base, order) {
		if base.OriginalRefundedQuota < 0 || base.RebasedDebitedQuota < 0 || base.RebasedDebitedQuota > math.MaxInt64-base.OriginalRefundedQuota {
			return ErrSubscriptionRefundReconciliationRequired
		}
		// Pre-migration payment refunds keep their old credit facts; new rows
		// record actual corrected credits. The order retains the old unit.
		expectedQuota = base.OriginalRefundedQuota + base.RebasedDebitedQuota
	}
	if expectedQuota != quotaRevoked {
		return fmt.Errorf("%w: subscription refund counter mismatch", ErrSubscriptionRefundReconciliationRequired)
	}
	return nil
}
