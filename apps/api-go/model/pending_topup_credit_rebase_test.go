package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPendingTopUpCreditRebaseLateCallbackGrantsEffectiveCreditsOnce(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, order, settlement := createSettlementFixture(t, db, "pending-rebased")
	order.PendingCreditRebaseKey = "migration-test"
	order.PendingCreditRebaseOriginalQuota = order.CreditedQuota
	order.PendingCreditRebaseEffectiveQuota = 184
	require.NoError(t, db.Save(&order).Error)
	completed, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	require.EqualValues(t, 184, completed.CreditedQuota)
	_, err = CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 284, user.Quota)
	require.NoError(t, db.First(&order, order.Id).Error)
	require.EqualValues(t, 1234, order.PendingCreditRebaseOriginalQuota)
	require.EqualValues(t, 184, normalizedTopUpCreditedQuota(&order))
	require.EqualValues(t, 12340000, order.ExpectedAmountMicros)
	require.EqualValues(t, 12340000, order.SettledAmountMicros)
	require.Equal(t, 12.34, order.Money)
	require.EqualValues(t, 2, order.Amount)
}
func TestPendingTopUpCreditRebaseCorruptSnapshotRollsBackCallback(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, order, settlement := createSettlementFixture(t, db, "pending-corrupt")
	order.PendingCreditRebaseKey = "migration-test"
	order.PendingCreditRebaseOriginalQuota = 1235
	order.PendingCreditRebaseEffectiveQuota = 184
	require.NoError(t, db.Save(&order).Error)
	_, err := CompleteExternalTopUp(settlement)
	require.ErrorIs(t, err, ErrInvalidTopUpQuota)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 100, user.Quota)
	require.NoError(t, db.First(&order, order.Id).Error)
	require.Equal(t, common.TopUpStatusPending, order.Status)
	require.Zero(t, order.SettledAmountMicros)
}
func TestPendingTopUpCreditRebaseLegacyFallbackAndNewOrders(t *testing.T) {
	oldUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldUnit })
	order := TopUp{Amount: 10, PaymentMethod: "alipay", Status: common.TopUpStatusPending}
	quota, err := pendingTopUpSettlementQuota(&order)
	require.NoError(t, err)
	require.EqualValues(t, 5000000, quota)
	order.PendingCreditRebaseKey = "migration-test"
	order.PendingCreditRebaseOriginalQuota = 5000000
	order.PendingCreditRebaseEffectiveQuota = 746269
	quota, err = pendingTopUpSettlementQuota(&order)
	require.NoError(t, err)
	require.EqualValues(t, 746269, quota)
	for _, change := range []func(*TopUp){
		func(o *TopUp) { o.PendingCreditRebaseKey = "" },
		func(o *TopUp) { o.PendingCreditRebaseKey = " " },
		func(o *TopUp) { o.PendingCreditRebaseEffectiveQuota = 0 },
		func(o *TopUp) { o.PendingCreditRebaseEffectiveQuota = 5000001 },
		func(o *TopUp) { o.Amount = 11 },
	} {
		invalid := order
		change(&invalid)
		_, err := pendingTopUpSettlementQuota(&invalid)
		require.ErrorIs(t, err, ErrInvalidTopUpQuota)
	}
}

func TestPendingTopUpCreditRebaseLegacyManualSettlement(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, order, _ := createSettlementFixture(t, db, "pending-manual")
	order.PaymentProvider = ""
	order.PaymentMethod = "alipay"
	order.Amount = 10
	order.CreditedQuota = 0
	order.PendingCreditRebaseKey = "migration-test"
	order.PendingCreditRebaseOriginalQuota = 5000000
	order.PendingCreditRebaseEffectiveQuota = 746269
	require.NoError(t, db.Save(&order).Error)
	require.NoError(t, ManualCompleteTopUp(order.TradeNo, ""))
	require.NoError(t, ManualCompleteTopUp(order.TradeNo, ""))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 746369, user.Quota)
	require.NoError(t, db.First(&order, order.Id).Error)
	require.EqualValues(t, 746269, order.CreditedQuota)
	require.EqualValues(t, 5000000, order.PendingCreditRebaseOriginalQuota)
	require.Equal(t, common.TopUpStatusSuccess, order.Status)
}

func TestPendingTopUpCreditRebaseAllLegacySettlementEntrypoints(t *testing.T) {
	cases := []struct {
		name, provider string
		settle         func(string) error
	}{
		{"epay", PaymentProviderEpay, func(trade string) error { _, err := RechargeEpay(trade, "alipay", ""); return err }},
		{"stripe", PaymentProviderStripe, func(trade string) error { return Recharge(trade, "", "") }},
		{"creem", PaymentProviderCreem, func(trade string) error { return RechargeCreem(trade, "", "", "") }},
		{"waffo", PaymentProviderWaffo, func(trade string) error { return RechargeWaffo(trade, "") }},
		{"pancake", PaymentProviderWaffoPancake, RechargeWaffoPancake},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupExternalTopUpSettlementDB(t, 1)
			user, order, _ := createSettlementFixture(t, db, "entry-"+tc.name)
			order.PaymentProvider = tc.provider
			order.PendingCreditRebaseKey = "migration-test"
			order.PendingCreditRebaseOriginalQuota = 1234
			order.PendingCreditRebaseEffectiveQuota = 184
			require.NoError(t, db.Save(&order).Error)
			require.NoError(t, tc.settle(order.TradeNo))
			require.NoError(t, db.First(&user, user.Id).Error)
			require.Equal(t, 284, user.Quota)
			require.NoError(t, db.First(&order, order.Id).Error)
			require.EqualValues(t, 184, order.CreditedQuota)
			require.EqualValues(t, 1234, order.PendingCreditRebaseOriginalQuota)
		})
	}
}
