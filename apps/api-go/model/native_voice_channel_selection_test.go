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

var nativeVoiceSelectionPaths = []string{"/v1/live/sessions", "/v1/realtime/transcription_sessions", "/v1/realtime/translations"}

func TestNativeVoiceSelectorsRestrictChannelsAndMissingCandidates(t *testing.T) {
	resetPricingEndpointTestTables(t)
	for id, channelType := range map[int]int{
		1101: constant.ChannelTypeOpenAI,
		1102: constant.ChannelTypeNewAPI,
		1103: constant.ChannelTypeCodex,
		1104: constant.ChannelTypeOpenHuman,
		1105: constant.ChannelTypeAdvancedCustom,
	} {
		settings := dto.ChannelOtherSettings{}
		if channelType == constant.ChannelTypeAdvancedCustom {
			routes := make([]dto.AdvancedCustomRoute, 0, len(nativeVoiceSelectionPaths))
			for _, path := range nativeVoiceSelectionPaths {
				routes = append(routes, dto.AdvancedCustomRoute{IncomingPath: path, UpstreamPath: path})
			}
			settings = pricingEndpointAdvancedCustomConfig(routes...)
		}
		insertPricingEndpointChannel(t, id, channelType, settings)
	}
	InitChannelCache()
	ids := []int{1101, 1102, 1103, 1104, 1105, 9999}
	abilities := make([]Ability, 0, len(ids))
	for _, id := range ids {
		abilities = append(abilities, Ability{ChannelId: id, Model: "voice-alias"})
	}
	for _, path := range nativeVoiceSelectionPaths {
		filteredAbilities := filterAbilitiesByRequestPathAndModel(abilities, path, "voice-alias")
		actual := make([]int, 0, len(filteredAbilities))
		for _, ability := range filteredAbilities {
			actual = append(actual, ability.ChannelId)
		}
		assert.Equal(t, []int{1101, 1102}, actual)
		channelSyncLock.RLock()
		filteredIDs := filterChannelsByRequestPathAndModel(ids, path, "voice-alias")
		channelSyncLock.RUnlock()
		assert.Equal(t, []int{1101, 1102}, filteredIDs)
		assert.Equal(t, []int{1101, 1102, 1103, 1104, 1105, 9999}, ids)
	}
}

func TestNativeVoiceSelectionFiltersBeforePriorityAndRetry(t *testing.T) {
	resetPricingEndpointTestTables(t)
	for _, item := range []struct {
		id, channelType int
		priority        int64
	}{
		{1201, constant.ChannelTypeOpenAI, 50},
		{1202, constant.ChannelTypeNewAPI, 10},
		{1203, constant.ChannelTypeCodex, 100},
		{1204, constant.ChannelTypeTypeSafe, 200},
	} {
		insertPricingEndpointChannel(t, item.id, item.channelType, dto.ChannelOtherSettings{})
		require.NoError(t, DB.Model(&Channel{}).Where("id = ?", item.id).Updates(map[string]any{
			"priority": item.priority, "group": "default", "models": "voice-alias",
		}).Error)
		require.NoError(t, DB.Create(&Ability{Group: "default", Model: "voice-alias", ChannelId: item.id, Priority: &item.priority, Enabled: true}).Error)
	}
	InitChannelCache()
	for _, memoryCache := range []bool{false, true} {
		common.MemoryCacheEnabled = memoryCache
		for _, path := range nativeVoiceSelectionPaths {
			for retry, wantID := range []int{1201, 1202, 1202} {
				selected, err := GetRandomSatisfiedChannel("default", "voice-alias", retry, path)
				require.NoError(t, err)
				require.NotNil(t, selected)
				assert.Equal(t, wantID, selected.Id)
			}
			selected, err := GetRandomSatisfiedChannelExcluding("default", "voice-alias", 0, path, map[int]struct{}{1201: {}})
			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, 1202, selected.Id)
			selected, err = GetRandomSatisfiedChannelExcluding("default", "voice-alias", 0, path, map[int]struct{}{1201: {}, 1202: {}})
			require.NoError(t, err)
			assert.Nil(t, selected)
		}
	}
}

func TestNativeVoiceSelectorFailsClosedOnChannelLookupError(t *testing.T) {
	resetPricingEndpointTestTables(t)
	callbackName := "test:native_voice_channel_lookup_failure"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callbackName, func(db *gorm.DB) {
		if db.Statement.Table == "channels" {
			db.AddError(errors.New("channel lookup unavailable"))
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove(callbackName) })
	for _, path := range nativeVoiceSelectionPaths {
		assert.Empty(t, filterAbilitiesByRequestPathAndModel([]Ability{{ChannelId: 1201}}, path, "voice-alias"))
	}
}
