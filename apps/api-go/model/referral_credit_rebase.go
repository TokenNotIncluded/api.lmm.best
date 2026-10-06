package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"gorm.io/gorm"
)

// This explicit audit is created only by the reviewed balance migration.
// ReferralReward counters retain their original units; these counters describe
// actual affiliate credit debits/restorations after migration.
type WalletReferralCreditRebase struct {
	RewardID            int `gorm:"primaryKey;autoIncrement:false"`
	UserID              int
	MigrationID         string
	OriginalQuota       int64
	Divisor             string
	Rounding            string
	RebasedQuota        int64
	RebasedRevokedQuota int64
	RebasedPenaltyQuota int64
}

func (WalletReferralCreditRebase) TableName() string { return "wallet_referral_credit_rebases" }

func rebasedReferralDeltaTx(tx *gorm.DB, reward *ReferralReward, kind string, originalDelta int) (int, error) {
	exists, err := walletCreditAuditTableExists(tx, "wallet_referral_credit_rebases")
	if err != nil {
		return 0, err
	}
	var base WalletReferralCreditRebase
	if exists {
		err = lockForUpdate(tx).Where("reward_id = ?", reward.Id).First(&base).Error
	}
	if !exists || errors.Is(err, gorm.ErrRecordNotFound) {
		return unrebasedReferralDeltaTx(tx, reward, originalDelta)
	}
	if err != nil {
		return 0, err
	}
	divisor, ok := new(big.Rat).SetString(base.Divisor)
	if (base.Rounding != "half-away-from-zero" && base.Rounding != "toward-zero") || !ok || divisor.Cmp(big.NewRat(1, 1)) <= 0 || base.UserID != reward.InviterId || base.MigrationID == "" ||
		base.OriginalQuota != int64(reward.Quota) || base.OriginalQuota < 0 || base.RebasedQuota < 0 || base.RebasedRevokedQuota < 0 || base.RebasedRevokedQuota > base.RebasedQuota || base.RebasedPenaltyQuota < 0 {
		return 0, fmt.Errorf("%w: invalid referral rebase baseline", ErrRefundAmountInvalid)
	}
	if base.RebasedQuota != scaleReferralCredit(int64(reward.Quota), divisor, base.Rounding) {
		return 0, fmt.Errorf("%w: referral rebase quota mismatch", ErrRefundAmountInvalid)
	}
	delta := int64(originalDelta)
	switch kind {
	case "clawback":
		if base.RebasedRevokedQuota != 0 {
			return 0, fmt.Errorf("%w: referral already revoked", ErrRefundAmountInvalid)
		}
		delta = -base.RebasedQuota
		base.RebasedRevokedQuota = base.RebasedQuota
	case "penalty":
		if base.RebasedPenaltyQuota != 0 {
			return 0, fmt.Errorf("%w: referral penalty already applied", ErrRefundAmountInvalid)
		}
		base.RebasedPenaltyQuota = scaleReferralCredit(int64(reward.PenaltyQuota), divisor, base.Rounding)
		delta = -base.RebasedPenaltyQuota
	case "restore_reward":
		expected := scaleReferralCredit(int64(reward.RevokedQuota), divisor, base.Rounding)
		if expected != base.RebasedRevokedQuota {
			return 0, fmt.Errorf("%w: referral revocation counter mismatch", ErrRefundAmountInvalid)
		}
		delta = base.RebasedRevokedQuota
		base.RebasedRevokedQuota = 0
	case "restore_penalty":
		expected := scaleReferralCredit(int64(reward.PenaltyQuota), divisor, base.Rounding)
		if expected != base.RebasedPenaltyQuota {
			return 0, fmt.Errorf("%w: referral penalty counter mismatch", ErrRefundAmountInvalid)
		}
		delta = base.RebasedPenaltyQuota
		base.RebasedPenaltyQuota = 0
	default:
		return 0, fmt.Errorf("%w: unexpected historical referral delta", ErrRefundAmountInvalid)
	}
	if err := tx.Save(&base).Error; err != nil {
		return 0, err
	}
	return int(delta), nil
}

func scaleReferralCredit(quota int64, divisor *big.Rat, rounding string) int64 {
	numerator := new(big.Int).Mul(big.NewInt(quota), divisor.Denom())
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, divisor.Num(), remainder)
	if rounding == "half-away-from-zero" && remainder.Lsh(remainder, 1).Cmp(divisor.Num()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient.Int64()
}

func unrebasedReferralDeltaTx(tx *gorm.DB, reward *ReferralReward, originalDelta int) (int, error) {
	exists, err := walletCreditAuditTableExists(tx, "wallet_credit_rebases")
	if err != nil {
		return 0, err
	}
	if !exists {
		return originalDelta, nil
	}
	var audits []struct{ Plan string }
	if err := tx.Table("wallet_credit_rebases").Select("plan").Find(&audits).Error; err != nil {
		return 0, err
	}
	for _, audit := range audits {
		var plan struct {
			UserIDs          []int           `json:"user_ids"`
			IncludeAffiliate bool            `json:"include_affiliate"`
			ReferralBases    json.RawMessage `json:"referral_bases"`
		}
		if json.Unmarshal([]byte(audit.Plan), &plan) != nil {
			return 0, fmt.Errorf("%w: invalid referral rebase audit plan", ErrRefundAmountInvalid)
		}
		affected := false
		for _, id := range plan.UserIDs {
			if id == reward.InviterId {
				affected = true
			}
		}
		if affected && plan.IncludeAffiliate && (len(plan.ReferralBases) == 0 || string(plan.ReferralBases) == "null") {
			return 0, fmt.Errorf("%w: referral rebase audit lacks baselines", ErrRefundAmountInvalid)
		}
		var bases []struct {
			RewardID int `json:"reward_id"`
		}
		if len(plan.ReferralBases) > 0 && json.Unmarshal(plan.ReferralBases, &bases) != nil {
			return 0, fmt.Errorf("%w: invalid referral rebase plan", ErrRefundAmountInvalid)
		}
		for _, base := range bases {
			if base.RewardID <= 0 {
				return 0, fmt.Errorf("%w: invalid referral rebase reward id", ErrRefundAmountInvalid)
			}
			if base.RewardID == reward.Id {
				return 0, fmt.Errorf("%w: referral rebase baseline missing", ErrRefundAmountInvalid)
			}
		}
	}
	return originalDelta, nil
}
