// Copyright (C) 2026 LIghtJUNction
// SPDX-License-Identifier: AGPL-3.0-or-later

package operation_setting

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

const ReferralRewardOptionKey = "ReferralRewardPolicy"
const DefaultReferralRewardPolicyJSON = `{"enabled":false,"fixed_quota":0,"reward_bps":0,"minimum_topup_quota":0,"maximum_reward_quota":0,"penalty_bps":0,"maximum_penalty_quota":0}`

// All amounts use integer platform quota, never a provider's currency. One
// basis point is 0.01%. Publish the whole policy atomically, not field by field.
type ReferralRewardPolicy struct {
	Enabled             bool  `json:"enabled"`
	FixedQuota          int64 `json:"fixed_quota"`
	RewardBPS           int64 `json:"reward_bps"`
	MinimumTopUpQuota   int64 `json:"minimum_topup_quota"`
	MaximumRewardQuota  int64 `json:"maximum_reward_quota"`
	PenaltyBPS          int64 `json:"penalty_bps"`
	MaximumPenaltyQuota int64 `json:"maximum_penalty_quota"`
}

var referralRewardPolicy atomic.Pointer[ReferralRewardPolicy]

func GetReferralRewardPolicy() ReferralRewardPolicy {
	if policy := referralRewardPolicy.Load(); policy != nil {
		return *policy
	}
	return ReferralRewardPolicy{}
}

func ParseReferralRewardPolicy(value string) (ReferralRewardPolicy, error) {
	var policy ReferralRewardPolicy
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if strings.TrimSpace(value) == "null" {
		return policy, errors.New("referral policy must be a JSON object")
	}
	if err := decoder.Decode(&policy); err != nil {
		return policy, err
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		return policy, errors.New("referral policy must contain one JSON object")
	}
	for _, value := range []int64{policy.FixedQuota, policy.MinimumTopUpQuota, policy.MaximumRewardQuota, policy.MaximumPenaltyQuota} {
		if value < 0 || value > int64(common.MaxWalletQuota) {
			return policy, errors.New("referral quota is outside the wallet safe range")
		}
	}
	if policy.RewardBPS < 0 || policy.RewardBPS > 10000 || policy.PenaltyBPS < 0 || policy.PenaltyBPS > 10000 {
		return policy, errors.New("referral percentages must be between 0 and 10000 basis points")
	}
	if policy.Enabled && policy.FixedQuota == 0 && policy.RewardBPS == 0 {
		return policy, errors.New("configure a positive referral reward before enabling it")
	}
	return policy, nil
}

func UpdateReferralRewardPolicy(value string) error {
	policy, err := ParseReferralRewardPolicy(value)
	if err != nil {
		return err
	}
	referralRewardPolicy.Store(&policy)
	return nil
}

func (policy ReferralRewardPolicy) Reward(quota int64) int64 {
	if !policy.Enabled || quota <= 0 || quota < policy.MinimumTopUpQuota {
		return 0
	}
	// Divide first: quota * basis points could overflow int64.
	amount := policy.FixedQuota + quota/10000*policy.RewardBPS + quota%10000*policy.RewardBPS/10000
	if policy.MaximumRewardQuota > 0 && amount > policy.MaximumRewardQuota {
		amount = policy.MaximumRewardQuota
	}
	if amount > int64(common.MaxWalletQuota) {
		amount = int64(common.MaxWalletQuota)
	}
	return amount
}

func (policy ReferralRewardPolicy) Penalty(reward int64) int64 {
	amount := reward/10000*policy.PenaltyBPS + reward%10000*policy.PenaltyBPS/10000
	if policy.MaximumPenaltyQuota > 0 && amount > policy.MaximumPenaltyQuota {
		amount = policy.MaximumPenaltyQuota
	}
	return amount
}
