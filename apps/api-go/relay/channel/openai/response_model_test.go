package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponseModelOpenAIHandlersObserveProviderBeforeConversion(t *testing.T) {
	previous := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previous })
	chat := `{"id":"chatcmpl_1","object":"chat.completion","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`
	responses := `{"id":"resp_1","object":"response","created_at":1710000000,"model":"gpt-4o-mini","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":20,"output_tokens":4,"total_tokens":24}}`
	chatSSE := "data: " + `{"id":"chatcmpl_1","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}` + "\n\n" +
		"data: " + `{"id":"chatcmpl_1","model":"gpt-4o","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}` + "\n\ndata: [DONE]\n\n"
	responseSSE := "data: " + `{"type":"response.created","response":{"id":"resp_1","created_at":1710000000,"model":"gpt-4o-mini"}}` + "\n\n" +
		"data: " + `{"type":"response.output_text.delta","delta":"hello"}` + "\n\n" +
		"data: " + `{"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"gpt-4o","usage":{"input_tokens":20,"output_tokens":4,"total_tokens":24}}}` + "\n\ndata: [DONE]\n\n"
	type handler func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *types.NewAPIError)
	for _, tt := range []struct {
		name, body, contentType string
		handle                  handler
		format                  types.RelayFormat
		stream, raw             bool
	}{
		{"chat JSON", chat, "application/json", OpenaiHandler, types.RelayFormatOpenAI, false, true},
		{"chat to Responses JSON", chat, "application/json", OaiChatToResponsesHandler, types.RelayFormatOpenAIResponses, false, false},
		{"Responses JSON", responses, "application/json", OaiResponsesHandler, types.RelayFormatOpenAIResponses, false, true},
		{"Responses to chat JSON", responses, "application/json", OaiResponsesToChatHandler, types.RelayFormatOpenAI, false, false},
		{"chat SSE sticky", chatSSE, "text/event-stream", OaiStreamHandler, types.RelayFormatOpenAI, true, false},
		{"chat to Responses SSE sticky", chatSSE, "text/event-stream", OaiChatToResponsesStreamHandler, types.RelayFormatOpenAIResponses, true, false},
		{"Responses SSE sticky", responseSSE, "text/event-stream", OaiResponsesStreamHandler, types.RelayFormatOpenAIResponses, true, false},
		{"Responses to chat SSE sticky", responseSSE, "text/event-stream", OaiResponsesToChatStreamHandler, types.RelayFormatOpenAI, true, false},
		{"Responses buffered SSE sticky", responseSSE, "text/event-stream", OaiResponsesToChatBufferedStreamHandler, types.RelayFormatOpenAI, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{OriginModelName: "client-model", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}, RelayFormat: tt.format, RelayMode: relayconstant.RelayModeChatCompletions, IsStream: tt.stream, ShouldIncludeUsage: true, DisablePing: true}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tt.contentType}}, Body: io.NopCloser(strings.NewReader(tt.body))}
			usage, apiErr := tt.handle(c, info, resp)
			require.Nil(t, apiErr)
			require.Equal(t, 20, usage.PromptTokens)
			require.Equal(t, 4, usage.CompletionTokens)
			require.Equal(t, 24, usage.TotalTokens)
			require.Equal(t, "gpt-4o", info.UpstreamModelName)
			require.Equal(t, &relaycommon.ResponseModel{RequestedModel: "client-model", UpstreamModel: "gpt-4o", ReturnedModel: "gpt-4o-mini"}, info.ResponseModel)
			require.True(t, info.ResponseModel.Mismatch())
			require.Contains(t, writer.Body.String(), "hello")
			require.NotContains(t, writer.Body.String(), "response_model")
			if tt.raw {
				require.Equal(t, tt.body, writer.Body.String())
			}
		})
	}
}

func TestResponseModelCompletionStreamReadsModelWithoutChangingDTO(t *testing.T) {
	info := &relaycommon.RelayInfo{OriginModelName: "gpt-4", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4"}, RelayMode: relayconstant.RelayModeCompletions}
	var text strings.Builder
	tools := 0
	require.NoError(t, processTokenData(info, `{"model":"gpt-4o","choices":[{"text":"hello"}]}`, &text, &tools))
	require.Equal(t, "hello", text.String())
	require.True(t, info.ResponseModel.Mismatch())
}
