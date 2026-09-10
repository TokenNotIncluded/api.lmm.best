// Copyright (C) 2026 LIghtJUNction
// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// recordWaffoPancakeSettlementTx records money, not access. The modern webhook
// has no cycle identity. SQL NULL preserves that fact and leaves the existing
// concrete-period uniqueness constraint intact for legacy inline evidence.
func recordWaffoPancakeSettlementTx(tx *gorm.DB, order *SubscriptionOrder, event *WaffoPancakeSubscriptionEvent) (bool, error) {
	var previous SubscriptionPaymentEvent
	err := tx.Where("payment_provider = ? AND provider_transaction_id = ?", PaymentProviderWaffoPancake, event.PaymentID).First(&previous).Error
	if err == nil {
		if previous.SubscriptionOrderId != order.Id || previous.SettlementCurrency != event.Currency || previous.SettlementAmountMicros != event.AmountMicros {
			return false, ErrPaymentEvidenceConflict
		}
		return false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	payment := SubscriptionPaymentEvent{
		SubscriptionOrderId: order.Id, PaymentProvider: PaymentProviderWaffoPancake,
		ProviderEventId: event.EventID, ProviderTransactionId: event.PaymentID,
		SettlementCurrency: event.Currency, SettlementAmountMicros: event.AmountMicros,
		PeriodStart: event.PeriodStart, PeriodEnd: event.PeriodEnd, CreatedTime: common.GetTimestamp(),
	}
	create := tx
	if event.PeriodEnd == 0 {
		create = tx.Omit("period_start", "period_end")
	}
	if err := create.Create(&payment).Error; err != nil {
		return false, err
	}
	if order.Status == common.TopUpStatusPending {
		if err := upsertSubscriptionTopUpTx(tx, order); err != nil {
			return false, err
		}
		order.Status = common.TopUpStatusSuccess
		order.CompleteTime = common.GetTimestamp()
		order.ProviderPayload = event.Payload
		if err := tx.Save(order).Error; err != nil {
			return false, err
		}
	}
	return true, nil
}

func waffoSubscriptionPlanSnapshot(order *SubscriptionOrder) (*SubscriptionPlan, error) {
	var plan SubscriptionPlan
	if err := json.Unmarshal([]byte(order.PlanSnapshot), &plan); err != nil {
		return nil, fmt.Errorf("decode frozen subscription plan: %w", err)
	}
	if plan.Id != order.PlanId || plan.PriceAmount != order.Money {
		return nil, ErrSubscriptionPlanChanged
	}
	plan.NormalizeDefaults()
	return &plan, nil
}

// applyWaffoPancakePeriodTx follows the provider's subscription domain event.
// Neither a guessed charge nor a delivered-at timestamp authorizes a grant.
func applyWaffoPancakePeriodTx(tx *gorm.DB, order *SubscriptionOrder, event *WaffoPancakeSubscriptionEvent, effects *subscriptionPaymentEffects) (bool, error) {
	if event.EventTimeMillis > 0 && event.PeriodStart > event.EventTimeMillis/1000 {
		return false, fmt.Errorf("%w: lifecycle grant starts after its signed event time", ErrPaymentEvidenceConflict)
	}
	if subscriptionProviderStateTerminal(order.ProviderSubscriptionState) {
		return false, nil
	}
	if event.EventTimeMillis > 0 && event.EventTimeMillis < order.ProviderEventTimeMillis {
		return false, nil
	}
	if event.PeriodEnd < order.CurrentPeriodEnd {
		return false, nil
	}
	if order.CurrentPeriodEnd == event.PeriodEnd && order.CurrentPeriodStart != event.PeriodStart {
		return false, ErrPaymentEvidenceConflict
	}
	if order.CurrentPeriodEnd > 0 && event.PeriodEnd > order.CurrentPeriodEnd && event.PeriodStart < order.CurrentPeriodEnd {
		return false, fmt.Errorf("%w: overlapping subscription lifecycle periods", ErrPaymentEvidenceConflict)
	}
	plan, err := waffoSubscriptionPlanSnapshot(order)
	if err != nil {
		return false, err
	}
	initial := order.UserSubscriptionId == 0
	newPeriod := event.PeriodEnd > order.CurrentPeriodEnd
	var subscription *UserSubscription
	var user *User
	if initial {
		var lockedUser User
		if err := lockForUpdate(tx).Where("id = ?", order.UserId).First(&lockedUser).Error; err != nil {
			return false, err
		}
		user = &lockedUser
		subscription, err = CreateUserSubscriptionFromPlanTx(tx, order.UserId, plan, "order")
		if err != nil {
			return false, err
		}
		order.UserSubscriptionId = subscription.Id
		effects.groupChanged = subscription.PrevUserGroup != ""
	} else {
		subscription, user, err = lockUserSubscriptionForMutationTx(tx, order.UserSubscriptionId, order.UserId)
		if err != nil {
			return false, err
		}
	}
	if !initial && !newPeriod && event.EventType != "subscription.recovered" {
		return false, nil
	}
	now := common.GetTimestamp()
	if initial || newPeriod {
		subscription.StartTime = event.PeriodStart
		subscription.EndTime = event.PeriodEnd
		subscription.AmountTotal = plan.TotalAmount
		subscription.AmountUsed = 0
		subscription.LastResetTime = event.PeriodStart
		subscription.NextResetTime = calcNextResetTime(time.Unix(event.PeriodStart, 0), plan, event.PeriodEnd)
		order.RefundedAmountMicros = 0
		order.RefundedQuota = 0
	}
	if event.PeriodEnd <= now {
		subscription.Status = "expired"
		subscription.NextResetTime = 0
	} else {
		subscription.Status = "active"
		if group := strings.TrimSpace(subscription.UpgradeGroup); group != "" && user.Group != group {
			if err := tx.Model(user).Update("group", group).Error; err != nil {
				return false, err
			}
			effects.groupChanged = true
		}
	}
	subscription.UpdatedAt = now
	if err := tx.Save(subscription).Error; err != nil {
		return false, err
	}
	if subscription.Status == "expired" {
		group, err := downgradeUserGroupForSubscriptionTx(tx, subscription, now)
		if err != nil {
			return false, err
		}
		effects.groupChanged = effects.groupChanged || group != ""
	}
	order.ProviderSubscriptionState = "active"
	order.CurrentPeriodStart = event.PeriodStart
	order.CurrentPeriodEnd = event.PeriodEnd
	if event.EventTimeMillis > order.ProviderEventTimeMillis {
		order.ProviderEventTimeMillis = event.EventTimeMillis
	}
	if err := tx.Save(order).Error; err != nil {
		return false, err
	}
	effects.logUserId = order.UserId
	// Do not publish a monetary renewal log for a lifecycle-only event.
	return initial || newPeriod, nil
}
