package typesafe

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relay/channel"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func systemOneTestInfo(baseURL string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayMode:      relayconstant.RelayModeSystemOne,
		RelayFormat:    types.RelayFormatSystemOne,
		RequestURLPath: "/typesafe/v1/systemone",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: baseURL,
			ApiKey:         "upstream-secret",
		},
	}
}

func TestTypeSafeSystemOneRequestURL(t *testing.T) {
	adaptor := &Adaptor{}
	tests := []struct {
		name string
		base string
		want string
	}{
		{"default vendor", "", "https://api.typesafe.ai/v1/systemone"},
		{"vendor origin", "https://api.typesafe.ai", "https://api.typesafe.ai/v1/systemone"},
		{"vendor trailing slash", "https://api.typesafe.ai///", "https://api.typesafe.ai/v1/systemone"},
		{"optional version", "https://api.typesafe.ai/v1", "https://api.typesafe.ai/v1/systemone"},
		{"version trailing slash", "https://api.typesafe.ai/v1/", "https://api.typesafe.ai/v1/systemone"},
		{"gateway prefix", "https://gateway.example/typesafe", "https://gateway.example/typesafe/v1/systemone"},
		{"gateway prefix and version", "https://gateway.example/typesafe/v1/", "https://gateway.example/typesafe/v1/systemone"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			url, err := adaptor.GetRequestURL(systemOneTestInfo(test.base))
			require.NoError(t, err)
			assert.Equal(t, test.want, url)
		})
	}
	for _, mode := range []int{relayconstant.RelayModeChatCompletions, relayconstant.RelayModeResponses, relayconstant.RelayModeEmbeddings} {
		info := systemOneTestInfo("https://api.typesafe.ai")
		info.RelayMode = mode
		_, err := adaptor.GetRequestURL(info)
		require.Error(t, err, "unsupported relay mode %d must fail before upstream work", mode)
	}
}

func TestTypeSafeSystemOneHeaderUsesChannelCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/typesafe/v1/systemone", nil)
	c.Request.Header.Set("Authorization", "Bearer client-gateway-key")
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("Content-Type", "application/json")
	info := systemOneTestInfo("https://api.typesafe.ai")
	headers := make(http.Header)
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &headers, info))
	assert.Equal(t, "Bearer upstream-secret", headers.Get("Authorization"))
	assert.Equal(t, "application/json", headers.Get("Content-Type"))
	assert.Equal(t, "application/json", headers.Get("Accept"))
}

func TestTypeSafeOnlySupportsSystemOne(t *testing.T) {
	adaptor := &Adaptor{}
	assert.True(t, channel.SupportsEndpoint(adaptor, channel.EndpointSystemOne))
	for _, endpoint := range []channel.Endpoint{channel.EndpointClaudeMessages, channel.EndpointRerank, "chat_completions", "responses", "unknown"} {
		assert.False(t, channel.SupportsEndpoint(adaptor, endpoint), string(endpoint))
	}
	assert.ElementsMatch(t, []string{"jev-1.13.0", "jev-latest", "jev-preview"}, adaptor.GetModelList())
}

func TestTypeSafeRejectsOtherProtocolConversions(t *testing.T) {
	adaptor := &Adaptor{}
	info := systemOneTestInfo("https://api.typesafe.ai")
	tests := []struct {
		name string
		call func() error
	}{
		{"chat", func() error {
			_, err := adaptor.ConvertOpenAIRequest(nil, info, &dto.GeneralOpenAIRequest{})
			return err
		}},
		{"responses", func() error {
			_, err := adaptor.ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{})
			return err
		}},
		{"claude", func() error { _, err := adaptor.ConvertClaudeRequest(nil, info, &dto.ClaudeRequest{}); return err }},
		{"gemini", func() error { _, err := adaptor.ConvertGeminiRequest(nil, info, &dto.GeminiChatRequest{}); return err }},
		{"embedding", func() error { _, err := adaptor.ConvertEmbeddingRequest(nil, info, dto.EmbeddingRequest{}); return err }},
		{"rerank", func() error {
			_, err := adaptor.ConvertRerankRequest(nil, relayconstant.RelayModeRerank, dto.RerankRequest{})
			return err
		}},
		{"audio", func() error { _, err := adaptor.ConvertAudioRequest(nil, info, dto.AudioRequest{}); return err }},
		{"image", func() error { _, err := adaptor.ConvertImageRequest(nil, info, dto.ImageRequest{}); return err }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			require.Error(t, err)
			assert.True(t, channel.IsUnsupportedEndpointError(err), "unsupported protocols must not become generic retryable conversion errors")
		})
	}
}
