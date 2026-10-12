package model

import (
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

var ErrTaskQuotaSettlementConflict = errors.New("task quota settlement conflicts with persisted state")

// ApplyTaskQuotaSettlement commits the funding, token, usage counters and task
// marker together. expectedQuota prevents a stale result from replacing a newer
// settlement. A retry for an already committed target has no financial effect.
// This function does not choose a price, change rounding, or authorize a refund.
func ApplyTaskQuotaSettlement(id int64, expectedQuota, actualQuota int) (int, bool, error) {
	if DB == nil || id <= 0 || expectedQuota < 0 || expectedQuota > common.MaxQuota ||
		actualQuota <= 0 || actualQuota > common.MaxQuota {
		return 0, false, gorm.ErrInvalidData
	}
	var previousQuota, userID int
	var tokenKey string
	var applied bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		var task Task
		if err := lockForUpdate(tx).Where("id = ?", id).First(&task).Error; err != nil {
			return err
		}
		previousQuota = task.Quota
		if task.UserId <= 0 || task.Quota < 0 || task.Quota > common.MaxQuota ||
			task.RefundStatus != "" || task.Status == TaskStatusFailure {
			return ErrTaskQuotaSettlementConflict
		}
		if task.Quota == actualQuota {
			return nil
		}
		if task.Quota != expectedQuota {
			return ErrTaskQuotaSettlementConflict
		}
		delta := actualQuota - task.Quota
		userID = task.UserId
		// Keep this compare-and-swap in the same transaction as every balance.
		// It is also required on databases where SELECT FOR UPDATE is omitted.
		result := tx.Model(&Task{}).
			Where("id = ? AND quota = ? AND (refund_status = '' OR refund_status IS NULL)", id, expectedQuota).
			Updates(map[string]interface{}{"quota": actualQuota, "updated_at": common.GetTimestamp()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrTaskQuotaSettlementConflict
		}

		if task.PrivateData.BillingSource == "subscription" && task.PrivateData.SubscriptionId > 0 {
			if err := postConsumeUserSubscriptionDeltaTx(tx, task.PrivateData.SubscriptionId, int64(delta)); err != nil {
				return err
			}
		} else if err := ApplyWalletQuotaDelta(tx, userID, -delta); err != nil {
			return err
		}

		if task.PrivateData.TokenId > 0 {
			var token Token
			err := lockForUpdate(tx).Where("id = ?", task.PrivateData.TokenId).First(&token).Error
			if err == nil {
				if token.UserId != userID {
					return ErrTaskQuotaSettlementConflict
				}
				tokenKey = token.Key
				if err := tx.Model(&Token{}).Where("id = ? AND user_id = ?", token.Id, userID).
					Updates(map[string]interface{}{
						"remain_quota":  gorm.Expr("remain_quota - ?", delta),
						"used_quota":    gorm.Expr("used_quota + ?", delta),
						"accessed_time": common.GetTimestamp(),
					}).Error; err != nil {
					return err
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			// A deleted token must not prevent settlement of its completed task.
		}
		if err := tx.Model(&User{}).Where("id = ?", userID).
			UpdateColumn("used_quota", boundedQuotaCounterExpr("used_quota", delta)).Error; err != nil {
			return err
		}
		if task.ChannelId > 0 {
			if err := tx.Model(&Channel{}).Where("id = ?", task.ChannelId).
				UpdateColumn("used_quota", boundedQuotaCounterExpr("used_quota", delta)).Error; err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	if err != nil || !applied {
		return previousQuota, false, err
	}
	// Cache failures cannot turn a committed settlement into a second debit.
	if err := invalidateUserCache(userID); err != nil {
		common.SysLog("invalidate settled task user cache: " + err.Error())
	}
	if tokenKey != "" {
		if err := invalidateTokenCacheForMutation(tokenKey); err != nil {
			common.SysLog("invalidate settled task token cache: " + err.Error())
		}
	}
	return previousQuota, true, nil
}
