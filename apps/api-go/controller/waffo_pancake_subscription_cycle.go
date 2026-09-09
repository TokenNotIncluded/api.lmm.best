package controller

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
)

func isWaffoPancakeSubscriptionCycleEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "subscription.activated", "subscription.renewed", "subscription.recovered", "subscription.payment_succeeded":
		return true
	default:
		return false
	}
}

// recordWaffoPancakeSubscriptionCycle normalizes verified provider evidence.
// Since 2026-09-06 payment events carry paymentDate while activated/renewed
// carry the billing window. Never substitute the order's rolling latest period
// or the webhook delivery timestamp for the period of a particular payment.
func recordWaffoPancakeSubscriptionCycle(event *service.WaffoPancakeWebhookEvent, order *model.SubscriptionOrder, plan *model.SubscriptionPlan, payload string) (bool, error) {
	if event == nil || order == nil || !isWaffoPancakeSubscriptionCycleEvent(event.EventType) {
		return false, fmt.Errorf("missing subscription cycle event")
	}
	if order.ExpectedAmountMicros <= 0 || strings.TrimSpace(order.SettlementCurrency) == "" {
		return false, fmt.Errorf("recurring order has no immutable settlement snapshot")
	}
	if err := validateWaffoPancakeSubscriptionEvent(event, order, plan); err != nil {
		return false, err
	}
	if strings.TrimSpace(event.Data.OrderMerchantExternalID) != order.TradeNo ||
		strings.TrimSpace(event.Data.MerchantProvidedBuyerIdentity) != service.WaffoPancakeBuyerIdentityFromUserID(order.UserId) {
		return false, fmt.Errorf("subscription order or buyer identity mismatch")
	}
	eventType := strings.TrimSpace(event.EventType)
	isPayment := eventType == "subscription.payment_succeeded"
	hasInlinePeriod := strings.TrimSpace(event.Data.CurrentPeriodStart) != "" || strings.TrimSpace(event.Data.CurrentPeriodEnd) != ""
	periodStart, periodEnd, err := waffoPancakeSubscriptionPeriod(event, !isPayment || hasInlinePeriod)
	if err != nil {
		return false, err
	}
	paymentDate, err := parseWaffoPancakeTimestamp(event.Data.PaymentDate, "paymentDate", isPayment && !hasInlinePeriod)
	if err != nil {
		return false, err
	}
	billingPeriod := strings.ToLower(strings.TrimSpace(event.Data.BillingPeriod))
	if !isPayment || billingPeriod != "" {
		frozenPlan := *plan
		if snapshot := strings.TrimSpace(order.PlanSnapshot); snapshot != "" {
			if err := json.Unmarshal([]byte(snapshot), &frozenPlan); err != nil {
				return false, fmt.Errorf("decode subscription plan snapshot: %w", err)
			}
		}
		expectedPeriod, err := service.WaffoPancakeBillingPeriodForDuration(frozenPlan.DurationUnit, frozenPlan.DurationValue)
		if err != nil {
			return false, err
		}
		if billingPeriod != string(expectedPeriod) {
			return false, fmt.Errorf("subscription billingPeriod mismatch: expected=%q actual=%q", expectedPeriod, billingPeriod)
		}
	}
	amountMicros, err := monetaryStringToMicros(event.Data.Amount)
	if err != nil {
		return false, err
	}
	providerEventID := strings.TrimSpace(event.EventID)
	if providerEventID == "" && isPayment {
		// Legacy payloads lack eventId. paymentId is payment-specific; the
		// envelope id is the subscription order and repeats on every renewal.
		providerEventID = strings.TrimSpace(event.Data.PaymentID)
	}
	eventTimeMillis, err := waffoPancakeEventTimeMillis(event)
	if err != nil {
		return false, err
	}
	return model.RecordWaffoPancakeSubscriptionEvent(order.TradeNo, &model.WaffoPancakeSubscriptionEvent{
		EventTimeMillis: eventTimeMillis,
		EventID:         providerEventID,
		EventType:       eventType,
		ProviderOrderID: strings.TrimSpace(event.Data.OrderID),
		PaymentID:       strings.TrimSpace(event.Data.PaymentID),
		PaymentDate:     paymentDate,
		BillingPeriod:   billingPeriod,
		PeriodStart:     periodStart,
		PeriodEnd:       periodEnd,
		AmountMicros:    amountMicros,
		Currency:        strings.ToUpper(strings.TrimSpace(event.Data.Currency)),
		Payload:         payload,
	})
}

// The signed payload creation time orders lifecycle facts. A retry's signature
// timestamp or arrival time must never make an older state appear newer.
func waffoPancakeEventTimeMillis(event *service.WaffoPancakeWebhookEvent) (int64, error) {
	raw := strings.TrimSpace(event.Timestamp)
	if raw == "" {
		return 0, nil // Explicit legacy/unknown time; never synthesize one.
	}
	created, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || created.UnixMilli() <= 0 {
		return 0, fmt.Errorf("invalid signed subscription event timestamp")
	}
	return created.UnixMilli(), nil
}
