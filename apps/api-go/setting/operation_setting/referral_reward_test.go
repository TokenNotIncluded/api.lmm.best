// Copyright (C) 2026 LIghtJUNction
// SPDX-License-Identifier: AGPL-3.0-or-later

package operation_setting

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReferralPolicyValidation(t *testing.T) {
	invalid := []string{"null", "[]", "", "{} {}", `{"enabled":true}`, `{"fixed_quota":-1}`, `{"fixed_quota":1.5}`, `{"reward_bps":10001}`, `{"penalty_bps":-1}`, `{"unknown":1}`, fmt.Sprintf(`{"fixed_quota":%d}`, int64(common.MaxWalletQuota)+1)}
	for _, value := range invalid {
		t.Run(value, func(t *testing.T) { _, err := ParseReferralRewardPolicy(value); require.Error(t, err) })
	}
	policy, err := ParseReferralRewardPolicy(`{"enabled":true,"fixed_quota":100,"reward_bps":1000,"minimum_topup_quota":1000,"maximum_reward_quota":250,"penalty_bps":2000,"maximum_penalty_quota":40}`)
	require.NoError(t, err)
	assert.EqualValues(t, 0, policy.Reward(999))
	assert.EqualValues(t, 200, policy.Reward(1000))
	assert.EqualValues(t, 223, policy.Reward(1239))
	assert.EqualValues(t, 250, policy.Reward(100000))
	assert.EqualValues(t, 40, policy.Penalty(250))
	policy.MaximumRewardQuota = 0
	policy.RewardBPS = 10000
	assert.EqualValues(t, common.MaxWalletQuota, policy.Reward(int64(common.MaxWalletQuota)))
	policy.Enabled = false
	assert.Zero(t, policy.Reward(100000))
}

func TestReferralPolicyInvalidUpdatePreservesSnapshot(t *testing.T) {
	previous := GetReferralRewardPolicy()
	t.Cleanup(func() { referralRewardPolicy.Store(&previous) })
	require.NoError(t, UpdateReferralRewardPolicy(`{"enabled":true,"fixed_quota":123}`))
	require.Error(t, UpdateReferralRewardPolicy(`{"enabled":true,"fixed_quota":-1}`))
	assert.EqualValues(t, 123, GetReferralRewardPolicy().FixedQuota)
}
