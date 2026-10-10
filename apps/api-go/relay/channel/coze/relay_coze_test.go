package coze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	appconstant "github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCozeResponseLimitTestContext(t *testing.T, limit int) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)
	context.Set("coze_conversation_id", "conversation")
	context.Set("coze_chat_id", "chat")
	common.SetContextKey(context, appconstant.ContextKeyResponseByteLimit, limit)
	return context
}

func TestCheckIfChatCompleteAppliesResponseBudget(t *testing.T) {
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		_, _ = io.WriteString(writer, "12345")
	}))
	defer server.Close()

	context := newCozeResponseLimitTestContext(t, 4)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL}}
	err, complete := checkIfChatComplete(&Adaptor{}, context, info)

	assert.False(t, complete)
	assert.ErrorIs(t, err, common.ErrLimitExceeded)
}

func TestCheckIfChatCompletePreservesValidResponse(t *testing.T) {
	service.InitHttpClient()
	var upstreamResponse CozeChatResponse
	upstreamResponse.Data.Status = "completed"
	upstreamResponse.Data.Usage = CozeChatUsage{TokenCount: 3, OutputCount: 2, InputCount: 1}
	responseBody, err := json.Marshal(upstreamResponse)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(responseBody)
	}))
	defer server.Close()

	context := newCozeResponseLimitTestContext(t, len(responseBody))
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL}}
	err, complete := checkIfChatComplete(&Adaptor{}, context, info)

	require.NoError(t, err)
	assert.True(t, complete)
	assert.Equal(t, 3, context.GetInt("coze_token_count"))
	assert.Equal(t, 2, context.GetInt("coze_output_count"))
	assert.Equal(t, 1, context.GetInt("coze_input_count"))
}

func TestCheckIfChatCompleteHonorsRequestCancellation(t *testing.T) {
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	requestContext, cancel := context.WithCancel(context.Background())
	ginContext := newCozeResponseLimitTestContext(t, 1024)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/relay", nil).WithContext(requestContext)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL}}
	cancel()

	err, complete := checkIfChatComplete(&Adaptor{}, ginContext, info)
	assert.False(t, complete)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestGetChatDetailRejectsKnownOversizeResponse(t *testing.T) {
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Length", "5")
		_, _ = io.WriteString(writer, "12345")
	}))
	defer server.Close()

	context := newCozeResponseLimitTestContext(t, 4)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL}}
	response, err := getChatDetail(&Adaptor{}, context, info)

	assert.Nil(t, response)
	require.Error(t, err)
	assert.True(t, errors.Is(err, common.ErrLimitExceeded))
}

func TestCozeResponseTextBufferHonorsResponseBudget(t *testing.T) {
	context := newCozeResponseLimitTestContext(t, 4)
	buffer := newCozeResponseTextBuffer(context)

	buffer.WriteString("abcdef")
	buffer.WriteString("gh")

	assert.Equal(t, "abcd", buffer.String())

	unicodeBuffer := newCozeResponseTextBuffer(context)
	unicodeBuffer.WriteString("ab€")
	assert.Equal(t, "ab", unicodeBuffer.String())
}

func TestCozeChatStreamHandlerPreservesTerminalUsage(t *testing.T) {
	context := newCozeResponseLimitTestContext(t, 4)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "coze-test"}}
	stream := strings.Join([]string{
		"event: conversation.message.delta",
		`data: {"content":"abcdef"}`,
		"",
		"event: conversation.chat.completed",
		`data: {"usage":{"input_count":2,"output_count":3,"token_count":5}}`,
		"",
	}, "\n")

	usage, apiErr := cozeChatStreamHandler(context, info, &http.Response{Body: io.NopCloser(strings.NewReader(stream))})

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 2, usage.PromptTokens)
	assert.Equal(t, 3, usage.CompletionTokens)
	assert.Equal(t, 5, usage.TotalTokens)
	assert.Nil(t, info.StreamStatus, "legacy billing must keep its original stream status")
	require.NotNil(t, info.RateLimitStreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.RateLimitStreamStatus.EndReason)
	assert.False(t, info.RateLimitStreamStatus.HasErrors())
}

func TestCozeChatStreamHandlerRecordsCleanEOFWithoutCompletion(t *testing.T) {
	for _, ending := range []string{"\n", "\n\n"} {
		t.Run(fmt.Sprintf("ending=%q", ending), func(t *testing.T) {
			c := newCozeResponseLimitTestContext(t, 1024)
			c.Set("coze_input_count", 17)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "coze-test"}}
			stream := "event: conversation.message.delta\ndata: {\"content\":\"hello\"}" + ending

			usage, apiErr := cozeChatStreamHandler(c, info, &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(stream)),
			})

			require.Nil(t, apiErr, "partial-usage billing must retain the legacy return")
			require.NotNil(t, usage)
			assert.Equal(t, 17, usage.PromptTokens)
			assert.Positive(t, usage.CompletionTokens)
			assert.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)
			assert.Nil(t, info.StreamStatus, "legacy billing must keep its original stream status")
			require.NotNil(t, info.RateLimitStreamStatus)
			assert.Equal(t, relaycommon.StreamEndReasonEOF, info.RateLimitStreamStatus.EndReason)
			assert.NoError(t, info.RateLimitStreamStatus.EndError, "the HTTP body ended cleanly")
			assert.True(t, info.RateLimitStreamStatus.HasErrors(), "missing protocol completion must release the success reservation")
			assert.Equal(t, http.StatusOK, c.Writer.Status())
			assert.Empty(t, c.Errors, "the failure must come from missing completion rather than transport errors")
		})
	}
}

func TestCozeChatStreamHandlerRecordsInBandFailure(t *testing.T) {
	for _, event := range []string{"error", "conversation.chat.failed", "conversation.chat.canceled"} {
		t.Run(event, func(t *testing.T) {
			c := newCozeResponseLimitTestContext(t, 1024)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "coze-test"}}
			stream := "event: " + event + "\ndata: {\"code\":500,\"message\":\"failed\"}\n\n"

			usage, apiErr := cozeChatStreamHandler(c, info, &http.Response{Body: io.NopCloser(strings.NewReader(stream))})

			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Nil(t, info.StreamStatus, "legacy billing must keep its original stream status")
			require.NotNil(t, info.RateLimitStreamStatus)
			assert.True(t, info.RateLimitStreamStatus.HasErrors())
			assert.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.RateLimitStreamStatus.EndReason)
		})
	}
}

func TestCozeChatStreamHandlerRecordsDecodeFailure(t *testing.T) {
	c := newCozeResponseLimitTestContext(t, 1024)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "coze-test"}}
	stream := "event: conversation.message.delta\ndata: invalid\n\n"

	usage, apiErr := cozeChatStreamHandler(c, info, &http.Response{Body: io.NopCloser(strings.NewReader(stream))})

	assert.Nil(t, usage)
	require.NotNil(t, apiErr)
	require.NotNil(t, info.RateLimitStreamStatus)
	assert.True(t, info.RateLimitStreamStatus.HasErrors())
	assert.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.RateLimitStreamStatus.EndReason)
}

func TestCozeChatStreamHandlerRecordsCancellationAtEOF(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	c := newCozeResponseLimitTestContext(t, 1024)
	c.Request = c.Request.WithContext(requestContext)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "coze-test"}}
	cancel()

	_, _ = cozeChatStreamHandler(c, info, &http.Response{Body: io.NopCloser(strings.NewReader(""))})

	require.NotNil(t, info.RateLimitStreamStatus)
	assert.True(t, info.RateLimitStreamStatus.HasErrors())
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.RateLimitStreamStatus.EndReason)
}
