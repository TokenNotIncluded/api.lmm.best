package middleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/baidu"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/coze"
	"github.com/LIghtJUNction/api.lmm.best/relay/channel/gemini"
	openai "github.com/LIghtJUNction/api.lmm.best/relay/channel/openai"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	relayconstant "github.com/LIghtJUNction/api.lmm.best/relay/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var modelOutcomeTestUserID atomic.Int64

func configureModelOutcomeTest(t *testing.T, backend string, totalLimit, successLimit int) *redis.Client {
	t.Helper()
	previousEnabled := setting.ModelRequestRateLimitEnabled
	previousDuration := setting.ModelRequestRateLimitDurationMinutes
	previousTotal := setting.ModelRequestRateLimitCount
	previousSuccess := setting.ModelRequestRateLimitSuccessCount
	previousRedisEnabled := common.RedisEnabled
	previousGinMode := gin.Mode()
	previousStreamTimeout := constant.StreamingTimeout
	setting.ModelRequestRateLimitMutex.Lock()
	previousGroups := setting.ModelRequestRateLimitGroup
	setting.ModelRequestRateLimitGroup = make(map[string][2]int)
	setting.ModelRequestRateLimitMutex.Unlock()

	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	setting.ModelRequestRateLimitCount = totalLimit
	setting.ModelRequestRateLimitSuccessCount = successLimit
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)
	constant.StreamingTimeout = 5
	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled = previousEnabled
		setting.ModelRequestRateLimitDurationMinutes = previousDuration
		setting.ModelRequestRateLimitCount = previousTotal
		setting.ModelRequestRateLimitSuccessCount = previousSuccess
		common.RedisEnabled = previousRedisEnabled
		gin.SetMode(previousGinMode)
		constant.StreamingTimeout = previousStreamTimeout
		setting.ModelRequestRateLimitMutex.Lock()
		setting.ModelRequestRateLimitGroup = previousGroups
		setting.ModelRequestRateLimitMutex.Unlock()
	})
	if backend == "redis" {
		_, client := useRateLimitMiniRedis(t)
		return client
	}
	return nil
}

func nextModelOutcomeUserID() int {
	return 1_000_000_000 + int(modelOutcomeTestUserID.Add(1))
}

func newModelOutcomeContext(userID int) (*gin.Context, *httptest.ResponseRecorder) {
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("id", userID)
	return c, writer
}

func genModelOutcomeRelayInfo(c *gin.Context, stream bool) *relaycommon.RelayInfo {
	info := relaycommon.GenRelayInfoResponses(c, &dto.OpenAIResponsesRequest{Model: "gpt-4o", Stream: &stream})
	info.ChannelMeta = &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}
	info.DisablePing = true
	return info
}

func modelOutcomeCommit(t *testing.T, c *gin.Context) ModelRequestRateLimitCommit {
	t.Helper()
	commit, apiErr := CheckModelRequestRateLimit(c)
	require.Nil(t, apiErr)
	require.NotNil(t, commit)
	t.Cleanup(func() { commit(false) })
	return commit
}

func requireModelOutcomeLimited(t *testing.T, c *gin.Context) {
	t.Helper()
	commit, apiErr := CheckModelRequestRateLimit(c)
	if commit != nil {
		t.Cleanup(func() { commit(false) })
	}
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
}

func TestModelRequestSucceededUsesFinalRelayOutcome(t *testing.T) {
	configureModelOutcomeTest(t, "memory", 0, 1)
	for _, tc := range []struct {
		name      string
		status    int
		stream    bool
		reason    relaycommon.StreamEndReason
		endError  error
		softError bool
		legacy    bool
		cancelled bool
		want      bool
	}{
		{name: "nonstream OK", status: http.StatusOK, want: true},
		{name: "nonstream no content", status: http.StatusNoContent, want: true},
		{name: "nonstream bad request", status: http.StatusBadRequest},
		{name: "nonstream server error", status: http.StatusInternalServerError},
		{name: "cancelled nonstream", status: http.StatusOK, cancelled: true},
		{name: "stream has no outcome", status: http.StatusOK, stream: true},
		{name: "stream completed", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonDone, want: true},
		{name: "stream normal EOF", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonEOF, want: true},
		{name: "stream normal handler stop", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonHandlerStop, want: true},
		{name: "stream timed out", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonTimeout},
		{name: "stream client gone", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonClientGone},
		{name: "stream scanner error", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonScannerErr},
		{name: "stream ping failed", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonPingFail},
		{name: "stream panicked", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonPanic},
		{name: "stream write failed", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonHandlerStop, endError: io.ErrClosedPipe},
		{name: "completed stream with end error", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonDone, endError: errors.New("write failed")},
		{name: "completed stream with soft error", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonDone, softError: true},
		{name: "completed stream with HTTP error", status: http.StatusBadGateway, stream: true, reason: relaycommon.StreamEndReasonDone},
		{name: "completed stream after cancellation", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonDone, cancelled: true},
		{name: "legacy stream completed", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonDone, legacy: true, want: true},
		{name: "legacy stream normal EOF", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonEOF, legacy: true, want: true},
		{name: "legacy stream scanner error", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonScannerErr, legacy: true},
		{name: "legacy stream write error", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonHandlerStop, endError: io.ErrClosedPipe, legacy: true},
		{name: "legacy completed stream with soft error", status: http.StatusOK, stream: true, reason: relaycommon.StreamEndReasonDone, softError: true, legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newModelOutcomeContext(nextModelOutcomeUserID())
			info := genModelOutcomeRelayInfo(c, tc.stream)
			linked, ok := common.GetContextKeyType[*relaycommon.RelayInfo](c, constant.ContextKeyRelayInfo)
			require.True(t, ok)
			require.Same(t, info, linked)
			if tc.reason != relaycommon.StreamEndReasonNone {
				status := relaycommon.NewStreamStatus()
				status.SetEndReason(tc.reason, tc.endError)
				if tc.softError {
					status.RecordError("upstream response failed")
				}
				if tc.legacy {
					info.RateLimitStreamStatus = status
				} else {
					info.StreamStatus = status
				}
			}
			if tc.cancelled {
				ctx, cancel := context.WithCancel(c.Request.Context())
				cancel()
				c.Request = c.Request.WithContext(ctx)
			}
			c.Status(tc.status)
			assert.Equal(t, tc.want, modelRequestSucceeded(c))
		})
	}
}

func TestModelRequestSucceededPrefersProtocolStatusOverLegacyRateLimitStatus(t *testing.T) {
	configureModelOutcomeTest(t, "memory", 0, 1)
	c, _ := newModelOutcomeContext(nextModelOutcomeUserID())
	info := genModelOutcomeRelayInfo(c, true)
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	info.RateLimitStreamStatus = relaycommon.NewStreamStatus()
	info.RateLimitStreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, io.ErrUnexpectedEOF)
	info.RateLimitStreamStatus.RecordError("legacy outcome failed")
	require.True(t, modelRequestSucceeded(c), "the actual protocol status takes precedence")

	info.StreamStatus = nil
	require.False(t, modelRequestSucceeded(c), "legacy rate-limit failure is used when no protocol status exists")
	info.RateLimitStreamStatus = relaycommon.NewStreamStatus()
	info.RateLimitStreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	require.True(t, modelRequestSucceeded(c))
	require.Nil(t, info.StreamStatus, "legacy rate-limit outcomes must not populate the billing status")
}

func TestModelRequestSucceededRejectsHTTP200MappedRelayError(t *testing.T) {
	configureModelOutcomeTest(t, "memory", 0, 1)
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprintf("adaptor completed=%t", completed), func(t *testing.T) {
			c, _ := newModelOutcomeContext(nextModelOutcomeUserID())
			info := genModelOutcomeRelayInfo(c, false)
			c.Status(http.StatusOK)
			info.ResponseCompleted = completed
			info.LastError = types.NewError(errors.New("upstream error mapped to HTTP 200"), types.ErrorCodeBadResponseStatusCode)
			require.False(t, modelRequestSucceeded(c), "a mapped status must not turn a relay error into success")

			// A later successful retry clears the prior error before the final
			// response is classified, so the earlier attempt cannot block it.
			info.LastError = nil
			info.ResponseCompleted = true
			require.True(t, modelRequestSucceeded(c))
		})
	}
}

func TestModelRequestSucceededReadsReplacedRelayInfoAndStreamStatus(t *testing.T) {
	configureModelOutcomeTest(t, "memory", 0, 1)
	c, _ := newModelOutcomeContext(nextModelOutcomeUserID())
	info := genModelOutcomeRelayInfo(c, true)
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	info.StreamStatus.RecordError("prior attempt failed")
	require.False(t, modelRequestSucceeded(c))

	// The scanner replaces its status for every upstream attempt. The context
	// must retain RelayInfo, so the final attempt determines the outcome.
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	require.True(t, modelRequestSucceeded(c))

	replacement := genModelOutcomeRelayInfo(c, true)
	linked, ok := common.GetContextKeyType[*relaycommon.RelayInfo](c, constant.ContextKeyRelayInfo)
	require.True(t, ok)
	require.Same(t, replacement, linked)
	require.NotSame(t, info, replacement)
	require.False(t, modelRequestSucceeded(c), "a new request must not reuse the old completed stream")

	// Upstream SSE detection can update this flag after request creation.
	replacement = genModelOutcomeRelayInfo(c, false)
	replacement.IsStream = true
	require.False(t, modelRequestSucceeded(c))
}

func runModelOutcomeResponsesStream(t *testing.T, c *gin.Context, terminal string, cancelled bool) *relaycommon.RelayInfo {
	t.Helper()
	info := genModelOutcomeRelayInfo(c, true)
	linked, ok := common.GetContextKeyType[*relaycommon.RelayInfo](c, constant.ContextKeyRelayInfo)
	require.True(t, ok)
	require.Same(t, info, linked)
	var body string
	if terminal != "" {
		body = fmt.Sprintf("data: {\"type\":\"response.%s\",\"response\":{\"id\":\"resp_rate_limit\",\"status\":\"%s\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"total_tokens\":12}}}\n\n", terminal, terminal)
	}
	if cancelled {
		ctx, cancel := context.WithCancel(c.Request.Context())
		cancel()
		c.Request = c.Request.WithContext(ctx)
	}
	usage, apiErr := openai.OaiResponsesStreamHandler(c, info, &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	})
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.NotNil(t, info.StreamStatus)
	if terminal != "" {
		assert.Equal(t, 12, usage.TotalTokens, "failed Responses still retain measured usage")
	}
	return info
}

type modelOutcomeReadError struct{}

func (modelOutcomeReadError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type modelOutcomeShortWriter struct{ *httptest.ResponseRecorder }

func (*modelOutcomeShortWriter) Write(data []byte) (int, error) { return len(data) / 2, nil }

func addModelOutcomeSuccessRoute(t *testing.T, router *gin.Engine, userID int) {
	t.Helper()
	router.POST("/success", func(c *gin.Context) { c.Set("id", userID) }, ModelRequestRateLimit(), func(c *gin.Context) {
		info := genModelOutcomeRelayInfo(c, false)
		info.RelayMode = relayconstant.RelayModeChatCompletions
		info.RelayFormat = types.RelayFormatOpenAI
		body := `{"id":"chat_success","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
		usage, apiErr := channel.DoResponse(&openai.Adaptor{}, c, &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)),
		}, info)
		require.Nil(t, apiErr)
		measuredUsage, ok := usage.(*dto.Usage)
		require.True(t, ok)
		assert.Equal(t, 12, measuredUsage.TotalTokens)
		assert.True(t, info.ResponseCompleted)
		assert.False(t, info.ResponseFailed)
		assert.True(t, modelRequestSucceeded(c))
	})
}

func TestModelRequestRateLimitReleasesNonStreamAdaptorFailures(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			configureModelOutcomeTest(t, backend, 0, 1)
			for _, tc := range []struct {
				name       string
				adaptor    channel.Adaptor
				mode       int
				body       string
				readError  bool
				writeError bool
				shortWrite bool
				wantTokens int
			}{
				{name: "Gemini prompt blocked mapped to 200", adaptor: &gemini.Adaptor{}, mode: relayconstant.RelayModeChatCompletions, body: `{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":10,"totalTokenCount":10}}`, wantTokens: 10},
				{name: "Gemini empty candidates mapped to 200", adaptor: &gemini.Adaptor{}, mode: relayconstant.RelayModeChatCompletions, body: `{"usageMetadata":{"promptTokenCount":10,"totalTokenCount":10}}`, wantTokens: 10},
				{name: "Gemini native prompt blocked", adaptor: &gemini.Adaptor{}, mode: relayconstant.RelayModeGemini, body: `{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":10,"totalTokenCount":10}}`, wantTokens: 10},
				{name: "TTS response read failed", adaptor: &openai.Adaptor{}, mode: relayconstant.RelayModeAudioSpeech, readError: true, wantTokens: 10},
				{name: "TTS client write failed", adaptor: &openai.Adaptor{}, mode: relayconstant.RelayModeAudioSpeech, body: strings.Repeat("x", 480), writeError: true, wantTokens: 27},
				{name: "TTS client short write", adaptor: &openai.Adaptor{}, mode: relayconstant.RelayModeAudioSpeech, body: strings.Repeat("x", 480), shortWrite: true, wantTokens: 27},
				{name: "chat client write failed", adaptor: &openai.Adaptor{}, mode: relayconstant.RelayModeChatCompletions, body: `{"id":"chat_failure","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`, writeError: true, wantTokens: 12},
				{name: "chat client short write", adaptor: &openai.Adaptor{}, mode: relayconstant.RelayModeChatCompletions, body: `{"id":"chat_failure","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`, shortWrite: true, wantTokens: 12},
			} {
				t.Run(tc.name, func(t *testing.T) {
					userID := nextModelOutcomeUserID()
					router := gin.New()
					router.POST("/v1/responses", func(c *gin.Context) { c.Set("id", userID) }, ModelRequestRateLimit(), func(c *gin.Context) {
						info := genModelOutcomeRelayInfo(c, false)
						info.RelayMode = tc.mode
						info.RelayFormat = types.RelayFormatOpenAI
						info.SetEstimatePromptTokens(10)
						if tc.mode == relayconstant.RelayModeAudioSpeech {
							info.Request = &dto.AudioRequest{ResponseFormat: "pcm"}
						}
						c.Set("status_code_mapping", `{"400":200,"500":200}`)
						var reader io.Reader = strings.NewReader(tc.body)
						if tc.readError {
							reader = modelOutcomeReadError{}
						}
						usage, apiErr := channel.DoResponse(tc.adaptor, c, &http.Response{
							StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(reader),
						}, info)
						require.Nil(t, apiErr, "retain the adaptor's existing retry and billing behavior")
						measuredUsage, ok := usage.(*dto.Usage)
						require.True(t, ok)
						assert.Equal(t, tc.wantTokens, measuredUsage.TotalTokens, "retain measured usage on a failed response")
						assert.True(t, info.ResponseCompleted)
						assert.True(t, info.ResponseFailed, "HTTP 200 must not hide a known response failure")
						assert.False(t, modelRequestSucceeded(c))
					})
					addModelOutcomeSuccessRoute(t, router, userID)
					writer := httptest.NewRecorder()
					var downstream http.ResponseWriter = writer
					if tc.writeError {
						downstream = &modelOutcomeFailedWriter{ResponseRecorder: writer, closed: make(chan bool)}
					} else if tc.shortWrite {
						downstream = &modelOutcomeShortWriter{ResponseRecorder: writer}
					}
					router.ServeHTTP(downstream, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
					require.Equal(t, http.StatusOK, writer.Code)
					if tc.mode == relayconstant.RelayModeGemini {
						assert.JSONEq(t, tc.body, writer.Body.String(), "retain the native Gemini response")
					}
					for _, wantStatus := range []int{http.StatusOK, http.StatusTooManyRequests} {
						writer := httptest.NewRecorder()
						router.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/success", nil))
						require.Equal(t, wantStatus, writer.Code, "only the successful response consumes the success slot")
					}
				})
			}
		})
	}
}

func TestModelRequestRateLimitReleasesBaiduStreamErrors(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			configureModelOutcomeTest(t, backend, 0, 1)
			for _, providerError := range []string{`"error_code":110,"error_msg":"token invalid"`, `"error_code":18`, `"error_msg":"token invalid"`} {
				t.Run(providerError, func(t *testing.T) {
					userID := nextModelOutcomeUserID()
					router := gin.New()
					router.POST("/v1/responses", func(c *gin.Context) { c.Set("id", userID) }, ModelRequestRateLimit(), func(c *gin.Context) {
						info := genModelOutcomeRelayInfo(c, true)
						info.RelayMode = relayconstant.RelayModeChatCompletions
						body := "data: {" + providerError + `,"usage":{"prompt_tokens":10,"total_tokens":10}}` + "\n\n"
						usage, apiErr := channel.DoResponse(&baidu.Adaptor{}, c, &http.Response{
							StatusCode: http.StatusOK,
							Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
							Body:       io.NopCloser(strings.NewReader(body)),
						}, info)
						require.Nil(t, apiErr)
						measuredUsage, ok := usage.(*dto.Usage)
						require.True(t, ok)
						assert.Equal(t, 10, measuredUsage.TotalTokens, "provider usage is retained")
						assert.True(t, info.ResponseCompleted)
						assert.True(t, info.ResponseFailed)
						assert.False(t, modelRequestSucceeded(c))
					})
					addModelOutcomeSuccessRoute(t, router, userID)
					for range 2 {
						writer := httptest.NewRecorder()
						router.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
						require.Equal(t, http.StatusOK, writer.Code, "a failed stream must not consume the success slot")
					}
					for _, wantStatus := range []int{http.StatusOK, http.StatusTooManyRequests} {
						writer := httptest.NewRecorder()
						router.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/success", nil))
						require.Equal(t, wantStatus, writer.Code, "only the successful response consumes the success slot")
					}
				})
			}
		})
	}
}

func runModelOutcomeLegacyStream(t *testing.T, c *gin.Context, fail bool) {
	t.Helper()
	info := genModelOutcomeRelayInfo(c, true)
	stream := "event: conversation.message.delta\ndata: {\"content\":\"hello\"}\n\n"
	var reader io.Reader
	if fail {
		reader = io.MultiReader(strings.NewReader(stream), modelOutcomeReadError{})
	} else {
		stream += "event: conversation.chat.completed\ndata: {\"usage\":{\"input_count\":10,\"output_count\":2,\"token_count\":12}}\n\n"
		reader = strings.NewReader(stream)
	}
	usage, apiErr := channel.DoResponse(&coze.Adaptor{}, c, &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(reader),
	}, info)
	require.Nil(t, info.StreamStatus, "legacy rate-limit outcomes must not change billing metadata")
	require.NotNil(t, info.RateLimitStreamStatus)
	assert.Equal(t, fail, info.RateLimitStreamStatus.HasErrors())
	assert.True(t, info.ResponseCompleted)
	assert.Equal(t, fail, info.ResponseFailed)
	if fail {
		require.NotNil(t, apiErr)
	} else {
		require.Nil(t, apiErr)
		measuredUsage, ok := usage.(*dto.Usage)
		require.True(t, ok)
		assert.Equal(t, 12, measuredUsage.TotalTokens)
	}
}

func TestModelRequestRateLimitCommitsActualHTTPOutcome(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			for _, tc := range []struct {
				name      string
				terminal  string
				cancelled bool
				streamNil bool
				legacy    bool
				legacyErr bool
				status    int
				wantCount bool
			}{
				{name: "completed Responses", terminal: "completed", status: http.StatusOK, wantCount: true},
				{name: "failed Responses over HTTP 200", terminal: "failed", status: http.StatusOK},
				{name: "incomplete Responses over HTTP 200", terminal: "incomplete", status: http.StatusOK},
				{name: "cancelled stream", cancelled: true, status: http.StatusOK},
				{name: "stream without final outcome", streamNil: true, status: http.StatusOK},
				{name: "legacy adaptor stream completed", legacy: true, status: http.StatusOK, wantCount: true},
				{name: "legacy adaptor stream failed", legacy: true, legacyErr: true, status: http.StatusOK},
				{name: "nonstream success", status: http.StatusNoContent, wantCount: true},
				{name: "nonstream failure", status: http.StatusBadRequest},
			} {
				t.Run(tc.name, func(t *testing.T) {
					client := configureModelOutcomeTest(t, backend, 0, 1)
					userID := nextModelOutcomeUserID()
					router := gin.New()
					var handled bool
					router.POST("/v1/responses", func(c *gin.Context) { c.Set("id", userID) }, ModelRequestRateLimit(), func(c *gin.Context) {
						handled = true
						if tc.legacy {
							runModelOutcomeLegacyStream(t, c, tc.legacyErr)
						} else if tc.terminal != "" || tc.cancelled {
							info := runModelOutcomeResponsesStream(t, c, tc.terminal, tc.cancelled)
							assert.Equal(t, tc.terminal != "completed", info.StreamStatus.HasErrors())
						} else {
							genModelOutcomeRelayInfo(c, tc.streamNil)
							c.Status(tc.status)
							c.Writer.WriteHeaderNow()
						}
						assert.Equal(t, tc.wantCount, modelRequestSucceeded(c))
					})
					writer := httptest.NewRecorder()
					router.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
					require.True(t, handled)
					assert.Equal(t, tc.status, writer.Code)
					if tc.terminal != "" {
						assert.Contains(t, writer.Body.String(), "event: response."+tc.terminal)
					}
					if client != nil {
						key := "rateLimit:" + ModelRequestRateLimitSuccessCountMark + ":" + strconv.Itoa(userID)
						count, err := client.LLen(context.Background(), key).Result()
						require.NoError(t, err)
						wantCount := int64(0)
						if tc.wantCount {
							wantCount = 1
						}
						assert.Equal(t, wantCount, count)
					}

					c, _ := newModelOutcomeContext(userID)
					if tc.wantCount {
						requireModelOutcomeLimited(t, c)
					} else {
						commit := modelOutcomeCommit(t, c)
						commit(false)
						commit = modelOutcomeCommit(t, c)
						commit(true)
						requireModelOutcomeLimited(t, c)
					}
				})
			}
		})
	}
}

func TestModelRequestRateLimitReleasesHTTPPanic(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			for _, writeBeforePanic := range []bool{false, true} {
				t.Run(fmt.Sprintf("headers committed=%t", writeBeforePanic), func(t *testing.T) {
					client := configureModelOutcomeTest(t, backend, 0, 1)
					userID := nextModelOutcomeUserID()
					router := gin.New()
					router.Use(gin.RecoveryWithWriter(io.Discard))
					router.POST("/v1/responses", func(c *gin.Context) { c.Set("id", userID) }, ModelRequestRateLimit(), func(c *gin.Context) {
						genModelOutcomeRelayInfo(c, false)
						if writeBeforePanic {
							c.String(http.StatusOK, "partial response")
						}
						panic("relay handler failed")
					})
					writer := httptest.NewRecorder()
					router.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
					if writeBeforePanic {
						assert.Equal(t, http.StatusOK, writer.Code)
					} else {
						assert.Equal(t, http.StatusInternalServerError, writer.Code)
					}
					if client != nil {
						key := "rateLimit:" + ModelRequestRateLimitSuccessCountMark + ":" + strconv.Itoa(userID)
						count, err := client.LLen(context.Background(), key).Result()
						require.NoError(t, err)
						assert.Zero(t, count)
					}
					c, _ := newModelOutcomeContext(userID)
					commit := modelOutcomeCommit(t, c)
					commit(true)
					requireModelOutcomeLimited(t, c)
				})
			}
		})
	}
}

func TestModelRequestRateLimitFailedRequestsConsumeOnlyTotalQuota(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			configureModelOutcomeTest(t, backend, 2, 1)
			userID := nextModelOutcomeUserID()
			router := gin.New()
			var handled int
			router.POST("/v1/responses", func(c *gin.Context) { c.Set("id", userID) }, ModelRequestRateLimit(), func(c *gin.Context) {
				handled++
				genModelOutcomeRelayInfo(c, false)
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			})
			request := func() *httptest.ResponseRecorder {
				writer := httptest.NewRecorder()
				router.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
				return writer
			}
			require.Equal(t, http.StatusBadRequest, request().Code)
			require.Equal(t, http.StatusBadRequest, request().Code)
			require.Equal(t, http.StatusTooManyRequests, request().Code)
			require.Equal(t, 2, handled, "failed attempts still exhaust the total request allowance")

			// Removing only the total cap exposes the independent success quota:
			// neither of those failed responses may consume or pin its capacity.
			setting.ModelRequestRateLimitCount = 0
			c, _ := newModelOutcomeContext(userID)
			commit := modelOutcomeCommit(t, c)
			commit(true)
			requireModelOutcomeLimited(t, c)
		})
	}
}

func TestLegacyModelRateLimitHandlersUseFinalOutcome(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			configureModelOutcomeTest(t, backend, 0, 1)
			userID := nextModelOutcomeUserID()
			var handler gin.HandlerFunc
			if backend == "redis" {
				handler = redisRateLimitHandler(60, 0, 1)
			} else {
				handler = memoryRateLimitHandler(60, 0, 1)
			}
			router := gin.New()
			var handled int
			router.POST("/v1/responses", func(c *gin.Context) { c.Set("id", userID) }, handler, func(c *gin.Context) {
				handled++
				terminal := "failed"
				if c.Query("terminal") == "completed" {
					terminal = "completed"
				}
				runModelOutcomeResponsesStream(t, c, terminal, false)
			})
			request := func(terminal string) *httptest.ResponseRecorder {
				writer := httptest.NewRecorder()
				router.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/v1/responses?terminal="+terminal, nil))
				return writer
			}
			for range 2 {
				failed := request("failed")
				require.Equal(t, http.StatusOK, failed.Code)
				require.Contains(t, failed.Body.String(), "event: response.failed")
			}
			completed := request("completed")
			require.Equal(t, http.StatusOK, completed.Code)
			require.Contains(t, completed.Body.String(), "event: response.completed")
			require.Equal(t, http.StatusTooManyRequests, request("completed").Code)
			assert.Equal(t, 3, handled, "legacy factories must share final-outcome accounting")
		})
	}
}

type modelOutcomeFailedWriter struct {
	*httptest.ResponseRecorder
	closed <-chan bool
}

func (*modelOutcomeFailedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func (*modelOutcomeFailedWriter) WriteString(string) (int, error) { return 0, io.ErrClosedPipe }

func (w *modelOutcomeFailedWriter) CloseNotify() <-chan bool { return w.closed }

func TestModelRequestRateLimitReleasesLegacyStreamWriteFailure(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			client := configureModelOutcomeTest(t, backend, 0, 1)
			userID := nextModelOutcomeUserID()
			writer := &modelOutcomeFailedWriter{ResponseRecorder: httptest.NewRecorder(), closed: make(chan bool)}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Set("id", userID)
			commit := modelOutcomeCommit(t, c)
			info := genModelOutcomeRelayInfo(c, true)
			stream := "event: conversation.message.delta\ndata: {\"content\":\"hello\"}\n\n" +
				"event: conversation.chat.completed\ndata: {\"usage\":{\"input_count\":10,\"output_count\":2,\"token_count\":12}}\n\n"
			usage, apiErr := channel.DoResponse(&coze.Adaptor{}, c, &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(stream)),
			}, info)
			// Gin records renderer errors on the context even when an adaptor
			// finishes normally and cannot return a downstream write failure.
			require.Nil(t, apiErr)
			measuredUsage, ok := usage.(*dto.Usage)
			require.True(t, ok)
			require.Equal(t, 12, measuredUsage.TotalTokens, "write errors must not change measured usage")
			require.Nil(t, info.StreamStatus, "legacy rate-limit metadata must not change billing status")
			require.NotNil(t, info.RateLimitStreamStatus)
			require.Equal(t, http.StatusOK, writer.Code)
			require.Equal(t, http.StatusOK, c.Writer.Status())
			require.NotEmpty(t, c.Errors)
			assert.ErrorIs(t, c.Errors[0].Err, io.ErrClosedPipe)
			assert.True(t, c.IsAborted())
			assert.True(t, info.ResponseCompleted)
			assert.False(t, modelRequestSucceeded(c))
			commit(modelRequestSucceeded(c))
			if client != nil {
				key := "rateLimit:" + ModelRequestRateLimitSuccessCountMark + ":" + strconv.Itoa(userID)
				count, err := client.LLen(context.Background(), key).Result()
				require.NoError(t, err)
				assert.Zero(t, count)
			}
			turn, _ := newModelOutcomeContext(userID)
			commit = modelOutcomeCommit(t, turn)
			commit(true)
			requireModelOutcomeLimited(t, turn)
		})
	}
}

func TestModelRequestRateLimitResponsesWebSocketHandshakeBypassesQuotas(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			totalLimit := 1
			if backend == "redis" {
				// The Redis token bucket binds its client once per process. Keep
				// total-quota assertions in the separate total-attempt fixture.
				totalLimit = 0
			}
			configureModelOutcomeTest(t, backend, totalLimit, 1)
			userID := nextModelOutcomeUserID()
			router := gin.New()
			var handled int
			router.GET("/v1/responses", func(c *gin.Context) { c.Set("id", userID) }, ModelRequestRateLimit(), func(c *gin.Context) {
				handled++
				c.Status(http.StatusNoContent)
			})
			handshake := func() *httptest.ResponseRecorder {
				writer := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
				request.Header.Set("Upgrade", "websocket")
				router.ServeHTTP(writer, request)
				return writer
			}
			require.Equal(t, http.StatusNoContent, handshake().Code)
			c, _ := newModelOutcomeContext(userID)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			c.Request.Header.Set("Upgrade", "websocket")
			// Admission still happens per response.create through the API, even
			// when the same context represents a WebSocket handshake.
			commit := modelOutcomeCommit(t, c)
			commit(true)
			requireModelOutcomeLimited(t, c)
			require.Equal(t, http.StatusNoContent, handshake().Code)
			require.Equal(t, 2, handled)
			ordinary := httptest.NewRecorder()
			router.ServeHTTP(ordinary, httptest.NewRequest(http.MethodGet, "/v1/responses", nil))
			assert.Equal(t, http.StatusTooManyRequests, ordinary.Code)
			assert.Equal(t, 2, handled)
		})
	}
}

func TestCheckModelRequestRateLimitCompletesEachTurnOnce(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			t.Run("failed turn releases and stays failed", func(t *testing.T) {
				configureModelOutcomeTest(t, backend, 0, 1)
				c, _ := newModelOutcomeContext(nextModelOutcomeUserID())
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
				c.Request.Header.Set("Upgrade", "websocket")
				commit := modelOutcomeCommit(t, c)
				commit(false)
				commit(true)
				commit = modelOutcomeCommit(t, c)
				commit(true)
				requireModelOutcomeLimited(t, c)
			})
			t.Run("successful turn commits once concurrently", func(t *testing.T) {
				configureModelOutcomeTest(t, backend, 0, 2)
				c, _ := newModelOutcomeContext(nextModelOutcomeUserID())
				commit := modelOutcomeCommit(t, c)
				var wait sync.WaitGroup
				for range 12 {
					wait.Add(1)
					go func() {
						defer wait.Done()
						commit(true)
					}()
				}
				wait.Wait()
				commit(false)
				commit = modelOutcomeCommit(t, c)
				commit(true)
				requireModelOutcomeLimited(t, c)
			})
		})
	}
}

func TestCheckModelRequestRateLimitMemoryReservesPendingTurn(t *testing.T) {
	configureModelOutcomeTest(t, "memory", 0, 1)
	c, _ := newModelOutcomeContext(nextModelOutcomeUserID())
	commit := modelOutcomeCommit(t, c)
	requireModelOutcomeLimited(t, c)
	commit(false)
	commit = modelOutcomeCommit(t, c)
	commit(true)
	requireModelOutcomeLimited(t, c)
}

type modelOutcomeAdaptor struct {
	channel.Adaptor
	response func(*gin.Context, *http.Response, *relaycommon.RelayInfo) (any, *types.NewAPIError)
}

func (a modelOutcomeAdaptor) DoResponse(c *gin.Context, response *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	return a.response(c, response, info)
}

func TestModelResponseOutcomePreservesAdaptorUsageAndError(t *testing.T) {
	configureModelOutcomeTest(t, "memory", 0, 1)
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed=%t", fail), func(t *testing.T) {
			c, _ := newModelOutcomeContext(nextModelOutcomeUserID())
			info := genModelOutcomeRelayInfo(c, true)
			info.ResponseCompleted = true
			info.ResponseFailed = true
			info.StreamStatus = relaycommon.NewStreamStatus()
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, nil)
			info.RateLimitStreamStatus = relaycommon.NewStreamStatus()
			info.RateLimitStreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, context.Canceled)
			wantUsage := &dto.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}
			var wantError *types.NewAPIError
			if fail {
				wantError = types.NewError(errors.New("legacy stream failed after partial usage"), types.ErrorCodeBadResponseBody)
			}
			response := &http.Response{StatusCode: http.StatusOK}
			var called int
			adaptor := modelOutcomeAdaptor{response: func(gotContext *gin.Context, gotResponse *http.Response, gotInfo *relaycommon.RelayInfo) (any, *types.NewAPIError) {
				called++
				require.Same(t, c, gotContext)
				require.Same(t, response, gotResponse)
				require.Same(t, info, gotInfo)
				assert.False(t, info.ResponseCompleted, "clear completion before another attempt")
				assert.False(t, info.ResponseFailed, "clear the previous attempt's failure")
				assert.Nil(t, info.StreamStatus, "clear the previous attempt's scanner status")
				assert.Nil(t, info.RateLimitStreamStatus, "clear the previous attempt's limiter status")
				return wantUsage, wantError
			}}
			usage, apiErr := channel.DoResponse(adaptor, c, response, info)
			require.Equal(t, 1, called)
			require.Same(t, wantUsage, usage)
			if fail {
				require.Same(t, wantError, apiErr)
			} else {
				require.Nil(t, apiErr)
			}
			assert.True(t, info.ResponseCompleted)
			assert.Equal(t, fail, info.ResponseFailed)
			assert.Nil(t, info.StreamStatus, "adaptors without instrumentation use their completed response outcome")
			assert.Nil(t, info.RateLimitStreamStatus)
			assert.Equal(t, !fail, modelRequestSucceeded(c))
			// Protocol errors override the compatibility completion marker.
			info.StreamStatus = relaycommon.NewStreamStatus()
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
			info.StreamStatus.RecordError("terminal response.failed")
			assert.False(t, modelRequestSucceeded(c))
		})
	}
}

func TestModelResponseOutcomeRetainsReportedFailureAndResetsBeforeRetry(t *testing.T) {
	configureModelOutcomeTest(t, "memory", 0, 1)
	c, _ := newModelOutcomeContext(nextModelOutcomeUserID())
	info := genModelOutcomeRelayInfo(c, false)
	wantUsage := &dto.Usage{PromptTokens: 10, TotalTokens: 10}
	for _, failed := range []bool{true, false} {
		adaptor := modelOutcomeAdaptor{response: func(_ *gin.Context, _ *http.Response, gotInfo *relaycommon.RelayInfo) (any, *types.NewAPIError) {
			require.False(t, gotInfo.ResponseFailed, "each response attempt starts without the previous failure")
			gotInfo.ResponseFailed = failed
			return wantUsage, nil
		}}
		usage, apiErr := channel.DoResponse(adaptor, c, &http.Response{StatusCode: http.StatusOK}, info)
		require.Same(t, wantUsage, usage)
		require.Nil(t, apiErr)
		assert.True(t, info.ResponseCompleted)
		assert.Equal(t, failed, info.ResponseFailed)
		assert.Equal(t, !failed, modelRequestSucceeded(c))
	}
}
