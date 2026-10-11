package model

import (
	"context"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func limitedDiscountReservationFixture(t *testing.T) (*gorm.DB, DiscountCode) {
	t.Helper()
	db := setupExternalTopUpSettlementDB(t, 1)
	require.NoError(t, db.AutoMigrate(&DiscountCode{}, &DiscountCodeReservation{}))
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })
	code := DiscountCode{
		Code: "LAST-DISCOUNT-SLOT", Status: DiscountCodeStatusEnabled,
		DiscountPercent: 50, MaxUses: 1,
	}
	require.NoError(t, db.Create(&code).Error)
	return db, code
}

func pendingDiscountReservationFixture(t *testing.T, db *gorm.DB, code DiscountCode, suffix, provider string) (*User, *TopUp) {
	t.Helper()
	user := &User{Username: "discount-" + suffix, AffCode: "aff-" + suffix, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(user).Error)
	return user, &TopUp{
		UserId: user.Id, Amount: 10, CreditedQuota: 1_000,
		ExpectedAmountMicros: 5_000_000, Money: 5,
		TradeNo:       "discount-reservation-" + suffix,
		PaymentMethod: provider, PaymentProvider: provider, SettlementCurrency: "USD",
		DiscountCodeId: code.Id, DiscountPercent: code.DiscountPercent,
		CreateTime: common.GetTimestamp(), Status: common.TopUpStatusPending,
	}
}

func discountReservationSettlement(topUp *TopUp) ExternalTopUpSettlement {
	return ExternalTopUpSettlement{
		TradeNo: topUp.TradeNo, PaymentProvider: topUp.PaymentProvider, PaymentMethod: topUp.PaymentMethod,
		SettledAmountMicros: topUp.ExpectedAmountMicros, SettlementCurrency: topUp.SettlementCurrency,
		ProviderEventId: "EVT-" + topUp.TradeNo, ProviderTransactionId: "PAY-" + topUp.TradeNo,
	}
}

func requireDiscountReservationCreditOnce(t *testing.T, db *gorm.DB, code DiscountCode, user *User, topUp *TopUp) {
	t.Helper()
	for i := 0; i < 2; i++ {
		_, err := CompleteExternalTopUp(discountReservationSettlement(topUp))
		require.NoError(t, err)
	}
	var reloaded User
	require.NoError(t, db.First(&reloaded, user.Id).Error)
	require.EqualValues(t, 1_000, reloaded.Quota)
	require.NoError(t, db.First(&code, code.Id).Error)
	require.EqualValues(t, 1, code.UsedCount)
	var consumedReservations int64
	require.NoError(t, db.Model(&DiscountCodeReservation{}).
		Where("discount_code_id = ? AND status = ?", code.Id, DiscountCodeReservationStatusConsumed).
		Count(&consumedReservations).Error)
	require.EqualValues(t, 1, consumedReservations)
}

func requireDiscountReservationCapacityBlocked(t *testing.T, db *gorm.DB, code DiscountCode, topUp *TopUp) {
	t.Helper()
	_, err := ValidateDiscountCodeForUser(code.Code, 10, common.GetTimestamp(), topUp.UserId)
	require.ErrorIs(t, err, ErrDiscountCodeExhausted, "preview must match atomic reservation")
	require.ErrorIs(t, topUp.Insert(), ErrDiscountCodeExhausted)
	var orders, reservations int64
	require.NoError(t, db.Model(&TopUp{}).Where("trade_no = ?", topUp.TradeNo).Count(&orders).Error)
	require.NoError(t, db.Model(&DiscountCodeReservation{}).Where("top_up_trade_no = ?", topUp.TradeNo).Count(&reservations).Error)
	require.Zero(t, orders)
	require.Zero(t, reservations)
}

func TestLimitedDiscountReservationExpiryKeepsPayableOrderCapacity(t *testing.T) {
	db, code := limitedDiscountReservationFixture(t)
	firstUser, first := pendingDiscountReservationFixture(t, db, code, "first", PaymentProviderWaffoPancake)
	require.NoError(t, first.Insert())
	_, blocked := pendingDiscountReservationFixture(t, db, code, "blocked", PaymentProviderWaffoPancake)
	requireDiscountReservationCapacityBlocked(t, db, code, blocked)

	// The old bug admitted another payable order after only this timestamp
	// changed. Expiry is now advisory; settlement eligibility owns capacity.
	require.NoError(t, db.Model(&DiscountCodeReservation{}).
		Where("top_up_trade_no = ?", first.TradeNo).
		Update("expires_time", common.GetTimestamp()-1).Error)
	requireDiscountReservationCapacityBlocked(t, db, code, blocked)
	requireDiscountReservationCreditOnce(t, db, code, firstUser, first)
	requireDiscountReservationCapacityBlocked(t, db, code, blocked)
}

func TestLimitedDiscountReservationWaffoTimeoutKeepsLatePaymentCapacity(t *testing.T) {
	for _, legacyReleased := range []bool{false, true} {
		name := "retained"
		if legacyReleased {
			name = "legacy_released"
		}
		t.Run(name, func(t *testing.T) {
			db, code := limitedDiscountReservationFixture(t)
			user, first := pendingDiscountReservationFixture(t, db, code, "first", PaymentProviderWaffoPancake)
			require.NoError(t, first.Insert())
			failed, err := FailExpiredWaffoPancakeTopUp(context.Background(), first.Id, first.CreateTime, common.GetTimestamp())
			require.NoError(t, err)
			require.True(t, failed)
			var reservation DiscountCodeReservation
			require.NoError(t, db.Where("top_up_trade_no = ?", first.TradeNo).First(&reservation).Error)
			require.Equal(t, DiscountCodeReservationStatusReserved, reservation.Status)
			if legacyReleased {
				// Old versions released this recoverable state. No migration or
				// current timestamp must make its outstanding liability invisible.
				require.NoError(t, db.Model(&reservation).Update("status", DiscountCodeReservationStatusReleased).Error)
			}
			require.NoError(t, db.Model(&reservation).Update("expires_time", common.GetTimestamp()-1).Error)
			_, blocked := pendingDiscountReservationFixture(t, db, code, "blocked", PaymentProviderWaffoPancake)
			requireDiscountReservationCapacityBlocked(t, db, code, blocked)
			requireDiscountReservationCreditOnce(t, db, code, user, first)
		})
	}
}

func TestLimitedDiscountReservationDefinitiveCloseReusesCapacity(t *testing.T) {
	for _, tc := range []struct {
		name, provider string
		close          func(*TopUp) error
	}{
		{"stripe_expired", PaymentProviderStripe, func(o *TopUp) error {
			return UpdatePendingTopUpStatus(o.TradeNo, o.PaymentProvider, common.TopUpStatusExpired)
		}},
		{"waffo_failed", PaymentProviderWaffo, func(o *TopUp) error {
			return UpdatePendingTopUpStatus(o.TradeNo, o.PaymentProvider, common.TopUpStatusFailed)
		}},
		{"company_billing_failure", PaymentProviderWaffoPancake, func(o *TopUp) error {
			return FailPendingTopUpForCheckout(o.TradeNo, o.PaymentProvider, PaymentOrderFailureCompanyBillingRules)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, code := limitedDiscountReservationFixture(t)
			firstUser, first := pendingDiscountReservationFixture(t, db, code, "first", tc.provider)
			require.NoError(t, first.Insert())
			require.NoError(t, tc.close(first))
			var reservation DiscountCodeReservation
			require.NoError(t, db.Where("top_up_trade_no = ?", first.TradeNo).First(&reservation).Error)
			require.Equal(t, DiscountCodeReservationStatusReleased, reservation.Status)
			_, err := CompleteExternalTopUp(discountReservationSettlement(first))
			require.ErrorIs(t, err, ErrTopUpStatusInvalid, "only existing non-settleable states free capacity")
			var unchanged User
			require.NoError(t, db.First(&unchanged, firstUser.Id).Error)
			require.Zero(t, unchanged.Quota)

			secondUser, second := pendingDiscountReservationFixture(t, db, code, "second", tc.provider)
			_, err = ValidateDiscountCodeForUser(code.Code, 10, common.GetTimestamp(), second.UserId)
			require.NoError(t, err)
			require.NoError(t, second.Insert())
			requireDiscountReservationCreditOnce(t, db, code, secondUser, second)
		})
	}
}

func TestLimitedDiscountReservationReleaseFailsClosedWithoutTerminalOrder(t *testing.T) {
	for _, state := range []string{common.TopUpStatusPending, "unknown", "missing"} {
		t.Run(state, func(t *testing.T) {
			db, code := limitedDiscountReservationFixture(t)
			_, first := pendingDiscountReservationFixture(t, db, code, "first", PaymentProviderWaffoPancake)
			require.NoError(t, first.Insert())
			if state == "missing" {
				require.NoError(t, db.Delete(first).Error)
			} else {
				require.NoError(t, db.Model(first).Update("status", state).Error)
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				return releaseDiscountCodeReservationTx(tx, first.TradeNo)
			}))
			var reservation DiscountCodeReservation
			require.NoError(t, db.Where("top_up_trade_no = ?", first.TradeNo).First(&reservation).Error)
			require.Equal(t, DiscountCodeReservationStatusReserved, reservation.Status)
			_, blocked := pendingDiscountReservationFixture(t, db, code, "blocked", PaymentProviderWaffoPancake)
			requireDiscountReservationCapacityBlocked(t, db, code, blocked)
		})
	}
}

func TestLimitedDiscountReservationLegacyPayableOrdersKeepCapacity(t *testing.T) {
	for _, missingReservation := range []bool{false, true} {
		name := "released_pending"
		if missingReservation {
			name = "missing_reservation"
		}
		t.Run(name, func(t *testing.T) {
			db, code := limitedDiscountReservationFixture(t)
			user, first := pendingDiscountReservationFixture(t, db, code, "first", PaymentProviderWaffoPancake)
			if missingReservation {
				// Legacy checkouts predate atomic reservation. Their existing
				// signed payment remains valid and cannot fund a new coupon slot.
				require.NoError(t, db.Create(first).Error)
			} else {
				require.NoError(t, first.Insert())
				require.NoError(t, db.Model(&DiscountCodeReservation{}).
					Where("top_up_trade_no = ?", first.TradeNo).
					Update("status", DiscountCodeReservationStatusReleased).Error)
			}
			_, blocked := pendingDiscountReservationFixture(t, db, code, "blocked", PaymentProviderWaffoPancake)
			requireDiscountReservationCapacityBlocked(t, db, code, blocked)
			requireDiscountReservationCreditOnce(t, db, code, user, first)
		})
	}
}

func TestLimitedDiscountReservationSavedTerminalOrdersReuseCapacity(t *testing.T) {
	for _, tc := range []struct {
		name, provider, status, reason string
		wantHeld                       bool
	}{
		{"stripe_async_payment_failed", PaymentProviderStripe, common.TopUpStatusFailed, "", false},
		{"waffo_checkout_failed", PaymentProviderWaffo, common.TopUpStatusFailed, "", false},
		{"historical_expired", PaymentProviderStripe, common.TopUpStatusExpired, "", false},
		{"waffo_recoverable_timeout", PaymentProviderWaffoPancake, common.TopUpStatusFailed, string(PaymentOrderFailureCheckoutTimeout), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, code := limitedDiscountReservationFixture(t)
			firstUser, first := pendingDiscountReservationFixture(t, db, code, "first", tc.provider)
			require.NoError(t, first.Insert())
			// Stripe async failure and Waffo SDK/business failure handlers use
			// this Save path. It leaves the reservation's status untouched.
			first.Status, first.FailureReasonCode = tc.status, tc.reason
			require.NoError(t, first.Update())
			var reservation DiscountCodeReservation
			require.NoError(t, db.Where("top_up_trade_no = ?", first.TradeNo).First(&reservation).Error)
			require.Equal(t, DiscountCodeReservationStatusReserved, reservation.Status)
			secondUser, second := pendingDiscountReservationFixture(t, db, code, "second", tc.provider)
			if tc.wantHeld {
				requireDiscountReservationCapacityBlocked(t, db, code, second)
				requireDiscountReservationCreditOnce(t, db, code, firstUser, first)
				return
			}
			_, err := CompleteExternalTopUp(discountReservationSettlement(first))
			require.ErrorIs(t, err, ErrTopUpStatusInvalid)
			_, err = ValidateDiscountCodeForUser(code.Code, 10, common.GetTimestamp(), second.UserId)
			require.NoError(t, err)
			require.NoError(t, second.Insert())
			requireDiscountReservationCreditOnce(t, db, code, secondUser, second)
		})
	}
}
