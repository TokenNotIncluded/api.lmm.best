package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestOllamaOpenAIChatSettingCreateUpdateReload(t *testing.T) {
	preserveChannelTestState(t)
	DB = openCacheTestDB(t, &Channel{}, &Ability{})
	db, err := DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	channel := &Channel{Type: constant.ChannelTypeOllama, Name: "ollama", Key: "test-key", Models: "llama3.2", Group: "default", Status: common.ChannelStatusEnabled}
	channel.SetOtherSettings(dto.ChannelOtherSettings{OllamaOpenAIChat: true, UpstreamModelUpdateCheckEnabled: true})
	require.NoError(t, channel.ValidateSettings())
	require.NoError(t, channel.Insert())
	loaded, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.True(t, loaded.GetOtherSettings().OllamaOpenAIChat)
	settings := loaded.GetOtherSettings()
	settings.OllamaOpenAIChat = false
	loaded.SetOtherSettings(settings)
	require.NoError(t, loaded.Update())
	loaded, err = GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.False(t, loaded.GetOtherSettings().OllamaOpenAIChat)
	require.True(t, loaded.GetOtherSettings().UpstreamModelUpdateCheckEnabled)

	legacy := &Channel{Type: constant.ChannelTypeOllama, OtherSettings: `{}`}
	require.NoError(t, legacy.ValidateSettings())
	require.False(t, legacy.GetOtherSettings().OllamaOpenAIChat)
}
