/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package model

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ErrRefundWalletQuotaInsufficient is returned when a wallet refund would
// make the user's spendable quota negative. The enclosing transaction is
// rolled back so the provider can retry after an administrator reconciles
// the account; the refund is never silently recorded as only partially
// applied.
var ErrRefundWalletQuotaInsufficient = errors.New("refund wallet quota insufficient")

// ErrPaymentRefundOrderConflict is returned when a provider event id has
// already been recorded for a different order (or with different immutable
// payment facts). Reusing the provider idempotency key must never allow the
// existing refund amount to be applied to another order.
var ErrPaymentRefundOrderConflict = errors.New("payment refund event is bound to another order")

// PaymentRefundResult describes the durable effects of one provider refund.
// Created is false for a replayed event; QuotaDebited remains useful when a
// legacy event was recorded before wallet reversal was implemented.
type PaymentRefundResult struct {
	UserID       int
	QuotaDebited int64
	Created      bool
}

// ApplyPaymentRefund atomically records a verified provider refund and reverses
// the wallet/subscription value that the original order granted. The provider
// event id is the idempotency boundary, while the order's cumulative fields
// make partial refunds safe across multiple events.
func ApplyPaymentRefund(
	tradeNo string,
	isSubscription bool,
	amountMicros int64,
	currency string,
	providerEventID string,
	paymentMethod string,
	paymentProvider string,
	note string,
	actorID int,
) (PaymentRefundResult, error) {
	tradeNo = strings.TrimSpace(tradeNo)
	providerEventID = strings.TrimSpace(providerEventID)
	paymentProvider = strings.TrimSpace(paymentProvider)
	paymentMethod = strings.TrimSpace(paymentMethod)
	if DB == nil || tradeNo == "" || amountMicros <= 0 || providerEventID == "" || paymentProvider == "" || actorID <= 0 {
		return PaymentRefundResult{}, gorm.ErrInvalidData
	}
	if amountMicros > 9_000_000_000_000_000 {
		return PaymentRefundResult{}, gorm.ErrInvalidData
	}
	if currency = strings.ToUpper(strings.TrimSpace(currency)); currency == "" {
		currency = FinanceCurrencyUSD
	}

	result := PaymentRefundResult{}
	idempotencyKey := paymentProvider + ":refund:" + providerEventID
	err := DB.Transaction(func(tx *gorm.DB) error {
		var ledger FinanceLedgerEntry
		ledgerErr := tx.Where("idempotency_key = ?", idempotencyKey).First(&ledger).Error
		ledgerExists := ledgerErr == nil
		if ledgerErr != nil && !errors.Is(ledgerErr, gorm.ErrRecordNotFound) {
			return ledgerErr
		}
		appliedAmount := amountMicros
		if ledgerExists {
			// A retry must use the amount that was durably recorded originally;
			// never let a changed webhook payload enlarge an old refund.
			appliedAmount = ledger.AmountMicros
			result.Created = false
		}

		var topUp TopUp
		if !isSubscription {
			topUpErr := lockForUpdate(tx).Where("trade_no = ?", tradeNo).First(&topUp).Error
			if topUpErr == nil {
				if topUp.Status != common.TopUpStatusSuccess || topUp.PaymentProvider != paymentProvider {
					return fmt.Errorf("refund order is not a settled %s top-up", paymentProvider)
				}
				result.UserID = topUp.UserId
				if ledgerExists && !refundLedgerBindsRequest(&ledger, tradeNo, providerEventID, paymentMethod, paymentProvider, currency, result.UserID) {
					return ErrPaymentRefundOrderConflict
				}
				// Newer handlers update the cumulative fields in the same transaction
				// as the ledger. An already-populated row therefore needs no second
				// wallet debit when the provider retries the same event.
				alreadyApplied := ledgerExists && topUp.RefundedAmountMicros > 0
				paidMicros := topUp.SettledAmountMicros
				if paidMicros <= 0 {
					paidMicros = expectedTopUpAmountMicros(&topUp)
				}
				if paidMicros > 0 && !alreadyApplied {
					remaining := paidMicros - topUp.RefundedAmountMicros
					if remaining < 0 || appliedAmount > remaining {
						return fmt.Errorf("%w: refund exceeds settled top-up amount", ErrRefundAmountInvalid)
					}
				}
				creditedQuota := normalizedTopUpCreditedQuota(&topUp)
				refundQuota := int64(0)
				if !alreadyApplied {
					refundQuota = proportionalRefundDelta(creditedQuota, paidMicros, topUp.RefundedQuota, topUp.RefundedAmountMicros, appliedAmount)
				}
				if refundQuota > 0 {
					// Refunds must never silently create a negative wallet. Keep
					// the balance predicate in the same atomic UPDATE as the
					// debit: a concurrent spend or stale refund cannot turn a
					// successful provider refund into an untracked debt. Returning
					// an error rolls back the cumulative refund and ledger rows,
					// allowing the webhook provider to retry after reconciliation.
					if refundQuota > int64(common.MaxWalletQuota) {
						return fmt.Errorf("%w: refund quota exceeds the wallet safe range", ErrRefundAmountInvalid)
					}
					debit := UpdateWalletQuotaByDelta(
						tx.Model(&User{}).Where("id = ? AND quota >= ?", topUp.UserId, refundQuota),
						-int(refundQuota),
					)
					if debit.Error != nil {
						return debit.Error
					}
					if debit.RowsAffected != 1 {
						return fmt.Errorf("%w: user_id=%d trade_no=%s required_quota=%d", ErrRefundWalletQuotaInsufficient, topUp.UserId, tradeNo, refundQuota)
					}
					result.QuotaDebited = refundQuota
				}
				if !alreadyApplied {
					updates := map[string]interface{}{
						"refunded_amount_micros": topUp.RefundedAmountMicros + appliedAmount,
						"refunded_quota":         topUp.RefundedQuota + refundQuota,
					}
					if err := tx.Model(&TopUp{}).Where("id = ?", topUp.Id).Updates(updates).Error; err != nil {
						return err
					}
				}
			} else {
				if errors.Is(topUpErr, gorm.ErrRecordNotFound) {
					return fmt.Errorf("refund order disappeared: %w", topUpErr)
				}
				return topUpErr
			}
		} else {
			var order SubscriptionOrder
			if err := lockForUpdate(tx).Where("trade_no = ?", tradeNo).First(&order).Error; err != nil {
				return fmt.Errorf("refund order disappeared: %w", err)
			}
			if order.Status != common.TopUpStatusSuccess || order.PaymentProvider != paymentProvider {
				return fmt.Errorf("refund order is not a settled %s subscription", paymentProvider)
			}
			result.UserID = order.UserId
			if ledgerExists && !refundLedgerBindsRequest(&ledger, tradeNo, providerEventID, paymentMethod, paymentProvider, currency, result.UserID) {
				return ErrPaymentRefundOrderConflict
			}
			alreadyApplied := ledgerExists && (order.RefundedAmountMicros > 0 ||
				subscriptionRefundAlreadyConsumedInPriorPeriod(&order, &ledger))
			paidMicros := order.ExpectedAmountMicros
			if paidMicros <= 0 {
				// Legacy fallback only. New subscription orders snapshot the real
				// provider amount independently from their plan list currency.
				paidMicros = moneyToMicros(order.Money)
			}
			if expectedCurrency := strings.ToUpper(strings.TrimSpace(order.SettlementCurrency)); expectedCurrency != "" && expectedCurrency != currency {
				return fmt.Errorf("%w: subscription refund currency mismatch", ErrPaymentEvidenceConflict)
			}
			if paidMicros > 0 && !alreadyApplied {
				remaining := paidMicros - order.RefundedAmountMicros
				if remaining < 0 || appliedAmount > remaining {
					return fmt.Errorf("%w: refund exceeds settled subscription amount", ErrRefundAmountInvalid)
				}
			}
			if order.UserSubscriptionId > 0 && !alreadyApplied {
				var subscription UserSubscription
				if err := lockForUpdate(tx).Where("id = ?", order.UserSubscriptionId).First(&subscription).Error; err != nil {
					return err
				}
				if paidMicros > 0 && subscription.AmountTotal > 0 {
					refundBaseQuota := subscription.AmountTotal + order.RefundedQuota
					targetRevoked := proportionalRefundTarget(refundBaseQuota, paidMicros, order.RefundedAmountMicros+appliedAmount)
					if targetRevoked < order.RefundedQuota {
						targetRevoked = order.RefundedQuota
					}
					refundQuota := targetRevoked - order.RefundedQuota
					newTotal := subscription.AmountTotal - refundQuota
					if newTotal < subscription.AmountUsed {
						newTotal = subscription.AmountUsed
					}
					updates := map[string]interface{}{"amount_total": newTotal}
					if newTotal == subscription.AmountUsed && subscription.Status == "active" {
						updates["status"] = "cancelled"
					}
					if err := tx.Model(&UserSubscription{}).Where("id = ?", subscription.Id).Updates(updates).Error; err != nil {
						return err
					}
					result.QuotaDebited = refundQuota
					if err := tx.Model(&SubscriptionOrder{}).Where("id = ?", order.Id).Updates(map[string]interface{}{
						"refunded_amount_micros": order.RefundedAmountMicros + appliedAmount,
						"refunded_quota":         order.RefundedQuota + refundQuota,
					}).Error; err != nil {
						return err
					}
				} else if err := tx.Model(&SubscriptionOrder{}).Where("id = ?", order.Id).Update("refunded_amount_micros", gorm.Expr("refunded_amount_micros + ?", appliedAmount)).Error; err != nil {
					return err
				}
			} else if !alreadyApplied {
				if err := tx.Model(&SubscriptionOrder{}).Where("id = ?", order.Id).Update("refunded_amount_micros", gorm.Expr("refunded_amount_micros + ?", appliedAmount)).Error; err != nil {
					return err
				}
			}
		}

		if !ledgerExists {
			entry := &FinanceLedgerEntry{
				EntryType:       FinanceEntryRevenue,
				Category:        FinanceSourceRefund,
				AmountMicros:    appliedAmount,
				Currency:        currency,
				Direction:       FinanceDirectionDebit,
				PaymentMethod:   paymentMethod,
				PaymentProvider: paymentProvider,
				UserId:          &result.UserID,
				SourceType:      FinanceSourceRefund,
				SourceId:        providerEventID,
				Note:            bindRefundNote(note, tradeNo),
				OccurredAt:      common.GetTimestamp(),
				CreatedBy:       actorID,
				IdempotencyKey:  idempotencyKey,
			}
			if err := normalizeFinanceEntry(entry); err != nil {
				return err
			}
			if err := tx.Create(entry).Error; err != nil {
				return err
			}
			result.Created = true
		}
		return nil
	})
	if err != nil {
		return PaymentRefundResult{}, err
	}
	if !isSubscription && result.UserID > 0 && result.QuotaDebited > 0 {
		if err := cacheDecrUserQuota(result.UserID, result.QuotaDebited); err != nil {
			common.SysLog("failed to update quota cache after payment refund: " + err.Error())
		}
		InvalidatePaidTopUpAggregate(result.UserID)
	}
	return result, nil
}

// bindRefundNote gives refund ledger rows a stable, order-specific binding
// without requiring a schema migration. Existing handlers already include a
// legacy "trade_no=<value>" token in their audit note, which is accepted by
// refundNoteContainsTradeNo for backwards compatibility.
func bindRefundNote(note, tradeNo string) string {
	note = strings.TrimSpace(note)
	if refundNoteContainsTradeNo(note, tradeNo) {
		return note
	}
	marker := "refund_trade_no=" + tradeNo
	if note == "" {
		return marker
	}
	candidate := note + " " + marker
	if len([]rune(candidate)) <= 500 {
		return candidate
	}
	// The marker is the security-critical part. Keep it even if a caller's
	// free-form note would otherwise overflow the ledger column.
	return marker
}

func refundNoteContainsTradeNo(note, tradeNo string) bool {
	if strings.TrimSpace(note) == "" || tradeNo == "" {
		return false
	}
	for _, token := range strings.Fields(note) {
		if token == "refund_trade_no="+tradeNo || token == "trade_no="+tradeNo {
			return true
		}
	}
	return false
}

// subscriptionRefundAlreadyConsumedInPriorPeriod reports that a ledger row
// belongs to an earlier billing cycle. Recurring renewal resets the order's
// cumulative refund counters so the new period can accept its own refunds;
// a provider retry of the previous cycle must not look like a legacy
// backfill and shrink the freshly restored grant.
func subscriptionRefundAlreadyConsumedInPriorPeriod(order *SubscriptionOrder, ledger *FinanceLedgerEntry) bool {
	if order == nil || ledger == nil {
		return false
	}
	return order.CurrentPeriodStart > 0 && ledger.OccurredAt > 0 && ledger.OccurredAt < order.CurrentPeriodStart
}

func refundLedgerBindsRequest(
	ledger *FinanceLedgerEntry,
	tradeNo string,
	providerEventID string,
	paymentMethod string,
	paymentProvider string,
	currency string,
	userID int,
) bool {
	if ledger == nil || ledger.SourceType != FinanceSourceRefund || ledger.SourceId != providerEventID {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(ledger.PaymentMethod), paymentMethod) ||
		!strings.EqualFold(strings.TrimSpace(ledger.PaymentProvider), paymentProvider) ||
		!strings.EqualFold(strings.TrimSpace(ledger.Currency), currency) {
		return false
	}
	if ledger.UserId == nil || *ledger.UserId != userID {
		return false
	}
	return refundNoteContainsTradeNo(ledger.Note, tradeNo)
}

// ApplyWaffoPancakeRefund is retained for existing callers. New provider
// handlers must call ApplyPaymentRefund after verifying their own webhook.
func ApplyWaffoPancakeRefund(
	tradeNo string,
	isSubscription bool,
	amountMicros int64,
	currency string,
	providerEventID string,
	paymentMethod string,
	paymentProvider string,
	note string,
	actorID int,
) (PaymentRefundResult, error) {
	return ApplyPaymentRefund(
		tradeNo, isSubscription, amountMicros, currency, providerEventID,
		paymentMethod, paymentProvider, note, actorID,
	)
}

func moneyToMicros(value float64) int64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return int64(math.Round(value * 1_000_000))
}

func proportionalRefundTarget(totalQuota, paidMicros, refundedMicros int64) int64 {
	if totalQuota <= 0 || paidMicros <= 0 || refundedMicros <= 0 {
		return 0
	}
	target := decimal.NewFromInt(totalQuota).
		Mul(decimal.NewFromInt(refundedMicros)).
		Div(decimal.NewFromInt(paidMicros)).
		Round(0).
		IntPart()
	if target < 0 {
		return 0
	}
	if target > totalQuota {
		return totalQuota
	}
	return target
}

func proportionalRefundDelta(totalQuota, paidMicros, alreadyRefundedQuota, alreadyRefundedMicros, refundMicros int64) int64 {
	if totalQuota <= 0 || paidMicros <= 0 || refundMicros <= 0 {
		return 0
	}
	target := proportionalRefundTarget(totalQuota, paidMicros, alreadyRefundedMicros+refundMicros)
	if target < alreadyRefundedQuota {
		return 0
	}
	return target - alreadyRefundedQuota
}
