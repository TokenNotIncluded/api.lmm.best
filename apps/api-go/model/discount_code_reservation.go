package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	DiscountCodeReservationStatusReserved = "reserved"
	DiscountCodeReservationStatusConsumed = "consumed"
	DiscountCodeReservationStatusReleased = "released"

	discountCodeReservationTTL = 24 * time.Hour
)

// DiscountCodeReservation atomically prevents two unpaid orders from both
// claiming the last limited-use coupon slot. ExpiresTime is not permission to
// reuse a slot while its order can still settle. A later signed payment is always
// credited, including legacy released reservations; provider settlement must never
// fail because coupon capacity changed after checkout.
type DiscountCodeReservation struct {
	Id             int    `json:"id"`
	DiscountCodeId int    `json:"discount_code_id" gorm:"not null;index"`
	TopUpTradeNo   string `json:"topup_trade_no" gorm:"type:varchar(255);not null;uniqueIndex"`
	UserId         int    `json:"user_id" gorm:"not null;index"`
	Status         string `json:"status" gorm:"type:varchar(16);not null;index"`
	ExpiresTime    int64  `json:"expires_time" gorm:"not null;index"`
	CreatedTime    int64  `json:"created_time" gorm:"not null"`
	UpdatedTime    int64  `json:"updated_time" gorm:"not null"`
}

func validateDiscountCodeReservationTerms(code *DiscountCode, topUp *TopUp, now int64) error {
	if code == nil || topUp == nil || code.Id <= 0 || topUp.TradeNo == "" {
		return ErrDiscountCodeNotFound
	}
	if code.Status != DiscountCodeStatusEnabled {
		return ErrDiscountCodeInactive
	}
	if code.StartsTime > 0 && code.StartsTime > now {
		return ErrDiscountCodeInactive
	}
	if code.ExpiredTime > 0 && code.ExpiredTime < now {
		return ErrDiscountCodeExpired
	}
	requestedAmount := decimal.NewFromInt(topUp.Amount)
	if topUp.PlatformAmountMicros > 0 {
		requestedAmount = decimal.NewFromInt(topUp.PlatformAmountMicros).Shift(-6)
	}

	if topUp.DiscountQualifyingAmount != "" {
		if len(topUp.DiscountQualifyingAmount) > 64 {
			return gorm.ErrInvalidData
		}
		captured, err := decimal.NewFromString(topUp.DiscountQualifyingAmount)
		if err != nil || captured.IsNegative() {
			return gorm.ErrInvalidData
		}
		expected := requestedAmount
		switch topUp.DiscountQualifyingUnit {
		case "CREDIT":
			if !captured.IsPositive() {
				return gorm.ErrInvalidData
			}
			expected = decimal.NewFromInt(topUp.CreditedQuota)
		case "LEGACY":
		default:
			return gorm.ErrInvalidData
		}
		if !captured.Equal(expected) {
			return gorm.ErrInvalidData
		}
		requestedAmount = captured
	}
	if requestedAmount.LessThan(decimal.NewFromInt(code.MinAmount)) {
		return ErrDiscountCodeMinimum
	}
	if code.OwnerUserID != 0 && code.OwnerUserID != topUp.UserId {
		return ErrDiscountCodeNotFound
	}
	return nil
}

// payableDiscountCodeTopUps mirrors CompleteExternalTopUp's unsettled states.
// A local Waffo checkout timeout is recoverable by a later signed payment, so
// neither that state nor the reservation TTL proves that a coupon slot is free.
func payableDiscountCodeTopUps(tx *gorm.DB) *gorm.DB {
	return tx.Model(&TopUp{}).Select("trade_no").
		Where("status = ? OR (status = ? AND payment_provider = ? AND failure_reason_code = ?)",
			common.TopUpStatusPending, common.TopUpStatusFailed, PaymentProviderWaffoPancake, string(PaymentOrderFailureCheckoutTimeout))
}

// closedDiscountCodeTopUps proves that an existing order cannot settle under
// the current lifecycle. Older callers persist terminal states with TopUp.Update
// without releasing the reservation, so both counting and release use this rule.
func closedDiscountCodeTopUps(tx *gorm.DB) *gorm.DB {
	return tx.Model(&TopUp{}).Select("trade_no").
		Where("trade_no IS NOT NULL").
		Where("status = ? OR (status = ? AND (COALESCE(payment_provider, '') <> ? OR failure_reason_code <> ?))",
			common.TopUpStatusExpired, common.TopUpStatusFailed, PaymentProviderWaffoPancake, string(PaymentOrderFailureCheckoutTimeout))
}

// countDiscountCodeCapacityHolds is shared by preview and atomic reservation.
// Reserved rows remain held unless an existing order is definitively closed;
// missing or unknown orders fail closed. Older released rows
// also retain capacity whenever their persisted order can still accept payment.
// Consumed rows are already represented by DiscountCode.UsedCount. Legacy
// payable orders without a reservation row are also outstanding liabilities.
func countDiscountCodeCapacityHolds(tx *gorm.DB, discountCodeID int) (int64, error) {
	var count int64
	err := tx.Model(&DiscountCodeReservation{}).
		Where("discount_code_id = ?", discountCodeID).
		Where("(status = ? AND top_up_trade_no NOT IN (?)) OR (status = ? AND top_up_trade_no IN (?))",
			DiscountCodeReservationStatusReserved, closedDiscountCodeTopUps(tx),
			DiscountCodeReservationStatusReleased, payableDiscountCodeTopUps(tx)).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	var unreservedOrders int64
	reservations := tx.Model(&DiscountCodeReservation{}).Select("1").
		Where("top_up_trade_no = top_ups.trade_no")
	err = payableDiscountCodeTopUps(tx).
		Where("discount_code_id = ?", discountCodeID).
		Where("NOT EXISTS (?)", reservations).
		Count(&unreservedOrders).Error
	return count + unreservedOrders, err
}

func reserveDiscountCodeUsageTx(tx *gorm.DB, topUp *TopUp) error {
	if topUp == nil || topUp.DiscountCodeId <= 0 {
		return nil
	}
	now := common.GetTimestamp()
	var code DiscountCode
	if err := lockForUpdate(tx).Where("id = ?", topUp.DiscountCodeId).First(&code).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDiscountCodeNotFound
		}
		return err
	}
	if err := validateDiscountCodeReservationTerms(&code, topUp, now); err != nil {
		return err
	}

	var existing DiscountCodeReservation
	existingErr := lockForUpdate(tx).Where("top_up_trade_no = ?", topUp.TradeNo).First(&existing).Error
	if existingErr == nil {
		if existing.DiscountCodeId != code.Id || existing.UserId != topUp.UserId {
			return ErrPaymentEvidenceConflict
		}
		return nil
	}
	if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
		return existingErr
	}

	if code.MaxUses > 0 {
		activeReservations, err := countDiscountCodeCapacityHolds(tx, code.Id)
		if err != nil {
			return err
		}
		if code.UsedCount+activeReservations >= code.MaxUses {
			return ErrDiscountCodeExhausted
		}
	}

	reservation := &DiscountCodeReservation{
		DiscountCodeId: code.Id,
		TopUpTradeNo:   strings.TrimSpace(topUp.TradeNo),
		UserId:         topUp.UserId,
		Status:         DiscountCodeReservationStatusReserved,
		ExpiresTime:    now + int64(discountCodeReservationTTL/time.Second),
		CreatedTime:    now,
		UpdatedTime:    now,
	}
	if err := tx.Create(reservation).Error; err != nil {
		return fmt.Errorf("reserve discount code usage: %w", err)
	}
	return nil
}

func consumeReservedDiscountCodeUsageTx(tx *gorm.DB, topUp *TopUp) error {
	if topUp == nil || topUp.DiscountCodeId <= 0 {
		return nil
	}
	now := common.GetTimestamp()
	var reservation DiscountCodeReservation
	reservationErr := lockForUpdate(tx).Where("top_up_trade_no = ?", topUp.TradeNo).First(&reservation).Error
	if reservationErr == nil {
		if reservation.DiscountCodeId != topUp.DiscountCodeId || reservation.UserId != topUp.UserId {
			return ErrPaymentEvidenceConflict
		}
		if reservation.Status == DiscountCodeReservationStatusConsumed {
			return nil
		}
	} else if !errors.Is(reservationErr, gorm.ErrRecordNotFound) {
		return reservationErr
	}

	usage := tx.Unscoped().Model(&DiscountCode{}).
		Where("id = ?", topUp.DiscountCodeId).
		UpdateColumn("used_count", gorm.Expr("used_count + ?", 1))
	if usage.Error != nil {
		return usage.Error
	}
	// A deleted legacy code must never block credit for an already-paid order.
	if usage.RowsAffected == 0 {
		return nil
	}

	if reservationErr == nil {
		return tx.Model(&reservation).Updates(map[string]any{
			"status":       DiscountCodeReservationStatusConsumed,
			"updated_time": now,
		}).Error
	}
	return tx.Create(&DiscountCodeReservation{
		DiscountCodeId: topUp.DiscountCodeId,
		TopUpTradeNo:   strings.TrimSpace(topUp.TradeNo),
		UserId:         topUp.UserId,
		Status:         DiscountCodeReservationStatusConsumed,
		ExpiresTime:    now,
		CreatedTime:    now,
		UpdatedTime:    now,
	}).Error
}

func releaseDiscountCodeReservationTx(tx *gorm.DB, tradeNo string) error {
	tradeNo = strings.TrimSpace(tradeNo)
	if tradeNo == "" {
		return nil
	}
	// Only existing failed/expired states that settlement rejects can release a
	// hold. Keep pending, unknown, missing, and recoverable timeout orders held.
	closedOrders := closedDiscountCodeTopUps(tx).Where("trade_no = ?", tradeNo)
	return tx.Model(&DiscountCodeReservation{}).
		Where("top_up_trade_no = ? AND status = ? AND top_up_trade_no IN (?)", tradeNo, DiscountCodeReservationStatusReserved, closedOrders).
		Updates(map[string]any{
			"status":       DiscountCodeReservationStatusReleased,
			"updated_time": common.GetTimestamp(),
		}).Error
}
