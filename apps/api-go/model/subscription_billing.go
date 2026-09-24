package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const (
	SubscriptionBillingRecoveryPending      = "pending"
	SubscriptionBillingRecoveryManual       = "manual"
	SubscriptionBillingRecoveryMaxAttempts  = 8
	subscriptionBillingRecoveryRetrySeconds = 60
)

// ListSubscriptionBillingRecoveryCandidates returns a bounded snapshot. Only
// managed records are eligible; legacy/non-managed rows are never touched.
func ListSubscriptionBillingRecoveryCandidates(ctx context.Context, limit int, retryAfter time.Duration) ([]SubscriptionPreConsumeRecord, error) {
	if limit <= 0 {
		return nil, nil
	}
	var records []SubscriptionPreConsumeRecord
	cutoff := common.GetTimestamp() - int64(retryAfter.Seconds())
	err := DB.WithContext(ctx).Where("billing_managed = ? AND status = ? AND COALESCE(recovery_state, '') <> ? AND request_id <> '' AND user_id > 0 AND actual_quota >= 0 AND recovery_attempts < ? AND (recovery_last_attempt_at = 0 OR recovery_last_attempt_at <= ?)", true, "settling", SubscriptionBillingRecoveryManual, SubscriptionBillingRecoveryMaxAttempts, cutoff).Order("updated_at asc, id asc").Limit(limit).Find(&records).Error
	return records, err
}

func MarkSubscriptionBillingRecoveryAttempt(ctx context.Context, id int) error {
	now := common.GetTimestamp()
	res := DB.WithContext(ctx).Model(&SubscriptionPreConsumeRecord{}).Where("id = ? AND billing_managed = ? AND status = ? AND recovery_attempts < ? AND (recovery_last_attempt_at = 0 OR recovery_last_attempt_at <= ?)", id, true, "settling", SubscriptionBillingRecoveryMaxAttempts, now-int64(subscriptionBillingRecoveryRetrySeconds)).Updates(map[string]interface{}{"recovery_attempts": gorm.Expr("recovery_attempts + 1"), "recovery_last_attempt_at": now, "recovery_last_error": "", "recovery_state": "recovering"})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errors.New("subscription billing recovery already claimed")
	}
	return nil
}

func MarkSubscriptionBillingRecoveryFailure(ctx context.Context, id int, err error, manual bool) error {
	message := "recovery failed"
	if err != nil {
		message = err.Error()
		if len(message) > 2048 {
			message = message[:2048]
		}
	}
	state := SubscriptionBillingRecoveryPending
	if manual {
		state = SubscriptionBillingRecoveryManual
	}
	return DB.WithContext(ctx).Model(&SubscriptionPreConsumeRecord{}).Where("id = ? AND status = ?", id, "settling").Updates(map[string]interface{}{"recovery_last_error": message, "recovery_state": state}).Error
}

func MarkSubscriptionBillingRecoverySuccess(ctx context.Context, id int) error {
	return DB.WithContext(ctx).Model(&SubscriptionPreConsumeRecord{}).Where("id = ? AND status = ?", id, "settled").Updates(map[string]interface{}{"recovery_state": "", "recovery_last_error": ""}).Error
}

var ErrSubscriptionBillingTokenQuota = errors.New("token quota insufficient")
var ErrSubscriptionBillingWalletQuota = errors.New("wallet quota insufficient for subscription overflow")

func pendingSubscriptionWalletQuota(tx *gorm.DB, userID int, excludeRequestID string) (int64, error) {
	var pending int64
	query := tx.Model(&SubscriptionPreConsumeRecord{}).
		Select("COALESCE(SUM(CASE WHEN token_consumed > pre_consumed THEN token_consumed - pre_consumed ELSE 0 END), 0)").
		Where("user_id = ? AND billing_managed = ? AND wallet_overflow = ? AND status IN ?", userID, true, true, []string{"consumed", "settling"})
	if excludeRequestID != "" {
		query = query.Where("request_id <> ?", excludeRequestID)
	}
	if err := query.Scan(&pending).Error; err != nil {
		// Legacy SQLite fixtures can omit the billing ledger. A missing table in
		// a production database is an error, never an authorization bypass.
		if tx.Dialector.Name() == "sqlite" && strings.Contains(err.Error(), "no such table: subscription_pre_consume_records") {
			return 0, nil
		}
		return 0, err
	}
	return pending, nil
}

// Check the uncovered budget under the managed user lock. Keeping the wallet
// debit at final settlement preserves rollback compatibility with older Go
// packages, which know how to refund subscription/token reservations only.
// Other in-flight managed requests count against the same available balance.
func authorizeSubscriptionWalletQuota(tx *gorm.DB, userID int, requestID string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	if err := common.ValidateWalletQuota(int(amount)); err != nil {
		return err
	}
	quota, err := currentWalletQuota(tx, userID)
	if err != nil {
		return err
	}
	pending, err := pendingSubscriptionWalletQuota(tx, userID, requestID)
	if err != nil {
		return err
	}
	if pending > math.MaxInt64-amount || int64(quota) < pending+amount {
		return ErrSubscriptionBillingWalletQuota
	}
	return nil
}

// SubscriptionBillingResult separates measured cost from committed debits.
// A settling record can be retried with the same request ID and actual quota,
// without resending the upstream request. ReservedQuota includes every Reserve.
type SubscriptionBillingResult struct {
	RequestId         string
	Status            string
	ActualQuota       int64
	ReservedQuota     int64
	SubscriptionQuota int64
	WalletQuota       int64
	TokenQuota        int64
}

func subscriptionBillingResult(r *SubscriptionPreConsumeRecord) *SubscriptionBillingResult {
	sub := r.PreConsumed
	if r.Status == "settled" {
		sub = r.ActualQuota - r.WalletConsumed
	}
	if r.Status == "refunded" {
		sub = 0
	}
	return &SubscriptionBillingResult{
		RequestId: r.RequestId, Status: r.Status, ActualQuota: r.ActualQuota,
		ReservedQuota: r.PreConsumed, SubscriptionQuota: sub, WalletQuota: r.WalletConsumed, TokenQuota: r.TokenConsumed,
	}
}

func GetSubscriptionBillingResult(requestID string, userID int) (*SubscriptionBillingResult, error) {
	var r SubscriptionPreConsumeRecord
	if err := DB.Where("request_id = ? AND user_id = ? AND billing_managed = ?", requestID, userID, true).First(&r).Error; err != nil {
		return nil, err
	}
	return subscriptionBillingResult(&r), nil
}

// All managed operations lock user -> request -> subscriptions -> token. The
// user lock also serializes competing requests and subscription policy changes.
func lockSubscriptionBillingUser(tx *gorm.DB, userID int) error {
	var user User
	return lockForUpdate(tx).Select("id").Where("id = ?", userID).First(&user).Error
}

func subscriptionBillingTokenDelta(tx *gorm.DB, userID, tokenID int, delta int64, reserve bool) (string, error) {
	if tokenID == 0 {
		return "", nil
	} // playground/assistant
	var token Token
	tokenDB := tx
	if !reserve {
		tokenDB = tokenDB.Unscoped()
	}
	if err := lockForUpdate(tokenDB).Where("id = ? AND user_id = ?", tokenID, userID).First(&token).Error; err != nil {
		return "", err
	}
	if reserve && !token.UnlimitedQuota && int64(token.RemainQuota) < delta {
		return "", ErrSubscriptionBillingTokenQuota
	}
	if delta > 0 && (int64(token.RemainQuota) < math.MinInt64+delta || int64(token.UsedQuota) > math.MaxInt64-delta) {
		return "", errors.New("token quota overflow")
	}
	if delta < 0 && (int64(token.RemainQuota) > math.MaxInt64+delta || int64(token.UsedQuota) < math.MinInt64-delta) {
		return "", errors.New("token quota overflow")
	}
	res := tokenDB.Model(&Token{}).Where("id = ? AND user_id = ?", tokenID, userID).Updates(map[string]interface{}{
		"remain_quota": gorm.Expr("remain_quota - ?", delta), "used_quota": gorm.Expr("used_quota + ?", delta), "accessed_time": common.GetTimestamp(),
	})
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected != 1 {
		return "", errors.New("billing token disappeared")
	}
	return token.Key, nil
}

func syncSubscriptionBillingCache(userID int, walletDelta int64, tokenKey string) {
	// Invalidate after commit rather than applying a delta to a cache that may
	// already have hydrated the committed value. Never enqueue batch DB writes.
	if walletDelta != 0 && common.RedisEnabled {
		if err := invalidateUserCache(userID); err != nil {
			common.SysLog("invalidate subscription wallet cache: " + err.Error())
		}
	}
	if tokenKey != "" {
		if err := invalidateTokenCacheForMutation(tokenKey); err != nil {
			common.SysLog("invalidate subscription token cache: " + err.Error())
		}
	}
}

// PreConsumeSubscriptionBilling atomically reserves subscription and token,
// including request-ID replay protection. tokenID=0 is for internal requests.
func PreConsumeSubscriptionBilling(requestID string, userID, tokenID int, modelName string, amount int64, walletOverflow bool) (*SubscriptionPreConsumeResult, error) {
	var result *SubscriptionPreConsumeResult
	var tokenKey string
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockSubscriptionBillingUser(tx, userID); err != nil {
			return err
		}
		var existing SubscriptionPreConsumeRecord
		q := tx.Where("request_id = ?", requestID).Limit(1).Find(&existing)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected > 0 && (!existing.BillingManaged || existing.UserId != userID || existing.TokenId != tokenID || existing.WalletOverflow != walletOverflow || existing.Status != "consumed") {
			return errors.New("subscription billing replay mismatch")
		}
		var err error
		result, err = preConsumeUserSubscriptionWithPolicy(tx, requestID, userID, modelName, 0, amount, walletOverflow)
		if err != nil {
			return err
		}
		if q.RowsAffected > 0 {
			result.TokenConsumed = existing.TokenConsumed
			return nil
		}
		walletReserve := amount - result.PreConsumed
		if walletReserve > 0 {
			if !walletOverflow {
				return ErrSubscriptionQuotaInsufficient
			}
			if err := authorizeSubscriptionWalletQuota(tx, userID, requestID, walletReserve); err != nil {
				return err
			}
		}
		var subscription UserSubscription
		if err := tx.First(&subscription, result.UserSubscriptionId).Error; err != nil {
			return err
		}
		// The subscription may reserve only its remaining grant, but an API
		// token limit still authorizes the entire requested budget.
		tokenKey, err = subscriptionBillingTokenDelta(tx, userID, tokenID, amount, true)
		if err != nil {
			return err
		}
		tokenConsumed := amount
		if tokenID == 0 {
			tokenConsumed = 0
		}
		result.TokenConsumed = tokenConsumed
		return tx.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", requestID).Updates(map[string]interface{}{
			"billing_managed": true, "token_id": tokenID, "token_consumed": tokenConsumed, "wallet_overflow": walletOverflow,
			"reserved_version": subscription.QuotaVersion,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	syncSubscriptionBillingCache(userID, 0, tokenKey)
	return result, nil
}

func mutateSubscriptionBillingContext(ctx context.Context, requestID string, userID int, fn func(*gorm.DB, *SubscriptionPreConsumeRecord) (int64, string, error)) (*SubscriptionBillingResult, error) {
	var record SubscriptionPreConsumeRecord
	var walletDelta int64
	var tokenKey string
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockSubscriptionBillingUser(tx, userID); err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("request_id = ? AND user_id = ? AND billing_managed = ?", requestID, userID, true).First(&record).Error; err != nil {
			return err
		}
		var err error
		walletDelta, tokenKey, err = fn(tx, &record)
		if err != nil {
			return err
		}
		return tx.Save(&record).Error
	})
	if err != nil {
		return nil, err
	}
	syncSubscriptionBillingCache(userID, walletDelta, tokenKey)
	return subscriptionBillingResult(&record), nil
}

func mutateSubscriptionBilling(requestID string, userID int, fn func(*gorm.DB, *SubscriptionPreConsumeRecord) (int64, string, error)) (*SubscriptionBillingResult, error) {
	return mutateSubscriptionBillingContext(context.Background(), requestID, userID, fn)
}

func ReserveSubscriptionBilling(requestID string, userID int, target int64) (*SubscriptionBillingResult, error) {
	if target < 0 {
		return nil, errors.New("negative subscription reserve")
	}
	return mutateSubscriptionBilling(requestID, userID, func(tx *gorm.DB, r *SubscriptionPreConsumeRecord) (int64, string, error) {
		if r.Status != "consumed" {
			return 0, "", errors.New("subscription billing is no longer reservable")
		}
		fundingDelta := target - r.PreConsumed
		tokenDelta := int64(0)
		if r.TokenId != 0 && target > r.TokenConsumed {
			tokenDelta = target - r.TokenConsumed
		}
		if fundingDelta <= 0 && tokenDelta == 0 {
			return 0, "", nil
		}
		var active []UserSubscription
		if err := lockForUpdate(tx).Where("user_id = ? AND status = ? AND end_time > ?", userID, "active", getDBTimestamp(tx)).Order("end_time asc, id asc").Find(&active).Error; err != nil {
			return 0, "", err
		}
		var subscription UserSubscription
		if err := lockForUpdate(tx).First(&subscription, r.UserSubscriptionId).Error; err != nil {
			return 0, "", err
		}
		if subscription.QuotaVersion != r.ReservedVersion {
			return 0, "", errors.New("subscription period changed; reserve rejected")
		}
		if fundingDelta < 0 {
			fundingDelta = 0
		}
		if subscription.AmountTotal > 0 {
			remaining := subscription.AmountTotal - subscription.AmountUsed
			if remaining < 0 {
				remaining = 0
			}
			if fundingDelta > remaining {
				allowed := r.WalletOverflow && subscription.AllowWalletOverflow
				for _, policy := range active {
					allowed = allowed && policy.AllowWalletOverflow
				}
				if !allowed {
					return 0, "", ErrSubscriptionQuotaInsufficient
				}
				fundingDelta = remaining
			}
		}
		if fundingDelta > 0 {
			if err := postConsumeUserSubscriptionDeltaTx(tx, r.UserSubscriptionId, fundingDelta); err != nil {
				return 0, "", err
			}
		}
		walletReserve := target - (r.PreConsumed + fundingDelta)
		if walletReserve > 0 {
			if err := authorizeSubscriptionWalletQuota(tx, userID, requestID, walletReserve); err != nil {
				return 0, "", err
			}
		}
		var key string
		if tokenDelta > 0 {
			var err error
			key, err = subscriptionBillingTokenDelta(tx, userID, r.TokenId, tokenDelta, true)
			if err != nil {
				return 0, "", err
			}
		}
		r.PreConsumed += fundingDelta
		r.TokenConsumed += tokenDelta
		return 0, key, nil
	})
}

func SettleSubscriptionBilling(requestID string, userID int, actual int64) (*SubscriptionBillingResult, error) {
	return SettleSubscriptionBillingContext(context.Background(), requestID, userID, actual)
}

func SettleSubscriptionBillingContext(ctx context.Context, requestID string, userID int, actual int64) (*SubscriptionBillingResult, error) {
	if actual < 0 {
		return nil, errors.New("negative actual quota")
	}
	// Persist the cost fact before trying to debit. On failure this record
	// remains settling, blocks refunds, and retains the exact retry amount.
	intent, err := mutateSubscriptionBillingContext(ctx, requestID, userID, func(tx *gorm.DB, r *SubscriptionPreConsumeRecord) (int64, string, error) {
		if r.Status == "settled" || r.Status == "settling" {
			if r.ActualQuota != actual {
				return 0, "", errors.New("subscription settlement amount mismatch")
			}
			return 0, "", nil
		}
		if r.Status != "consumed" {
			return 0, "", errors.New("subscription billing already refunded")
		}
		r.ActualQuota, r.Status = actual, "settling"
		return 0, "", nil
	})
	if err != nil {
		return nil, err
	}
	result, err := mutateSubscriptionBillingContext(ctx, requestID, userID, func(tx *gorm.DB, r *SubscriptionPreConsumeRecord) (int64, string, error) {
		if r.Status == "settled" {
			return 0, "", nil
		}
		if r.Status != "settling" || r.ActualQuota != actual {
			return 0, "", errors.New("subscription settlement state mismatch")
		}
		// Match the ordering used by pre-consume; lock every active policy row
		// before locking the selected subscription (which may have expired).
		var active []UserSubscription
		if err := lockForUpdate(tx).Where("user_id = ? AND status = ? AND end_time > ?", userID, "active", getDBTimestamp(tx)).Order("end_time asc, id asc").Find(&active).Error; err != nil {
			return 0, "", err
		}
		var sub UserSubscription
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", r.UserSubscriptionId, userID).First(&sub).Error; err != nil {
			return 0, "", err
		}
		delta := actual - r.PreConsumed
		subDelta, wallet := delta, int64(0)
		if delta > 0 && sub.AmountTotal > 0 {
			remaining := sub.AmountTotal - sub.AmountUsed
			if remaining < 0 {
				remaining = 0
			}
			if delta > remaining {
				allowed := r.WalletOverflow && sub.AllowWalletOverflow
				for _, policy := range active {
					allowed = allowed && policy.AllowWalletOverflow
				}
				if !allowed {
					return 0, "", ErrSubscriptionQuotaInsufficient
				}
				subDelta, wallet = remaining, delta-remaining
			}
		}
		if subDelta > 0 && sub.AmountUsed > math.MaxInt64-subDelta {
			return 0, "", errors.New("subscription quota overflow")
		}
		// A refund of an expired grant must not erase another request's usage
		// in the current grant. Positive overage still consumes current capacity.
		if subDelta < 0 && sub.QuotaVersion != r.ReservedVersion {
			subDelta = 0
		}
		sub.AmountUsed += subDelta
		if sub.AmountUsed < 0 {
			sub.AmountUsed = 0
		} // preserve reset/refund semantics
		if err := tx.Save(&sub).Error; err != nil {
			return 0, "", err
		}
		if wallet > 0 {
			if err := ApplyWalletQuotaDelta(tx, userID, -int(wallet)); err != nil {
				return 0, "", err
			}
		}
		key, err := subscriptionBillingTokenDelta(tx, userID, r.TokenId, actual-r.TokenConsumed, false)
		if err != nil {
			return 0, "", fmt.Errorf("settle subscription token: %w", err)
		}
		r.Status, r.WalletConsumed = "settled", wallet
		if r.TokenId != 0 {
			r.TokenConsumed = actual
		}
		return -wallet, key, nil
	})
	if err != nil {
		return intent, err
	}
	return result, nil
}

func RefundSubscriptionBilling(requestID string, userID int) error {
	_, err := mutateSubscriptionBilling(requestID, userID, func(tx *gorm.DB, r *SubscriptionPreConsumeRecord) (int64, string, error) {
		if r.Status == "refunded" {
			return 0, "", nil
		}
		if r.Status != "consumed" {
			return 0, "", errors.New("cannot refund completed upstream usage; retry settlement")
		}
		var subscription UserSubscription
		if err := lockForUpdate(tx).First(&subscription, r.UserSubscriptionId).Error; err != nil {
			return 0, "", err
		}
		if subscription.QuotaVersion == r.ReservedVersion {
			if err := postConsumeUserSubscriptionDeltaTx(tx, r.UserSubscriptionId, -r.PreConsumed); err != nil {
				return 0, "", err
			}
		}
		key, err := subscriptionBillingTokenDelta(tx, userID, r.TokenId, -r.TokenConsumed, false)
		if err != nil {
			return 0, "", err
		}
		r.Status, r.TokenConsumed = "refunded", 0
		return 0, key, nil
	})
	return err
}
