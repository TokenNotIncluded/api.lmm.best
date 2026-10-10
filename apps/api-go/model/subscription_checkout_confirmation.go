package model

import (
	"context"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// SubscriptionCheckoutConfirmation contains only order-specific, read-only
// evidence. Order success is the existing settlement transaction's receipt;
// UserSubscriptionId must also resolve to that order's user and plan.
type SubscriptionCheckoutConfirmation struct {
	TradeNo            string `json:"trade_no"`
	UserId             int    `json:"user_id"`
	PlanId             int    `json:"plan_id"`
	PaymentStatus      string `json:"payment_status"`
	CompleteTime       int64  `json:"complete_time"`
	UserSubscriptionId int    `json:"user_subscription_id"`
	Confirmed          bool   `json:"confirmed"`
}

// One statement observes the committed order/grant pair. Mutable usage,
// reset times and subscription-list ordering are deliberately absent.
const subscriptionCheckoutConfirmationSQL = `
SELECT o.trade_no, o.user_id, o.plan_id, o.status AS payment_status,
       o.complete_time, COALESCE(s.id, 0) AS user_subscription_id,
       CASE WHEN o.status = ? AND o.complete_time > 0 AND s.id IS NOT NULL
            THEN 1 ELSE 0 END AS confirmed
FROM subscription_orders AS o
LEFT JOIN user_subscriptions AS s
  ON s.id = o.user_subscription_id AND s.user_id = o.user_id AND s.plan_id = o.plan_id
WHERE o.user_id = ? AND o.trade_no = ?
LIMIT 1`

func GetSubscriptionCheckoutConfirmation(ctx context.Context, userId int, tradeNo string) (*SubscriptionCheckoutConfirmation, error) {
	if userId <= 0 || tradeNo == "" || len(tradeNo) > 255 || strings.TrimSpace(tradeNo) != tradeNo {
		return nil, ErrSubscriptionOrderNotFound
	}
	var result SubscriptionCheckoutConfirmation
	query := DB.WithContext(ctx).Raw(subscriptionCheckoutConfirmationSQL,
		common.TopUpStatusSuccess, userId, tradeNo).Scan(&result)
	if query.Error != nil {
		return nil, query.Error
	}
	// Preserve exact identity even on databases with case-insensitive collation.
	if query.RowsAffected != 1 || result.TradeNo != tradeNo || result.UserId != userId {
		return nil, gorm.ErrRecordNotFound
	}
	return &result, nil
}
