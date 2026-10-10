package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponseModelGeminiUsesRawModelVersionNotSynthesizedModel(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	const body = `{"modelVersion":"gemini-2.5-flash","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":4,"totalTokenCount":24}}`
	type handler func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *types.NewAPIError)
	for _, tt := range []struct {
		name        string
		handle      handler
		format      types.RelayFormat
		stream, raw bool
	}{
		{"native JSON", GeminiTextGenerationHandler, types.RelayFormatGemini, false, true},
		{"chat JSON", GeminiChatHandler, types.RelayFormatOpenAI, false, false},
		{"Responses JSON", GeminiResponsesHandler, types.RelayFormatOpenAIResponses, false, false},
		{"native SSE", GeminiTextGenerationStreamHandler, types.RelayFormatGemini, true, true},
		{"chat SSE", GeminiChatStreamHandler, types.RelayFormatOpenAI, true, false},
		{"Responses SSE", GeminiResponsesStreamHandler, types.RelayFormatOpenAIResponses, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{OriginModelName: "client-pro", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-pro"}, RelayFormat: tt.format, IsStream: tt.stream, DisablePing: true, ShouldIncludeUsage: true}
			payload := body
			if tt.stream {
				payload = "data: " + body + "\n\ndata: [DONE]\n\n"
			}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}
			usage, apiErr := tt.handle(c, info, resp)
			require.Nil(t, apiErr)
			require.Equal(t, 20, usage.PromptTokens)
			require.Equal(t, 4, usage.CompletionTokens)
			require.Equal(t, 24, usage.TotalTokens)
			require.Equal(t, "gemini-2.5-pro", info.UpstreamModelName)
			require.Equal(t, &relaycommon.ResponseModel{RequestedModel: "client-pro", UpstreamModel: "gemini-2.5-pro", ReturnedModel: "gemini-2.5-flash"}, info.ResponseModel)
			require.True(t, info.ResponseModel.Mismatch())
			got := writer.Body.String()
			require.Contains(t, got, "hello")
			require.NotContains(t, got, "response_model")
			if tt.raw {
				if tt.stream {
					require.Contains(t, got, body)
				} else {
					require.Equal(t, body, got)
				}
			} else {
				require.Contains(t, got, `"model":"gemini-2.5-pro"`)
				require.NotContains(t, got, "gemini-2.5-flash")
			}
		})
	}
}

func TestResponseModelGeminiMissingDeclarationDoesNotUseConverterModel(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{OriginModelName: "client-pro", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-pro"}, RelayFormat: types.RelayFormatOpenAI}
	body := `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":4,"totalTokenCount":24}}`
	_, apiErr := GeminiChatHandler(c, info, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))})
	require.Nil(t, apiErr)
	require.Nil(t, info.ResponseModel)
}
