/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package model

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

var (
	ErrSubscriptionRefundPaymentRequired        = errors.New("subscription refund requires its original payment identity")
	ErrSubscriptionRefundReconciliationRequired = errors.New("subscription refund requires reconciliation of unbound legacy evidence")
)

// SubscriptionPaymentRefund is append-only. A provider refund and its finance
// debit are permanently bound to one immutable payment, not the order's rolling
// current period. An older cycle's refund must never revoke a newer paid grant.
type SubscriptionPaymentRefund struct {
	ID                         int64  `gorm:"primaryKey"`
	SubscriptionOrderID        int    `gorm:"not null;index"`
	SubscriptionPaymentEventID int    `gorm:"not null;index"`
	PaymentProvider            string `gorm:"type:varchar(64);not null;uniqueIndex:idx_subscription_provider_refund,priority:1"`
	ProviderEventID            string `gorm:"type:varchar(255);not null;uniqueIndex:idx_subscription_provider_refund,priority:2"`
	Currency                   string `gorm:"type:varchar(8);not null"`
	AmountMicros               int64  `gorm:"not null"`
	QuotaRevoked               int64  `gorm:"not null"`
	FinanceLedgerEntryID       int64  `gorm:"not null;uniqueIndex"`
	CreatedTime                int64  `gorm:"not null"`
}

// SubscriptionRefundNeedsPaymentIdentity fails closed for legacy/unknown
// product types. Only a frozen, explicitly one-time purchase may use the
// order-level refund path; recurring state cannot be overridden by its label.
func SubscriptionRefundNeedsPaymentIdentity(order *SubscriptionOrder) bool {
	if order == nil || order.ProviderSubscriptionId != "" || order.CurrentPeriodStart != 0 || order.CurrentPeriodEnd != 0 {
		return true
	}
	var snapshot struct {
		ProductType string `json:"waffo_pancake_product_type"`
	}
	return json.Unmarshal([]byte(order.PlanSnapshot), &snapshot) != nil ||
		NormalizeWaffoPancakeProductType(snapshot.ProductType) != WaffoPancakeProductTypeOneTime
}

type SubscriptionPaymentRefundRequest struct {
	TradeNo, ProviderTransactionID, ProviderEventID, PaymentMethod, PaymentProvider, Currency, Note string
	AmountMicros                                                                                    int64
	ActorID                                                                                         int
}

func (request *SubscriptionPaymentRefundRequest) normalize() error {
	request.TradeNo = strings.TrimSpace(request.TradeNo)
	request.ProviderTransactionID = strings.TrimSpace(request.ProviderTransactionID)
	request.ProviderEventID = strings.TrimSpace(request.ProviderEventID)
	request.PaymentMethod = strings.TrimSpace(request.PaymentMethod)
	request.PaymentProvider = strings.TrimSpace(request.PaymentProvider)
	request.Currency = strings.ToUpper(strings.TrimSpace(request.Currency))
	if request.ProviderTransactionID == "" {
		return ErrSubscriptionRefundPaymentRequired
	}
	if DB == nil || request.TradeNo == "" || request.ProviderEventID == "" || request.PaymentProvider == "" ||
		request.PaymentMethod == "" || request.Currency == "" || request.AmountMicros <= 0 ||
		request.AmountMicros > 9_000_000_000_000_000 || request.ActorID <= 0 {
		return gorm.ErrInvalidData
	}
	return nil
}

// ApplySubscriptionPaymentRefund requires payment-specific verified evidence.
// Missing receipts remain retryable; receipt time and the latest order/payment
// are never used to infer a refund's owner or repair historical finance rows.
func ApplySubscriptionPaymentRefund(request SubscriptionPaymentRefundRequest) (PaymentRefundResult, error) {
	if err := request.normalize(); err != nil {
		return PaymentRefundResult{}, err
	}
	var result PaymentRefundResult
	var groupChanged bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order SubscriptionOrder
		if err := lockForUpdate(tx).Where("trade_no = ?", request.TradeNo).First(&order).Error; err != nil {
			return err
		}
		if order.Status != common.TopUpStatusSuccess || order.PaymentProvider != request.PaymentProvider ||
			order.PaymentMethod != request.PaymentMethod {
			return ErrPaymentEvidenceConflict
		}
		var payment SubscriptionPaymentEvent
		if err := tx.Where("subscription_order_id = ? AND payment_provider = ? AND provider_transaction_id = ?",
			order.Id, request.PaymentProvider, request.ProviderTransactionID).First(&payment).Error; err != nil {
			return err
		}
		if payment.SettlementCurrency != request.Currency || payment.SettlementAmountMicros <= 0 ||
			payment.PeriodStart < 0 || (payment.PeriodStart == 0) != (payment.PeriodEnd == 0) ||
			(payment.PeriodEnd != 0 && payment.PeriodEnd <= payment.PeriodStart) {
			return ErrPaymentEvidenceConflict
		}
		result.UserID = order.UserId
		replayed, err := subscriptionRefundReplayTx(tx, &order, &payment, &request)
		if err != nil || replayed {
			return err
		}
		if err := rejectUnboundSubscriptionRefundsTx(tx, &order, &request); err != nil {
			return err
		}
		var totals struct {
			AmountMicros int64
			QuotaRevoked int64
		}
		if err := tx.Model(&SubscriptionPaymentRefund{}).
			Select("COALESCE(SUM(amount_micros), 0) AS amount_micros, COALESCE(SUM(quota_revoked), 0) AS quota_revoked").
			Where("subscription_payment_event_id = ?", payment.Id).Scan(&totals).Error; err != nil {
			return err
		}
		if totals.AmountMicros < 0 || totals.AmountMicros > payment.SettlementAmountMicros ||
			request.AmountMicros > payment.SettlementAmountMicros-totals.AmountMicros {
			return ErrRefundAmountInvalid
		}
		inlinePeriod, err := subscriptionRefundHasInlinePeriodTx(tx, &payment)
		if err != nil {
			return err
		}
		if inlinePeriod && order.UserSubscriptionId > 0 && order.CurrentPeriodStart == payment.PeriodStart && order.CurrentPeriodEnd == payment.PeriodEnd {
			if order.RefundedAmountMicros != totals.AmountMicros || order.RefundedQuota != totals.QuotaRevoked {
				return ErrSubscriptionRefundReconciliationRequired
			}
			result.QuotaDebited, groupChanged, err = revokeSubscriptionPaymentQuotaTx(tx, &order, &payment, totals.AmountMicros+request.AmountMicros)
			if err != nil {
				return err
			}
		}
		if err := insertSubscriptionPaymentRefundTx(tx, &order, &payment, &request, result.QuotaDebited); err != nil {
			return err
		}
		result.Created = true
		return nil
	})
	if err != nil {
		return PaymentRefundResult{}, err
	}
	if groupChanged {
		refreshSubscriptionUserGroupCache(result.UserID, "payment-bound subscription refund")
	}
	return result, nil
}

// Old timestamp-derived ledger boundaries are not proof of refund ownership.
// Only boundaries preserved in the original verified payment itself can
// authorize quota reversal. Modern receipts leave access to lifecycle events.
func subscriptionRefundHasInlinePeriodTx(tx *gorm.DB, payment *SubscriptionPaymentEvent) (bool, error) {
	if payment.PeriodStart <= 0 || payment.PeriodEnd <= payment.PeriodStart {
		return false, nil
	}
	var raw WaffoPancakeSubscriptionPayment
	err := tx.Where("subscription_order_id = ? AND payment_id = ?", payment.SubscriptionOrderId, payment.ProviderTransactionId).First(&raw).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if raw.Currency != payment.SettlementCurrency || raw.AmountMicros != payment.SettlementAmountMicros {
		return false, ErrPaymentEvidenceConflict
	}
	if raw.PeriodStart == 0 && raw.PeriodEnd == 0 {
		return false, nil
	}
	if raw.PeriodStart != payment.PeriodStart || raw.PeriodEnd != payment.PeriodEnd {
		return false, ErrSubscriptionRefundReconciliationRequired
	}
	return true, nil
}

func subscriptionRefundReplayTx(tx *gorm.DB, order *SubscriptionOrder, payment *SubscriptionPaymentEvent, request *SubscriptionPaymentRefundRequest) (bool, error) {
	var previous SubscriptionPaymentRefund
	err := tx.Where("payment_provider = ? AND provider_event_id = ?", request.PaymentProvider, request.ProviderEventID).First(&previous).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if previous.SubscriptionOrderID != order.Id || previous.SubscriptionPaymentEventID != payment.Id ||
		previous.Currency != request.Currency || previous.AmountMicros != request.AmountMicros {
		return false, ErrPaymentRefundOrderConflict
	}
	var ledger FinanceLedgerEntry
	if err := tx.First(&ledger, previous.FinanceLedgerEntryID).Error; err != nil {
		return false, err
	}
	if !refundLedgerBindsRequest(&ledger, request.TradeNo, request.ProviderEventID, request.PaymentMethod, request.PaymentProvider, request.Currency, order.UserId) ||
		ledger.AmountMicros != previous.AmountMicros || ledger.Direction != FinanceDirectionDebit ||
		ledger.EntryType != FinanceEntryRevenue || ledger.Category != FinanceSourceRefund {
		return false, ErrPaymentRefundOrderConflict
	}
	return true, nil
}

func rejectUnboundSubscriptionRefundsTx(tx *gorm.DB, order *SubscriptionOrder, request *SubscriptionPaymentRefundRequest) error {
	var existing FinanceLedgerEntry
	err := tx.Where("idempotency_key = ?", request.PaymentProvider+":refund:"+request.ProviderEventID).First(&existing).Error
	if err == nil {
		if !refundLedgerBindsRequest(&existing, request.TradeNo, request.ProviderEventID, request.PaymentMethod, request.PaymentProvider, request.Currency, order.UserId) {
			return ErrPaymentRefundOrderConflict
		}
		return ErrSubscriptionRefundReconciliationRequired
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var unbound []FinanceLedgerEntry
	if err := tx.Table("finance_ledger_entries AS ledger").Select("ledger.id, ledger.note").
		Joins("LEFT JOIN subscription_payment_refunds AS refund ON refund.finance_ledger_entry_id = ledger.id").
		Where("ledger.user_id = ? AND ledger.payment_provider = ? AND ledger.source_type = ? AND refund.id IS NULL",
			order.UserId, request.PaymentProvider, FinanceSourceRefund).Scan(&unbound).Error; err != nil {
		return err
	}
	for _, ledger := range unbound {
		if refundNoteContainsTradeNo(ledger.Note, order.TradeNo) || !refundNoteHasOrderBinding(ledger.Note) {
			return ErrSubscriptionRefundReconciliationRequired
		}
	}
	return nil
}

func refundNoteHasOrderBinding(note string) bool {
	for _, token := range strings.Fields(note) {
		if strings.HasPrefix(token, "refund_trade_no=") || strings.HasPrefix(token, "trade_no=") {
			return true
		}
	}
	return false
}

func revokeSubscriptionPaymentQuotaTx(tx *gorm.DB, order *SubscriptionOrder, payment *SubscriptionPaymentEvent, refundedMicros int64) (int64, bool, error) {
	var plan SubscriptionPlan
	if strings.TrimSpace(order.PlanSnapshot) == "" || json.Unmarshal([]byte(order.PlanSnapshot), &plan) != nil || plan.TotalAmount < 0 {
		return 0, false, ErrSubscriptionRefundReconciliationRequired
	}
	subscription, _, err := lockUserSubscriptionForMutationTx(tx, order.UserSubscriptionId, order.UserId)
	if err != nil {
		return 0, false, err
	}
	newTotal := subscription.AmountTotal
	if plan.TotalAmount > 0 {
		target := plan.TotalAmount - proportionalRefundTarget(plan.TotalAmount, payment.SettlementAmountMicros, refundedMicros)
		if target < subscription.AmountUsed {
			target = subscription.AmountUsed
		}
		if target < newTotal {
			newTotal = target
		}
	}
	quotaRevoked := subscription.AmountTotal - newTotal
	subscription.AmountTotal = newTotal
	if refundedMicros == payment.SettlementAmountMicros || (plan.TotalAmount > 0 && newTotal <= subscription.AmountUsed) {
		subscription.Status = "cancelled"
		subscription.NextResetTime = 0
	}
	subscription.UpdatedAt = common.GetTimestamp()
	if err := tx.Save(subscription).Error; err != nil {
		return 0, false, err
	}
	if err := tx.Model(order).Updates(map[string]interface{}{
		"refunded_amount_micros": refundedMicros,
		"refunded_quota":         order.RefundedQuota + quotaRevoked,
	}).Error; err != nil {
		return 0, false, err
	}
	if subscription.Status != "cancelled" {
		return quotaRevoked, false, nil
	}
	restoredGroup, err := downgradeUserGroupForSubscriptionTx(tx, subscription, common.GetTimestamp())
	return quotaRevoked, restoredGroup != "", err
}

func insertSubscriptionPaymentRefundTx(tx *gorm.DB, order *SubscriptionOrder, payment *SubscriptionPaymentEvent, request *SubscriptionPaymentRefundRequest, quotaRevoked int64) error {
	ledger := FinanceLedgerEntry{
		EntryType: FinanceEntryRevenue, Category: FinanceSourceRefund, AmountMicros: request.AmountMicros,
		Currency: request.Currency, Direction: FinanceDirectionDebit, PaymentMethod: request.PaymentMethod,
		PaymentProvider: request.PaymentProvider, UserId: &order.UserId, SourceType: FinanceSourceRefund,
		SourceId: request.ProviderEventID, Note: bindRefundNote(request.Note, order.TradeNo),
		OccurredAt: common.GetTimestamp(), CreatedBy: request.ActorID,
		IdempotencyKey: request.PaymentProvider + ":refund:" + request.ProviderEventID,
	}
	if err := normalizeFinanceEntry(&ledger); err != nil {
		return err
	}
	if err := tx.Create(&ledger).Error; err != nil {
		return err
	}
	return tx.Create(&SubscriptionPaymentRefund{
		SubscriptionOrderID: order.Id, SubscriptionPaymentEventID: payment.Id,
		PaymentProvider: request.PaymentProvider, ProviderEventID: request.ProviderEventID,
		Currency: request.Currency, AmountMicros: request.AmountMicros, QuotaRevoked: quotaRevoked,
		FinanceLedgerEntryID: ledger.Id, CreatedTime: common.GetTimestamp(),
	}).Error
}
