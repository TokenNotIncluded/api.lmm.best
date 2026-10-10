package model

import (
	"encoding/json"
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"gorm.io/gorm"
)

const (
	referralStageRegistration = "awaiting_first_top_up"
	referralStageCompleted    = "first_top_up_completed"
	referralStageIneligible   = "first_top_up_ineligible"
)

func referralPairEligible(invitee, inviter *User) bool {
	return invitee.Id != inviter.Id && invitee.Status == common.UserStatusEnabled &&
		inviter.Status == common.UserStatusEnabled && promotionRewardsAllowedForUser(invitee) &&
		promotionRewardsAllowedForUser(inviter) &&
		(invitee.Email == "" || NormalizeEmail(invitee.Email) != NormalizeEmail(inviter.Email)) &&
		(invitee.StripeCustomer == "" || invitee.StripeCustomer != inviter.StripeCustomer)
}

func referralAwardCapacity(inviter *User, wanted, alreadyGranted int, policy ReferralPolicy) int {
	quota := max(0, wanted)
	if policy.MaxRewardQuota > 0 {
		quota = min(quota, max(0, policy.MaxRewardQuota-alreadyGranted))
	}
	return max(0, min(quota, common.MaxWalletQuota-alreadyGranted,
		common.MaxWalletQuota-max(inviter.AffQuota, 0), common.MaxWalletQuota-max(inviter.AffHistoryQuota, 0)))
}

func recordReferralAwardTx(tx *gorm.DB, reward *ReferralReward, kind, reason string, quota int) error {
	if err := applyReferralDeltaTx(tx, reward, kind, reason, quota); err != nil {
		return err
	}
	return tx.Model(&User{}).Where("id = ?", reward.InviterId).
		UpdateColumn("aff_history", gorm.Expr("aff_history + ?", quota)).Error
}

// Called inside user creation, after the inviter is validated. The default
// advance is zero: upgrades never silently switch signup farming rewards on.
func grantRegistrationReferralTx(tx *gorm.DB, invitee *User) error {
	policy := GetReferralPolicy()
	if policy.RegistrationRewardQuota <= 0 || !operation_setting.IsPaymentComplianceConfirmed() {
		return nil
	}
	var inviter User
	if err := lockForUpdate(tx).First(&inviter, invitee.InviterId).Error; err != nil {
		return err
	}
	if !referralPairEligible(invitee, &inviter) {
		return nil
	}
	quota := referralAwardCapacity(&inviter, policy.RegistrationRewardQuota, 0, policy)
	if quota <= 0 {
		return nil
	}
	snapshot, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	reward := ReferralReward{InviteeId: invitee.Id, InviterId: inviter.Id,
		Quota: quota, RegistrationQuota: quota, Stage: referralStageRegistration,
		PolicySnapshot: string(snapshot), Status: "earned", PenaltyPercent: policy.PenaltyPercent,
		MaxPenaltyQuota: policy.MaxPenaltyQuota, CreatedAt: common.GetTimestamp()}
	if err := tx.Create(&reward).Error; err != nil {
		return err
	}
	return recordReferralAwardTx(tx, &reward, "registration_reward", "registration", quota)
}

// The first-payment marker is already persisted by the caller, even on a
// non-qualifying first payment. A later top-up must never retry qualification.
func completeFirstTopUpReferralStageTx(tx *gorm.DB, invitee *User, topUp *TopUp, prior int64) error {
	if invitee.InviterId <= 0 || invitee.InviterId == invitee.Id {
		return nil
	}
	var reward ReferralReward
	err := lockForUpdate(tx).Where("invitee_id = ?", invitee.Id).First(&reward).Error
	existing := err == nil
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if existing && reward.TopUpId != nil {
		return nil
	}

	policy := GetReferralPolicy()
	if existing && reward.PolicySnapshot != "" {
		if err := json.Unmarshal([]byte(reward.PolicySnapshot), &policy); err != nil {
			return err
		}
	}
	closeWithoutTail := func() error {
		if !existing {
			return nil
		}
		reward.TopUpId, reward.Stage, reward.UpdatedAt = &topUp.Id, referralStageIneligible, common.GetTimestamp()
		return tx.Save(&reward).Error
	}
	if prior > 0 || invitee.Status != common.UserStatusEnabled || !promotionRewardsAllowedForUser(invitee) ||
		!operation_setting.IsPaymentComplianceConfirmed() || policy.RewardQuota <= 0 ||
		topUp.CreditedQuota < int64(policy.MinTopUpQuota) || !referralCashMinimumMet(policy, topUp) ||
		(existing && reward.Status != "earned") {
		return closeWithoutTail()
	}
	var inviter User
	err = lockForUpdate(tx).First(&inviter, invitee.InviterId).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return closeWithoutTail()
	}
	if err != nil {
		return err
	}
	if !referralPairEligible(invitee, &inviter) {
		return closeWithoutTail()
	}
	quota := referralAwardCapacity(&inviter, policy.RewardQuota, reward.Quota, policy)
	if quota <= 0 {
		return closeWithoutTail()
	}
	if !existing {
		reward = ReferralReward{InviteeId: invitee.Id, InviterId: inviter.Id,
			Status: "earned", PenaltyPercent: policy.PenaltyPercent,
			MaxPenaltyQuota: policy.MaxPenaltyQuota, CreatedAt: common.GetTimestamp()}
	}
	reward.TopUpId, reward.Stage, reward.UpdatedAt = &topUp.Id, referralStageCompleted, common.GetTimestamp()
	reward.Quota += quota
	if existing {
		err = tx.Save(&reward).Error
	} else {
		err = tx.Create(&reward).Error
	}
	if err != nil {
		return err
	}
	return recordReferralAwardTx(tx, &reward, "reward", "first_top_up", quota)
}
