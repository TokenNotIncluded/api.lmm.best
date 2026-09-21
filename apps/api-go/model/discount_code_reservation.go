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
// claiming the last limited-use coupon slot. A later signed payment is always credited even
// when its reservation expired or was released; provider settlement must never
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
	if requestedAmount.LessThan(decimal.NewFromInt(code.MinAmount)) {
		return ErrDiscountCodeMinimum
	}
	if code.OwnerUserID != 0 && code.OwnerUserID != topUp.UserId {
		return ErrDiscountCodeNotFound
	}
	return nil
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
		var activeReservations int64
		if err := tx.Model(&DiscountCodeReservation{}).
			Where("discount_code_id = ? AND status = ? AND expires_time > ?", code.Id, DiscountCodeReservationStatusReserved, now).
			Count(&activeReservations).Error; err != nil {
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
	return tx.Model(&DiscountCodeReservation{}).
		Where("top_up_trade_no = ? AND status = ?", tradeNo, DiscountCodeReservationStatusReserved).
		Updates(map[string]any{
			"status":       DiscountCodeReservationStatusReleased,
			"updated_time": common.GetTimestamp(),
		}).Error
}
