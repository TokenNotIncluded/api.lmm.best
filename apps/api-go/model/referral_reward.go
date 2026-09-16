// Copyright (C) 2026 LIghtJUNction
// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A zero award still consumes the first-payment opportunity. Refunds and
// moderation never delete this row or make an account eligible again.
type ReferralReward struct {
	InviteeID           int    `json:"invitee_id" gorm:"primaryKey;autoIncrement:false"`
	InviterID           int    `json:"inviter_id" gorm:"not null;index"`
	TopUpID             int    `json:"topup_id" gorm:"not null;uniqueIndex"`
	AwardQuota          int64  `json:"award_quota" gorm:"not null;default:0"`
	RefundRevokedQuota  int64  `json:"refund_revoked_quota" gorm:"not null;default:0"`
	AbuseRevokedQuota   int64  `json:"abuse_revoked_quota" gorm:"not null;default:0"`
	PenaltyQuota        int64  `json:"penalty_quota" gorm:"not null;default:0"`
	MaximumPenaltyQuota int64  `json:"maximum_penalty_quota" gorm:"not null;default:0"`
	AbuseActive         bool   `json:"abuse_active" gorm:"not null;default:false"`
	Reason              string `json:"reason" gorm:"type:varchar(64);not null"`
	Revision            int64  `json:"revision" gorm:"not null;default:1"`
	CreatedAt           int64  `json:"created_at" gorm:"autoCreateTime"`
}

// Entries are append-only. Quota is signed; BalanceDelta and DebtDelta explain
// exactly how a used/transferred reward was recovered without touching cash.
type ReferralRewardEntry struct {
	ID           int64  `json:"id" gorm:"primaryKey"`
	OperationKey string `json:"-" gorm:"type:varchar(128);not null;uniqueIndex"`
	InviteeID    int    `json:"invitee_id" gorm:"not null;index"`
	InviterID    int    `json:"-" gorm:"not null;index"`
	Kind         string `json:"kind" gorm:"type:varchar(32);not null"`
	Quota        int64  `json:"quota" gorm:"not null"`
	BalanceDelta int64  `json:"balance_delta" gorm:"not null"`
	DebtDelta    int64  `json:"debt_delta" gorm:"not null"`
	Reason       string `json:"reason" gorm:"type:varchar(64);not null"`
	ActorID      int    `json:"-" gorm:"not null;default:0"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime"`
}

type ReferralModerationOperation struct {
	Key              string `gorm:"primaryKey;type:varchar(64)"`
	Fingerprint      string `gorm:"type:char(64);not null"`
	UserID           int    `gorm:"not null;index"`
	ActorID          int    `gorm:"not null"`
	Note             string `gorm:"type:varchar(500);not null"`
	Action           string `gorm:"type:varchar(32);not null"`
	Reason           string `gorm:"type:varchar(64);not null"`
	ConfirmPenalty   bool   `gorm:"not null;default:false"`
	ExpectedRevision int64  `gorm:"not null;default:0"`
	CreatedAt        int64  `gorm:"autoCreateTime"`
}

var ErrReferralConflict = errors.New("referral facts changed; refresh the preview and retry")
var referralOperationKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

func referralPaidTopUps(tx *gorm.DB) *gorm.DB {
	expression, args := positiveNormalizedCreditedQuotaSQL()
	return successfulExternalPaidTopUpQuery(tx.Model(&TopUp{})).Where("("+expression+") > 0", args...).
		Where("LOWER(COALESCE(payment_method, '')) NOT IN ?", []string{"gift", "bonus", "checkin", "invite", "bounty", "internal", "admin"})
}

// Called only on the pending -> paid transition, in the wallet transaction.
// creditTopUpQuota already owns the invitee's write lock. The unique invitee
// key remains authoritative across processes and concurrent payment orders.
func settleReferralRewardTx(tx *gorm.DB, topUp *TopUp) error {
	policy := operation_setting.GetReferralRewardPolicy()
	if !policy.Enabled || !operation_setting.IsPaymentComplianceConfirmed() {
		return nil
	}
	var eligible int64
	if err := referralPaidTopUps(tx).Where("id = ?", topUp.Id).Count(&eligible).Error; err != nil {
		return err
	}
	if eligible != 1 {
		return nil
	}
	var invitee User
	if err := lockForUpdate(tx).First(&invitee, topUp.UserId).Error; err != nil {
		return err
	}
	if invitee.InviterId <= 0 || invitee.InviterId == invitee.Id {
		return nil
	}
	var previous int64
	if err := referralPaidTopUps(tx).Where("user_id = ? AND id <> ?", invitee.Id, topUp.Id).Count(&previous).Error; err != nil {
		return err
	}
	reward := ReferralReward{InviteeID: invitee.Id, InviterID: invitee.InviterId, TopUpID: topUp.Id, Revision: 1, Reason: "first_topup"}
	if previous > 0 {
		reward.Reason = "previous_payment"
	} else if invitee.Status != common.UserStatusEnabled || !promotionRewardsAllowedForUser(&invitee) {
		reward.Reason = "invitee_ineligible"
	} else {
		var inviter User
		err := lockForUpdate(tx).First(&inviter, invitee.InviterId).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err != nil || inviter.Status != common.UserStatusEnabled || !promotionRewardsAllowedForUser(&inviter) {
			reward.Reason = "inviter_ineligible"
		} else {
			reward.AwardQuota = policy.Reward(topUp.CreditedQuota)
			reward.MaximumPenaltyQuota = policy.Penalty(reward.AwardQuota)
			if reward.AwardQuota == 0 {
				reward.Reason = "below_minimum"
			}
		}
	}
	insert := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "invitee_id"}}, DoNothing: true}).Create(&reward)
	if insert.Error != nil {
		return insert.Error
	}
	if insert.RowsAffected == 0 || reward.AwardQuota == 0 {
		return nil
	}
	return applyReferralEntryTx(tx, &reward, "award", reward.AwardQuota, "first_topup", 0, fmt.Sprintf("award:%d", invitee.Id))
}

func applyReferralEntryTx(tx *gorm.DB, reward *ReferralReward, kind string, delta int64, reason string, actor int, key string) error {
	var user User
	if err := lockForUpdate(tx.Unscoped()).First(&user, reward.InviterID).Error; err != nil {
		return err
	}
	balance, debt, history := int64(user.AffQuota), user.AffDebtQuota, int64(user.AffHistoryQuota)
	if balance < 0 || debt < 0 {
		return ErrWalletQuotaOutOfRange
	}
	newBalance, newDebt := balance, debt
	if delta >= 0 {
		offset := min(debt, delta)
		newDebt -= offset
		newBalance += delta - offset
	} else {
		recovered := min(balance, -delta)
		newBalance -= recovered
		newDebt += -delta - recovered
	}
	if kind == "award" {
		history += delta
	}
	for _, value := range []int64{newBalance, newDebt, history} {
		if value < 0 || value > int64(common.MaxWalletQuota) {
			return ErrWalletQuotaOutOfRange
		}
	}
	result := tx.Unscoped().Model(&User{}).Where("id = ? AND aff_quota = ? AND aff_debt_quota = ? AND aff_history = ?", user.Id, balance, debt, user.AffHistoryQuota).
		Updates(map[string]interface{}{"aff_quota": newBalance, "aff_debt_quota": newDebt, "aff_history": history})
	if result.Error != nil {
		return result.Error
	}
	// MySQL can report zero changed rows for a zero-valued audit entry.
	if result.RowsAffected != 1 && (newBalance != balance || newDebt != debt || history != int64(user.AffHistoryQuota)) {
		return ErrReferralConflict
	}
	return tx.Create(&ReferralRewardEntry{OperationKey: key, InviteeID: reward.InviteeID, InviterID: reward.InviterID,
		Kind: kind, Quota: delta, BalanceDelta: newBalance - balance, DebtDelta: newDebt - debt, Reason: reason, ActorID: actor}).Error
}

// Refunds use a cumulative proportional target, so rounding across partial
// events equals one full refund. A prior abuse clawback is reclassified rather
// than charged twice; overturning a ban cannot restore refunded rewards.
func refundReferralRewardTx(tx *gorm.DB, topUp *TopUp, refundedMicros int64, actor int) error {
	var reward ReferralReward
	err := lockForUpdate(tx).Where("top_up_id = ?", topUp.Id).First(&reward).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	target := proportionalRefundTarget(reward.AwardQuota, topUpPaidAmountMicros(topUp), refundedMicros)
	delta := target - reward.RefundRevokedQuota
	if delta <= 0 {
		return nil
	}
	shifted := min(reward.AbuseRevokedQuota, delta)
	reward.AbuseRevokedQuota -= shifted
	reward.RefundRevokedQuota = target
	reward.Revision++
	if err := applyReferralEntryTx(tx, &reward, "refund_clawback", -(delta - shifted), "refund", actor,
		fmt.Sprintf("refund:%d:%d", topUp.Id, refundedMicros)); err != nil {
		return err
	}
	return tx.Save(&reward).Error
}

type ReferralModerationRequest struct {
	UserID           int    `json:"user_id"`
	ActorID          int    `json:"actor_id"`
	ActorRole        int    `json:"-"`
	Action           string `json:"action"`
	Reason           string `json:"reason"`
	Note             string `json:"note"`
	ConfirmPenalty   bool   `json:"confirm_penalty"`
	OperationKey     string `json:"operation_key"`
	ExpectedRevision int64  `json:"expected_revision"`
}

// Moderation owns invitee -> reward -> inviter locks, the same order as
// settlement/refunds. Authorization is rechecked on the locked current row.
func ModerateReferralUser(request ReferralModerationRequest) error {
	request.Note = strings.TrimSpace(request.Note)
	if DB == nil || request.UserID <= 0 || request.ActorID <= 0 || request.ActorRole < common.RoleAdminUser ||
		!referralOperationKeyPattern.MatchString(request.OperationKey) || request.Note == "" || len([]rune(request.Note)) > 500 || request.ExpectedRevision < 0 {
		return gorm.ErrInvalidData
	}
	if request.Action != "disable_abuse" && request.Action != "restore_referral" {
		return gorm.ErrInvalidData
	}
	if request.Action == "disable_abuse" && request.Reason != "abuse" && request.Reason != "bulk_registration" {
		return gorm.ErrInvalidData
	}
	if request.Action == "restore_referral" && (request.Reason != "mistaken_ban" || request.ConfirmPenalty) {
		return gorm.ErrInvalidData
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(digest[:])
	for attempt := 0; attempt < 5; attempt++ {
		err = DB.Transaction(func(tx *gorm.DB) error {
			var user User
			if err := lockForUpdate(tx).First(&user, request.UserID).Error; err != nil {
				return err
			}
			if user.Role == common.RoleRootUser || user.Role >= request.ActorRole {
				return errors.New("insufficient permission for this user")
			}
			var operation ReferralModerationOperation
			found := tx.Where(map[string]interface{}{"key": request.OperationKey}).First(&operation).Error
			if found == nil {
				if operation.Fingerprint != fingerprint {
					return ErrReferralConflict
				}
				return nil
			}
			if !errors.Is(found, gorm.ErrRecordNotFound) {
				return found
			}
			var reward ReferralReward
			found = lockForUpdate(tx).First(&reward, "invitee_id = ?", user.Id).Error
			if found != nil && !errors.Is(found, gorm.ErrRecordNotFound) {
				return found
			}
			if reward.Revision != request.ExpectedRevision {
				return ErrReferralConflict
			}
			if request.Action == "disable_abuse" {
				user.Status = common.UserStatusDisabled
				if reward.AwardQuota > 0 {
					if !reward.AbuseActive {
						reward.AbuseRevokedQuota = reward.AwardQuota - reward.RefundRevokedQuota
						if err := applyReferralEntryTx(tx, &reward, "clawback", -reward.AbuseRevokedQuota, request.Reason, request.ActorID, request.OperationKey+":clawback"); err != nil {
							return err
						}
					}
					if request.ConfirmPenalty && reward.PenaltyQuota == 0 && reward.MaximumPenaltyQuota > 0 {
						reward.PenaltyQuota = reward.MaximumPenaltyQuota
						if err := applyReferralEntryTx(tx, &reward, "penalty", -reward.PenaltyQuota, request.Reason, request.ActorID, request.OperationKey+":penalty"); err != nil {
							return err
						}
					}
					reward.AbuseActive = true
				}
			} else {
				user.Status = common.UserStatusEnabled
				if reward.AbuseActive {
					if err := applyReferralEntryTx(tx, &reward, "clawback_reversal", reward.AbuseRevokedQuota, request.Reason, request.ActorID, request.OperationKey+":clawback_reversal"); err != nil {
						return err
					}
					if reward.PenaltyQuota > 0 {
						if err := applyReferralEntryTx(tx, &reward, "penalty_reversal", reward.PenaltyQuota, request.Reason, request.ActorID, request.OperationKey+":penalty_reversal"); err != nil {
							return err
						}
					}
					reward.AbuseRevokedQuota, reward.PenaltyQuota, reward.AbuseActive = 0, 0, false
				}
			}
			if found == nil {
				reward.Revision++
				if err := tx.Save(&reward).Error; err != nil {
					return err
				}
			}
			if err := user.UpdateWithTx(tx, false); err != nil {
				return err
			}
			return tx.Create(&ReferralModerationOperation{Key: request.OperationKey, Fingerprint: fingerprint, UserID: user.Id, ActorID: request.ActorID, Note: request.Note, Action: request.Action, Reason: request.Reason, ConfirmPenalty: request.ConfirmPenalty, ExpectedRevision: request.ExpectedRevision}).Error
		})
		if err == nil || !referralRetryable(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return err
}

func referralRetryable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "locked") || strings.Contains(message, "deadlock") || strings.Contains(message, "serialization") || strings.Contains(message, "sqlstate 40001")
}

func GetReferralRewardPreview(userID int) (ReferralReward, error) {
	var reward ReferralReward
	err := DB.First(&reward, "invitee_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ReferralReward{}, nil
	}
	return reward, err
}

func GetReferralRewardEntries(userID int, before int64) ([]ReferralRewardEntry, int64, error) {
	entries := make([]ReferralRewardEntry, 0, 51)
	query := DB.Where("inviter_id = ?", userID)
	if before > 0 {
		query = query.Where("id < ?", before)
	}
	if err := query.Order("id DESC").Limit(51).Find(&entries).Error; err != nil {
		return nil, 0, err
	}
	var next int64
	if len(entries) > 50 {
		entries = entries[:50]
		next = entries[49].ID
	}
	return entries, next, nil
}

func bindRegistrationInviterTx(tx *gorm.DB, user *User, inviterID int) error {
	user.InviterId = 0
	if inviterID <= 0 {
		return nil
	}
	if user.Id > 0 && user.Id == inviterID {
		return gorm.ErrInvalidData
	}
	var inviter User
	if err := tx.First(&inviter, inviterID).Error; err != nil {
		return err
	}
	if inviter.Status == common.UserStatusEnabled {
		user.InviterId = inviterID
	}
	return nil
}
