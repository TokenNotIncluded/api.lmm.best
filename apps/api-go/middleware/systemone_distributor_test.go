package middleware

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/assert"
)

func TestSystemOneChannelCapabilitiesDoNotPermitOtherProtocols(t *testing.T) {
	native := &model.Channel{Type: constant.ChannelTypeTypeSafe, Key: "typesafe-fixture-key"}
	for _, endpoint := range []string{"/v1/systemone", "/typesafe/v1/systemone"} {
		assert.True(t, channelSupportsRequestPath(native, endpoint, "jev-latest"), endpoint)
	}
	for _, endpoint := range []string{
		"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/embeddings", "/v1/rerank",
		"/v1/audio/speech", "/v1/audio/transcriptions", "/v1/images/generations", "/v1/images/edits",
		"/v1/alpha/search", "/v1/videos", "/suno/submit/music", "/mj/submit/imagine", "/future/provider/task",
		"/v1/systemone/extra", "/typesafe/v1/systemone/extra",
	} {
		assert.False(t, channelSupportsRequestPath(native, endpoint, "jev-latest"), endpoint)
	}
}

func TestSystemOneRoutesRequireExplicitProtocolCapability(t *testing.T) {
	for _, endpoint := range []string{"/v1/systemone", "/typesafe/v1/systemone"} {
		assert.True(t, channelSupportsRequestPath(&model.Channel{Type: constant.ChannelTypeNewAPI}, endpoint, "jev-latest"), endpoint)
		for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeAnthropic, constant.ChannelTypeBaidu, constant.ChannelTypeGemini} {
			assert.False(t, channelSupportsRequestPath(&model.Channel{Type: channelType}, endpoint, "jev-latest"), "channel %d at %s", channelType, endpoint)
		}
	}
}
