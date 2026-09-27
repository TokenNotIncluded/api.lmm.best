package model

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWaffoPancakePaymentCheckedAtMigratesExistingTopUps(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy-topups.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	require.NoError(t, db.Migrator().DropColumn(&TopUp{}, "payment_checked_at"))
	require.NoError(t, db.Exec(`INSERT INTO top_ups (id, trade_no, payment_provider, status, create_time) VALUES (1, 'legacy-waffo', 'waffo_pancake', 'pending', 100)`).Error)
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	var order TopUp
	require.NoError(t, db.First(&order, 1).Error)
	require.Zero(t, order.PaymentCheckedAt)
}

func TestFailExpiredWaffoPancakeTopUpPreservesSettlementAndReleasesReservation(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	require.NoError(t, db.AutoMigrate(&DiscountCodeReservation{}))
	const cutoff int64 = 1_800_000_000
	orders := []TopUp{
		{TradeNo: "old-waffo", PaymentProvider: PaymentProviderWaffoPancake, PaymentMethod: PaymentMethodWaffoPancake, Status: common.TopUpStatusPending, CreateTime: cutoff - 1, DiscountCodeId: 7},
		{TradeNo: "fresh-waffo", PaymentProvider: PaymentProviderWaffoPancake, Status: common.TopUpStatusPending, CreateTime: cutoff + 1},
		{TradeNo: "paid-waffo", PaymentProvider: PaymentProviderWaffoPancake, Status: common.TopUpStatusSuccess, CreateTime: cutoff - 1},
		{TradeNo: "old-stripe", PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusPending, CreateTime: cutoff - 1},
	}
	for i := range orders {
		require.NoError(t, db.Create(&orders[i]).Error)
	}
	require.NoError(t, db.Create(&DiscountCodeReservation{
		DiscountCodeId: 7, TopUpTradeNo: orders[0].TradeNo,
		Status: DiscountCodeReservationStatusReserved,
	}).Error)

	due, err := DueWaffoPancakeTopUps(context.Background(), cutoff, cutoff, 100)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, orders[0].Id, due[0].Id)

	failed, err := FailExpiredWaffoPancakeTopUp(context.Background(), orders[0].Id, cutoff, cutoff+5)
	require.NoError(t, err)
	require.True(t, failed)
	var stored TopUp
	require.NoError(t, db.First(&stored, orders[0].Id).Error)
	require.Equal(t, common.TopUpStatusFailed, stored.Status)
	require.Equal(t, string(PaymentOrderFailureCheckoutTimeout), stored.FailureReasonCode)
	require.Equal(t, cutoff+5, stored.CompleteTime)
	var reservation DiscountCodeReservation
	require.NoError(t, db.Where("top_up_trade_no = ?", orders[0].TradeNo).First(&reservation).Error)
	require.Equal(t, DiscountCodeReservationStatusReleased, reservation.Status)

	for _, order := range orders[1:] {
		failed, err = FailExpiredWaffoPancakeTopUp(context.Background(), order.Id, cutoff, cutoff+5)
		require.NoError(t, err)
		require.False(t, failed)
	}
	stored = TopUp{}
	require.NoError(t, db.First(&stored, orders[1].Id).Error)
	require.Equal(t, common.TopUpStatusPending, stored.Status)
	stored = TopUp{}
	require.NoError(t, db.First(&stored, orders[2].Id).Error)
	require.Equal(t, common.TopUpStatusSuccess, stored.Status)
	stored = TopUp{}
	require.NoError(t, db.First(&stored, orders[3].Id).Error)
	require.Equal(t, common.TopUpStatusPending, stored.Status)
}

func TestWaffoPancakePaymentCheckRotatesUncertainOrders(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	orders := []TopUp{
		{TradeNo: "uncertain", PaymentProvider: PaymentProviderWaffoPancake, Status: common.TopUpStatusPending, CreateTime: 100},
		{TradeNo: "unexamined", PaymentProvider: PaymentProviderWaffoPancake, Status: common.TopUpStatusPending, CreateTime: 100},
	}
	for i := range orders {
		require.NoError(t, db.Create(&orders[i]).Error)
	}
	require.NoError(t, MarkWaffoPancakeTopUpPaymentChecked(context.Background(), orders[0].Id, 1000))
	due, err := DueWaffoPancakeTopUps(context.Background(), 200, 900, 1)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, orders[1].Id, due[0].Id)
	due, err = DueWaffoPancakeTopUps(context.Background(), 200, 1000, 2)
	require.NoError(t, err)
	require.Len(t, due, 2)
}

func TestLateSignedWaffoPaymentRecoversOnlyTimeoutFailureOnce(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, order, settlement := makeWaffoPancakeTopUpFixture(t, "late-timeout-payment")
	require.NoError(t, db.Model(&TopUp{}).Where("id = ?", order.Id).Update("create_time", 100).Error)
	failed, err := FailExpiredWaffoPancakeTopUp(context.Background(), order.Id, 200, 201)
	require.NoError(t, err)
	require.True(t, failed)

	completed, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	require.Equal(t, common.TopUpStatusSuccess, completed.Status)
	require.Empty(t, completed.FailureReasonCode)
	_, err = CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	var storedUser User
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.Equal(t, user.Quota+int(order.CreditedQuota), storedUser.Quota)

	_, companyFailure, companySettlement := makeWaffoPancakeTopUpFixture(t, "late-company-payment")
	require.NoError(t, FailPendingTopUpForCheckout(companyFailure.TradeNo, PaymentProviderWaffoPancake, PaymentOrderFailureCompanyBillingRules))
	_, err = CompleteExternalTopUp(companySettlement)
	require.ErrorIs(t, err, ErrTopUpStatusInvalid)
}
