/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

package model

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestSearchChannelsPageFiltersBeforePagination(t *testing.T) {
	truncateTables(t)

	channels := []Channel{
		{Name: "needle-active-a", Status: common.ChannelStatusEnabled, Type: 1, Models: "gpt-5", Group: "default"},
		{Name: "needle-disabled", Status: common.ChannelStatusManuallyDisabled, Type: 1, Models: "gpt-5", Group: "default"},
		{Name: "needle-active-b", Status: common.ChannelStatusEnabled, Type: 2, Models: "gpt-5", Group: "default"},
		{Name: "other", Status: common.ChannelStatusEnabled, Type: 2, Models: "gpt-5", Group: "default"},
	}
	for i := 0; i < 101; i++ {
		channels = append(channels, Channel{
			Name:   fmt.Sprintf("needle-bulk-%03d", i),
			Status: common.ChannelStatusEnabled,
			Type:   1,
			Models: "gpt-5",
			Group:  "default",
		})
	}
	require.NoError(t, DB.Create(&channels).Error)

	page, err := SearchChannelsPage(
		"needle",
		"",
		"",
		common.ChannelStatusEnabled,
		-1,
		0,
		1,
		false,
		false,
		NewChannelSortOptions("id", "asc", false),
	)
	require.NoError(t, err)
	require.Equal(t, int64(103), page.Total)
	require.Equal(t, map[int64]int64{1: 102, 2: 1}, page.TypeCounts)
	require.Len(t, page.Channels, 1)
	require.Equal(t, "needle-active-a", page.Channels[0].Name)

	page, err = SearchChannelsPage(
		"needle",
		"",
		"",
		common.ChannelStatusEnabled,
		2,
		0,
		10000,
		false,
		false,
		NewChannelSortOptions("id", "asc", false),
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Len(t, page.Channels, 1)
	require.Equal(t, "needle-active-b", page.Channels[0].Name)

	page, err = SearchChannelsPage(
		"needle-bulk",
		"",
		"",
		common.ChannelStatusEnabled,
		-1,
		0,
		10000,
		false,
		false,
		NewChannelSortOptions("id", "asc", false),
	)
	require.NoError(t, err)
	require.Equal(t, int64(101), page.Total)
	require.Len(t, page.Channels, channelSearchMaxPageSize)
}

func TestChannelSearchSensitiveFieldsRequirePermission(t *testing.T) {
	truncateTables(t)

	privateBaseURL := "https://private-host.example.com/v1"
	keyTag := "key-secret-tag"
	baseURLTag := "base-url-secret-tag"
	keySearch := "provider-key-search-sentinel"
	channels := []Channel{
		{
			Name:   "ordinary key channel",
			Key:    keySearch,
			Status: common.ChannelStatusEnabled,
			Models: "gpt-5",
			Group:  "default",
			Tag:    &keyTag,
		},
		{
			Name:    "ordinary base url channel",
			BaseURL: &privateBaseURL,
			Status:  common.ChannelStatusEnabled,
			Models:  "gpt-5",
			Group:   "default",
			Tag:     &baseURLTag,
		},
	}
	require.NoError(t, DB.Create(&channels).Error)

	for _, test := range []struct {
		name    string
		keyword string
		tag     string
	}{
		{name: "key", keyword: keySearch, tag: keyTag},
		{name: "base url", keyword: "private-host.example.com", tag: baseURLTag},
	} {
		t.Run(test.name, func(t *testing.T) {
			withoutSecrets, err := SearchChannelsPage(test.keyword, "", "", -1, -1, 0, 20, false, false)
			require.NoError(t, err)
			require.Zero(t, withoutSecrets.Total)
			require.Empty(t, withoutSecrets.Channels)

			withoutSecretTags, err := SearchTags(test.keyword, "", "", false, false)
			require.NoError(t, err)
			require.Empty(t, withoutSecretTags)

			withSecrets, err := SearchChannelsPage(test.keyword, "", "", -1, -1, 0, 20, true, false)
			require.NoError(t, err)
			require.Equal(t, int64(1), withSecrets.Total)
			require.Len(t, withSecrets.Channels, 1)

			withSecretTags, err := SearchTags(test.keyword, "", "", true, false)
			require.NoError(t, err)
			require.Len(t, withSecretTags, 1)
			require.Equal(t, test.tag, *withSecretTags[0])
		})
	}
}
