package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"

	"gorm.io/gorm"
)

const (
	RedemptionRewardQuota        = "quota"
	RedemptionRewardResetVoucher = "reset_voucher"
)

type Redemption struct {
	Id                    int            `json:"id"`
	UserId                int            `json:"user_id"`
	Key                   string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status                int            `json:"status" gorm:"default:1"`
	Name                  string         `json:"name" gorm:"index"`
	Quota                 int            `json:"quota" gorm:"default:100"`
	RewardType            string         `json:"reward_type" gorm:"type:varchar(24);not null;default:'quota';index"`
	ResetPlanId           int            `json:"reset_plan_id" gorm:"not null;default:0;index"`
	ResetVoucherExpiresAt int64          `json:"reset_voucher_expires_at" gorm:"bigint;not null;default:0"`
	CreatedTime           int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime          int64          `json:"redeemed_time" gorm:"bigint"`
	Count                 int            `json:"count" gorm:"-:all"` // only for api request
	UsedUserId            int            `json:"used_user_id"`
	DeletedAt             gorm.DeletedAt `gorm:"index"`
	ExpiredTime           int64          `json:"expired_time" gorm:"bigint"` // 过期时间，0 表示不过期
}

type RedemptionRedeemResult struct {
	RewardType            string `json:"reward_type"`
	Quota                 int    `json:"quota,omitempty"`
	ResetVoucherId        int    `json:"reset_voucher_id,omitempty"`
	ResetPlanId           int    `json:"reset_plan_id,omitempty"`
	ResetVoucherExpiresAt int64  `json:"reset_voucher_expires_at,omitempty"`
}

func NormalizeRedemptionRewardType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", RedemptionRewardQuota:
		return RedemptionRewardQuota
	case RedemptionRewardResetVoucher:
		return RedemptionRewardResetVoucher
	default:
		return ""
	}
}

func ValidateRedemptionReward(redemption *Redemption) error {
	if redemption == nil {
		return errors.New("兑换码奖励为空")
	}
	rewardType := NormalizeRedemptionRewardType(redemption.RewardType)
	if rewardType == "" {
		return errors.New("不支持的兑换码奖励类型")
	}
	redemption.RewardType = rewardType
	if rewardType == RedemptionRewardQuota {
		if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
			return err
		}
		redemption.ResetPlanId = 0
		redemption.ResetVoucherExpiresAt = 0
		return nil
	}
	if redemption.ResetPlanId <= 0 {
		return errors.New("banked reset 券必须指定订阅计划")
	}
	if redemption.ResetVoucherExpiresAt < 0 {
		return errors.New("banked reset 券过期时间无效")
	}
	var plan SubscriptionPlan
	if err := DB.First(&plan, "id = ?", redemption.ResetPlanId).Error; err != nil {
		return errors.New("banked reset 券对应的订阅计划不存在")
	}
	redemption.Quota = 0
	return nil
}

func GetAllRedemptions(startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	err = tx.Model(&Redemption{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func SearchRedemptions(keyword string, status string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&Redemption{})

	if keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
		} else {
			query = query.Where("name LIKE ?", keyword+"%")
		}
	}

	if status != "" {
		now := common.GetTimestamp()
		switch status {
		case "expired":
			query = query.Where(
				"status = ? AND expired_time != 0 AND expired_time < ?",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusEnabled):
			query = query.Where(
				"status = ? AND (expired_time = 0 OR expired_time >= ?)",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusDisabled):
			query = query.Where("status = ?", common.RedemptionCodeStatusDisabled)
		case strconv.Itoa(common.RedemptionCodeStatusUsed):
			query = query.Where("status = ?", common.RedemptionCodeStatusUsed)
		}
	}

	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func GetRedemptionById(id int) (*Redemption, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	var err error = nil
	err = DB.First(&redemption, "id = ?", id).Error
	return &redemption, err
}

// Redeem preserves the legacy numeric return value for existing callers.
// Reset-voucher rewards return zero quota while RedeemWithResult exposes the
// complete reward details to new clients.
func Redeem(key string, userId int) (quota int, err error) {
	result, err := RedeemWithResult(key, userId)
	if err != nil {
		return 0, err
	}
	return result.Quota, nil
}

func RedeemWithResult(key string, userId int) (*RedemptionRedeemResult, error) {
	if key == "" {
		return nil, errors.New("未提供兑换码")
	}
	if userId == 0 {
		return nil, errors.New("无效的 user id")
	}
	redemption := &Redemption{}
	result := &RedemptionRedeemResult{}

	keyCol := "`key`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		keyCol = `"key"`
	}
	common.RandomSleep()
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where(keyCol+" = ?", key).First(redemption).Error; err != nil {
			return errors.New("无效的兑换码")
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return errors.New("该兑换码已被使用")
		}
		now := common.GetTimestamp()
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < now {
			return errors.New("该兑换码已过期")
		}
		rewardType := NormalizeRedemptionRewardType(redemption.RewardType)
		if rewardType == "" {
			return errors.New("兑换码奖励类型无效")
		}

		updated := tx.Model(&Redemption{}).
			Where("id = ? AND status = ?", redemption.Id, common.RedemptionCodeStatusEnabled).
			Updates(map[string]interface{}{
				"redeemed_time": now,
				"status":        common.RedemptionCodeStatusUsed,
				"used_user_id":  userId,
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return errors.New("该兑换码已被使用")
		}

		result.RewardType = rewardType
		switch rewardType {
		case RedemptionRewardQuota:
			if err := ApplyWalletQuotaDelta(tx, userId, redemption.Quota); err != nil {
				return err
			}
			result.Quota = redemption.Quota
		case RedemptionRewardResetVoucher:
			if redemption.ResetPlanId <= 0 {
				return errors.New("banked reset 券未绑定订阅计划")
			}
			var plan SubscriptionPlan
			if err := tx.First(&plan, "id = ?", redemption.ResetPlanId).Error; err != nil {
				return errors.New("banked reset 券对应的订阅计划不存在")
			}
			expiresAt := redemption.ResetVoucherExpiresAt
			if expiresAt == 0 {
				expiresAt = MaxSubscriptionResetVoucherExpiresAt
			}
			if expiresAt <= now {
				return errors.New("banked reset 券已过期")
			}
			voucher := SubscriptionResetVoucher{
				UserId:      userId,
				PlanId:      redemption.ResetPlanId,
				OperationId: fmt.Sprintf("redemption:%d", redemption.Id),
				Status:      SubscriptionResetVoucherAvailable,
				ExpiresAt:   expiresAt,
				CreatedBy:   redemption.UserId,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if err := tx.Create(&voucher).Error; err != nil {
				return err
			}
			result.ResetVoucherId = voucher.Id
			result.ResetPlanId = voucher.PlanId
			result.ResetVoucherExpiresAt = voucher.ExpiresAt
		default:
			return errors.New("兑换码奖励类型无效")
		}
		return nil
	})
	if err != nil {
		common.SysError("redemption failed: " + err.Error())
		return nil, ErrRedeemFailed
	}

	if result.RewardType == RedemptionRewardQuota {
		syncCreditUserQuotaCache(userId, redemption.Quota, "redemption")
		RecordLog(userId, LogTypeTopup, fmt.Sprintf("通过兑换码充值 %s，兑换码ID %d", logger.LogQuota(redemption.Quota), redemption.Id))
	} else {
		RecordLog(userId, LogTypeTopup, fmt.Sprintf("通过兑换码获得 banked reset 券，订阅计划ID %d，兑换码ID %d", redemption.ResetPlanId, redemption.Id))
	}
	return result, nil
}

func (redemption *Redemption) Insert() error {
	redemption.RewardType = NormalizeRedemptionRewardType(redemption.RewardType)
	if redemption.RewardType == "" {
		redemption.RewardType = RedemptionRewardQuota
	}
	return DB.Create(redemption).Error
}

func (redemption *Redemption) SelectUpdate() error {
	return DB.Model(redemption).Select("redeemed_time", "status").Updates(redemption).Error
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (redemption *Redemption) Update() error {
	return DB.Model(redemption).Select(
		"name", "status", "quota", "reward_type", "reset_plan_id", "reset_voucher_expires_at", "redeemed_time", "expired_time",
	).Updates(redemption).Error
}

func (redemption *Redemption) Delete() error {
	return DB.Delete(redemption).Error
}

func DeleteRedemptionById(id int) (err error) {
	if id == 0 {
		return errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	err = DB.Where(redemption).First(&redemption).Error
	if err != nil {
		return err
	}
	return redemption.Delete()
}

func DeleteInvalidRedemptions() (int64, error) {
	now := common.GetTimestamp()
	result := DB.Where("status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?)", []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, now).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}
