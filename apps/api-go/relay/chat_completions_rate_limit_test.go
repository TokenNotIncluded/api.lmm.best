package relay

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type chatResponsesRetryOutcomeAdaptor struct {
	channel.Adaptor
	response      *http.Response
	failedStatus  *relaycommon.StreamStatus
	failure       *types.NewAPIError
	beforeRequest func(*relaycommon.RelayInfo)
}

func (a *chatResponsesRetryOutcomeAdaptor) ConvertOpenAIResponsesRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return &request, nil
}

func (a *chatResponsesRetryOutcomeAdaptor) DoRequest(_ *gin.Context, info *relaycommon.RelayInfo, _ io.Reader) (any, error) {
	a.beforeRequest(info)
	return a.response, nil
}

func (a *chatResponsesRetryOutcomeAdaptor) DoResponse(_ *gin.Context, _ *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	info.StreamStatus = a.failedStatus
	info.RateLimitStreamStatus = a.failedStatus
	return nil, a.failure
}

func TestChatCompletionsViaResponsesClearsFailedRetryOutcome(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	jsonResponse := `{"id":"resp_retry","object":"response","created_at":1710000000,"model":"gpt-test","status":"completed","output":[{"type":"message","id":"msg_retry","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`
	streamResponse := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_retry","model":"gpt-test","created_at":1710000000}}`,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		`data: [DONE]`,
		``,
	}, "\n\n")

	for _, tc := range []struct {
		name         string
		clientStream bool
		contentType  string
		body         string
	}{
		{name: "JSON response", contentType: "application/json", body: jsonResponse},
		{name: "buffered stream response", contentType: "text/event-stream", body: streamResponse},
		{name: "stream response", clientStream: true, contentType: "text/event-stream", body: streamResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			request := &dto.GeneralOpenAIRequest{
				Model:    "gpt-test",
				Messages: []dto.Message{{Role: "user", Content: "hello"}},
				Stream:   &tc.clientStream,
			}
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType:       constant.ChannelTypeOpenAI,
					UpstreamModelName: "gpt-test",
				},
				OriginModelName:    "gpt-test",
				RelayMode:          relayconstant.RelayModeChatCompletions,
				RelayFormat:        types.RelayFormatOpenAI,
				RequestURLPath:     "/v1/chat/completions",
				Request:            request,
				IsStream:           tc.clientStream,
				ShouldIncludeUsage: true,
				DisablePing:        true,
			}
			failedStatus := relaycommon.NewStreamStatus()
			failedStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, io.ErrUnexpectedEOF)
			failedStatus.RecordError("first attempt failed")
			adaptor := &chatResponsesRetryOutcomeAdaptor{
				failedStatus: failedStatus,
				failure:      types.NewError(errors.New("first attempt failed"), types.ErrorCodeBadResponse),
				response: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{tc.contentType}},
					Body:       io.NopCloser(strings.NewReader(tc.body)),
				},
				beforeRequest: func(info *relaycommon.RelayInfo) {
					require.False(t, info.ResponseFailed, "the retry must clear the previous failure before forwarding")
					require.False(t, info.ResponseCompleted)
					require.Nil(t, info.StreamStatus)
					require.Nil(t, info.RateLimitStreamStatus)
				},
			}

			// The normal adaptor path fails, then the same RelayInfo is reused by
			// a channel whose Chat Completions endpoint uses the Responses shortcut.
			_, apiErr := channel.DoResponse(adaptor, c, nil, info)
			require.Same(t, adaptor.failure, apiErr)
			require.True(t, info.ResponseFailed)
			require.True(t, info.ResponseCompleted)
			require.Same(t, failedStatus, info.StreamStatus)

			usage, apiErr := chatCompletionsViaResponses(c, info, adaptor, request)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			require.Equal(t, 5, usage.TotalTokens)
			require.Contains(t, recorder.Body.String(), `"content":"hello"`)
			require.True(t, info.ResponseCompleted)
			require.False(t, info.ResponseFailed, "the successful shortcut retry must count as a success")
			require.Nil(t, info.RateLimitStreamStatus)
			if tc.clientStream {
				require.NotNil(t, info.StreamStatus)
				require.NotSame(t, failedStatus, info.StreamStatus)
				require.True(t, info.StreamStatus.IsNormalEnd())
				require.NoError(t, info.StreamStatus.EndError)
				require.False(t, info.StreamStatus.HasErrors())
			} else {
				require.Nil(t, info.StreamStatus, "nonstream retries must not reuse the failed stream status")
			}
		})
	}
}
