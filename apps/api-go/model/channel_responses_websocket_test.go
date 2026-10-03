package model

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestResponsesWebSocketChannelSettingCreateUpdateReload(t *testing.T) {
	for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeCodex, constant.ChannelTypeOpenHuman, constant.ChannelTypeAdvancedCustom, constant.ChannelTypeSub2API, constant.ChannelTypeNewAPI} {
		t.Run(fmt.Sprint(channelType), func(t *testing.T) {
			preserveChannelTestState(t)
			DB = openCacheTestDB(t, &Channel{}, &Ability{})
			sqlDB, err := DB.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			channel := &Channel{Type: channelType, Name: "ws-setting", Key: "key", Models: "ws-model", Group: "default", Status: common.ChannelStatusEnabled}
			if channelType == constant.ChannelTypeAdvancedCustom {
				channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{IncomingPath: "/v1/responses", UpstreamPath: "/v1/responses"}}}})
			}
			channel.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: common.GetPointer(true), HTTP2ConnectionShards: 3})
			require.NoError(t, channel.ValidateSettings())
			require.NoError(t, channel.Insert())
			loaded, err := GetChannelById(channel.Id, true)
			require.NoError(t, err)
			require.NotNil(t, loaded.GetSetting().ResponsesWebSocketEnabled)
			require.True(t, *loaded.GetSetting().ResponsesWebSocketEnabled)
			settings := loaded.GetSetting()
			settings.ResponsesWebSocketEnabled = common.GetPointer(false)
			loaded.SetSetting(settings)
			require.Contains(t, *loaded.Setting, `"responses_websocket_enabled":false`)
			require.NoError(t, loaded.Update())
			loaded, err = GetChannelById(channel.Id, true)
			require.NoError(t, err)
			require.NotNil(t, loaded.GetSetting().ResponsesWebSocketEnabled)
			require.False(t, *loaded.GetSetting().ResponsesWebSocketEnabled)
			require.Equal(t, 3, loaded.GetSetting().HTTP2ConnectionShards)
			settings = loaded.GetSetting()
			settings.ResponsesWebSocketEnabled = nil
			loaded.SetSetting(settings)
			require.NoError(t, loaded.Update())
			loaded, err = GetChannelById(channel.Id, true)
			require.NoError(t, err)
			require.Nil(t, loaded.GetSetting().ResponsesWebSocketEnabled, "legacy unset remains distinct from explicit false")
		})
	}
}
