package xai

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
	require.Equal(t, types.RelayFormatClaude, info.FinalRequestRelayFormat)
}
