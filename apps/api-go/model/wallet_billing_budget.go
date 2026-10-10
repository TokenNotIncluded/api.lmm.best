package model

import (
	"errors"
	"math"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

var ErrWalletBillingBudgetQuota = errors.New("wallet quota insufficient for billing budget")

// ReserveWalletBillingBudget atomically reserves a wallet budget and its token
// budget. Pending subscription wallet holds count against the available wallet;
// minimum applies to the starting balance, and only amount is deducted. Callers
// may use tokenID=0 only for internal requests that do not carry an API token.
func ReserveWalletBillingBudget(userID, tokenID, amount, minimum int) error {
	if userID <= 0 || tokenID < 0 {
		return gorm.ErrInvalidData
	}
	if amount < 0 || minimum < 0 {
		return errors.New("billing budget and minimum must not be negative")
	}
	if err := common.ValidateWalletQuota(amount); err != nil {
		return err
	}
	if err := common.ValidateWalletQuota(minimum); err != nil {
		return err
	}

	var tokenKey string
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockSubscriptionBillingUser(tx, userID); err != nil {
			return err
		}
		pending, err := pendingSubscriptionWalletQuota(tx, userID, "")
		if err != nil {
			return err
		}
		if pending > math.MaxInt64-int64(amount) {
			return ErrWalletQuotaOutOfRange
		}
		if amount == 0 {
			quota, err := currentWalletQuota(tx, userID)
			if err != nil {
				return err
			}
			if err := common.ValidateWalletQuota(quota); err != nil {
				return ErrWalletQuotaOutOfRange
			}
			if int64(quota) < pending || quota < minimum {
				return ErrWalletBillingBudgetQuota
			}
			if tokenID != 0 {
				var token Token
				return lockForUpdate(tx).Select("id").Where("id = ? AND user_id = ?", tokenID, userID).First(&token).Error
			}
			return nil
		}
		reserved := UpdateWalletQuotaByDelta(
			tx.Model(&User{}).Where("id = ? AND quota >= ? AND quota >= ?", userID, pending+int64(amount), minimum),
			-amount,
		)
		if reserved.Error != nil {
			return reserved.Error
		}
		if reserved.RowsAffected != 1 {
			quota, err := currentWalletQuota(tx, userID)
			if err != nil {
				return err
			}
			if err := common.ValidateWalletQuota(quota); err != nil {
				return ErrWalletQuotaOutOfRange
			}
			return ErrWalletBillingBudgetQuota
		}
		tokenKey, err = subscriptionBillingTokenDelta(tx, userID, tokenID, int64(amount), true)
		return err
	})
	if err != nil {
		return err
	}
	if amount != 0 {
		syncSubscriptionBillingCache(userID, -int64(amount), tokenKey)
	}
	return nil
}

// RefundWalletBillingBudget returns an unused reservation to the wallet and
// token in one transaction, including a token deleted after admission. The
// billing session must ensure each reservation is refunded at most once.
func RefundWalletBillingBudget(userID, tokenID, amount int) error {
	if userID <= 0 || tokenID < 0 {
		return gorm.ErrInvalidData
	}
	if amount < 0 {
		return errors.New("billing budget refund must not be negative")
	}
	if err := common.ValidateWalletQuota(amount); err != nil {
		return err
	}
	var tokenKey string
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockSubscriptionBillingUser(tx, userID); err != nil {
			return err
		}
		if err := ApplyWalletQuotaDelta(tx, userID, amount); err != nil {
			return err
		}
		if amount == 0 {
			if tokenID != 0 {
				var token Token
				return lockForUpdate(tx.Unscoped()).Select("id").Where("id = ? AND user_id = ?", tokenID, userID).First(&token).Error
			}
			return nil
		}
		var err error
		tokenKey, err = subscriptionBillingTokenDelta(tx, userID, tokenID, -int64(amount), false)
		return err
	})
	if err != nil {
		return err
	}
	if amount != 0 {
		syncSubscriptionBillingCache(userID, int64(amount), tokenKey)
	}
	return nil
}
