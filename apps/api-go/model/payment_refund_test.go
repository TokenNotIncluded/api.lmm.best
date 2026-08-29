// Copyright (C) 2026 LIghtJUNction
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.

package model

import (
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestApplyWaffoPancakeRefundAccumulatesSubscriptionPartialRefunds(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&SubscriptionOrder{}, &UserSubscription{}, &FinanceLedgerEntry{}))

	user := User{Username: "refund-subscription-owner", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	subscription := UserSubscription{
		UserId: user.Id, AmountTotal: 100_000, AmountUsed: 0,
		Status: "active", Source: "order", EndTime: common.GetTimestamp() + 3600,
	}
	require.NoError(t, db.Create(&subscription).Error)
	order := SubscriptionOrder{
		UserId: user.Id, TradeNo: "refund-subscription-partial",
		PaymentProvider: PaymentProviderWaffoPancake, PaymentMethod: PaymentMethodWaffoPancake,
		UserSubscriptionId: subscription.Id, Money: 6.8, PlanCurrency: "CNY",
		ExpectedAmountMicros: 1_000_000, SettlementCurrency: "USD",
		Status: common.TopUpStatusSuccess,
	}
	require.NoError(t, db.Create(&order).Error)

	for _, eventID := range []string{"subscription-refund-event-1", "subscription-refund-event-2"} {
		result, err := ApplyWaffoPancakeRefund(
			order.TradeNo, true, 250_000, FinanceCurrencyUSD,
			eventID,
			PaymentMethodWaffoPancake, PaymentProviderWaffoPancake, "test refund", user.Id,
		)
		require.NoError(t, err)
		assert.Equal(t, int64(25_000), result.QuotaDebited)
	}

	var refreshedSubscription UserSubscription
	require.NoError(t, db.First(&refreshedSubscription, subscription.Id).Error)
	assert.Equal(t, int64(50_000), refreshedSubscription.AmountTotal)

	var refreshedOrder SubscriptionOrder
	require.NoError(t, db.First(&refreshedOrder, order.Id).Error)
	assert.Equal(t, int64(500_000), refreshedOrder.RefundedAmountMicros)
	assert.Equal(t, int64(50_000), refreshedOrder.RefundedQuota)

	var refreshedUser User
	require.NoError(t, db.First(&refreshedUser, user.Id).Error)
	assert.Zero(t, refreshedUser.Quota, "subscription refunds must not debit the wallet quota")
}

func TestApplyWaffoPancakeRefundRejectsMissingWalletOrder(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&TopUp{}, &FinanceLedgerEntry{}))

	_, err := ApplyWaffoPancakeRefund(
		"missing-wallet-order", false, 1_000_000, FinanceCurrencyUSD,
		"missing-wallet-refund-event", PaymentMethodWaffoPancake,
		PaymentProviderWaffoPancake, "test refund", 1,
	)
	require.Error(t, err)
	assert.ErrorContains(t, err, "refund order disappeared")

	var entries []FinanceLedgerEntry
	require.NoError(t, db.Find(&entries).Error)
	assert.Empty(t, entries)
}

func TestApplyWaffoPancakeRefundRejectsWalletNegativeBalanceAndKeepsEventRetryable(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &FinanceLedgerEntry{}))

	user := User{
		Username: "refund-wallet-insufficient",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    10,
	}
	require.NoError(t, db.Create(&user).Error)
	tradeNo := "refund-wallet-insufficient"
	require.NoError(t, db.Create(&TopUp{
		UserId:              user.Id,
		TradeNo:             tradeNo,
		PaymentProvider:     PaymentProviderWaffoPancake,
		PaymentMethod:       PaymentMethodWaffoPancake,
		Status:              common.TopUpStatusSuccess,
		CreditedQuota:       100_000,
		SettledAmountMicros: 10_000_000,
		Money:               10,
	}).Error)

	result, err := ApplyWaffoPancakeRefund(
		tradeNo, false, 2_500_000, FinanceCurrencyUSD,
		"refund-wallet-insufficient-event", PaymentMethodWaffoPancake,
		PaymentProviderWaffoPancake, "test refund", user.Id,
	)
	assert.Zero(t, result)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRefundWalletQuotaInsufficient), err)
	assert.ErrorContains(t, err, "user_id=")
	assert.ErrorContains(t, err, "required_quota=25000")

	var refreshedUser User
	require.NoError(t, db.First(&refreshedUser, user.Id).Error)
	assert.Equal(t, 10, refreshedUser.Quota, "insufficient balance must not become negative")
	var refreshedTopUp TopUp
	require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&refreshedTopUp).Error)
	assert.Zero(t, refreshedTopUp.RefundedAmountMicros)
	assert.Zero(t, refreshedTopUp.RefundedQuota)
	var entries []FinanceLedgerEntry
	require.NoError(t, db.Where("source_type = ?", FinanceSourceRefund).Find(&entries).Error)
	assert.Empty(t, entries, "failed debit must not commit a ledger row")

	// After reconciliation, the exact same provider event can be retried and
	// is applied once because the failed transaction left no idempotency row.
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 30_000).Error)
	result, err = ApplyWaffoPancakeRefund(
		tradeNo, false, 2_500_000, FinanceCurrencyUSD,
		"refund-wallet-insufficient-event", PaymentMethodWaffoPancake,
		PaymentProviderWaffoPancake, "test refund", user.Id,
	)
	require.NoError(t, err)
	assert.True(t, result.Created)
	assert.Equal(t, int64(25_000), result.QuotaDebited)
	assert.Equal(t, user.Id, result.UserID)
	assert.Equal(t, 5_000, getUserQuotaForRefundTest(t, db, user.Id))
}

func TestApplyWaffoPancakeRefundBindsProviderEventToOriginalOrder(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &FinanceLedgerEntry{}))

	firstUser := User{
		Username: "refund-event-owner-one",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    100_000,
		AffCode:  "refund-event-owner-one-aff",
	}
	secondUser := User{
		Username: "refund-event-owner-two",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    100_000,
		AffCode:  "refund-event-owner-two-aff",
	}
	require.NoError(t, db.Create(&firstUser).Error)
	require.NoError(t, db.Create(&secondUser).Error)

	createTopUp := func(userID int, tradeNo string) TopUp {
		return TopUp{
			UserId:              userID,
			TradeNo:             tradeNo,
			PaymentProvider:     PaymentProviderWaffoPancake,
			PaymentMethod:       PaymentMethodWaffoPancake,
			Status:              common.TopUpStatusSuccess,
			CreditedQuota:       100_000,
			SettledAmountMicros: 10_000_000,
			Money:               10,
		}
	}
	firstOrder := createTopUp(firstUser.Id, "refund-event-order-one")
	secondOrder := createTopUp(secondUser.Id, "refund-event-order-two")
	require.NoError(t, db.Create(&firstOrder).Error)
	require.NoError(t, db.Create(&secondOrder).Error)

	const providerEventID = "refund-event-reused-across-orders"
	result, err := ApplyWaffoPancakeRefund(
		firstOrder.TradeNo, false, 2_500_000, FinanceCurrencyUSD,
		providerEventID, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake,
		"provider refund", firstUser.Id,
	)
	require.NoError(t, err)
	assert.True(t, result.Created)
	assert.Equal(t, int64(25_000), result.QuotaDebited)
	assert.Equal(t, 75_000, getUserQuotaForRefundTest(t, db, firstUser.Id))

	// A normal provider retry for the original order remains idempotent.
	result, err = ApplyWaffoPancakeRefund(
		firstOrder.TradeNo, false, 9_000_000, FinanceCurrencyUSD,
		providerEventID, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake,
		"changed payload", firstUser.Id,
	)
	require.NoError(t, err)
	assert.False(t, result.Created)
	assert.Zero(t, result.QuotaDebited)
	assert.Equal(t, 75_000, getUserQuotaForRefundTest(t, db, firstUser.Id))

	// Reusing the same event id for another order must fail before any wallet
	// or cumulative-refund update is committed.
	result, err = ApplyWaffoPancakeRefund(
		secondOrder.TradeNo, false, 2_500_000, FinanceCurrencyUSD,
		providerEventID, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake,
		"provider refund", secondUser.Id,
	)
	assert.Zero(t, result)
	require.ErrorIs(t, err, ErrPaymentRefundOrderConflict)
	assert.Equal(t, 100_000, getUserQuotaForRefundTest(t, db, secondUser.Id))

	var refreshedSecondOrder TopUp
	require.NoError(t, db.First(&refreshedSecondOrder, secondOrder.Id).Error)
	assert.Zero(t, refreshedSecondOrder.RefundedAmountMicros)
	assert.Zero(t, refreshedSecondOrder.RefundedQuota)

	var entries []FinanceLedgerEntry
	require.NoError(t, db.Where("source_type = ?", FinanceSourceRefund).Find(&entries).Error)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Note, "refund_trade_no="+firstOrder.TradeNo)
}

func getUserQuotaForRefundTest(t *testing.T, db *gorm.DB, userID int) int {
	t.Helper()
	var quota int
	require.NoError(t, db.Model(&User{}).Where("id = ?", userID).Select("quota").Scan(&quota).Error)
	return quota
}
