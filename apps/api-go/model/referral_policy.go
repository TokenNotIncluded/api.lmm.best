package model

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

type ReferralPolicy struct {
	RewardQuota     int `json:"reward_quota"`
	MinTopUpQuota   int `json:"min_top_up_quota"`
	MaxRewardQuota  int `json:"max_reward_quota"`
	PenaltyPercent  int `json:"penalty_percent"`
	MaxPenaltyQuota int `json:"max_penalty_quota"`
}

func referralOptionDefaults() map[string]string {
	return map[string]string{"ReferralMinTopUpQuota": "0", "ReferralMaxRewardQuota": "0",
		"ReferralPenaltyPercent": "20", "ReferralMaxPenaltyQuota": "0"}
}

func validateReferralOption(key, value string) error {
	_, known := referralOptionDefaults()[key]
	if !known && key != "QuotaForInviter" {
		return nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 || parsed > common.MaxWalletQuota || (key == "ReferralPenaltyPercent" && parsed > 100) {
		return fmt.Errorf("invalid non-negative referral setting: %s", key)
	}
	return nil
}

func GetReferralPolicy() ReferralPolicy {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	values := referralOptionDefaults()
	for key := range values {
		if value, ok := common.OptionMap[key]; ok && validateReferralOption(key, value) == nil {
			values[key] = value
		}
	}
	parse := func(key string) int { value, _ := strconv.Atoi(strings.TrimSpace(values[key])); return value }
	return ReferralPolicy{RewardQuota: max(0, min(common.QuotaForInviter, common.MaxWalletQuota)),
		MinTopUpQuota: parse("ReferralMinTopUpQuota"), MaxRewardQuota: parse("ReferralMaxRewardQuota"),
		PenaltyPercent: parse("ReferralPenaltyPercent"), MaxPenaltyQuota: parse("ReferralMaxPenaltyQuota")}
}
