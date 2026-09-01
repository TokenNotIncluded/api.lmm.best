package model

import (
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func makeWaffoPancakeTopUpFixture(t *testing.T, tradeNo string) (User, TopUp, ExternalTopUpSettlement) {
	t.Helper()
	user, topUp, settlement := createSettlementFixture(t, DB, tradeNo)
	require.NoError(t, DB.Model(&TopUp{}).Where("id = ?", topUp.Id).Updates(map[string]interface{}{
		"payment_method":   PaymentMethodWaffoPancake,
		"payment_provider": PaymentProviderWaffoPancake,
	}).Error)
	topUp.PaymentMethod = PaymentMethodWaffoPancake
	topUp.PaymentProvider = PaymentProviderWaffoPancake
	settlement.PaymentMethod = PaymentMethodWaffoPancake
	settlement.PaymentProvider = PaymentProviderWaffoPancake
	settlement.ProviderProductId = ""
	return user, topUp, settlement
}

func TestCompanyBillingCheckoutFailuresLeaveNoWalletPendingOrders(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	const sensitive = "Example Company TAX-41"

	for index, reason := range []PaymentOrderFailureReason{
		PaymentOrderFailureCompanyBillingRequiredFields,
		PaymentOrderFailureCompanyBillingPreview,
	} {
		tradeNo := "company-wallet-" + string(rune('a'+index))
		_, _, _ = makeWaffoPancakeTopUpFixture(t, tradeNo)
		require.NoError(t, FailPendingTopUpForCheckout(tradeNo, PaymentProviderWaffoPancake, reason))

		var stored TopUp
		require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&stored).Error)
		require.Equal(t, common.TopUpStatusFailed, stored.Status)
		require.Equal(t, string(reason), stored.FailureReasonCode)
		require.NotContains(t, stored.FailureReasonCode, sensitive)
		require.Positive(t, stored.CompleteTime)
	}

	var pending int64
	require.NoError(t, db.Model(&TopUp{}).
		Where("payment_provider = ? AND status = ?", PaymentProviderWaffoPancake, common.TopUpStatusPending).
		Count(&pending).Error)
	require.Zero(t, pending, "repeated checkout attempts must not accumulate orphan pending orders")
}

func TestCompanyBillingWalletFailureCASPreservesConcurrentStatusAndRejectsLateWebhook(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, topUp, settlement := makeWaffoPancakeTopUpFixture(t, "company-wallet-late")

	require.NoError(t, FailPendingTopUpForCheckout(topUp.TradeNo, PaymentProviderWaffoPancake, PaymentOrderFailureCompanyBillingRules))
	_, err := CompleteExternalTopUp(settlement)
	require.ErrorIs(t, err, ErrTopUpStatusInvalid)

	var storedUser User
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.Equal(t, user.Quota, storedUser.Quota, "late webhook must not credit a terminal order")

	_, concurrent, _ := makeWaffoPancakeTopUpFixture(t, "company-wallet-concurrent")
	require.NoError(t, db.Model(&TopUp{}).Where("id = ?", concurrent.Id).Update("status", common.TopUpStatusSuccess).Error)
	err = FailPendingTopUpForCheckout(concurrent.TradeNo, PaymentProviderWaffoPancake, PaymentOrderFailureCompanyBillingPreview)
	require.ErrorIs(t, err, ErrTopUpStatusInvalid)
	var preserved TopUp
	require.NoError(t, db.First(&preserved, concurrent.Id).Error)
	require.Equal(t, common.TopUpStatusSuccess, preserved.Status)
	require.Empty(t, preserved.FailureReasonCode)
}

func TestCompanyBillingSubscriptionFailureCASRejectsLateWebhookAndPreservesConcurrentStatus(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	require.NoError(t, db.AutoMigrate(&SubscriptionOrder{}, &UserSubscription{}))

	failed := SubscriptionOrder{
		UserId:               41,
		PlanId:               7,
		TradeNo:              "company-subscription-late",
		PaymentMethod:        PaymentMethodWaffoPancake,
		PaymentProvider:      PaymentProviderWaffoPancake,
		ExpectedAmountMicros: 12_340_000,
		SettlementCurrency:   "USD",
		Status:               common.TopUpStatusPending,
	}
	require.NoError(t, db.Create(&failed).Error)
	require.NoError(t, FailPendingSubscriptionOrderForCheckout(
		failed.TradeNo,
		PaymentProviderWaffoPancake,
		PaymentOrderFailureCompanyBillingRequiredFields,
	))

	var stored SubscriptionOrder
	require.NoError(t, db.First(&stored, failed.Id).Error)
	require.Equal(t, common.TopUpStatusFailed, stored.Status)
	require.Equal(t, string(PaymentOrderFailureCompanyBillingRequiredFields), stored.FailureReasonCode)
	require.NotContains(t, stored.FailureReasonCode, "Example Company")
	require.NotContains(t, stored.FailureReasonCode, "TAX-41")
	require.ErrorIs(t,
		CompleteSubscriptionOrder(failed.TradeNo, `{"businessName":"must-not-be-saved"}`, PaymentProviderWaffoPancake, ""),
		ErrSubscriptionOrderStatusInvalid,
	)
	var subscriptions int64
	require.NoError(t, db.Model(&UserSubscription{}).Count(&subscriptions).Error)
	require.Zero(t, subscriptions, "late webhook must not activate a terminal order")

	concurrent := SubscriptionOrder{
		UserId:          41,
		PlanId:          7,
		TradeNo:         "company-subscription-concurrent",
		PaymentProvider: PaymentProviderWaffoPancake,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, db.Create(&concurrent).Error)
	require.NoError(t, db.Model(&SubscriptionOrder{}).Where("id = ?", concurrent.Id).Update("status", common.TopUpStatusSuccess).Error)
	err := FailPendingSubscriptionOrderForCheckout(
		concurrent.TradeNo,
		PaymentProviderWaffoPancake,
		PaymentOrderFailureCompanyBillingPreview,
	)
	require.ErrorIs(t, err, ErrSubscriptionOrderStatusInvalid)
	stored = SubscriptionOrder{}
	require.NoError(t, db.First(&stored, concurrent.Id).Error)
	require.Equal(t, common.TopUpStatusSuccess, stored.Status)
	require.Empty(t, stored.FailureReasonCode)
}

func TestCompanyBillingCheckoutFailureReasonRejectsArbitraryText(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	_, topUp, _ := makeWaffoPancakeTopUpFixture(t, "company-wallet-sensitive-reason")

	err := FailPendingTopUpForCheckout(topUp.TradeNo, PaymentProviderWaffoPancake, PaymentOrderFailureReason("Example Company TAX-41"))
	require.True(t, errors.Is(err, ErrInvalidPaymentFailureReason))
	var stored TopUp
	require.NoError(t, db.First(&stored, topUp.Id).Error)
	require.Equal(t, common.TopUpStatusPending, stored.Status)
	require.Empty(t, stored.FailureReasonCode)
}
