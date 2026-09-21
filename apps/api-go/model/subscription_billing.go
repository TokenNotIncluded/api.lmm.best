package model

import (
	"errors"
	"fmt"
	"math"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

var ErrSubscriptionBillingTokenQuota = errors.New("token quota insufficient")

// SubscriptionBillingResult separates measured cost from committed debits.
// A settling record can be retried with the same request ID and actual quota,
// without resending the upstream request. PreConsumed includes every Reserve.
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
		result, err = preConsumeUserSubscription(tx, requestID, userID, modelName, 0, amount)
		if err != nil {
			return err
		}
		if q.RowsAffected > 0 {
			return nil
		}
		var subscription UserSubscription
		if err := tx.First(&subscription, result.UserSubscriptionId).Error; err != nil {
			return err
		}
		tokenKey, err = subscriptionBillingTokenDelta(tx, userID, tokenID, amount, true)
		if err != nil {
			return err
		}
		tokenConsumed := amount
		if tokenID == 0 {
			tokenConsumed = 0
		}
		return tx.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", requestID).Updates(map[string]interface{}{
			"billing_managed": true, "token_id": tokenID, "token_consumed": tokenConsumed, "wallet_overflow": walletOverflow, "reserved_version": subscription.QuotaVersion,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	syncSubscriptionBillingCache(userID, 0, tokenKey)
	return result, nil
}

func mutateSubscriptionBilling(requestID string, userID int, fn func(*gorm.DB, *SubscriptionPreConsumeRecord) (int64, string, error)) (*SubscriptionBillingResult, error) {
	var record SubscriptionPreConsumeRecord
	var walletDelta int64
	var tokenKey string
	err := DB.Transaction(func(tx *gorm.DB) error {
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

func ReserveSubscriptionBilling(requestID string, userID int, target int64) (*SubscriptionBillingResult, error) {
	if target < 0 {
		return nil, errors.New("negative subscription reserve")
	}
	return mutateSubscriptionBilling(requestID, userID, func(tx *gorm.DB, r *SubscriptionPreConsumeRecord) (int64, string, error) {
		if r.Status != "consumed" {
			return 0, "", errors.New("subscription billing is no longer reservable")
		}
		if target <= r.PreConsumed {
			return 0, "", nil
		}
		var subscription UserSubscription
		if err := lockForUpdate(tx).First(&subscription, r.UserSubscriptionId).Error; err != nil {
			return 0, "", err
		}
		if subscription.QuotaVersion != r.ReservedVersion {
			return 0, "", errors.New("subscription period changed; reserve rejected")
		}
		delta := target - r.PreConsumed
		if err := postConsumeUserSubscriptionDeltaTx(tx, r.UserSubscriptionId, delta); err != nil {
			return 0, "", err
		}
		key, err := subscriptionBillingTokenDelta(tx, userID, r.TokenId, delta, true)
		if err != nil {
			return 0, "", err
		}
		r.PreConsumed = target
		if r.TokenId != 0 {
			r.TokenConsumed += delta
		}
		return 0, key, nil
	})
}

func SettleSubscriptionBilling(requestID string, userID int, actual int64) (*SubscriptionBillingResult, error) {
	if actual < 0 {
		return nil, errors.New("negative actual quota")
	}
	// Persist the cost fact before trying to debit. On failure this record
	// remains settling, blocks refunds, and retains the exact retry amount.
	intent, err := mutateSubscriptionBilling(requestID, userID, func(tx *gorm.DB, r *SubscriptionPreConsumeRecord) (int64, string, error) {
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
	result, err := mutateSubscriptionBilling(requestID, userID, func(tx *gorm.DB, r *SubscriptionPreConsumeRecord) (int64, string, error) {
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
			if err := common.ValidateWalletQuota(int(wallet)); err != nil {
				return 0, "", err
			}
			// As with wallet settlement, completed usage can create wallet debt.
			if err := ApplyWalletQuotaDelta(tx, userID, -int(wallet)); err != nil {
				return 0, "", err
			}
		}
		key, err := subscriptionBillingTokenDelta(tx, userID, r.TokenId, delta, false)
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
