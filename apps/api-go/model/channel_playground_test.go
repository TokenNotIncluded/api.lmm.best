package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestPlaygroundSelectsAdvancedCustomChannelsWithAndWithoutCache(t *testing.T) {
	preserveChannelTestState(t)
	DB = openCacheTestDB(t, &Channel{}, &Ability{})
	db, err := DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	channel := &Channel{
		Id: 1, Type: constant.ChannelTypeAdvancedCustom, Status: common.ChannelStatusEnabled,
		Key: "test-key", Models: "image-model", Group: "image-2",
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{IncomingPath: "/v1/images/generations", UpstreamPath: "/images/generations", Models: []string{"image-model"}},
			{IncomingPath: "/v1/images/edits", UpstreamPath: "/images/edits", Models: []string{"image-model"}},
		},
	}})
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(DB))
	require.NoError(t, refreshChannelCache())

	for _, cached := range []bool{false, true} {
		common.MemoryCacheEnabled = cached
		for _, path := range []string{"/pg/images/generations", "/pg/images/edits"} {
			selected, err := GetRandomSatisfiedChannel("image-2", "image-model", 0, path)
			require.NoError(t, err)
			require.NotNil(t, selected, "cache=%t path=%s: valid channel was filtered out", cached, path)
			require.Equal(t, channel.Id, selected.Id)
		}
		selected, err := GetRandomSatisfiedChannel("image-2", "image-model", 0, "/pg/chat/completions")
		require.NoError(t, err)
		require.Nil(t, selected, "an image-only channel must not serve chat")
	}
}
