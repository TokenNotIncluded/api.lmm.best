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
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPublicRelayPublication(t *testing.T) *gorm.DB {
	t.Helper()
	installPublicRelayCreditFixture(t)
	previous := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previous })
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&PublicRelayContribution{}, &PublicRelayPreference{}, &PublicRelayReport{}, &Channel{}, &Ability{}))
	return db
}

func publicRelaySubmissionFixture(t *testing.T, mode string) *PublicRelayContribution {
	t.Helper()
	raw := `{"mode":"` + mode + `","multi_key_mode":"polling","batch_add_set_key_prefix_2_name":true,"channel":{"id":999,"type":1,"name":"shared relay","models":"model-a,model-b","base_url":"https://relay.example.test/v1/","key":"test-only-one\ntest-only-two","group":"paid","status":2,"priority":999,"used_quota":999,"public_relay_contribution_id":999,"channel_info":{"is_multi_key":true,"multi_key_size":999},"model_mapping":"{\"model-a\":\"upstream-a\"}"}}`
	if mode == "single" {
		raw = strings.Replace(raw, `test-only-one\ntest-only-two`, `test-only-one`, 1)
	}
	item, err := CreatePublicRelayContribution(1, "owner@example.test", "shared relay", "https://relay.example.test/v1", "model-a,model-b", "fixture", raw)
	require.NoError(t, err)
	return item
}

func TestPublicRelayApprovalPublishesOneRoutableChannel(t *testing.T) {
	for _, mode := range []string{"single", "multi_to_single", "batch"} {
		t.Run(mode, func(t *testing.T) {
			db := setupPublicRelayPublication(t)
			item := publicRelaySubmissionFixture(t, mode)
			before, err := ListApprovedPublicRelays(50)
			require.NoError(t, err)
			require.Empty(t, before)
			approved, err := ReviewPublicRelayContribution(item.Id, 2, true, "checked")
			require.NoError(t, err)
			require.Equal(t, PublicRelayApproved, approved.Status)
			require.Positive(t, approved.ChannelId)
			require.NotEqual(t, 999, approved.ChannelId)
			var channel Channel
			require.NoError(t, db.First(&channel, approved.ChannelId).Error)
			require.Equal(t, item.Id, channel.PublicRelayContributionId)
			require.Equal(t, "FREE", channel.Group)
			require.Equal(t, common.ChannelStatusEnabled, channel.Status)
			require.Zero(t, channel.UsedQuota)
			require.EqualValues(t, 0, channel.GetPriority())
			require.Equal(t, "shared relay", channel.Name, "public names must not contain key prefixes")
			require.Equal(t, "https://relay.example.test/v1", channel.GetBaseURL())
			if mode != "single" {
				require.True(t, channel.ChannelInfo.IsMultiKey)
				require.Equal(t, 2, channel.ChannelInfo.MultiKeySize)
				require.Equal(t, constant.MultiKeyMode("polling"), channel.ChannelInfo.MultiKeyMode)
			} else {
				require.False(t, channel.ChannelInfo.IsMultiKey)
			}
			var abilities []Ability
			require.NoError(t, db.Where("channel_id = ?", channel.Id).Find(&abilities).Error)
			require.Len(t, abilities, 2)
			for _, ability := range abilities {
				require.True(t, ability.Enabled)
				require.Equal(t, "FREE", ability.Group)
			}
			catalog, err := ListApprovedPublicRelays(50)
			require.NoError(t, err)
			require.Len(t, catalog, 1)
			routing, _, err := ListPublicRelayRouting(3)
			require.NoError(t, err)
			require.Len(t, routing, 1)
			require.Equal(t, channel.Id, routing[0].ChannelId)
			selected, err := GetRandomSatisfiedChannel("FREE", "model-a", 0, "/v1/chat/completions")
			require.NoError(t, err)
			require.NotNil(t, selected, "an approved listing must be selected by the real relay router")
			require.Equal(t, channel.Id, selected.Id)
			managed, err := ListUserPublicRelayContributions(1, 50)
			require.NoError(t, err)
			encoded, err := json.Marshal(managed)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "test-only-")
			require.NotContains(t, string(encoded), "channel_config")
			_, err = ReviewPublicRelayContribution(item.Id, 2, true, "repeat")
			require.ErrorIs(t, err, ErrPublicRelayAlreadyReviewed)
			var count int64
			require.NoError(t, db.Model(&Channel{}).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestPublicRelayApprovalRollsBackAndRepairsLegacyUnlinkedApprovals(t *testing.T) {
	db := setupPublicRelayPublication(t)
	item := publicRelaySubmissionFixture(t, "single")
	require.NoError(t, db.Migrator().DropTable(&Ability{}))
	_, err := ReviewPublicRelayContribution(item.Id, 2, true, "checked")
	require.Error(t, err)
	var stored PublicRelayContribution
	require.NoError(t, db.First(&stored, item.Id).Error)
	require.Equal(t, PublicRelayPending, stored.Status)
	require.Zero(t, stored.ChannelId)
	var count int64
	require.NoError(t, db.Model(&Channel{}).Count(&count).Error)
	require.Zero(t, count, "channel creation and review must roll back together")
	require.NoError(t, db.AutoMigrate(&Ability{}))
	require.NoError(t, db.Model(&stored).Update("status", PublicRelayApproved).Error)
	reviewable, err := ListAdminPublicRelayContributions("reviewable", 100)
	require.NoError(t, err)
	require.Len(t, reviewable, 1)
	repaired, err := ReviewPublicRelayContribution(item.Id, 2, true, "republished")
	require.NoError(t, err)
	require.Positive(t, repaired.ChannelId)
	reviewable, err = ListAdminPublicRelayContributions("reviewable", 100)
	require.NoError(t, err)
	require.Empty(t, reviewable)
}

func TestPublicRelayRejectionDoesNotCreateChannels(t *testing.T) {
	db := setupPublicRelayPublication(t)
	item := publicRelaySubmissionFixture(t, "single")
	_, err := ReviewPublicRelayContribution(item.Id, 2, false, " ")
	require.ErrorIs(t, err, ErrPublicRelayInvalidInput)
	rejected, err := ReviewPublicRelayContribution(item.Id, 2, false, "invalid upstream")
	require.NoError(t, err)
	require.Equal(t, PublicRelayRejected, rejected.Status)
	require.Zero(t, rejected.ChannelId)
	var count int64
	require.NoError(t, db.Model(&Channel{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestPublicRelayCatalogHidesUnusableChannels(t *testing.T) {
	for _, state := range []string{"disabled", "deleted", "no-ability"} {
		t.Run(state, func(t *testing.T) {
			db := setupPublicRelayPublication(t)
			item := publicRelaySubmissionFixture(t, "single")
			approved, err := ReviewPublicRelayContribution(item.Id, 2, true, "ok")
			require.NoError(t, err)
			switch state {
			case "disabled":
				require.NoError(t, db.Model(&Channel{}).Where("id = ?", approved.ChannelId).Update("status", common.ChannelStatusManuallyDisabled).Error)
			case "deleted":
				require.NoError(t, db.Delete(&Channel{}, approved.ChannelId).Error)
			case "no-ability":
				require.NoError(t, db.Where("channel_id = ?", approved.ChannelId).Delete(&Ability{}).Error)
			}
			list, err := ListApprovedPublicRelays(50)
			require.NoError(t, err)
			require.Empty(t, list)
			routing, _, err := ListPublicRelayRouting(3)
			require.NoError(t, err)
			require.Empty(t, routing)
			mine, err := ListUserPublicRelayContributions(1, 50)
			require.NoError(t, err)
			require.Len(t, mine, 1, "contributors keep access to their records and tip balance")
		})
	}
}

func TestPublicRelayReportCanBeClosedAndReopened(t *testing.T) {
	db := setupPublicRelayPublication(t)
	item := publicRelaySubmissionFixture(t, "single")
	_, err := ReviewPublicRelayContribution(item.Id, 2, true, "ok")
	require.NoError(t, err)
	report, err := CreatePublicRelayReport(item.Id, 3, "connection failed")
	require.NoError(t, err)
	require.NoError(t, ReviewPublicRelayReport(report.Id, 2, true, "resolved"))
	require.NoError(t, ReviewPublicRelayReport(report.Id, 2, true, "resolved"), "closing twice is safe")
	open, err := ListAdminPublicRelayReports("open", 100)
	require.NoError(t, err)
	require.Empty(t, open)
	reopened, err := CreatePublicRelayReport(item.Id, 3, "failing again")
	require.NoError(t, err)
	require.Equal(t, report.Id, reopened.Id)
	require.Equal(t, "failing again", reopened.Reason)
	require.Zero(t, reopened.ReviewedBy)
	require.Zero(t, reopened.ReviewedAt)
	require.Empty(t, reopened.ReviewNote)
	open, err = ListAdminPublicRelayReports("open", 100)
	require.NoError(t, err)
	require.Len(t, open, 1)
	var count int64
	require.NoError(t, db.Model(&PublicRelayReport{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.ErrorIs(t, ReviewPublicRelayReport(999, 2, true, "not found"), ErrPublicRelayNotFound)
	require.ErrorIs(t, ReviewPublicRelayReport(report.Id, 2, true, strings.Repeat("a", 2001)), ErrPublicRelayInvalidInput)
}

func TestPublicRelaySubmissionRejectsUnusableConfiguration(t *testing.T) {
	setupPublicRelayPublication(t)
	for _, entry := range []struct{ name, config string }{
		{"unknown provider", `{"mode":"single","channel":{"type":999,"name":"relay","models":"model-a","key":"test-only"}}`},
		{"blank keys", `{"mode":"batch","channel":{"type":1,"name":"relay","models":"model-a","key":" \n "}}`},
		{"private provider default", `{"mode":"single","channel":{"type":4,"name":"relay","models":"model-a","key":"test-only"}}`},
		{"server proxy", `{"mode":"single","channel":{"type":1,"name":"relay","models":"model-a","key":"test-only","setting":"{\"proxy\":\"http://127.0.0.1:8080\"}"}}`},
		{"bad mode", `{"mode":"invalid","channel":{"type":1,"name":"relay","models":"model-a","key":"test-only"}}`},
	} {
		_, err := CreatePublicRelayContribution(1, "owner@example.test", "relay", "", "model-a", "", entry.config)
		require.Error(t, err, entry.name)
	}
	for _, raw := range []string{"http://localhost.", "https://a.localhost", "http://127.0.0.1", "http://[::1]", "http://192.168.1.2", "http://169.254.169.254", "https://:443", "https://relay.example.test:65536", "https://relay.example.test:0", "https://relay.example.test?key=secret", "https://relay.example.test#fragment"} {
		_, err := normalizePublicRelayURL(raw)
		require.ErrorIs(t, err, ErrPublicRelayInvalidURL, raw)
	}
}

func TestPublicRelayChannelPreparationDoesNotMutateBatchNames(t *testing.T) {
	input := &Channel{Type: 1, Name: "batch", Models: "model-a", Key: "test-one\n\ntest-two"}
	rows, err := PrepareChannelCreation(ChannelCreationInput{Mode: "batch", Channel: input, BatchAddSetKeyPrefix2Name: true})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "batch test-one", rows[0].Name)
	require.Equal(t, "batch test-two", rows[1].Name)
	require.Equal(t, "batch", input.Name)
	require.Equal(t, "test-one\n\ntest-two", input.Key)
	input.Key = " \n "
	_, err = PrepareChannelCreation(ChannelCreationInput{Mode: "batch", Channel: input})
	require.Error(t, err)
}
