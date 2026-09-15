package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
)

func TestBatchInsertChannelsPublishesNewEnabledChannelToRoutingCache(t *testing.T) {
	preserveChannelTestState(t)
	DB = openCacheTestDB(t, &Channel{}, &Ability{})
	common.MemoryCacheEnabled = true

	if err := InitChannelCache(); err != nil {
		t.Fatalf("initialize empty channel cache: %v", err)
	}

	weight := uint(1)
	priority := int64(0)
	baseURL := "http://127.0.0.1:18080/ok"
	const modelName = "channel-create-cache-model"
	channels := []Channel{{
		Type:      constant.ChannelTypeOpenAI,
		Key:       "release-ci-upstream-key",
		Status:    common.ChannelStatusEnabled,
		Name:      "newly-created-enabled-channel",
		Weight:    &weight,
		Priority:  &priority,
		BaseURL:   &baseURL,
		Models:    modelName,
		Group:     "default",
		AutoBan:   common.GetPointer(0),
	}}

	if err := BatchInsertChannels(channels); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	selected, err := GetRandomSatisfiedChannel("default", modelName, 0, "/v1/chat/completions")
	if err != nil {
		t.Fatalf("select newly created channel: %v", err)
	}
	if selected == nil {
		t.Fatal("newly created enabled channel was not immediately routable")
	}
	if selected.Name != "newly-created-enabled-channel" {
		t.Fatalf("selected unexpected channel %q", selected.Name)
	}
}
