package model

import (
	"errors"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const (
	ViolationFeeRecordStatusCharged  = "charged"
	ViolationFeeRecordStatusReversed = "reversed"

	ViolationFeeAppealStatusPending  = "pending"
	ViolationFeeAppealStatusApproved = "approved"
	ViolationFeeAppealStatusRejected = "rejected"
)

var (
	ErrViolationFeeRecordNotFound = errors.New("违规扣费记录不存在")
	ErrViolationFeeRecordReviewed = errors.New("违规扣费记录已经处理")
	ErrViolationFeeAppealPending  = errors.New("该违规扣费已有待处理申诉")
	ErrViolationFeeAppealState    = errors.New("该违规扣费记录当前不可申诉")
)

// ViolationFeeState holds only the counter for the selected group policy.
// It is reset lazily on the first violation after the configured period.
type ViolationFeeState struct {
	ID              uint   `json:"id" gorm:"primaryKey"`
	UserID          int    `json:"user_id" gorm:"not null;uniqueIndex:idx_violation_fee_state_user_policy,priority:1;index"`
	PolicyKey       string `json:"policy_key" gorm:"type:varchar(128);not null;uniqueIndex:idx_violation_fee_state_user_policy,priority:2"`
	PeriodStartedAt int64  `json:"period_started_at" gorm:"not null"`
	ViolationCount  int    `json:"violation_count" gorm:"not null;default:0"`
	UpdatedAt       int64  `json:"updated_at" gorm:"not null;index"`
}

func (ViolationFeeState) TableName() string { return "violation_fee_states" }

// ViolationFeeRecord is the immutable charging audit row. The policy is
// matched by group, while model/provider details are deliberately absent.
// Empty AmountCurrency marks historical legacy amounts; new moderation
// receipts explicitly use USD without rewriting existing numeric fields.
type ViolationFeeRecord struct {
	ID                 uint    `json:"id" gorm:"primaryKey"`
	UserID             int     `json:"user_id" gorm:"not null;index;uniqueIndex:idx_violation_fee_request,priority:1"`
	RequestID          string  `json:"request_id" gorm:"type:varchar(128);not null;uniqueIndex:idx_violation_fee_request,priority:2"`
	PolicyKey          string  `json:"policy_key" gorm:"type:varchar(128);not null;index"`
	Group              string  `json:"group" gorm:"type:varchar(64);not null;index"`
	Occurrence         int     `json:"occurrence" gorm:"not null"`
	PeriodStartedAt    int64   `json:"period_started_at" gorm:"not null"`
	PeriodEndsAt       int64   `json:"period_ends_at" gorm:"not null"`
	AmountCurrency     string  `json:"amount_currency" gorm:"type:varchar(24);not null;default:''"`
	RequestedAmountUSD float64 `json:"requested_amount_usd" gorm:"not null"`
	ChargedAmountUSD   float64 `json:"charged_amount_usd" gorm:"not null"`
	RequestedQuota     int     `json:"requested_quota" gorm:"not null"`
	ChargedQuota       int     `json:"charged_quota" gorm:"not null"`
	ErrorCode          string  `json:"error_code" gorm:"type:varchar(128);not null"`
	Status             string  `json:"status" gorm:"type:varchar(20);not null;index"`
	CreatedAt          int64   `json:"created_at" gorm:"not null;index"`
	ReversedAt         int64   `json:"reversed_at" gorm:"not null;default:0"`
	ReversedBy         int     `json:"reversed_by" gorm:"index"`
}

func (ViolationFeeRecord) TableName() string { return "violation_fee_records" }

type ViolationFeeAppeal struct {
	ID          uint   `json:"id" gorm:"primaryKey"`
	RecordID    uint   `json:"record_id" gorm:"not null;index"`
	UserID      int    `json:"user_id" gorm:"not null;index"`
	Reason      string `json:"reason" gorm:"type:text;not null"`
	Status      string `json:"status" gorm:"type:varchar(20);not null;index"`
	AdminUserID int    `json:"admin_user_id" gorm:"index"`
	AdminNote   string `json:"admin_note" gorm:"type:text"`
	CreatedAt   int64  `json:"created_at" gorm:"not null;index"`
	ReviewedAt  int64  `json:"reviewed_at" gorm:"not null;default:0"`
}

func (ViolationFeeAppeal) TableName() string { return "violation_fee_appeals" }

func SubmitViolationFeeAppeal(userID int, recordID uint, reason string) (*ViolationFeeAppeal, error) {
	reason = strings.TrimSpace(reason)
	if userID <= 0 || recordID == 0 || len([]rune(reason)) < 5 || len([]rune(reason)) > 2000 {
		return nil, errors.New("申诉说明需要 5 至 2000 个字符")
	}
	var appeal ViolationFeeAppeal
	err := DB.Transaction(func(tx *gorm.DB) error {
		var record ViolationFeeRecord
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", recordID, userID).First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrViolationFeeRecordNotFound
			}
			return err
		}
		if record.Status != ViolationFeeRecordStatusCharged || record.ChargedQuota <= 0 {
			return ErrViolationFeeAppealState
		}
		var pending ViolationFeeAppeal
		if err := lockForUpdate(tx).Where("record_id = ? AND user_id = ? AND status = ?", recordID, userID, ViolationFeeAppealStatusPending).First(&pending).Error; err == nil {
			return ErrViolationFeeAppealPending
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		appeal = ViolationFeeAppeal{RecordID: recordID, UserID: userID, Reason: reason, Status: ViolationFeeAppealStatusPending, CreatedAt: common.GetTimestamp()}
		return tx.Create(&appeal).Error
	})
	if err != nil {
		return nil, err
	}
	return &appeal, nil
}

func ListViolationFeeAppeals(status string, limit int) ([]ViolationFeeAppeal, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := DB.Order("id desc").Limit(limit)
	if status = strings.TrimSpace(status); status != "" {
		if status != ViolationFeeAppealStatusPending && status != ViolationFeeAppealStatusApproved && status != ViolationFeeAppealStatusRejected {
			return nil, errors.New("申诉状态无效")
		}
		query = query.Where("status = ?", status)
	}
	var appeals []ViolationFeeAppeal
	return appeals, query.Find(&appeals).Error
}

func ReviewViolationFeeAppeal(adminUserID int, appealID uint, approve bool, note string) (*ViolationFeeAppeal, error) {
	if adminUserID <= 0 || appealID == 0 {
		return nil, errors.New("无效的申诉审核请求")
	}
	if !approve && len([]rune(strings.TrimSpace(note))) < 2 {
		return nil, errors.New("拒绝申诉时必须填写至少 2 个字符的管理员意见")
	}
	var appeal ViolationFeeAppeal
	var reversedQuota int
	var reversedUserID int
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ?", appealID).First(&appeal).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrViolationFeeRecordNotFound
			}
			return err
		}
		if appeal.Status != ViolationFeeAppealStatusPending {
			return ErrViolationFeeRecordReviewed
		}
		var record ViolationFeeRecord
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", appeal.RecordID, appeal.UserID).First(&record).Error; err != nil {
			return err
		}
		now := common.GetTimestamp()
		status := ViolationFeeAppealStatusRejected
		if approve {
			status = ViolationFeeAppealStatusApproved
			if record.Status == ViolationFeeRecordStatusCharged && record.ChargedQuota > 0 {
				refundQuota, err := WalletFutureCreditQuota(tx, record.UserID, "violation_fee_refund", strconv.FormatUint(uint64(record.ID), 10), record.ChargedQuota, record.CreatedAt)
				if err != nil {
					return err
				}
				if err := ApplyWalletQuotaDelta(tx, record.UserID, refundQuota); err != nil {
					return err
				}
				if err := tx.Model(&ViolationFeeRecord{}).Where("id = ? AND status = ?", record.ID, ViolationFeeRecordStatusCharged).Updates(map[string]interface{}{
					"status": ViolationFeeRecordStatusReversed, "reversed_at": now, "reversed_by": adminUserID,
				}).Error; err != nil {
					return err
				}
				// Background moderation penalties do not count as normal model
				// consumption. Their reversal must leave real usage untouched.
				if !strings.HasPrefix(record.ErrorCode, "moderation.") {
					if err := tx.Model(&User{}).Where("id = ?", record.UserID).Update("used_quota", gorm.Expr("CASE WHEN used_quota >= ? THEN used_quota - ? ELSE 0 END", record.ChargedQuota, record.ChargedQuota)).Error; err != nil {
						return err
					}
				}
				reversedQuota = refundQuota
				reversedUserID = record.UserID
			}
		}
		return tx.Model(&ViolationFeeAppeal{}).Where("id = ?", appeal.ID).Updates(map[string]interface{}{
			"status": status, "admin_user_id": adminUserID, "admin_note": strings.TrimSpace(note), "reviewed_at": now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	if reversedQuota > 0 && common.RedisEnabled {
		go func(userID, quota int) {
			if err := cacheIncrUserQuota(userID, int64(quota)); err != nil {
				common.SysLog("failed to restore violation fee quota cache: " + err.Error())
			}
		}(reversedUserID, reversedQuota)
	}
	return &appeal, nil
}

func ListUserViolationFeeRecords(userID int, limit int) ([]ViolationFeeRecord, error) {
	if userID <= 0 {
		return nil, errors.New("用户身份无效")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var records []ViolationFeeRecord
	err := DB.Where("user_id = ?", userID).Order("id desc").Limit(limit).Find(&records).Error
	return records, err
}
