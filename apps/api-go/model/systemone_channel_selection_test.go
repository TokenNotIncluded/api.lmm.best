package model

import (
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSystemOneChannelSelectorsRestrictProtocols(t *testing.T) {
	resetPricingEndpointTestTables(t)
	for id, channelType := range map[int]int{
		901: constant.ChannelTypeTypeSafe,
		902: constant.ChannelTypeNewAPI,
		903: constant.ChannelTypeOpenAI,
		904: constant.ChannelTypeAdvancedCustom,
	} {
		settings := dto.ChannelOtherSettings{}
		if channelType == constant.ChannelTypeAdvancedCustom {
			settings = pricingEndpointAdvancedCustomConfig(
				dto.AdvancedCustomRoute{IncomingPath: "/typesafe/v1/systemone", UpstreamPath: "/typesafe/v1/systemone"},
				dto.AdvancedCustomRoute{IncomingPath: "/v1/chat/completions", UpstreamPath: "/v1/chat/completions"},
			)
		}
		insertPricingEndpointChannel(t, id, channelType, settings)
	}
	InitChannelCache()
	ids := []int{901, 902, 903, 904, 999}
	abilities := make([]Ability, len(ids))
	for index, id := range ids {
		abilities[index] = Ability{ChannelId: id, Model: "customer-decision"}
	}
	for _, tt := range []struct {
		path string
		want []int
	}{
		{path: "/v1/systemone", want: []int{901, 902}},
		{path: "/typesafe/v1/systemone", want: []int{901, 902}},
		{path: "/v1/chat/completions", want: []int{902, 903, 904, 999}},
		{path: "/v1/responses", want: []int{902, 903, 999}},
		{path: "", want: ids},
	} {
		t.Run(tt.path, func(t *testing.T) {
			filteredAbilities := filterAbilitiesByRequestPathAndModel(abilities, tt.path, "customer-decision")
			actualIDs := make([]int, 0, len(filteredAbilities))
			for _, ability := range filteredAbilities {
				actualIDs = append(actualIDs, ability.ChannelId)
			}
			assert.Equal(t, tt.want, actualIDs)
			channelSyncLock.RLock()
			filteredIDs := filterChannelsByRequestPathAndModel(ids, tt.path, "customer-decision")
			channelSyncLock.RUnlock()
			assert.Equal(t, tt.want, filteredIDs)
			assert.Equal(t, []int{901, 902, 903, 904, 999}, ids, "filtering must not mutate cache candidates")
		})
	}
}

func TestSystemOneDatabaseSelectorFailsClosedOnChannelLookupError(t *testing.T) {
	resetPricingEndpointTestTables(t)
	abilities := []Ability{{ChannelId: 901}}
	callbackName := "test:systemone_channel_lookup_failure"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callbackName, func(db *gorm.DB) {
		if db.Statement.Table == "channels" {
			db.AddError(errors.New("channel lookup unavailable"))
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove(callbackName) })
	assert.Empty(t, filterAbilitiesByRequestPathAndModel(abilities, "/typesafe/v1/systemone", "jev-latest"))
	assert.Equal(t, abilities, filterAbilitiesByRequestPathAndModel(abilities, "/v1/chat/completions", "gpt-4o"))
}

func TestSystemOneChannelSelectionFiltersBeforePriority(t *testing.T) {
	resetPricingEndpointTestTables(t)
	for _, tt := range []struct {
		id, channelType int
		priority        int64
	}{
		{id: 911, channelType: constant.ChannelTypeOpenAI, priority: 100},
		{id: 912, channelType: constant.ChannelTypeTypeSafe, priority: 50},
		{id: 913, channelType: constant.ChannelTypeNewAPI, priority: 10},
	} {
		insertPricingEndpointChannel(t, tt.id, tt.channelType, dto.ChannelOtherSettings{})
		require.NoError(t, DB.Model(&Channel{}).Where("id = ?", tt.id).Updates(map[string]any{"priority": tt.priority, "group": "default", "models": "customer-decision"}).Error)
		require.NoError(t, DB.Create(&Ability{Group: "default", Model: "customer-decision", ChannelId: tt.id, Priority: &tt.priority, Enabled: true}).Error)
	}
	InitChannelCache()
	for _, memoryCache := range []bool{false, true} {
		common.MemoryCacheEnabled = memoryCache
		for retry, wantID := range []int{912, 913, 913} {
			channel, err := GetRandomSatisfiedChannel("default", "customer-decision", retry, "/typesafe/v1/systemone")
			require.NoError(t, err)
			require.NotNil(t, channel)
			assert.Equal(t, wantID, channel.Id)
		}
		channel, err := GetRandomSatisfiedChannelExcluding("default", "customer-decision", 0, "/typesafe/v1/systemone", map[int]struct{}{912: {}})
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 913, channel.Id)
	}
}

func TestTypeSafePriorityDoesNotHideChatChannels(t *testing.T) {
	resetPricingEndpointTestTables(t)
	for _, tt := range []struct {
		id, channelType int
		priority        int64
	}{
		{id: 921, channelType: constant.ChannelTypeTypeSafe, priority: 100},
		{id: 922, channelType: constant.ChannelTypeOpenAI, priority: 50},
	} {
		insertPricingEndpointChannel(t, tt.id, tt.channelType, dto.ChannelOtherSettings{})
		require.NoError(t, DB.Model(&Channel{}).Where("id = ?", tt.id).Updates(map[string]any{"priority": tt.priority, "group": "default", "models": "customer-model"}).Error)
		require.NoError(t, DB.Create(&Ability{Group: "default", Model: "customer-model", ChannelId: tt.id, Priority: &tt.priority, Enabled: true}).Error)
	}
	InitChannelCache()
	for _, memoryCache := range []bool{false, true} {
		common.MemoryCacheEnabled = memoryCache
		channel, err := GetRandomSatisfiedChannel("default", "customer-model", 0, "/v1/chat/completions")
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 922, channel.Id)
	}
}
