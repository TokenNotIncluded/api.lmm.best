package model

import (
	"context"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// DueWaffoPancakeTopUps returns a bounded set of old wallet checkouts. The
// payment check timestamp rotates uncertain orders behind unchecked ones so a
// paid order awaiting a webhook cannot starve the rest of the queue.
func DueWaffoPancakeTopUps(ctx context.Context, createdBefore, checkedBefore int64, limit int) ([]TopUp, error) {
	if limit <= 0 {
		return nil, nil
	}
	if limit > 100 {
		limit = 100
	}
	var orders []TopUp
	err := DB.WithContext(ctx).Select("id", "trade_no", "create_time", "payment_checked_at").
		Where("payment_provider = ? AND status = ? AND create_time > 0 AND create_time <= ? AND payment_checked_at <= ?",
			PaymentProviderWaffoPancake, common.TopUpStatusPending, createdBefore, checkedBefore).
		Order("payment_checked_at ASC, id ASC").Limit(limit).Find(&orders).Error
	return orders, err
}

func HasDueWaffoPancakeTopUps(ctx context.Context, createdBefore, checkedBefore int64) (bool, error) {
	orders, err := DueWaffoPancakeTopUps(ctx, createdBefore, checkedBefore, 1)
	return len(orders) != 0, err
}

// MarkWaffoPancakeTopUpPaymentChecked delays another provider lookup while
// preserving a concurrent settlement or timeout transition.
func MarkWaffoPancakeTopUpPaymentChecked(ctx context.Context, id int, checkedAt int64) error {
	return DB.WithContext(ctx).Model(&TopUp{}).
		Where("id = ? AND payment_provider = ? AND status = ? AND payment_checked_at < ?",
			id, PaymentProviderWaffoPancake, common.TopUpStatusPending, checkedAt).
		Update("payment_checked_at", checkedAt).Error
}

// FailExpiredWaffoPancakeTopUp closes an old wallet checkout. The caller must
// first confirm the provider has no live or successful payment. The status and
// age predicates preserve a concurrent signed webhook settlement.
func FailExpiredWaffoPancakeTopUp(ctx context.Context, id int, createdBefore, failedAt int64) (bool, error) {
	failed := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&TopUp{}).
			Where("id = ? AND payment_provider = ? AND status = ? AND create_time > 0 AND create_time <= ?",
				id, PaymentProviderWaffoPancake, common.TopUpStatusPending, createdBefore).
			Updates(map[string]any{
				"status":              common.TopUpStatusFailed,
				"complete_time":       failedAt,
				"failure_reason_code": string(PaymentOrderFailureCheckoutTimeout),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		var order TopUp
		if err := tx.Select("trade_no", "discount_code_id").First(&order, id).Error; err != nil {
			return err
		}
		if order.DiscountCodeId != 0 {
			if err := releaseDiscountCodeReservationTx(tx, order.TradeNo); err != nil {
				return err
			}
		}
		failed = true
		return nil
	})
	return failed, err
}
