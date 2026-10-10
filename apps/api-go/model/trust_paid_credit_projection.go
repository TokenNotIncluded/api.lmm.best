package model

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

var ErrPaidCreditProjectionUnavailable = errors.New("net paid credit projection unavailable")

// Eligibility consumes successful external-paid orders and independently
// verified NET credit facts. Presentation gross quotas are never used here.
// Historical counters remain in their original unit; the audited refundable
// pool and its actual reversals supply the present credit unit without live FX.
func trustPaidCreditSQL(tx *gorm.DB) (string, []interface{}, error) {
	credited, args, _, err := legacyPaidPolicyCreditedQuotaSQL()
	if err != nil {
		return "", nil, err
	}
	args[len(args)-1] = int64(500000)
	// Unrebased order credited/refunded counters share the immutable order unit.
	raw := "CASE WHEN top_ups.refunded_quota < 0 OR top_ups.refunded_quota > (" + credited + ") THEN NULL ELSE (" + credited + ") - top_ups.refunded_quota END"
	args = append(args, args...)
	p, err := loadAdminTopupProjector(tx)
	if err != nil {
		return "", nil, err
	}
	if p.invalid {
		return "", nil, ErrPaidCreditProjectionUnavailable
	}
	if p.cutoff <= 0 {
		childExists, err := walletCreditAuditTableExists(tx, "wallet_topup_credit_rebases")
		if err != nil {
			return "", nil, err
		}
		if childExists {
			var count int64
			if err := tx.Model(&WalletTopUpCreditRebase{}).Count(&count).Error; err != nil {
				return "", nil, err
			}
			if count > 0 {
				return "", nil, ErrPaidCreditProjectionUnavailable
			}
		}
		return raw, args, nil
	}
	ids := make([]int, 0, len(p.orders))
	refundIDs := make([]int, 0, len(p.refundFacts))
	for id := range p.orders {
		ids = append(ids, id)
	}
	for id := range p.refundFacts {
		refundIDs = append(refundIDs, id)
	}
	sort.Ints(ids)
	current := map[int]TopUp{}
	if len(refundIDs) > 0 {
		var orders []TopUp
		if err := tx.Where("id IN ?", refundIDs).Find(&orders).Error; err != nil {
			return "", nil, err
		}
		for _, order := range orders {
			current[order.Id] = order
		}
	}
	var sql strings.Builder
	// A fully reversed historical order contributes no paid entitlement even
	// when it predates the optional migration's refundable-order audit.
	sql.WriteString("CASE WHEN top_ups.refunded_quota = (" + credited + ") THEN 0 ")
	// PostgreSQL resolves an inner CASE with only untyped NULL branches to
	// text before combining it with the outer integer credit branches.
	unknownQuota := "CAST(NULL AS BIGINT)"
	if p.dialect == "mysql" {
		unknownQuota = "CAST(NULL AS SIGNED)"
	}
	for _, id := range ids {
		guard := p.orders[id].condition
		quota := unknownQuota
		if base, ok := p.refundFacts[id]; ok {
			order, present := current[id]
			remainingPaid := base.OriginalPaidAmountMicros - base.OriginalRefundedAmountMicros
			refundAfterFreeze := order.RefundedAmountMicros - base.OriginalRefundedAmountMicros
			if present && remainingPaid > 0 && refundAfterFreeze >= 0 && refundAfterFreeze <= remainingPaid &&
				order.RefundedQuota == base.OriginalRefundedQuota+proportionalRefundDelta(base.OriginalCreditedQuota, base.OriginalPaidAmountMicros, base.OriginalRefundedQuota, base.OriginalRefundedAmountMicros, refundAfterFreeze) &&
				base.RebasedDebitedQuota == rebasedRefundTarget(base.RefundableQuota, remainingPaid, refundAfterFreeze) {
				quota = strconv.FormatInt(base.RefundableQuota-base.RebasedDebitedQuota, 10)
				guard += " AND top_ups.refunded_quota = " + strconv.FormatInt(order.RefundedQuota, 10) + " AND top_ups.refunded_amount_micros = " + strconv.FormatInt(order.RefundedAmountMicros, 10)
			}
		} else {
			// Audited pending settlements contain current integer credited_quota.
			// Noncash/blocked sources retain the validator's always-false guard.
			quota = "CASE WHEN top_ups.refunded_quota BETWEEN 0 AND top_ups.credited_quota THEN top_ups.credited_quota-top_ups.refunded_quota ELSE NULL END"
		}
		sql.WriteString("WHEN top_ups.id = " + strconv.Itoa(id) + " THEN CASE WHEN " + guard + " THEN " + quota + " ELSE NULL END ")
	}
	cutoff := strconv.FormatInt(p.cutoff, 10)
	sql.WriteString("WHEN top_ups.create_time > " + cutoff + " AND top_ups.complete_time > " + cutoff +
		" AND top_ups.credited_quota > 0 AND top_ups.credited_quota <= " + strconv.FormatInt(common.MaxWalletQuota, 10) +
		" AND top_ups.expected_amount_micros > 0 AND TRIM(COALESCE(top_ups.settlement_currency,'')) <> ''" +
		" AND COALESCE(top_ups.pending_credit_rebase_key,'') = '' AND COALESCE(top_ups.pending_credit_rebase_original_quota,0)=0 AND COALESCE(top_ups.pending_credit_rebase_effective_quota,0)=0" +
		" AND top_ups.refunded_quota BETWEEN 0 AND top_ups.credited_quota THEN top_ups.credited_quota-top_ups.refunded_quota ELSE NULL END")
	// Only the opening raw fully-refunded guard contains positional arguments.
	return sql.String(), args[:len(args)/2], nil
}
