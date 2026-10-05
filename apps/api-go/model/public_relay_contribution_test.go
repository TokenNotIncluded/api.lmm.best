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

	"github.com/shopspring/decimal"

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
	installPublicRelayCreditFixture(t)
	item := PublicRelayContribution{
		Name:          "shared relay",
		ChannelConfig: `{"channel":{"key":"secret-key"}}`,
	}
	view, err := item.PublicView()
	require.NoError(t, err)
	encoded, err := json.Marshal(view)
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
	installPublicRelayCreditFixture(t)
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&PublicRelayContribution{}, &PublicRelayTip{}, &Log{}))

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
	installPublicRelayCreditFixture(t)
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

func installPublicRelayCreditFixture(t *testing.T) {
	t.Helper()
	anchor, priorErr := common.CreditsPerUSD()
	legacy, _ := common.LegacyPricingQuotaPerUnit()
	oldQ := common.QuotaPerUnit
	oldFX := operation_setting.USDExchangeRate
	oldGroup := operation_setting.GetPublicRelaySetting().Group
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQ
		operation_setting.USDExchangeRate = oldFX
		operation_setting.GetPublicRelaySetting().Group = oldGroup
		if priorErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(anchor, legacy))
		}
	})
	common.QuotaPerUnit = 500000
	operation_setting.USDExchangeRate = 7
	operation_setting.GetPublicRelaySetting().Group = "FREE"
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(500000)))
}

func TestPublicRelayRealUSDViewsAndHistoricalCreditPolicy(t *testing.T) {
	installPublicRelayCreditFixture(t)
	item := PublicRelayContribution{UsedQuota: 3500000, TipQuota: 7000000, WithdrawnQuota: 3500000}
	view, err := item.PublicView()
	require.NoError(t, err)
	require.Equal(t, float64(1), view.UsedQuotaUSD)
	require.Equal(t, float64(2), view.TipQuotaUSD)
	require.Equal(t, float64(1), view.WithdrawnQuotaUSD)
	require.Equal(t, item.UsedQuota, view.UsedQuota)
	minimum, maximum, err := PublicRelayTipBounds()
	require.NoError(t, err)
	require.EqualValues(t, 5000000, minimum)
	require.EqualValues(t, 50000000, maximum)
	operation_setting.USDExchangeRate = 9.9
	current, err := item.PublicView()
	require.NoError(t, err)
	require.Equal(t, view, current, "USD and historical raw Credit thresholds do not change with FX")
	common.ClearCreditsPerUSD()
	_, err = item.PublicView()
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	_, _, err = PublicRelayTipBounds()
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	_, err = ListApprovedPublicRelays(20)
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	_, _, err = ListPublicRelayRouting(1)
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	_, err = ListUserPublicRelayContributions(1, 20)
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	_, err = ListAdminPublicRelayContributions("", 20)
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	require.ErrorIs(t, TipPublicRelayContribution(1, 2, 3500000, ""), common.ErrCreditUnitsUnavailable)
	_, err = WithdrawPublicRelayTips(1, 1, "default")
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
}

func TestPublicRelayHistoricalPendingWithdrawalIsExactAndCannotRepeat(t *testing.T) {
	installPublicRelayCreditFixture(t)
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&PublicRelayContribution{}, &PublicRelayTip{}, &Log{}))
	owner := User{Username: "historical-relay-owner", AffCode: "historical-relay-owner", Quota: 12345}
	require.NoError(t, db.Create(&owner).Error)
	historical := PublicRelayContribution{UserId: owner.Id, Name: "historical", Status: PublicRelayApproved, TipQuota: 8123456, WithdrawnQuota: 123456}
	require.NoError(t, db.Create(&historical).Error)
	tip := PublicRelayTip{ContributionId: historical.Id, TipperUserId: owner.Id + 1, Quota: 8123456, Message: "before migration", CreatedAt: 123}
	require.NoError(t, db.Create(&tip).Error)
	operation_setting.USDExchangeRate = 9.9
	amount, err := WithdrawPublicRelayTips(historical.Id, owner.Id, "default")
	require.NoError(t, err)
	require.EqualValues(t, 8000000, amount, "withdraw original pending raw credits, never reinterpret a historical tip as USD")
	var current User
	require.NoError(t, db.First(&current, owner.Id).Error)
	require.Equal(t, 8012345, current.Quota)
	amount, err = WithdrawPublicRelayTips(historical.Id, owner.Id, "default")
	require.ErrorIs(t, err, ErrPublicRelayInvalidInput)
	require.Zero(t, amount)
	require.NoError(t, db.First(&current, owner.Id).Error)
	require.Equal(t, 8012345, current.Quota)
	var unchangedTip PublicRelayTip
	require.NoError(t, db.First(&unchangedTip, tip.Id).Error)
	require.Equal(t, tip, unchangedTip)
	var updated PublicRelayContribution
	require.NoError(t, db.First(&updated, historical.Id).Error)
	require.Equal(t, historical.TipQuota, updated.TipQuota)
	require.Equal(t, historical.TipQuota, updated.WithdrawnQuota)
}

func TestPublicRelayLedgerOverflowRollsBackTipAndWithdrawal(t *testing.T) {
	installPublicRelayCreditFixture(t)
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&PublicRelayContribution{}, &PublicRelayTip{}, &Log{}))
	owner := User{Username: "overflow-relay-owner", AffCode: "overflow-owner", Quota: common.MaxWalletQuota}
	tipper := User{Username: "overflow-relay-tipper", AffCode: "overflow-tipper", Quota: 10000000}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&tipper).Error)
	contribution := PublicRelayContribution{UserId: owner.Id, Name: "overflow", Group: "FREE", Status: PublicRelayApproved, ChannelId: 1, TipQuota: common.MaxWalletQuota - 1}
	require.NoError(t, db.Create(&contribution).Error)
	require.ErrorIs(t, TipPublicRelayContribution(contribution.Id, tipper.Id, 2, ""), ErrWalletQuotaOutOfRange)
	var count int64
	require.NoError(t, db.Model(&PublicRelayTip{}).Count(&count).Error)
	require.Zero(t, count)
	var current User
	require.NoError(t, db.First(&current, tipper.Id).Error)
	require.Equal(t, tipper.Quota, current.Quota)
	require.NoError(t, db.Model(&contribution).Update("tip_quota", 5000000).Error)
	_, err := WithdrawPublicRelayTips(contribution.Id, owner.Id, "default")
	require.ErrorIs(t, err, ErrWalletQuotaOutOfRange)
	require.NoError(t, db.First(&contribution, contribution.Id).Error)
	require.Zero(t, contribution.WithdrawnQuota)
	current = User{}
	require.NoError(t, db.First(&current, owner.Id).Error)
	require.Equal(t, owner.Quota, current.Quota)
	require.NoError(t, db.Model(&contribution).Update("used_quota", common.MaxWalletQuota).Error)
	require.ErrorIs(t, RecordPublicRelayUsage(1, 1), ErrWalletQuotaOutOfRange)
}
