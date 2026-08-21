/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package model

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicRelayChannelConfigPreservesFullConfiguration(t *testing.T) {
	raw := `{
		"mode":"single",
		"channel":{
			"name":"shared relay",
			"base_url":"",
			"key":"secret-key",
			"models":"model-a,model-b",
			"model_mapping":"{\"model-a\":\"upstream-a\"}",
			"setting":"{\"proxy\":\"enabled\"}"
		}
	}`

	normalized, err := normalizePublicRelayChannelConfig(raw, "shared relay", "", "model-a,model-b")
	require.NoError(t, err)
	assert.Contains(t, normalized, `"model_mapping"`)
	assert.Contains(t, normalized, `"setting"`)

	_, err = normalizePublicRelayChannelConfig(`{
		"channel":{"name":"shared relay","base_url":"","models":"model-a"}
	}`, "shared relay", "", "model-a")
	assert.ErrorIs(t, err, ErrPublicRelayInvalidInput)
}

func TestPublicRelayPublicViewDoesNotExposeChannelConfig(t *testing.T) {
	item := PublicRelayContribution{
		Name:          "shared relay",
		ChannelConfig: `{"channel":{"key":"secret-key"}}`,
	}
	encoded, err := json.Marshal(item.PublicView())
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "channel_config")
	assert.NotContains(t, string(encoded), "secret-key")
}

func TestPublicRelayContributionDoesNotSerializeChannelConfig(t *testing.T) {
	item := PublicRelayContribution{
		Name:          "shared relay",
		ChannelConfig: `{"channel":{"key":"secret-key"}}`,
	}

	encoded, err := json.Marshal(item)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "channel_config")
	assert.NotContains(t, string(encoded), "secret-key")
}

func TestPublicRelayTipsRemainPendingUntilWithdrawal(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&PublicRelayContribution{}, &PublicRelayTip{}))

	owner := User{Username: "relay-owner", Password: "password", AffCode: "relay-owner-aff", Group: "default"}
	tipper := User{Username: "relay-tipper", Password: "password", AffCode: "relay-tipper-aff", Quota: int(common.QuotaPerUnit * 20), Group: "default"}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&tipper).Error)
	contribution := PublicRelayContribution{
		UserId: owner.Id, ContributorEmail: "owner@example.com", Name: "shared relay",
		BaseURL: "https://relay.example.com", Group: "FREE", Status: PublicRelayApproved,
		ChannelId: 1, CreatedAt: common.GetTimestamp(), UpdatedAt: common.GetTimestamp(),
	}
	require.NoError(t, db.Create(&contribution).Error)

	tipQuota := int64(common.QuotaPerUnit * 10)
	require.NoError(t, TipPublicRelayContribution(contribution.Id, tipper.Id, tipQuota, "thanks"))

	var afterTipOwner, afterTipper User
	require.NoError(t, db.First(&afterTipOwner, owner.Id).Error)
	require.NoError(t, db.First(&afterTipper, tipper.Id).Error)
	assert.Zero(t, afterTipOwner.Quota, "tips must not be spendable before withdrawal")
	assert.Equal(t, tipper.Quota-int(tipQuota), afterTipper.Quota)

	withdrawn, err := WithdrawPublicRelayTips(contribution.Id, owner.Id, "default")
	require.NoError(t, err)
	assert.Equal(t, tipQuota, withdrawn)
	require.NoError(t, db.First(&afterTipOwner, owner.Id).Error)
	assert.Equal(t, int(tipQuota), afterTipOwner.Quota)
}

func TestPublicRelayRoutingBoundsPoolAndPreferenceValidation(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&PublicRelayContribution{}, &PublicRelayPreference{}))
	previousGroup := operation_setting.GetPublicRelaySetting().Group
	operation_setting.GetPublicRelaySetting().Group = "FREE"
	t.Cleanup(func() { operation_setting.GetPublicRelaySetting().Group = previousGroup })

	owner := User{Username: "relay-routing-owner", Password: "password", AffCode: "relay-routing-owner-aff"}
	require.NoError(t, db.Create(&owner).Error)
	for index := 0; index < publicRelayRoutingMaxItems+25; index++ {
		require.NoError(t, db.Create(&PublicRelayContribution{
			UserId: owner.Id, ContributorEmail: "owner@example.com", Name: "relay",
			BaseURL: "https://relay.example.com", Group: "FREE", Status: PublicRelayApproved,
			ChannelId: index + 1, CreatedAt: int64(index + 1), UpdatedAt: int64(index + 1),
		}).Error)
	}

	items, group, err := ListPublicRelayRouting(owner.Id)
	require.NoError(t, err)
	assert.Equal(t, "FREE", group)
	assert.Len(t, items, publicRelayRoutingMaxItems)

	// Preference validation only checks the submitted IDs; it must not load the
	// entire approved pool to construct a membership set.
	require.NoError(t, UpdatePublicRelayRouting(owner.Id, "FREE", []int{1}, []int{2}))
	disabled, ordered, err := GetPublicRelayRoutingPreference(owner.Id, "FREE")
	require.NoError(t, err)
	assert.Equal(t, []int{1}, disabled)
	assert.Equal(t, []int{2}, ordered)
}
