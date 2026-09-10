// Copyright (C) 2026 LIghtJUNction
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.

package model

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestApplySubscriptionPaymentRefundHistoricalPaymentLeavesCurrentEntitlementUntouched(t *testing.T) {
	db := setupSubscriptionPaymentRefundTestDB(t)
	fixture := createSubscriptionPaymentRefundFixture(t, db, "one")
	fixture.apply(t, fixture.currentPayment, "current-partial", 250_000, 25_000)
	before := readSubscriptionPaymentRefundState(t, db)

	// The old receipt paid twice as much as the current one. Neither the
	// current receipt's amount nor its existing refund counters may cap it.
	fixture.apply(t, fixture.oldPayment, "old-partial", 1_500_000, 0)
	fixture.apply(t, fixture.oldPayment, "old-remainder", 500_000, 0)
	after := readSubscriptionPaymentRefundState(t, db)
	assertSubscriptionPaymentRefundEntitlementUnchanged(t, before, after)
	require.Len(t, after.refunds, 3)
	require.Len(t, after.ledger, 3)

	request := fixture.request(fixture.oldPayment, "old-overrefund", 1)
	result, err := ApplySubscriptionPaymentRefund(request)
	require.Error(t, err, "historical payments still have their own cumulative refund cap")
	assert.Zero(t, result)
	assert.Equal(t, after, readSubscriptionPaymentRefundState(t, db))
}

func TestApplySubscriptionPaymentRefundUnprovenPeriodNeverRevokesAccess(t *testing.T) {
	for _, unknownPeriod := range []bool{false, true} {
		t.Run(fmt.Sprintf("unbound_%t", unknownPeriod), func(t *testing.T) {
			db := setupSubscriptionPaymentRefundTestDB(t)
			fixture := createSubscriptionPaymentRefundFixture(t, db, "unbound")
			require.NoError(t, db.Model(&WaffoPancakeSubscriptionPayment{}).
				Where("payment_id = ?", fixture.currentPayment.ProviderTransactionId).
				Updates(map[string]interface{}{"period_start": 0, "period_end": 0}).Error)
			if unknownPeriod {
				require.NoError(t, db.Model(&SubscriptionPaymentEvent{}).Where("id = ?", fixture.currentPayment.Id).
					Updates(map[string]interface{}{"period_start": nil, "period_end": nil}).Error)
				fixture.currentPayment.PeriodStart, fixture.currentPayment.PeriodEnd = 0, 0
			}
			before := readSubscriptionPaymentRefundState(t, db)
			fixture.apply(t, fixture.currentPayment, "financial-only", 1_000_000, 0)
			after := readSubscriptionPaymentRefundState(t, db)
			assertSubscriptionPaymentRefundEntitlementUnchanged(t, before, after)
			assert.Equal(t, before.payments, after.payments)
			assert.Equal(t, before.users, after.users)
		})
	}
}

func TestApplySubscriptionPaymentRefundCurrentPartialsUsePerPaymentCumulativeCap(t *testing.T) {
	db := setupSubscriptionPaymentRefundTestDB(t)
	fixture := createSubscriptionPaymentRefundFixture(t, db, "one")
	before := readSubscriptionPaymentRefundState(t, db)

	for index := 1; index <= 2; index++ {
		fixture.apply(t, fixture.currentPayment, fmt.Sprintf("partial-%d", index), 250_000, 25_000)
		state := readSubscriptionPaymentRefundState(t, db)
		assert.Equal(t, int64(100_000-index*25_000), state.subscriptions[0].AmountTotal)
		assert.Zero(t, state.subscriptions[0].AmountUsed)
		assert.Equal(t, int64(index*250_000), state.orders[0].RefundedAmountMicros)
		assert.Equal(t, int64(index*25_000), state.orders[0].RefundedQuota)
		assert.Equal(t, before.users, state.users, "subscription refunds must not touch the wallet")
	}

	partialState := readSubscriptionPaymentRefundState(t, db)
	result, err := ApplySubscriptionPaymentRefund(fixture.request(fixture.currentPayment, "over-cap", 500_001))
	require.Error(t, err)
	assert.Zero(t, result)
	assert.Equal(t, partialState, readSubscriptionPaymentRefundState(t, db), "overrefund must not consume its event ID")

	fixture.apply(t, fixture.currentPayment, "remainder", 500_000, 50_000)
	fullyRefunded := readSubscriptionPaymentRefundState(t, db)
	assert.Zero(t, fullyRefunded.subscriptions[0].AmountTotal)
	assert.Equal(t, int64(1_000_000), fullyRefunded.orders[0].RefundedAmountMicros)
	assert.Equal(t, int64(100_000), fullyRefunded.orders[0].RefundedQuota)
	assert.Equal(t, before.users, fullyRefunded.users)
	assert.Equal(t, before.payments, fullyRefunded.payments, "settlement receipts are immutable")
	assert.Equal(t, before.topUps, fullyRefunded.topUps, "the mirrored order must not receive a wallet refund")
	require.Len(t, fullyRefunded.refunds, 3)
	require.Len(t, fullyRefunded.ledger, 3)

	result, err = ApplySubscriptionPaymentRefund(fixture.request(fixture.currentPayment, "one-micro-too-many", 1))
	require.Error(t, err)
	assert.Zero(t, result)
	assert.Equal(t, fullyRefunded, readSubscriptionPaymentRefundState(t, db))
}

func TestApplySubscriptionPaymentRefundDoesNotRevokeConsumedAllowance(t *testing.T) {
	db := setupSubscriptionPaymentRefundTestDB(t)
	fixture := createSubscriptionPaymentRefundFixture(t, db, "one")
	require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", fixture.subscription.Id).
		Update("amount_used", 90_000).Error)
	before := readSubscriptionPaymentRefundState(t, db)

	// Half of the purchased grant is 50,000, but only 10,000 remains unused.
	// Persist the amount actually revoked, not an imaginary wallet debt.
	fixture.apply(t, fixture.currentPayment, "mostly-consumed", 500_000, 10_000)
	fixture.apply(t, fixture.currentPayment, "consumed-remainder", 500_000, 0)
	after := readSubscriptionPaymentRefundState(t, db)
	assert.Equal(t, int64(90_000), after.subscriptions[0].AmountTotal)
	assert.Equal(t, int64(90_000), after.subscriptions[0].AmountUsed)
	assert.Equal(t, int64(1_000_000), after.orders[0].RefundedAmountMicros)
	assert.Equal(t, int64(10_000), after.orders[0].RefundedQuota)
	assert.Equal(t, before.users, after.users)
	assert.Equal(t, before.payments, after.payments)
	assert.Equal(t, before.topUps, after.topUps)
	require.Len(t, after.refunds, 2)
	require.Len(t, after.ledger, 2)
}

func TestApplySubscriptionPaymentRefundReplayAfterRenewalUsesImmutableBinding(t *testing.T) {
	for _, receiptOffset := range []int64{-60, 0, 60} {
		t.Run(fmt.Sprintf("renewal_receipt_offset_%d", receiptOffset), func(t *testing.T) {
			db := setupSubscriptionPaymentRefundTestDB(t)
			fixture := createSubscriptionPaymentRefundFixture(t, db, "one")
			request := fixture.apply(t, fixture.currentPayment, "before-renewal", 250_000, 25_000)
			refunded := readSubscriptionPaymentRefundState(t, db)

			// Model an already committed renewal without testing the separate
			// lifecycle handler. Delivery timestamps cannot identify its payment.
			nextPayment := fixture.currentPayment
			nextPayment.Id = 0
			nextPayment.ProviderEventId = "EVT_one_next"
			nextPayment.ProviderTransactionId = "PAY_one_next"
			nextPayment.PeriodStart = fixture.currentPayment.PeriodEnd
			nextPayment.PeriodEnd = nextPayment.PeriodStart + 7_200
			nextPayment.CreatedTime = refunded.ledger[0].OccurredAt + receiptOffset
			require.NoError(t, db.Create(&nextPayment).Error)
			require.NoError(t, db.Model(&SubscriptionOrder{}).Where("id = ?", fixture.order.Id).
				Updates(map[string]interface{}{
					"current_period_start":   nextPayment.PeriodStart,
					"current_period_end":     nextPayment.PeriodEnd,
					"refunded_amount_micros": 0,
					"refunded_quota":         0,
				}).Error)
			require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", fixture.subscription.Id).
				Updates(map[string]interface{}{
					"amount_total": 100_000,
					"amount_used":  0,
					"status":       "active",
					"start_time":   nextPayment.PeriodStart,
					"end_time":     nextPayment.PeriodEnd,
				}).Error)
			beforeReplay := readSubscriptionPaymentRefundState(t, db)

			for attempt := 0; attempt < 2; attempt++ {
				result, err := ApplySubscriptionPaymentRefund(request)
				require.NoError(t, err)
				assert.False(t, result.Created)
				assert.Zero(t, result.QuotaDebited)
				assert.Equal(t, fixture.user.Id, result.UserID)
				assert.Equal(t, beforeReplay, readSubscriptionPaymentRefundState(t, db),
					"a replay must not rewrite either durable row or debit the new grant")
			}

			fixture.apply(t, fixture.currentPayment, "another-old-refund", 250_000, 0)
			assertSubscriptionPaymentRefundEntitlementUnchanged(t, beforeReplay, readSubscriptionPaymentRefundState(t, db))
		})
	}
}

func TestApplySubscriptionPaymentRefundRejectsSwitchedRefundIdentity(t *testing.T) {
	db := setupSubscriptionPaymentRefundTestDB(t)
	first := createSubscriptionPaymentRefundFixture(t, db, "one")
	second := createSubscriptionPaymentRefundFixture(t, db, "two")
	original := first.apply(t, first.currentPayment, "bound-refund", 250_000, 25_000)
	before := readSubscriptionPaymentRefundState(t, db)

	cases := []struct {
		name   string
		change func(*SubscriptionPaymentRefundRequest)
	}{
		{"another_payment_same_order", func(request *SubscriptionPaymentRefundRequest) {
			request.ProviderTransactionID = first.oldPayment.ProviderTransactionId
		}},
		{"another_order_and_payment", func(request *SubscriptionPaymentRefundRequest) {
			request.TradeNo = second.order.TradeNo
			request.ProviderTransactionID = second.currentPayment.ProviderTransactionId
			request.ActorID = second.user.Id
		}},
		{"another_order_original_payment", func(request *SubscriptionPaymentRefundRequest) {
			request.TradeNo = second.order.TradeNo
		}},
		{"changed_amount", func(request *SubscriptionPaymentRefundRequest) {
			request.AmountMicros = 125_000
		}},
		{"changed_currency", func(request *SubscriptionPaymentRefundRequest) {
			request.Currency = "CNY"
		}},
		{"changed_provider", func(request *SubscriptionPaymentRefundRequest) {
			request.PaymentProvider = "stripe"
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := original
			testCase.change(&request)
			result, err := ApplySubscriptionPaymentRefund(request)
			require.Error(t, err)
			assert.Zero(t, result)
			assert.Equal(t, before, readSubscriptionPaymentRefundState(t, db), "identity conflicts must leave both owners untouched")
		})
	}

	result, err := ApplySubscriptionPaymentRefund(original)
	require.NoError(t, err)
	assert.False(t, result.Created)
	assert.Zero(t, result.QuotaDebited)
	assert.Equal(t, before, readSubscriptionPaymentRefundState(t, db))
}

func TestApplySubscriptionPaymentRefundRejectsMissingOrUnknownPaymentIdentity(t *testing.T) {
	db := setupSubscriptionPaymentRefundTestDB(t)
	fixture := createSubscriptionPaymentRefundFixture(t, db, "one")
	other := createSubscriptionPaymentRefundFixture(t, db, "two")
	before := readSubscriptionPaymentRefundState(t, db)
	cases := []struct {
		name      string
		paymentID string
	}{
		{"missing", ""},
		{"blank", "   "},
		{"unknown", "PAY_not_recorded"},
		{"subscription_order_is_not_a_payment", fixture.order.ProviderSubscriptionId},
		{"other_orders_payment", other.currentPayment.ProviderTransactionId},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := fixture.request(fixture.currentPayment, "missing-identity", 250_000)
			request.ProviderTransactionID = testCase.paymentID
			result, err := ApplySubscriptionPaymentRefund(request)
			require.Error(t, err, "never infer a refund payment from the latest order receipt")
			assert.Zero(t, result)
			assert.Equal(t, before, readSubscriptionPaymentRefundState(t, db))
		})
	}
	fixture.apply(t, fixture.currentPayment, "missing-identity", 250_000, 25_000)
}

func TestSubscriptionRefundNeedsPaymentIdentityFailsClosed(t *testing.T) {
	oneTimeSnapshot := common.GetJsonString(SubscriptionPlan{TotalAmount: 100_000, WaffoPancakeProductType: "one_time"})
	cases := []struct {
		name     string
		order    *SubscriptionOrder
		required bool
	}{
		{"missing_order", nil, true},
		{"missing_snapshot", &SubscriptionOrder{}, true},
		{"empty_snapshot", &SubscriptionOrder{PlanSnapshot: `{}`}, true},
		{"null_snapshot", &SubscriptionOrder{PlanSnapshot: `null`}, true},
		{"malformed_snapshot", &SubscriptionOrder{PlanSnapshot: `{`}, true},
		{"unknown_product", &SubscriptionOrder{PlanSnapshot: `{"waffo_pancake_product_type":"unknown"}`}, true},
		{"recurring_snapshot", &SubscriptionOrder{PlanSnapshot: `{"waffo_pancake_product_type":"subscription"}`}, true},
		{"one_time_with_provider_subscription", &SubscriptionOrder{PlanSnapshot: oneTimeSnapshot, ProviderSubscriptionId: "ORD_recurring"}, true},
		{"one_time_with_period_start", &SubscriptionOrder{PlanSnapshot: oneTimeSnapshot, CurrentPeriodStart: 1}, true},
		{"one_time_with_period_end", &SubscriptionOrder{PlanSnapshot: oneTimeSnapshot, CurrentPeriodEnd: 2}, true},
		{"explicit_frozen_one_time", &SubscriptionOrder{PlanSnapshot: oneTimeSnapshot}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.required, SubscriptionRefundNeedsPaymentIdentity(testCase.order))
		})
	}
}

func TestApplySubscriptionPaymentRefundCannotBypassIdentityThroughGenericRefunds(t *testing.T) {
	cases := []struct {
		name                string
		snapshot            string
		clearRecurringState bool
	}{
		{"recurring", `{"total_amount":100000,"waffo_pancake_product_type":"subscription"}`, false},
		{"missing_snapshot_and_state", "", true},
		{"unknown_product_and_state", `{"total_amount":100000,"waffo_pancake_product_type":"unknown"}`, true},
		{"subscription_snapshot_without_state", `{"total_amount":100000,"waffo_pancake_product_type":"subscription"}`, true},
		{"one_time_label_with_recurring_state", `{"total_amount":100000,"waffo_pancake_product_type":"one_time"}`, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			db := setupSubscriptionPaymentRefundTestDB(t)
			fixture := createSubscriptionPaymentRefundFixture(t, db, "one")
			updates := map[string]interface{}{"plan_snapshot": testCase.snapshot}
			if testCase.clearRecurringState {
				updates["provider_subscription_id"] = ""
				updates["provider_subscription_state"] = ""
				updates["current_period_start"] = 0
				updates["current_period_end"] = 0
			}
			require.NoError(t, db.Model(&SubscriptionOrder{}).Where("id = ?", fixture.order.Id).Updates(updates).Error)
			before := readSubscriptionPaymentRefundState(t, db)
			entryPoints := []struct {
				name  string
				apply func(string, bool, int64, string, string, string, string, string, int) (PaymentRefundResult, error)
			}{
				{"generic", ApplyPaymentRefund},
				{"waffo_wrapper", ApplyWaffoPancakeRefund},
			}
			for _, entryPoint := range entryPoints {
				t.Run(entryPoint.name, func(t *testing.T) {
					result, err := entryPoint.apply(
						fixture.order.TradeNo, true, 250_000, FinanceCurrencyUSD,
						"REF_generic-bypass", fixture.order.PaymentMethod, fixture.order.PaymentProvider,
						"refund without original payment ID", fixture.user.Id,
					)
					require.ErrorIs(t, err, ErrSubscriptionRefundPaymentRequired)
					assert.Zero(t, result)
					assert.Equal(t, before, readSubscriptionPaymentRefundState(t, db),
						"the generic APIs must not bypass immutable payment identity")
				})
			}
		})
	}
}

func TestApplySubscriptionPaymentRefundRejectsMirroredTopUpBypass(t *testing.T) {
	entryPoints := []struct {
		name  string
		apply func(string, bool, int64, string, string, string, string, string, int) (PaymentRefundResult, error)
	}{
		{"generic", ApplyPaymentRefund},
		{"waffo_wrapper", ApplyWaffoPancakeRefund},
	}
	for _, entryPoint := range entryPoints {
		t.Run(entryPoint.name, func(t *testing.T) {
			db := setupSubscriptionPaymentRefundTestDB(t)
			fixture := createSubscriptionPaymentRefundFixture(t, db, "one")
			before := readSubscriptionPaymentRefundState(t, db)
			// Subscription orders have a TopUp mirror for order history. A
			// caller's classification flag cannot turn it into an unbound refund.
			result, err := entryPoint.apply(
				fixture.order.TradeNo, false, 250_000, FinanceCurrencyUSD,
				"REF_mirror-bypass", fixture.order.PaymentMethod, fixture.order.PaymentProvider,
				"misclassified subscription refund", fixture.user.Id,
			)
			require.ErrorIs(t, err, ErrPaymentRefundOrderConflict)
			assert.Zero(t, result)
			assert.Equal(t, before, readSubscriptionPaymentRefundState(t, db))
		})
	}
}

func TestApplySubscriptionPaymentRefundRejectsInvalidEvidenceWithoutWrites(t *testing.T) {
	db := setupSubscriptionPaymentRefundTestDB(t)
	fixture := createSubscriptionPaymentRefundFixture(t, db, "one")
	before := readSubscriptionPaymentRefundState(t, db)
	cases := []struct {
		name   string
		change func(*SubscriptionPaymentRefundRequest)
	}{
		{"wrong_currency", func(request *SubscriptionPaymentRefundRequest) { request.Currency = "CNY" }},
		{"missing_currency", func(request *SubscriptionPaymentRefundRequest) { request.Currency = "" }},
		{"wrong_provider", func(request *SubscriptionPaymentRefundRequest) { request.PaymentProvider = "stripe" }},
		{"wrong_method", func(request *SubscriptionPaymentRefundRequest) { request.PaymentMethod = "stripe" }},
		{"zero_amount", func(request *SubscriptionPaymentRefundRequest) { request.AmountMicros = 0 }},
		{"negative_amount", func(request *SubscriptionPaymentRefundRequest) { request.AmountMicros = -1 }},
		{"above_payment_amount", func(request *SubscriptionPaymentRefundRequest) { request.AmountMicros = 1_000_001 }},
		{"unsafe_amount", func(request *SubscriptionPaymentRefundRequest) { request.AmountMicros = 9_000_000_000_000_001 }},
		{"missing_refund_id", func(request *SubscriptionPaymentRefundRequest) { request.ProviderEventID = "" }},
		{"missing_provider", func(request *SubscriptionPaymentRefundRequest) { request.PaymentProvider = "" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := fixture.request(fixture.currentPayment, "invalid-evidence", 250_000)
			testCase.change(&request)
			result, err := ApplySubscriptionPaymentRefund(request)
			require.Error(t, err)
			assert.Zero(t, result)
			assert.Equal(t, before, readSubscriptionPaymentRefundState(t, db))
		})
	}
	fixture.apply(t, fixture.currentPayment, "invalid-evidence", 250_000, 25_000)
}

func TestApplySubscriptionPaymentRefundRejectsAmbiguousLegacyLedgerWithoutBackfill(t *testing.T) {
	for _, receiptOffset := range []int64{-60, 0, 60} {
		t.Run(fmt.Sprintf("legacy_receipt_offset_%d", receiptOffset), func(t *testing.T) {
			db := setupSubscriptionPaymentRefundTestDB(t)
			fixture := createSubscriptionPaymentRefundFixture(t, db, "one")
			legacyEventID := "REF_legacy-unbound"
			legacyTime := fixture.currentPayment.CreatedTime + receiptOffset
			legacy := FinanceLedgerEntry{
				EntryType: FinanceEntryRevenue, Category: FinanceSourceRefund,
				AmountMicros: 250_000, Currency: FinanceCurrencyUSD, Direction: FinanceDirectionDebit,
				PaymentMethod: fixture.order.PaymentMethod, PaymentProvider: fixture.order.PaymentProvider,
				UserId: &fixture.user.Id, SourceType: FinanceSourceRefund, SourceId: legacyEventID,
				Note: "trade_no=" + fixture.order.TradeNo, OccurredAt: legacyTime, CreatedAt: legacyTime,
				CreatedBy: fixture.user.Id, IdempotencyKey: fixture.order.PaymentProvider + ":refund:" + legacyEventID,
			}
			require.NoError(t, db.Create(&legacy).Error)
			before := readSubscriptionPaymentRefundState(t, db)
			require.Empty(t, before.refunds)

			// A ledger-only refund cannot be assigned to either cycle using its
			// timestamp. Even a new refund needs reconciliation of the old cap.
			for _, payment := range []SubscriptionPaymentEvent{fixture.oldPayment, fixture.currentPayment} {
				for _, eventID := range []string{"legacy-unbound", "new-with-legacy-present"} {
					request := fixture.request(payment, eventID, 250_000)
					result, err := ApplySubscriptionPaymentRefund(request)
					require.ErrorIs(t, err, ErrSubscriptionRefundReconciliationRequired, "an unbound legacy refund requires manual reconciliation")
					assert.Zero(t, result)
					assert.Equal(t, before, readSubscriptionPaymentRefundState(t, db),
						"do not guess a payment, backfill quota, or rewrite the append-only ledger")
				}
			}
		})
	}
}

func TestApplySubscriptionPaymentRefundRollsBackFinanceAndBindingTogether(t *testing.T) {
	for _, modelName := range []string{"FinanceLedgerEntry", "SubscriptionPaymentRefund"} {
		t.Run(modelName, func(t *testing.T) {
			db := setupSubscriptionPaymentRefundTestDB(t)
			fixture := createSubscriptionPaymentRefundFixture(t, db, "one")
			// Fail either half of the durable pair, independently of insertion
			// order, and require every entitlement/counter write to roll back.
			if modelName == "FinanceLedgerEntry" {
				require.NoError(t, db.Exec(`CREATE TRIGGER subscription_refund_insert_failure
					BEFORE INSERT ON finance_ledger_entries BEGIN
					SELECT RAISE(ABORT, 'forced_subscription_refund_insert_failure'); END`).Error)
			} else {
				require.NoError(t, db.Exec(`CREATE TRIGGER subscription_refund_insert_failure
					BEFORE INSERT ON subscription_payment_refunds BEGIN
					SELECT RAISE(ABORT, 'forced_subscription_refund_insert_failure'); END`).Error)
			}
			before := readSubscriptionPaymentRefundState(t, db)
			request := fixture.request(fixture.currentPayment, "retry-after-rollback", 250_000)
			result, err := ApplySubscriptionPaymentRefund(request)
			require.ErrorContains(t, err, "forced_subscription_refund_insert_failure")
			assert.Zero(t, result)
			assert.Equal(t, before, readSubscriptionPaymentRefundState(t, db))

			require.NoError(t, db.Exec("DROP TRIGGER subscription_refund_insert_failure").Error)
			fixture.apply(t, fixture.currentPayment, "retry-after-rollback", 250_000, 25_000)
			after := readSubscriptionPaymentRefundState(t, db)
			require.Len(t, after.refunds, 1)
			require.Len(t, after.ledger, 1)
			assert.Equal(t, int64(75_000), after.subscriptions[0].AmountTotal)
			assert.Equal(t, int64(250_000), after.orders[0].RefundedAmountMicros)
			assert.Equal(t, int64(25_000), after.orders[0].RefundedQuota)
			assert.Equal(t, before.users, after.users)
			assert.Equal(t, before.payments, after.payments)
			assert.Equal(t, before.topUps, after.topUps)
		})
	}
}

type subscriptionPaymentRefundFixture struct {
	db             *gorm.DB
	user           User
	subscription   UserSubscription
	order          SubscriptionOrder
	oldPayment     SubscriptionPaymentEvent
	currentPayment SubscriptionPaymentEvent
}

func setupSubscriptionPaymentRefundTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&SubscriptionPlan{}, &SubscriptionOrder{}, &UserSubscription{},
		&SubscriptionPaymentEvent{}, &SubscriptionPaymentRefund{}, &WaffoPancakeSubscriptionPayment{}, &FinanceLedgerEntry{}, &TopUp{},
	))
	return db
}

func createSubscriptionPaymentRefundFixture(t *testing.T, db *gorm.DB, suffix string) subscriptionPaymentRefundFixture {
	t.Helper()
	now := common.GetTimestamp()
	fixture := subscriptionPaymentRefundFixture{db: db}
	fixture.user = User{
		Username: "spr-" + suffix, Password: "password", AffCode: "spr-aff-" + suffix,
		Status: common.UserStatusEnabled, Group: "default", Quota: 123_456, UsedQuota: 4_321,
	}
	require.NoError(t, db.Create(&fixture.user).Error)
	plan := SubscriptionPlan{
		Title: "Immutable refund plan " + suffix, PriceAmount: 6.8, Currency: "CNY",
		TotalAmount: 100_000, WaffoPancakeProductType: "subscription", Enabled: true,
		DurationUnit: SubscriptionDurationMonth, DurationValue: 1,
	}
	require.NoError(t, db.Create(&plan).Error)
	fixture.subscription = UserSubscription{
		UserId: fixture.user.Id, PlanId: plan.Id, AmountTotal: 100_000,
		Status: "active", Source: "order", StartTime: now - 3_600, EndTime: now + 3_600,
	}
	require.NoError(t, db.Create(&fixture.subscription).Error)
	fixture.order = SubscriptionOrder{
		UserId: fixture.user.Id, PlanId: plan.Id, UserSubscriptionId: fixture.subscription.Id,
		TradeNo: "spr-order-" + suffix, Money: plan.PriceAmount, PlanCurrency: plan.Currency,
		PaymentProvider: PaymentProviderWaffoPancake, PaymentMethod: PaymentMethodWaffoPancake,
		Status: common.TopUpStatusSuccess, CreateTime: now - 10_800, CompleteTime: now - 10_800,
		PlanSnapshot: common.GetJsonString(plan), ExpectedAmountMicros: 1_000_000,
		SettlementCurrency: FinanceCurrencyUSD, ProviderSubscriptionId: "ORD_" + suffix,
		ProviderSubscriptionState: "active", CurrentPeriodStart: fixture.subscription.StartTime,
		CurrentPeriodEnd: fixture.subscription.EndTime,
	}
	require.NoError(t, db.Create(&fixture.order).Error)
	fixture.oldPayment = SubscriptionPaymentEvent{
		SubscriptionOrderId: fixture.order.Id, PaymentProvider: fixture.order.PaymentProvider,
		ProviderEventId: "EVT_" + suffix + "_old", ProviderTransactionId: "PAY_" + suffix + "_old",
		SettlementCurrency: FinanceCurrencyUSD, SettlementAmountMicros: 2_000_000,
		PeriodStart: now - 10_800, PeriodEnd: fixture.order.CurrentPeriodStart,
		CreatedTime: now,
	}
	fixture.currentPayment = SubscriptionPaymentEvent{
		SubscriptionOrderId: fixture.order.Id, PaymentProvider: fixture.order.PaymentProvider,
		ProviderEventId: "EVT_" + suffix + "_current", ProviderTransactionId: "PAY_" + suffix + "_current",
		SettlementCurrency: FinanceCurrencyUSD, SettlementAmountMicros: 1_000_000,
		PeriodStart: fixture.order.CurrentPeriodStart, PeriodEnd: fixture.order.CurrentPeriodEnd,
		CreatedTime: now - 30,
	}
	// The older payment was delivered later. CreatedTime is not a cycle key.
	require.NoError(t, db.Create(&fixture.oldPayment).Error)
	require.NoError(t, db.Create(&fixture.currentPayment).Error)
	// These legacy fixtures include explicit boundaries on the ORIGINAL
	// payment. A timestamp-derived SubscriptionPaymentEvent alone is not proof.
	for _, payment := range []SubscriptionPaymentEvent{fixture.oldPayment, fixture.currentPayment} {
		require.NoError(t, db.Create(&WaffoPancakeSubscriptionPayment{
			SubscriptionOrderID: fixture.order.Id, EventID: payment.ProviderEventId,
			ProviderOrderID: fixture.order.ProviderSubscriptionId, PaymentID: payment.ProviderTransactionId,
			Currency: payment.SettlementCurrency, AmountMicros: payment.SettlementAmountMicros,
			PeriodStart: payment.PeriodStart, PeriodEnd: payment.PeriodEnd,
		}).Error)
	}
	mirror := TopUp{
		UserId: fixture.user.Id, TradeNo: fixture.order.TradeNo, Money: 1,
		PaymentProvider: fixture.order.PaymentProvider, PaymentMethod: fixture.order.PaymentMethod,
		Status: common.TopUpStatusSuccess, ExpectedAmountMicros: 1_000_000,
		SettledAmountMicros: 1_000_000, SettlementCurrency: FinanceCurrencyUSD,
	}
	require.NoError(t, db.Create(&mirror).Error)
	return fixture
}

func (fixture subscriptionPaymentRefundFixture) request(payment SubscriptionPaymentEvent, eventID string, amount int64) SubscriptionPaymentRefundRequest {
	return SubscriptionPaymentRefundRequest{
		TradeNo: fixture.order.TradeNo, ProviderTransactionID: payment.ProviderTransactionId,
		ProviderEventID: "REF_" + eventID, PaymentMethod: fixture.order.PaymentMethod,
		PaymentProvider: fixture.order.PaymentProvider, Currency: payment.SettlementCurrency,
		AmountMicros: amount, Note: "immutable payment refund test", ActorID: fixture.user.Id,
	}
}

func (fixture subscriptionPaymentRefundFixture) apply(t *testing.T, payment SubscriptionPaymentEvent, eventID string, amount, revoked int64) SubscriptionPaymentRefundRequest {
	t.Helper()
	request := fixture.request(payment, eventID, amount)
	result, err := ApplySubscriptionPaymentRefund(request)
	require.NoError(t, err)
	assert.True(t, result.Created)
	assert.Equal(t, fixture.user.Id, result.UserID)
	assert.Equal(t, revoked, result.QuotaDebited)

	var bindings []SubscriptionPaymentRefund
	require.NoError(t, fixture.db.Where("payment_provider = ? AND provider_event_id = ?", request.PaymentProvider, request.ProviderEventID).
		Find(&bindings).Error)
	require.Len(t, bindings, 1)
	binding := bindings[0]
	assert.Positive(t, binding.ID)
	assert.Equal(t, fixture.order.Id, binding.SubscriptionOrderID)
	assert.Equal(t, payment.Id, binding.SubscriptionPaymentEventID, "money must remain bound to the exact original receipt")
	assert.Equal(t, request.PaymentProvider, binding.PaymentProvider)
	assert.Equal(t, request.ProviderEventID, binding.ProviderEventID)
	assert.Equal(t, request.Currency, binding.Currency)
	assert.Equal(t, amount, binding.AmountMicros)
	assert.Equal(t, revoked, binding.QuotaRevoked)
	assert.Positive(t, binding.CreatedTime)
	require.Positive(t, binding.FinanceLedgerEntryID)

	var ledger FinanceLedgerEntry
	require.NoError(t, fixture.db.First(&ledger, binding.FinanceLedgerEntryID).Error)
	assert.Equal(t, FinanceEntryRevenue, ledger.EntryType)
	assert.Equal(t, FinanceSourceRefund, ledger.Category)
	assert.Equal(t, FinanceSourceRefund, ledger.SourceType)
	assert.Equal(t, request.ProviderEventID, ledger.SourceId)
	assert.Equal(t, int8(FinanceDirectionDebit), ledger.Direction)
	assert.Equal(t, amount, ledger.AmountMicros)
	assert.Equal(t, request.Currency, ledger.Currency)
	assert.Equal(t, request.PaymentProvider, ledger.PaymentProvider)
	assert.Equal(t, request.PaymentMethod, ledger.PaymentMethod)
	require.NotNil(t, ledger.UserId)
	assert.Equal(t, fixture.user.Id, *ledger.UserId)
	assert.Equal(t, request.ActorID, ledger.CreatedBy)
	assert.Positive(t, ledger.OccurredAt)
	assert.Positive(t, ledger.CreatedAt)
	return request
}

type subscriptionPaymentRefundState struct {
	users         []User
	plans         []SubscriptionPlan
	orders        []SubscriptionOrder
	subscriptions []UserSubscription
	payments      []SubscriptionPaymentEvent
	refunds       []SubscriptionPaymentRefund
	ledger        []FinanceLedgerEntry
	topUps        []TopUp
}

func readSubscriptionPaymentRefundState(t *testing.T, db *gorm.DB) subscriptionPaymentRefundState {
	t.Helper()
	var state subscriptionPaymentRefundState
	for _, rows := range []interface{}{
		&state.users, &state.plans, &state.orders, &state.subscriptions,
		&state.payments, &state.refunds, &state.ledger, &state.topUps,
	} {
		require.NoError(t, db.Unscoped().Order("id ASC").Find(rows).Error)
	}
	return state
}

func assertSubscriptionPaymentRefundEntitlementUnchanged(t *testing.T, before, after subscriptionPaymentRefundState) {
	t.Helper()
	assert.Equal(t, before.users, after.users, "subscription refunds must not debit wallet balances")
	assert.Equal(t, before.plans, after.plans)
	assert.Equal(t, before.orders, after.orders, "historical refunds must not alter current-period counters or state")
	assert.Equal(t, before.subscriptions, after.subscriptions, "historical refunds must not shrink the current grant")
	assert.Equal(t, before.payments, after.payments, "payment receipts must remain immutable")
	assert.Equal(t, before.topUps, after.topUps, "subscription refunds must not take the mirrored wallet-order path")
}
