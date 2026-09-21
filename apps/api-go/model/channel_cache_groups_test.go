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

func TestRefreshChannelCachePreservesAbilityGroups(t *testing.T) {
	preserveChannelTestState(t)
	db := openCacheTestDB(t, &Channel{}, &Ability{})
	DB = db
	common.MemoryCacheEnabled = true

	require.NoError(t, db.Create(&Channel{
		Id:     1201,
		Name:   "cache-group-channel",
		Status: common.ChannelStatusEnabled,
		Group:  "default",
		Models: "cache-group-model",
	}).Error)
	require.NoError(t, db.Create(&[]Ability{
		{Group: "default", Model: "cache-group-model", ChannelId: 1201, Enabled: true},
		{Group: "orphan-group", Model: "unused-model", ChannelId: 1201, Enabled: false},
	}).Error)

	require.NoError(t, refreshChannelCache())

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	require.Contains(t, group2model2channels, "default")
	require.Contains(t, group2model2channels["default"], "cache-group-model")
	require.Contains(t, group2model2channels, "orphan-group")
	require.Empty(t, group2model2channels["orphan-group"])
}

func TestRefreshChannelCacheBuildsMissingAbilityGroups(t *testing.T) {
	for _, withAbility := range []bool{false, true} {
		t.Run(fmt.Sprintf("ability=%t", withAbility), func(t *testing.T) {
			preserveChannelTestState(t)
			DB = openCacheTestDB(t, &Channel{}, &Ability{})
			common.MemoryCacheEnabled = true
			low, high := int64(1), int64(10)
			require.NoError(t, DB.Create(&[]Channel{
				{Id: 1, Status: common.ChannelStatusEnabled, Group: "default,missing", Models: "m1,m2", Priority: &low},
				{Id: 2, Status: common.ChannelStatusEnabled, Group: "missing", Models: "m1", Priority: &high},
				{Id: 3, Status: common.ChannelStatusManuallyDisabled, Group: "missing,disabled-only", Models: "m1"},
			}).Error)
			if withAbility {
				require.NoError(t, DB.Create(&Ability{Group: "default", Model: "m1", ChannelId: 1, Enabled: true}).Error)
			}
			require.NoError(t, refreshChannelCache())
			channelSyncLock.RLock()
			defer channelSyncLock.RUnlock()
			require.Equal(t, []int{2, 1}, group2model2channels["missing"]["m1"])
			require.Equal(t, []int{1}, group2model2channels["missing"]["m2"])
			require.Equal(t, []int{1}, group2model2channels["default"]["m1"])
			require.NotContains(t, group2model2channels, "disabled-only")
		})
	}
}
