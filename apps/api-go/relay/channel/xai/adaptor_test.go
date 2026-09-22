package xai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClaudeMessagesUseNativeXAIEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adaptor := &Adaptor{}
	request := &dto.ClaudeRequest{Model: "grok-4.6"}

	converted, err := adaptor.ConvertClaudeRequest(nil, nil, request)
	require.NoError(t, err)
	require.Same(t, request, converted)

	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		RequestURLPath:  "/v1/messages",
		OriginModelName: "grok-4.6",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeXai,
			ChannelBaseUrl:    "https://api.x.ai",
			ApiKey:            "xai-test-key",
			UpstreamModelName: "grok-4.6",
		},
	}

	requestURL, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	require.Equal(t, "https://api.x.ai/v1/messages", requestURL)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx.Request.Header.Set("Content-Type", "application/json")

	headers := make(http.Header)
	require.NoError(t, adaptor.SetupRequestHeader(ctx, &headers, info))
	require.Equal(t, "Bearer xai-test-key", headers.Get("Authorization"))
	require.Equal(t, "application/json", headers.Get("Content-Type"))
}

func TestClaudeMessagesResponseUsesClaudePipeline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adaptor := &Adaptor{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		OriginModelName: "grok-4.6",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeXai,
			UpstreamModelName: "grok-4.6",
		},
	}

	body := `{"id":"msg_xai","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"grok-4.6","stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":7,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":2}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	usageValue, apiErr := adaptor.DoResponse(ctx, resp, info)
	require.Nil(t, apiErr)
	usage, ok := usageValue.(*dto.Usage)
	require.True(t, ok)
	require.Equal(t, 7, usage.PromptTokens)
	require.Equal(t, 2, usage.CompletionTokens)
	require.Equal(t, 9, usage.TotalTokens)
	require.Equal(t, "anthropic", usage.UsageSemantic)
	require.JSONEq(t, body, recorder.Body.String())
	require.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.FinalRequestRelayFormat)
}

func TestClaudeMessagesSSEPreservesSharedNativeToolEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// The application initializes this global during startup; adapter tests do not.
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	body, err := os.ReadFile("testdata/native-messages.sse")
	require.NoError(t, err)
	body = append(body, '\n') // Complete the final SSE frame delimiter.
	adaptor := &Adaptor{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		OriginModelName: "grok-test",
		IsStream:        true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeXai,
			UpstreamModelName: "grok-test",
		},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
	}
	usageValue, apiErr := adaptor.DoResponse(ctx, resp, info)
	require.Nil(t, apiErr)
	usage, ok := usageValue.(*dto.Usage)
	require.True(t, ok)
	require.Equal(t, 7, usage.PromptTokens)
	require.Equal(t, 2, usage.CompletionTokens)
	require.Equal(t, "anthropic", usage.UsageSemantic)
	// The Go pipeline may enrich message_delta usage. Native tool events and
	// unknown future fields must otherwise remain the same shared wire data.
	for _, line := range strings.Split(string(body), "\n") {
		// SSE accepts CRLF input; downstream encoding normalizes line endings.
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "data: ") && !strings.Contains(line, `"type":"message_delta"`) {
			require.Contains(t, recorder.Body.String(), line)
		}
	}
	require.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.FinalRequestRelayFormat)
}
