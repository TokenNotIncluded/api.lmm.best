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

func TestSystemOneEndpointMetadata(t *testing.T) {
	for _, modelName := range []string{"jev-1.13.0", "jev-latest", "jev-preview"} {
		assert.Equal(t, []constant.EndpointType{constant.EndpointTypeSystemOne}, GetEndpointTypesByChannelType(constant.ChannelTypeTypeSafe, modelName))
		assert.Equal(t, []constant.EndpointType{constant.EndpointTypeSystemOne}, GetEndpointTypesByChannelType(constant.ChannelTypeNewAPI, modelName))
	}
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeSystemOne}, GetEndpointTypesByChannelType(constant.ChannelTypeTypeSafe, "customer-alias"))
	assert.NotContains(t, GetEndpointTypesByChannelType(constant.ChannelTypeNewAPI, "gpt-4o"), constant.EndpointTypeSystemOne)
	assert.NotContains(t, GetEndpointTypesByChannelType(constant.ChannelTypeSub2API, "jev-latest"), constant.EndpointTypeSystemOne)
	info, ok := GetDefaultEndpointInfo(constant.EndpointTypeSystemOne)
	assert.True(t, ok)
	assert.Equal(t, EndpointInfo{Path: "/typesafe/v1/systemone", Method: "POST"}, info)
	apiType, ok := ChannelType2APIType(constant.ChannelTypeTypeSafe)
	assert.True(t, ok)
	assert.Equal(t, constant.APITypeTypeSafe, apiType)
}

func TestModerationEndpointMetadata(t *testing.T) {
	for _, modelName := range []string{"omni-moderation-latest", "omni-moderation-2024-09-26", "text-moderation-latest", "text-moderation-stable"} {
		for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeOpenHuman, constant.ChannelTypeAzure, constant.ChannelTypeNewAPI, constant.ChannelTypeSub2API} {
			assert.Equal(t, []constant.EndpointType{constant.EndpointTypeModeration}, GetEndpointTypesByChannelType(channelType, modelName))
		}
	}
	assert.True(t, IsModerationModel(" OMNI-MODERATION-LATEST "))
	assert.False(t, IsModerationModel("chat-omni-moderation"))
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeSystemOne}, GetEndpointTypesByChannelType(constant.ChannelTypeTypeSafe, "omni-moderation-latest"))
	assert.NotContains(t, GetEndpointTypesByChannelType(constant.ChannelTypeAnthropic, "omni-moderation-latest"), constant.EndpointTypeModeration)
	assert.NotContains(t, GetEndpointTypesByChannelType(constant.ChannelTypeNewAPI, "gpt-4o"), constant.EndpointTypeModeration)
	info, ok := GetDefaultEndpointInfo(constant.EndpointTypeModeration)
	assert.True(t, ok)
	assert.Equal(t, EndpointInfo{Path: "/v1/moderations", Method: "POST"}, info)
}

func TestNativeVoiceEndpointMetadata(t *testing.T) {
	for _, tt := range []struct {
		modelName string
		endpoint  constant.EndpointType
		path      string
	}{
		{"gpt-live-1", constant.EndpointTypeLive, "/v1/live/sessions"},
		{"gpt-live-transcribe", constant.EndpointTypeRealtimeTranscription, "/v1/realtime?intent=transcription"},
		{"gpt-realtime-whisper", constant.EndpointTypeRealtimeTranscription, "/v1/realtime?intent=transcription"},
		{"gpt-realtime-translate", constant.EndpointTypeRealtimeTranslation, "/v1/realtime/translations"},
	} {
		endpoint, nativeVoice := NativeVoiceEndpointType(tt.modelName)
		assert.True(t, nativeVoice)
		assert.Equal(t, tt.endpoint, endpoint)
		for _, channelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeNewAPI} {
			assert.Equal(t, []constant.EndpointType{tt.endpoint}, GetEndpointTypesByChannelType(channelType, tt.modelName))
		}
		for _, channelType := range []int{constant.ChannelTypeOpenHuman, constant.ChannelTypeCodex, constant.ChannelTypeAnthropic, constant.ChannelTypeSub2API} {
			assert.Empty(t, GetEndpointTypesByChannelType(channelType, tt.modelName))
		}
		info, ok := GetDefaultEndpointInfo(tt.endpoint)
		assert.True(t, ok)
		assert.Equal(t, EndpointInfo{Path: tt.path, Method: "GET"}, info)
	}
	_, nativeVoice := NativeVoiceEndpointType("gpt-realtime")
	assert.False(t, nativeVoice)
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAI}, GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, "gpt-realtime"))
	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeSystemOne}, GetEndpointTypesByChannelType(constant.ChannelTypeTypeSafe, "gpt-live-1"))
}
