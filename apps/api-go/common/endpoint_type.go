package common

import (
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
)

func IsModerationModel(modelName string) bool {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	return strings.HasPrefix(modelName, "omni-moderation") || strings.HasPrefix(modelName, "text-moderation")
}

func supportsNativeModeration(channelType int) bool {
	switch channelType {
	case constant.ChannelTypeOpenAI, constant.ChannelTypeOpenHuman, constant.ChannelTypeAzure,
		constant.ChannelTypeNewAPI, constant.ChannelTypeSub2API:
		return true
	default:
		return false
	}
}

// NativeVoiceEndpointType identifies the duration-metered session protocols.
// These models do not accept ordinary chat or a synchronous channel probe.
func NativeVoiceEndpointType(modelName string) (constant.EndpointType, bool) {
	switch modelName {
	case "gpt-live-1":
		return constant.EndpointTypeLive, true
	case "gpt-live-transcribe", "gpt-realtime-whisper":
		return constant.EndpointTypeRealtimeTranscription, true
	case "gpt-realtime-translate":
		return constant.EndpointTypeRealtimeTranslation, true
	default:
		return "", false
	}
}

// GetEndpointTypesByChannelType 获取渠道最优先端点类型。
func GetEndpointTypesByChannelType(channelType int, modelName string) []constant.EndpointType {
	if endpoint, nativeVoice := NativeVoiceEndpointType(modelName); nativeVoice && channelType != constant.ChannelTypeTypeSafe {
		if channelType == constant.ChannelTypeOpenAI || channelType == constant.ChannelTypeNewAPI {
			return []constant.EndpointType{endpoint}
		}
		return []constant.EndpointType{}
	}
	if IsModerationModel(modelName) && supportsNativeModeration(channelType) {
		return []constant.EndpointType{constant.EndpointTypeModeration}
	}
	var endpointTypes []constant.EndpointType
	switch channelType {
	case constant.ChannelTypeTypeSafe:
		// TypeSafe uses its native protocol even when the public model is an alias.
		return []constant.EndpointType{constant.EndpointTypeSystemOne}
	case constant.ChannelTypeNewAPI:
		if dto.IsSystemOneModel(modelName) {
			return []constant.EndpointType{constant.EndpointTypeSystemOne}
		}
		fallthrough
	case constant.ChannelTypeSub2API:
		endpointTypes = []constant.EndpointType{
			constant.EndpointTypeOpenAI,
			constant.EndpointTypeOpenAIResponse,
			constant.EndpointTypeOpenAIResponseCompact,
			constant.EndpointTypeAnthropic,
			constant.EndpointTypeGemini,
			constant.EndpointTypeOpenAIAlphaSearch,
		}
	case constant.ChannelTypeJina:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeJinaRerank}
	//case constant.ChannelTypeMidjourney, constant.ChannelTypeMidjourneyPlus:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeMidjourney}
	//case constant.ChannelTypeSunoAPI:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeSuno}
	//case constant.ChannelTypeKling:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeKling}
	//case constant.ChannelTypeJimeng:
	//	endpointTypes = []constant.EndpointType{constant.EndpointTypeJimeng}
	case constant.ChannelTypeAws:
		fallthrough
	case constant.ChannelTypeAnthropic:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI}
	case constant.ChannelTypeVertexAi:
		fallthrough
	case constant.ChannelTypeGemini:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeGemini, constant.EndpointTypeOpenAI}
	case constant.ChannelTypeOpenRouter: // OpenRouter 只支持 OpenAI 端点
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI}
	case constant.ChannelTypeXai, constant.ChannelTypeOllama, constant.ChannelTypeZhipu_v4:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse}
	case constant.ChannelTypeSora:
		endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIVideo}
	case constant.ChannelTypeCodex:
		endpointTypes = []constant.EndpointType{
			constant.EndpointTypeOpenAIResponse,
			constant.EndpointTypeOpenAIResponseCompact,
			constant.EndpointTypeOpenAIAlphaSearch,
		}
	default:
		if IsOpenAIResponseOnlyModel(modelName) {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIResponse}
		} else {
			endpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAI}
		}
	}
	if IsImageGenerationModel(modelName) {
		// add to first
		endpointTypes = append([]constant.EndpointType{constant.EndpointTypeImageGeneration}, endpointTypes...)
	}
	return endpointTypes
}
