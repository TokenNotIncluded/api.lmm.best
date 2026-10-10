package model

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestLimitedDiscountReservationRemainsHeldUntilOrderTerminates(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	require.NoError(t, db.AutoMigrate(&DiscountCode{}, &DiscountCodeReservation{}))
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	code := DiscountCode{
		Code:            "LAST-DISCOUNT-SLOT",
		Status:          DiscountCodeStatusEnabled,
		DiscountPercent: 50,
		MaxUses:         1,
	}
	require.NoError(t, db.Create(&code).Error)

	newPending := func(suffix string) (*User, *TopUp) {
		user := &User{Username: "discount-" + suffix, AffCode: "aff-" + suffix, Status: common.UserStatusEnabled, Group: "default"}
		require.NoError(t, db.Create(user).Error)
		topUp := &TopUp{
			UserId:               user.Id,
			Amount:               10,
			CreditedQuota:        1_000,
			ExpectedAmountMicros: 5_000_000,
			Money:                5,
			TradeNo:              "discount-reservation-" + suffix,
			PaymentMethod:        PaymentMethodWaffoPancake,
			PaymentProvider:      PaymentProviderWaffoPancake,
			SettlementCurrency:   "USD",
			DiscountCodeId:       code.Id,
			DiscountPercent:      code.DiscountPercent,
			CreateTime:           common.GetTimestamp(),
			Status:               common.TopUpStatusPending,
		}
		return user, topUp
	}

	firstUser, first := newPending("first")
	require.NoError(t, first.Insert())
	_, blocked := newPending("blocked")
	require.ErrorIs(t, blocked.Insert(), ErrDiscountCodeExhausted)

	// The provider checkout may remain payable after this timestamp, so expiry
	// alone must not make its limited-use coupon slot available again.
	require.NoError(t, db.Model(&DiscountCodeReservation{}).
		Where("top_up_trade_no = ?", first.TradeNo).
		Update("expires_time", common.GetTimestamp()-1).Error)
	_, stillBlocked := newPending("still-blocked")
	require.ErrorIs(t, stillBlocked.Insert(), ErrDiscountCodeExhausted)

	// A terminal order transition explicitly releases the slot.
	require.NoError(t, UpdatePendingTopUpStatus(first.TradeNo, PaymentProviderWaffoPancake, common.TopUpStatusFailed))
	secondUser, second := newPending("second")
	require.NoError(t, second.Insert())

	settle := func(topUp *TopUp, index int) {
		_, err := CompleteExternalTopUp(ExternalTopUpSettlement{
			TradeNo:               topUp.TradeNo,
			PaymentProvider:       PaymentProviderWaffoPancake,
			PaymentMethod:         topUp.PaymentMethod,
			SettledAmountMicros:   topUp.ExpectedAmountMicros,
			SettlementCurrency:    topUp.SettlementCurrency,
			ProviderEventId:       fmt.Sprintf("EVT-discount-%d", index),
			ProviderTransactionId: fmt.Sprintf("PAY-discount-%d", index),
		})
		require.NoError(t, err)
	}
	settle(second, 2)

	var reloadedFirst User
	require.NoError(t, db.First(&reloadedFirst, firstUser.Id).Error)
	require.Zero(t, reloadedFirst.Quota)
	var reloadedSecond User
	require.NoError(t, db.First(&reloadedSecond, secondUser.Id).Error)
	require.EqualValues(t, 1_000, reloadedSecond.Quota)
	var consumed DiscountCode
	require.NoError(t, db.First(&consumed, code.Id).Error)
	require.EqualValues(t, 1, consumed.UsedCount)
	var consumedReservations int64
	require.NoError(t, db.Model(&DiscountCodeReservation{}).
		Where("discount_code_id = ? AND status = ?", code.Id, DiscountCodeReservationStatusConsumed).
		Count(&consumedReservations).Error)
	require.EqualValues(t, 1, consumedReservations)
	var releasedReservations int64
	require.NoError(t, db.Model(&DiscountCodeReservation{}).
		Where("discount_code_id = ? AND status = ?", code.Id, DiscountCodeReservationStatusReleased).
		Count(&releasedReservations).Error)
	require.EqualValues(t, 1, releasedReservations)
}
