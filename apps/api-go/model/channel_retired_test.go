package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/stretchr/testify/require"
)

func TestRetiredOpenHumanExcludedBeforePriorityInBothSelectors(t *testing.T) {
	preserveChannelTestState(t)
	DB = openCacheTestDB(t, &Channel{}, &Ability{})
	initCol()
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, item := range []struct {
		id, channelType int
		priority        int64
		models          string
	}{
		{6101, constant.ChannelTypeOpenHuman, 100, "shared-model,retired-only"},
		{6102, constant.ChannelTypeOpenAI, 10, "shared-model"},
		{6103, constant.ChannelTypeTypeSafe, 20, "shared-model"},
	} {
		channel := Channel{Id: item.id, Type: item.channelType, Name: "fixture", Key: "fixture-key", Group: "human", Models: item.models, Priority: &item.priority, Status: common.ChannelStatusEnabled}
		require.NoError(t, DB.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(nil))
	}
	common.MemoryCacheEnabled = true
	require.NoError(t, InitChannelCache())
	for _, useCache := range []bool{false, true} {
		common.MemoryCacheEnabled = useCache
		for _, item := range []struct {
			path     string
			expected int
		}{
			{"/v1/chat/completions", 6102},
			{"/v1/systemone", 6103},
			{"", 6103},
		} {
			channel, err := GetRandomSatisfiedChannel("human", "shared-model", 0, item.path)
			require.NoError(t, err)
			require.NotNil(t, channel)
			require.Equal(t, item.expected, channel.Id, "cache=%v path=%s", useCache, item.path)
		}
	}
	models, err := GetGroupEnabledModelsWithError("human")
	require.NoError(t, err)
	require.Equal(t, []string{"shared-model"}, models)
	abilities, err := GetAllEnableAbilityWithChannels()
	require.NoError(t, err)
	for _, ability := range abilities {
		require.NotEqual(t, constant.ChannelTypeOpenHuman, ability.ChannelType)
	}
	var historical Channel
	require.NoError(t, DB.First(&historical, 6101).Error)
	require.Equal(t, 61, historical.Type)
	require.Equal(t, "human", historical.Group)
	require.Equal(t, "fixture-key", historical.Key)
}
