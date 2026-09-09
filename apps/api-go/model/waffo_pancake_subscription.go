package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// WaffoPancakeSubscriptionEvent contains signed, normalized evidence. Payment
// dates and lifecycle boundaries are provider timestamps, never receipt times.
type WaffoPancakeSubscriptionEvent struct {
	EventID, EventType, ProviderOrderID, PaymentID, BillingPeriod, Currency, Payload string
	PaymentDate, PeriodStart, PeriodEnd, AmountMicros                                int64
}

// WaffoPancakeSubscriptionPayment preserves payments that arrive before their
// lifecycle event. These receipts are permanent and never pruned by the
// short-lived refund.failed replay-guard cleanup.
type WaffoPancakeSubscriptionPayment struct {
	ID                  int    `gorm:"primaryKey"`
	SubscriptionOrderID int    `gorm:"not null;index"`
	EventID             string `gorm:"type:varchar(255);not null;uniqueIndex"`
	ProviderOrderID     string `gorm:"type:varchar(255);not null;index"`
	PaymentID           string `gorm:"type:varchar(255);not null;uniqueIndex"`
	Currency            string `gorm:"type:varchar(8);not null"`
	AmountMicros        int64  `gorm:"not null"`
	PaymentDate         int64  `gorm:"not null"`
	PeriodStart         int64  `gorm:"not null;default:0"`
	PeriodEnd           int64  `gorm:"not null;default:0"`
	Payload             string `gorm:"type:text;not null"`
	ReceivedAt          int64  `gorm:"not null"`
}

// WaffoPancakeSubscriptionPeriod is append-only lifecycle history. Multiple
// event IDs may attest the same period (including a legacy inline payment),
// but conflicting or overlapping boundaries cannot settle a payment.
type WaffoPancakeSubscriptionPeriod struct {
	ID                  int    `gorm:"primaryKey"`
	SubscriptionOrderID int    `gorm:"not null;index"`
	EventID             string `gorm:"type:varchar(255);not null;uniqueIndex"`
	EventType           string `gorm:"type:varchar(64);not null"`
	ProviderOrderID     string `gorm:"type:varchar(255);not null;index"`
	BillingPeriod       string `gorm:"type:varchar(32);not null"`
	Currency            string `gorm:"type:varchar(8);not null"`
	AmountMicros        int64  `gorm:"not null"`
	PeriodStart         int64  `gorm:"not null"`
	PeriodEnd           int64  `gorm:"not null;index"`
	Payload             string `gorm:"type:text;not null"`
	ReceivedAt          int64  `gorm:"not null"`
}

// RecordWaffoPancakeSubscriptionEvent persists either half of a settlement and
// reconciles all matching receipts under the order lock. Initial completion,
// entitlement mutation and settlement ledger insertion commit together. A
// false result means no new pair settled; the accepted evidence is still durable.
func RecordWaffoPancakeSubscriptionEvent(tradeNo string, event *WaffoPancakeSubscriptionEvent) (bool, error) {
	if event == nil || strings.TrimSpace(tradeNo) == "" {
		return false, gorm.ErrInvalidData
	}
	evidence := *event
	evidence.EventID = strings.TrimSpace(evidence.EventID)
	evidence.ProviderOrderID = strings.TrimSpace(evidence.ProviderOrderID)
	evidence.PaymentID = strings.TrimSpace(evidence.PaymentID)
	evidence.Currency = strings.ToUpper(strings.TrimSpace(evidence.Currency))
	if evidence.EventID == "" || evidence.ProviderOrderID == "" || evidence.Currency == "" || evidence.AmountMicros <= 0 {
		return false, gorm.ErrInvalidData
	}
	isPayment := evidence.EventType == "subscription.payment_succeeded"
	legacyPeriod := evidence.PeriodStart > 0 && evidence.PeriodEnd > evidence.PeriodStart
	if isPayment {
		if evidence.PaymentID == "" || (evidence.PaymentDate <= 0 && !legacyPeriod) ||
			((evidence.PeriodStart != 0 || evidence.PeriodEnd != 0) && !legacyPeriod) {
			return false, gorm.ErrInvalidData
		}
	} else if (evidence.EventType != "subscription.activated" && evidence.EventType != "subscription.renewed") ||
		!legacyPeriod || strings.TrimSpace(evidence.BillingPeriod) == "" {
		return false, gorm.ErrInvalidData
	}

	settled := false
	var completion subscriptionCompletionEffects
	var paymentEffects []subscriptionPaymentEffects
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Match completion's plan -> order -> user -> subscription lock order.
		var orderRef SubscriptionOrder
		if err := tx.Select("plan_id", "status").Where("trade_no = ?", tradeNo).First(&orderRef).Error; err != nil {
			return subscriptionOrderLookupError(err)
		}
		if orderRef.Status == common.TopUpStatusPending {
			if _, err := getSubscriptionPlanForPersistenceTx(tx, orderRef.PlanId); err != nil {
				return err
			}
		}
		var order SubscriptionOrder
		if err := lockForUpdate(tx).Where("trade_no = ?", tradeNo).First(&order).Error; err != nil {
			return subscriptionOrderLookupError(err)
		}
		if order.PaymentProvider != PaymentProviderWaffoPancake {
			return ErrPaymentMethodMismatch
		}
		if order.PlanId != orderRef.PlanId {
			return ErrSubscriptionPlanChanged
		}
		if order.Status != common.TopUpStatusPending && order.Status != common.TopUpStatusSuccess {
			return ErrSubscriptionOrderStatusInvalid
		}
		if order.ExpectedAmountMicros <= 0 || order.ExpectedAmountMicros != evidence.AmountMicros ||
			!strings.EqualFold(strings.TrimSpace(order.SettlementCurrency), evidence.Currency) ||
			(order.ProviderSubscriptionId != "" && order.ProviderSubscriptionId != evidence.ProviderOrderID) {
			return fmt.Errorf("%w: subscription provider/order/amount mismatch", ErrPaymentEvidenceConflict)
		}
		if order.ProviderSubscriptionId == "" {
			if err := tx.Model(&order).Update("provider_subscription_id", evidence.ProviderOrderID).Error; err != nil {
				return err
			}
		}
		if isPayment {
			if err := recordWaffoPancakePaymentTx(tx, order.Id, &evidence); err != nil {
				return err
			}
		}
		if !isPayment || legacyPeriod {
			if err := recordWaffoPancakePeriodTx(tx, order.Id, &evidence); err != nil {
				return err
			}
		}

		var pending []WaffoPancakeSubscriptionPayment
		if err := tx.Where("subscription_order_id = ? AND NOT EXISTS (SELECT 1 FROM subscription_payment_events AS settled WHERE settled.payment_provider = ? AND settled.provider_transaction_id = waffo_pancake_subscription_payments.payment_id)", order.Id, PaymentProviderWaffoPancake).
			Order("payment_date ASC, id ASC").Find(&pending).Error; err != nil {
			return err
		}
		var periods []WaffoPancakeSubscriptionPeriod
		if err := tx.Where("subscription_order_id = ? AND provider_order_id = ?", order.Id, evidence.ProviderOrderID).
			Order("period_end ASC, id ASC").Find(&periods).Error; err != nil {
			return err
		}
		for _, payment := range pending {
			period, err := matchWaffoPancakePeriod(&payment, periods)
			if err != nil {
				return err
			}
			if period == nil {
				continue
			}
			// A cancellation received before the initial pair must not be
			// undone by delayed activation/payment delivery.
			if order.Status == common.TopUpStatusPending && subscriptionProviderStateTerminal(order.ProviderSubscriptionState) {
				continue
			}
			if err := completeSubscriptionOrderTx(tx, tradeNo, payment.Payload, PaymentProviderWaffoPancake, "", &completion); err != nil {
				return err
			}
			ledger := &SubscriptionPaymentEvent{
				PaymentProvider: PaymentProviderWaffoPancake, ProviderEventId: payment.EventID,
				ProviderTransactionId: payment.PaymentID, SettlementCurrency: payment.Currency,
				SettlementAmountMicros: payment.AmountMicros, PeriodStart: period.PeriodStart, PeriodEnd: period.PeriodEnd,
			}
			var effects subscriptionPaymentEffects
			if err := applySubscriptionPaymentEventTx(tx, tradeNo, ledger, payment.ProviderOrderID, "active", period.EventType == "subscription.renewed", &effects); err != nil {
				return err
			}
			paymentEffects = append(paymentEffects, effects)
			settled = true
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	completion.publish()
	for _, effects := range paymentEffects {
		effects.publish()
	}
	return settled, nil
}

func subscriptionOrderLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrSubscriptionOrderNotFound
	}
	return err
}

func subscriptionProviderStateTerminal(state string) bool {
	return state == "canceled" || state == "expired" || state == "paused" || state == "unpaid"
}

func recordWaffoPancakePaymentTx(tx *gorm.DB, orderID int, event *WaffoPancakeSubscriptionEvent) error {
	// A payment settled before this inbox existed still owns its provider
	// transaction/event IDs. Validate that ledger before treating it as done.
	var settled []SubscriptionPaymentEvent
	if err := tx.Where("provider_event_id = ? OR (payment_provider = ? AND provider_transaction_id = ?)", event.EventID, PaymentProviderWaffoPancake, event.PaymentID).Find(&settled).Error; err != nil {
		return err
	}
	for _, payment := range settled {
		if payment.SubscriptionOrderId != orderID || payment.PaymentProvider != PaymentProviderWaffoPancake ||
			payment.ProviderTransactionId != event.PaymentID || payment.SettlementCurrency != event.Currency || payment.SettlementAmountMicros != event.AmountMicros ||
			(event.PeriodEnd == 0 && event.PaymentDate > 0 && (event.PaymentDate < payment.PeriodStart || event.PaymentDate >= payment.PeriodEnd)) ||
			(event.PeriodEnd > 0 && (event.PeriodStart != payment.PeriodStart || event.PeriodEnd != payment.PeriodEnd)) {
			return fmt.Errorf("%w: conflicting existing subscription settlement", ErrPaymentEvidenceConflict)
		}
	}
	var lifecycleCount int64
	if err := tx.Model(&WaffoPancakeSubscriptionPeriod{}).Where("event_id = ? AND event_type <> ?", event.EventID, event.EventType).Count(&lifecycleCount).Error; err != nil {
		return err
	}
	if lifecycleCount > 0 {
		return fmt.Errorf("%w: subscription event ID reused across event types", ErrPaymentEvidenceConflict)
	}
	var previous []WaffoPancakeSubscriptionPayment
	if err := tx.Where("event_id = ? OR payment_id = ?", event.EventID, event.PaymentID).Find(&previous).Error; err != nil {
		return err
	}
	for _, payment := range previous {
		if payment.SubscriptionOrderID != orderID || payment.ProviderOrderID != event.ProviderOrderID ||
			payment.PaymentID != event.PaymentID || payment.Currency != event.Currency || payment.AmountMicros != event.AmountMicros ||
			payment.PaymentDate != event.PaymentDate || payment.PeriodStart != event.PeriodStart || payment.PeriodEnd != event.PeriodEnd {
			return fmt.Errorf("%w: conflicting subscription payment receipt", ErrPaymentEvidenceConflict)
		}
	}
	if len(previous) > 0 {
		return nil
	}
	return tx.Create(&WaffoPancakeSubscriptionPayment{
		SubscriptionOrderID: orderID, EventID: event.EventID, ProviderOrderID: event.ProviderOrderID,
		PaymentID: event.PaymentID, Currency: event.Currency, AmountMicros: event.AmountMicros,
		PaymentDate: event.PaymentDate, PeriodStart: event.PeriodStart, PeriodEnd: event.PeriodEnd,
		Payload: event.Payload, ReceivedAt: common.GetTimestamp(),
	}).Error
}

func recordWaffoPancakePeriodTx(tx *gorm.DB, orderID int, event *WaffoPancakeSubscriptionEvent) error {
	if event.EventType != "subscription.payment_succeeded" {
		var paymentCount int64
		if err := tx.Model(&WaffoPancakeSubscriptionPayment{}).Where("event_id = ?", event.EventID).Count(&paymentCount).Error; err != nil {
			return err
		}
		if paymentCount > 0 {
			return fmt.Errorf("%w: subscription event ID reused across event types", ErrPaymentEvidenceConflict)
		}
	}
	var previous WaffoPancakeSubscriptionPeriod
	err := tx.Where("event_id = ?", event.EventID).First(&previous).Error
	if err == nil {
		if previous.SubscriptionOrderID != orderID || previous.ProviderOrderID != event.ProviderOrderID ||
			previous.EventType != event.EventType || previous.BillingPeriod != event.BillingPeriod ||
			previous.Currency != event.Currency || previous.AmountMicros != event.AmountMicros ||
			previous.PeriodStart != event.PeriodStart || previous.PeriodEnd != event.PeriodEnd {
			return fmt.Errorf("%w: conflicting subscription lifecycle receipt", ErrPaymentEvidenceConflict)
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return tx.Create(&WaffoPancakeSubscriptionPeriod{
		SubscriptionOrderID: orderID, EventID: event.EventID, EventType: event.EventType,
		ProviderOrderID: event.ProviderOrderID, BillingPeriod: event.BillingPeriod,
		Currency: event.Currency, AmountMicros: event.AmountMicros,
		PeriodStart: event.PeriodStart, PeriodEnd: event.PeriodEnd,
		Payload: event.Payload, ReceivedAt: common.GetTimestamp(),
	}).Error
}

func matchWaffoPancakePeriod(payment *WaffoPancakeSubscriptionPayment, periods []WaffoPancakeSubscriptionPeriod) (*WaffoPancakeSubscriptionPeriod, error) {
	var matched *WaffoPancakeSubscriptionPeriod
	for i := range periods {
		period := &periods[i]
		if period.ProviderOrderID != payment.ProviderOrderID || period.Currency != payment.Currency || period.AmountMicros != payment.AmountMicros {
			continue
		}
		if payment.PeriodEnd > payment.PeriodStart {
			if period.PeriodStart != payment.PeriodStart || period.PeriodEnd != payment.PeriodEnd {
				continue
			}
		} else if payment.PaymentDate < period.PeriodStart || payment.PaymentDate >= period.PeriodEnd {
			continue
		}
		if matched != nil && (matched.PeriodStart != period.PeriodStart || matched.PeriodEnd != period.PeriodEnd) {
			return nil, fmt.Errorf("%w: ambiguous subscription payment period", ErrPaymentEvidenceConflict)
		}
		matched = period
	}
	return matched, nil
}
