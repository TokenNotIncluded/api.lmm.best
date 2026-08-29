package common

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/stretchr/testify/assert"
)

func TestResponsesCapableChannelsAdvertiseEndpoint(t *testing.T) {
	want := []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
	}

	assert.Equal(t, want, GetEndpointTypesByChannelType(constant.ChannelTypeOllama, "llama3.2"))
	assert.Equal(t, want, GetEndpointTypesByChannelType(constant.ChannelTypeZhipu_v4, "glm-4.5"))
}
