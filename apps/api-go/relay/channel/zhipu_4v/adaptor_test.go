package zhipu_4v

import (
	"encoding/json"
	"testing"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRequestURLUsesGLMResponsesEndpoint(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://glm.example.com",
		},
		RelayMode: relayconstant.RelayModeResponses,
	}

	requestURL, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://glm.example.com/api/v1/responses", requestURL)
}

func TestConvertOpenAIResponsesRequestReturnsCompatibleRequest(t *testing.T) {
	maxOutputTokens := uint(512)
	stream := true
	request := dto.OpenAIResponsesRequest{
		Model:           "glm-4.5",
		Input:           json.RawMessage(`"hello"`),
		Instructions:    json.RawMessage(`"be concise"`),
		MaxOutputTokens: &maxOutputTokens,
		Stream:          &stream,
	}

	convertedValue, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, nil, request)
	require.NoError(t, err)
	converted, ok := convertedValue.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Equal(t, request, converted)

	encoded, err := json.Marshal(converted)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"model":"glm-4.5",
		"input":"hello",
		"instructions":"be concise",
		"max_output_tokens":512,
		"stream":true
	}`, string(encoded))
}
