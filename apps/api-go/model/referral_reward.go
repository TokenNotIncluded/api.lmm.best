package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A reward is permanently tied to the first real-money top-up. Neither a
// refund nor an overturned ban deletes this fact or reopens first-top-up eligibility.
type ReferralReward struct {
	Id              int    `json:"id"`
	InviteeId       int    `json:"invitee_id" gorm:"uniqueIndex;not null"`
	InviterId       int    `json:"inviter_id" gorm:"index;not null"`
	TopUpId         int    `json:"top_up_id" gorm:"uniqueIndex;not null"`
	Quota           int    `json:"quota" gorm:"type:bigint;not null"`
	Status          string `json:"status" gorm:"type:varchar(24);not null"`
	RevokedQuota    int    `json:"revoked_quota" gorm:"type:bigint;not null;default:0"`
	PenaltyQuota    int    `json:"penalty_quota" gorm:"type:bigint;not null;default:0"`
	PenaltyPercent  int    `json:"penalty_percent" gorm:"not null;default:0"`
	MaxPenaltyQuota int    `json:"max_penalty_quota" gorm:"type:bigint;not null;default:0"`
	Revision        int    `json:"revision" gorm:"not null;default:0"`
	Reason          string `json:"reason" gorm:"type:varchar(32);not null;default:''"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

// These entries are append-only. Do not put moderation evidence, payment
// identifiers or another user's email in this user-visible ledger.
type ReferralLedgerEntry struct {
	Id        int    `json:"id"`
	RewardId  int    `json:"reward_id" gorm:"index;not null"`
	UserId    int    `json:"-" gorm:"index;not null"`
	EventKey  string `json:"-" gorm:"type:varchar(160);uniqueIndex;not null"`
	Kind      string `json:"kind" gorm:"type:varchar(32);not null"`
	Quota     int    `json:"quota" gorm:"type:bigint;not null"`
	Reason    string `json:"reason" gorm:"type:varchar(32);not null"`
	CreatedAt int64  `json:"created_at"`
}

type ReferralModerationEvent struct {
	// Bound retryable session cleanup to sessions that existed before this
	// decision. An old ban retry must not revoke sessions issued after appeal.
	RevokeThroughAuthVersion int64 `gorm:"not null;default:0"`
	Id                       int
	RequestId                string `gorm:"type:varchar(100);uniqueIndex;not null"`
	ActorId                  int
	UserId                   int    `gorm:"index;not null"`
	Action                   string `gorm:"type:varchar(32);not null"`
	Reason                   string `gorm:"type:varchar(32);not null"`
	Evidence                 string `gorm:"type:varchar(1000);not null"`
	Penalize                 bool
	CreatedAt                int64
}

var ErrReferralConflict = errors.New("referral operation conflicts with an earlier request")

// createUserWithInviterTx binds only a valid, pre-existing inviter at creation.
// Public profile updates must never be able to change inviter_id later.
func createUserWithInviterTx(tx *gorm.DB, user *User, inviterId int) error {
	user.InviterId = 0
	user.ReferralFirstTopUpId = 0
	if inviterId > 0 && inviterId != user.Id {
		var inviter User
		err := tx.Where("id = ? AND status = ?", inviterId, common.UserStatusEnabled).First(&inviter).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			user.InviterId = inviter.Id
		}
	}
	if err := tx.Create(user).Error; err != nil {
		return err
	}
	if user.InviterId > 0 && promotionRewardsAllowedForUser(user) {
		return tx.Model(&User{}).Where("id = ?", user.InviterId).
			UpdateColumn("aff_count", boundedInt32CounterExpr("aff_count", 1)).Error
	}
	return nil
}

// grantFirstTopUpReferralTx runs only from verified external settlement, in the
// transaction that credits the payer. The payer row is already write-locked.
func grantFirstTopUpReferralTx(tx *gorm.DB, topUp *TopUp) error {
	if topUp.ReferralExcluded || topUp.SettledAmountMicros <= 0 || topUp.CreditedQuota <= 0 ||
		(evidenceValue(topUp.ProviderEventId) == "" && evidenceValue(topUp.ProviderTransactionId) == "") ||
		!IsFinancialPaymentSource(topUp.PaymentMethod, topUp.PaymentProvider) {
		return nil
	}
	switch topUp.PaymentProvider {
	case PaymentProviderStripe, PaymentProviderCreem, PaymentProviderWaffo, PaymentProviderWaffoPancake:
	case PaymentProviderEpay:
		// The ePay adapter also handles non-cash community credits.
		if !strings.EqualFold(topUp.SettlementCurrency, "CNY") {
			return nil
		}
	default:
		return nil
	}
	var invitee User
	if err := lockForUpdate(tx).First(&invitee, topUp.UserId).Error; err != nil {
		return err
	}
	if invitee.ReferralFirstTopUpId != 0 {
		return nil
	}
	result := tx.Model(&User{}).Where("id = ? AND referral_first_top_up_id = 0", invitee.Id).
		UpdateColumn("referral_first_top_up_id", topUp.Id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return nil
	}
	// Set the durable marker even while rewards are disabled/below threshold.
	// Historical paid accounts must not obtain another first-top-up reward.
	expression, args := positiveNormalizedCreditedQuotaSQL()
	var prior int64
	if err := successfulExternalPaidTopUpQuery(tx.Model(&TopUp{})).
		Where("user_id = ? AND id <> ? AND referral_excluded = ?", invitee.Id, topUp.Id, false).
		Where("("+expression+") > 0", args...).Count(&prior).Error; err != nil {
		return err
	}
	policy := GetReferralPolicy()
	if prior > 0 || invitee.InviterId <= 0 || invitee.InviterId == invitee.Id ||
		invitee.Status != common.UserStatusEnabled || !promotionRewardsAllowedForUser(&invitee) ||
		!operation_setting.IsPaymentComplianceConfirmed() || policy.RewardQuota <= 0 ||
		topUp.CreditedQuota < int64(policy.MinTopUpQuota) {
		return nil
	}
	var inviter User
	err := lockForUpdate(tx).First(&inviter, invitee.InviterId).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if inviter.Status != common.UserStatusEnabled || !promotionRewardsAllowedForUser(&inviter) ||
		(invitee.Email != "" && NormalizeEmail(invitee.Email) == NormalizeEmail(inviter.Email)) ||
		(invitee.StripeCustomer != "" && invitee.StripeCustomer == inviter.StripeCustomer) {
		return nil
	}
	quota := policy.RewardQuota
	if policy.MaxRewardQuota > 0 {
		quota = min(quota, policy.MaxRewardQuota)
	}
	// Never silently saturate the ledger or block a paid order at a reward ceiling.
	quota = min(quota, common.MaxWalletQuota-max(inviter.AffQuota, 0), common.MaxWalletQuota-max(inviter.AffHistoryQuota, 0))
	if quota <= 0 {
		return nil
	}
	reward := ReferralReward{InviteeId: invitee.Id, InviterId: inviter.Id, TopUpId: topUp.Id,
		Quota: quota, Status: "earned", PenaltyPercent: policy.PenaltyPercent,
		MaxPenaltyQuota: policy.MaxPenaltyQuota, CreatedAt: common.GetTimestamp()}
	if err := tx.Create(&reward).Error; err != nil {
		return err
	}
	if err := applyReferralDeltaTx(tx, &reward, "reward", "first_top_up", quota); err != nil {
		return err
	}
	return tx.Model(&User{}).Where("id = ?", inviter.Id).
		UpdateColumn("aff_history", gorm.Expr("aff_history + ?", quota)).Error
}

// The signed affiliate balance is independent of the user's purchased wallet.
// A negative balance is durable reward debt; subsequent awards offset it before
// anything can be transferred. Never clamp a debit and silently lose the debt.
func applyReferralDeltaTx(tx *gorm.DB, reward *ReferralReward, kind, reason string, delta int) error {
	if delta == 0 {
		return nil
	}
	if err := common.ValidateWalletQuota(delta); err != nil {
		return err
	}
	low, high, err := walletQuotaCurrentBounds(delta)
	if err != nil {
		return err
	}
	result := tx.Unscoped().Model(&User{}).Where("id = ? AND aff_quota >= ? AND aff_quota <= ?", reward.InviterId, low, high).
		UpdateColumn("aff_quota", gorm.Expr("aff_quota + ?", delta))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWalletQuotaOutOfRange
	}
	return tx.Create(&ReferralLedgerEntry{RewardId: reward.Id, UserId: reward.InviterId,
		EventKey: fmt.Sprintf("referral:%d:%d:%s", reward.Id, reward.Revision, kind),
		Kind:     kind, Reason: reason, Quota: delta, CreatedAt: common.GetTimestamp()}).Error
}

func referralPenalty(quota, percent, capQuota int) int {
	// Divide before multiplying: quota is a JS-safe integer but quota*100 may not be.
	penalty := quota/100*percent + quota%100*percent/100
	if capQuota > 0 {
		penalty = min(penalty, capQuota)
	}
	return penalty
}

func revokeReferralTx(tx *gorm.DB, reward *ReferralReward, reason string, penalize bool) error {
	if reward.Status != "earned" {
		return nil
	}
	reward.Revision++
	reward.Status, reward.Reason = "revoked", reason
	reward.RevokedQuota = reward.Quota
	reward.PenaltyQuota = 0
	if penalize {
		reward.PenaltyQuota = referralPenalty(reward.Quota, reward.PenaltyPercent, reward.MaxPenaltyQuota)
	}
	if err := applyReferralDeltaTx(tx, reward, "clawback", reason, -reward.RevokedQuota); err != nil {
		return err
	}
	if err := applyReferralDeltaTx(tx, reward, "penalty", reason, -reward.PenaltyQuota); err != nil {
		return err
	}
	reward.UpdatedAt = common.GetTimestamp()
	return tx.Save(reward).Error
}

func restoreReferralTx(tx *gorm.DB, reward *ReferralReward) error {
	if reward.Status == "earned" {
		return nil
	}
	if reward.Reason == "refund" {
		// Refunds remain clawed back, but an overturned abuse penalty is refundable.
		if err := applyReferralDeltaTx(tx, reward, "restore_penalty", "mistaken_ban", reward.PenaltyQuota); err != nil {
			return err
		}
		reward.PenaltyQuota = 0
		reward.UpdatedAt = common.GetTimestamp()
		return tx.Save(reward).Error
	}
	if err := applyReferralDeltaTx(tx, reward, "restore_reward", "mistaken_ban", reward.RevokedQuota); err != nil {
		return err
	}
	if err := applyReferralDeltaTx(tx, reward, "restore_penalty", "mistaken_ban", reward.PenaltyQuota); err != nil {
		return err
	}
	reward.Status, reward.Reason = "earned", ""
	reward.RevokedQuota, reward.PenaltyQuota = 0, 0
	reward.UpdatedAt = common.GetTimestamp()
	return tx.Save(reward).Error
}

// Called in the provider refund transaction. Full cumulative refunds revoke
// the reward; a refund after an abuse ban does not charge it a second time.
func refundReferralTx(tx *gorm.DB, topUp *TopUp, cumulativeRefund int64) error {
	if cumulativeRefund < topUpPaidAmountMicros(topUp) {
		return nil
	}
	var invitee User
	if err := lockForUpdate(tx).First(&invitee, topUp.UserId).Error; err != nil {
		return err
	}
	// Existing installations have no referral entries for historical payments.
	if invitee.InviterId <= 0 || invitee.ReferralFirstTopUpId != topUp.Id {
		return nil
	}
	var reward ReferralReward
	err := lockForUpdate(tx).Where("top_up_id = ?", topUp.Id).First(&reward).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := revokeReferralTx(tx, &reward, "refund", false); err != nil {
		return err
	}
	// Preserve an existing penalty, but prevent a later unban from resurrecting
	// an award whose qualifying payment has been refunded.
	return tx.Model(&reward).Update("reason", "refund").Error
}

// ModerateReferralUser is a separate, explicit confirmed-abuse path. Ordinary
// disable/enable operations never touch rewards. Request IDs make retries safe
// even when the original ban has subsequently been overturned.
func ModerateReferralUser(event ReferralModerationEvent) error {
	return moderateReferralUserWithEffects(event, referralModerationEffects{
		publish: PublishUserAuthCache, invalidate: InvalidateUserTokensCache,
		revoke: revokeReferralSessionsThroughVersion,
	})
}

// Effects are injected only by same-package tests, never by an HTTP caller.
func moderateReferralUserWithEffects(event ReferralModerationEvent, effects referralModerationEffects) error {
	if effects.publish == nil || effects.invalidate == nil || effects.revoke == nil {
		return gorm.ErrInvalidData
	}
	event.RequestId, event.Evidence = strings.TrimSpace(event.RequestId), strings.TrimSpace(event.Evidence)
	if event.UserId <= 0 || event.ActorId <= 0 || event.RequestId == "" || len(event.RequestId) > 100 ||
		event.Evidence == "" || len(event.Evidence) > 1000 {
		return gorm.ErrInvalidData
	}
	if event.Action == "ban_abuse" {
		if event.Reason != "abuse" && event.Reason != "bulk_registration" {
			return gorm.ErrInvalidData
		}
	} else if event.Action != "restore_referral" || event.Reason != "mistaken_ban" || event.Penalize {
		return gorm.ErrInvalidData
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var actor, user User
		if err := tx.First(&actor, event.ActorId).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&user, event.UserId).Error; err != nil {
			return err
		}
		if actor.Status != common.UserStatusEnabled || actor.Role < common.RoleAdminUser || actor.Role <= user.Role {
			return errors.New("no permission to moderate this user")
		}
		event.RevokeThroughAuthVersion = 0
		if event.Action == "ban_abuse" {
			event.RevokeThroughAuthVersion = max(user.AuthVersion, 1)
		}
		event.CreatedAt = common.GetTimestamp()
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			var previous ReferralModerationEvent
			if err := tx.Where("request_id = ?", event.RequestId).First(&previous).Error; err != nil {
				return err
			}
			if previous.ActorId != event.ActorId || previous.UserId != event.UserId || previous.Action != event.Action ||
				previous.Reason != event.Reason || previous.Penalize != event.Penalize || previous.Evidence != event.Evidence {
				return ErrReferralConflict
			}
			// Keep the original cleanup boundary, not the user's version now.
			event = previous
			return nil
		}
		var reward ReferralReward
		err := lockForUpdate(tx).Where("invitee_id = ?", user.Id).First(&reward).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if event.Action == "ban_abuse" {
				err = revokeReferralTx(tx, &reward, event.Reason, event.Penalize)
			} else {
				err = restoreReferralTx(tx, &reward)
			}
			if err != nil {
				return err
			}
		}
		status := common.UserStatusDisabled
		if event.Action == "restore_referral" {
			status = common.UserStatusEnabled
		}
		if user.Status == status {
			return nil
		}
		if _, err := IncrementUserAuthVersionWithTx(tx, user.Id); err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("status", status).Error
	})
	if err != nil {
		return err
	}
	// A database commit does not prove cache publication/session cleanup
	// completed. Exact retries repeat only these idempotent effects, publishing
	// CURRENT user state so an old ban cannot overwrite a later appeal.
	if err := effects.publish(event.UserId); err != nil {
		return err
	}
	if err := effects.invalidate(event.UserId); err != nil {
		return err
	}
	if event.Action == "ban_abuse" {
		_, err = effects.revoke(event.UserId, event.RevokeThroughAuthVersion)
	}
	return err
}
