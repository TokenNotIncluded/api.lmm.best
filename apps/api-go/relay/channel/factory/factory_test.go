package factory_test

import (
	"reflect"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/factory"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestAdaptorEndpointCapabilities(t *testing.T) {
	tests := []struct {
		name           string
		apiType        int
		apiKey         string
		claude, rerank bool
	}{
		{"ali", constant.APITypeAli, "key", true, true},
		{"anthropic", constant.APITypeAnthropic, "key", true, false},
		{"baidu", constant.APITypeBaidu, "key", false, false},
		{"gemini", constant.APITypeGemini, "key", true, false},
		{"openai", constant.APITypeOpenAI, "key", true, true},
		{"palm", constant.APITypePaLM, "key", false, false},
		{"tencent-native", constant.APITypeTencent, "id|secret", false, false},
		{"tencent-tokenhub", constant.APITypeTencent, "token", true, true},
		{"xunfei", constant.APITypeXunfei, "key", false, false},
		{"zhipu", constant.APITypeZhipu, "key", false, false},
		{"zhipu-v4", constant.APITypeZhipuV4, "key", true, false},
		{"ollama", constant.APITypeOllama, "key", true, false},
		{"perplexity", constant.APITypePerplexity, "key", true, false},
		{"aws", constant.APITypeAws, "key", true, false},
		{"cohere", constant.APITypeCohere, "key", false, true},
		{"dify", constant.APITypeDify, "key", false, false},
		{"jina", constant.APITypeJina, "key", false, true},
		{"cloudflare", constant.APITypeCloudflare, "key", false, true},
		{"siliconflow", constant.APITypeSiliconFlow, "key", true, true},
		{"vertex", constant.APITypeVertexAi, "key", true, false},
		{"mistral", constant.APITypeMistral, "key", false, false},
		{"deepseek", constant.APITypeDeepSeek, "key", true, false},
		{"mokaai", constant.APITypeMokaAI, "key", false, false},
		{"volcengine", constant.APITypeVolcEngine, "key", true, false},
		{"baidu-v2", constant.APITypeBaiduV2, "key", true, false},
		{"openrouter", constant.APITypeOpenRouter, "key", true, true},
		{"xinference", constant.APITypeXinference, "key", true, true},
		{"xai", constant.APITypeXai, "key", false, false},
		{"coze", constant.APITypeCoze, "key", false, false},
		{"jimeng", constant.APITypeJimeng, "key", false, false},
		{"moonshot", constant.APITypeMoonshot, "key", true, true},
		{"submodel", constant.APITypeSubmodel, "key", false, false},
		{"minimax", constant.APITypeMiniMax, "key", true, false},
		{"replicate", constant.APITypeReplicate, "key", false, false},
		{"codex", constant.APITypeCodex, "key", false, false},
		{"advanced-custom", constant.APITypeAdvancedCustom, "key", true, true},
		{"sub2api", constant.APITypeSub2API, "key", true, false},
		{"newapi", constant.APITypeNewAPI, "key", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.claude, factory.SupportsEndpoint(tt.apiType, tt.apiKey, channel.EndpointClaudeMessages))
			require.Equal(t, tt.rerank, factory.SupportsEndpoint(tt.apiType, tt.apiKey, channel.EndpointRerank))

			adaptor := factory.GetAdaptor(tt.apiType)
			require.NotNil(t, adaptor)
			if tt.apiType == constant.APITypeTencent {
				adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: tt.apiKey}})
			}
			if !tt.claude {
				value, err := adaptor.ConvertClaudeRequest(nil, nil, nil)
				require.Nil(t, value)
				require.Error(t, err)
				require.True(t, channel.IsUnsupportedEndpointError(err), "unexpected error: %v", err)
			}
			if !tt.rerank {
				value, err := adaptor.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
				require.Nil(t, value)
				require.Error(t, err)
				require.True(t, channel.IsUnsupportedEndpointError(err), "unexpected error: %v", err)
			}
		})
	}
}

func TestProvenRerankPassThroughAdaptorsPreserveRequest(t *testing.T) {
	request := dto.RerankRequest{Model: "rerank-model"}
	for _, apiType := range []int{
		constant.APITypeOpenAI,
		constant.APITypeCloudflare,
		constant.APITypeJina,
		constant.APITypeSiliconFlow,
		constant.APITypeMoonshot,
	} {
		adaptor := factory.GetAdaptor(apiType)
		converted, err := adaptor.ConvertRerankRequest(nil, 0, request)
		require.NoError(t, err)
		require.True(t, reflect.DeepEqual(request, converted), "api type %d changed pass-through request: %#v", apiType, converted)
	}
}

func TestXunfeiCapabilityOnlyRejectsUnimplementedEndpoints(t *testing.T) {
	adaptor := factory.GetAdaptor(constant.APITypeXunfei)
	require.False(t, channel.SupportsEndpoint(adaptor, channel.EndpointClaudeMessages))
	require.False(t, channel.SupportsEndpoint(adaptor, channel.EndpointRerank))
	require.True(t, channel.SupportsEndpoint(adaptor, channel.Endpoint("openai_websocket")))
}
