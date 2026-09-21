package relay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	appconstant "github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	appmodel "github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/wsmanager"
	relaychannel "github.com/LIghtJUNction/api.lmm.best/relay/channel"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesWSSessionCloseWithCodeUsesRequestedStatus(t *testing.T) {
	tests := []struct {
		name   string
		code   int
		reason string
	}{
		{name: "channel policy", code: websocket.ClosePolicyViolation, reason: "channel disabled"},
		{name: "service restart", code: websocket.CloseServiceRestart, reason: wsmanager.ServiceRestartReason},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, clientPeer := responsesWSTestPair(t)
			target, targetPeer := responsesWSTestPair(t)
			session := &responsesWSSession{client: client, target: target}

			session.closeWithCode(tt.code, tt.reason)

			for _, peer := range []*websocket.Conn{clientPeer, targetPeer} {
				require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
				_, _, err := peer.ReadMessage()
				var closeErr *websocket.CloseError
				require.ErrorAs(t, err, &closeErr)
				assert.Equal(t, tt.code, closeErr.Code)
				assert.Equal(t, tt.reason, closeErr.Text)
			}
		})
	}
}

func responsesWSTestPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	serverConnection := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		serverConnection <- connection
	}))
	t.Cleanup(server.Close)

	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	var connection *websocket.Conn
	select {
	case connection = <-serverConnection:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket upgrade")
	}
	t.Cleanup(func() {
		if connection != nil {
			_ = connection.Close()
		}
		_ = peer.Close()
	})
	return connection, peer
}

func TestNormalizeResponsesWSCreateEvent(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{
			name: "wrapped",
			message: `{"type":"response.create","event_id":"evt_1","generate":false,"response":{` +
				`"model":"gpt-5","input":"hi","store":false,"previous_response_id":"resp_1","stream":true}}`,
		},
		{
			name: "flat",
			message: `{"type":"response.create","event_id":"evt_1","model":"gpt-5","input":"hi",` +
				`"store":false,"previous_response_id":"resp_1","generate":false,"stream":true,"background":true}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			create, eventID, err := normalizeResponsesWSCreateEvent([]byte(tt.message))
			require.NoError(t, err)
			assert.Equal(t, "evt_1", eventID)
			assert.Equal(t, "gpt-5", create.Request.Model)
			assert.JSONEq(t, "false", string(create.Generate))
			assert.JSONEq(t, "false", string(create.Request.Store))
			assert.Equal(t, "resp_1", create.Request.PreviousResponseID)
			assert.Nil(t, create.Request.Stream)
			assert.Nil(t, create.Request.StreamOptions)
		})
	}
}

func TestBuildResponsesWSCreateEventPreservesSessionFields(t *testing.T) {
	payload := []byte(`{"model":"gpt-5","input":"next","store":false,"previous_response_id":"resp_1",` +
		`"stream":true,"background":true,"event_id":"drop"}`)
	got, err := buildResponsesWSCreateEvent(payload, common.RawMessage(`false`))
	require.NoError(t, err)

	var data map[string]any
	require.NoError(t, common.Unmarshal(got, &data))
	assert.Equal(t, responsesWSEventTypeResponseCreate, data["type"])
	assert.Equal(t, false, data["generate"])
	assert.Equal(t, false, data["store"])
	assert.Equal(t, "resp_1", data["previous_response_id"])
	for _, key := range []string{"response", "event_id", "stream", "background", "stream_options"} {
		assert.NotContains(t, data, key)
	}
}

func TestResponseCancelWithoutActiveResponseIsDeterministic(t *testing.T) {
	session := &responsesWSSession{}
	apiErr := session.handleResponseCancel(websocket.TextMessage, []byte(`{"type":"response.cancel"}`))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, "no response is active to cancel", apiErr.Error())

	payload, err := buildResponsesWSErrorPayload("evt_cancel", apiErr)
	require.NoError(t, err)
	var event responsesWSErrorEvent
	require.NoError(t, common.Unmarshal(payload, &event))
	assert.Equal(t, "error", event.Type)
	assert.Equal(t, "evt_cancel", event.EventID)
	assert.Equal(t, http.StatusBadRequest, event.Status)
	require.NotNil(t, event.Error)
	assert.Equal(t, "no response is active to cancel", event.Error.Message)
	assert.Nil(t, session.getCurrent())
}

func TestCancelTerminalSettlesTurnOnce(t *testing.T) {
	commits := make([]bool, 0, 1)
	state := &responsesWSCallState{
		info:  &relaycommon.RelayInfo{},
		usage: &dto.Usage{},
		commitRate: func(success bool) {
			commits = append(commits, success)
		},
	}
	session := &responsesWSSession{current: state}
	session.observeUpstreamMessage([]byte(`{"type":"response.cancelled"}`))
	session.observeUpstreamMessage([]byte(`{"type":"error"}`))
	assert.Nil(t, session.getCurrent())
	assert.Equal(t, []bool{false}, commits)
}

func TestResponsesWSIncompleteAfterOutputSettlesPartialUsage(t *testing.T) {
	previousPost := postResponsesWSConsumeQuota
	t.Cleanup(func() { postResponsesWSConsumeQuota = previousPost })

	var postedUsage *dto.Usage
	postResponsesWSConsumeQuota = func(_ *gin.Context, _ *relaycommon.RelayInfo, usage *dto.Usage, _ []string) {
		postedUsage = usage
	}

	commits := make([]bool, 0, 1)
	state := &responsesWSCallState{
		info: &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5"},
		},
		usage: &dto.Usage{},
		commitRate: func(success bool) {
			commits = append(commits, success)
		},
	}
	state.info.SetEstimatePromptTokens(7)
	session := &responsesWSSession{current: state}

	session.observeUpstreamMessage([]byte(`{"type":"response.output_text.delta","delta":"billable output"}`))
	session.observeUpstreamMessage([]byte(`{"type":"response.incomplete","response":{"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`))

	assert.Nil(t, session.getCurrent())
	assert.Equal(t, []bool{true}, commits)
	require.NotNil(t, postedUsage)
	assert.Equal(t, 7, postedUsage.PromptTokens)
	assert.Equal(t, 3, postedUsage.CompletionTokens)
	assert.Equal(t, 10, postedUsage.TotalTokens)
}

func TestResponsesWSDisconnectAfterOutputSettlesPartialUsage(t *testing.T) {
	previousPost := postResponsesWSConsumeQuota
	t.Cleanup(func() { postResponsesWSConsumeQuota = previousPost })

	posted := false
	postResponsesWSConsumeQuota = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage, _ []string) {
		posted = true
	}

	commits := make([]bool, 0, 1)
	state := &responsesWSCallState{
		info: &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5"},
		},
		usage: &dto.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
		commitRate: func(success bool) {
			commits = append(commits, success)
		},
	}
	session := &responsesWSSession{current: state}

	session.observeUpstreamMessage([]byte(`{"type":"response.output_text.delta","delta":"billable output"}`))
	session.failCurrent()

	assert.Nil(t, session.getCurrent())
	assert.Equal(t, []bool{true}, commits)
	assert.True(t, posted)
}

func TestResponsesWSOutputTextBufferHonorsResponseBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, appconstant.ContextKeyResponseByteLimit, 4)
	state := &responsesWSCallState{
		info:  &relaycommon.RelayInfo{},
		usage: &dto.Usage{},
	}
	session := &responsesWSSession{c: c, current: state}

	session.observeUpstreamMessage([]byte(`{"type":"response.output_text.delta","delta":"abcdef"}`))
	session.observeUpstreamMessage([]byte(`{"type":"response.output_text.delta","delta":"gh"}`))

	assert.Equal(t, 4, state.outputText.Len())
	assert.Equal(t, "abcd", state.outputText.String())

	unicodeState := &responsesWSCallState{info: &relaycommon.RelayInfo{}, usage: &dto.Usage{}}
	unicodeSession := &responsesWSSession{c: c, current: unicodeState}
	unicodeSession.observeUpstreamMessage([]byte(`{"type":"response.output_text.delta","delta":"ab€"}`))
	assert.Equal(t, "ab", unicodeState.outputText.String())
}

func TestBuildResponsesWSErrorPayload(t *testing.T) {
	payload, err := buildResponsesWSErrorPayload("evt", types.NewErrorWithStatusCode(
		errors.New("model is required"), types.ErrorCodeInvalidRequest, http.StatusBadRequest,
	))
	require.NoError(t, err)
	assert.Contains(t, string(payload), `"status":400`)
}

func TestResponsesWSHeaderOverrideUsesSanitizedRequestClone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	sensitive := map[string]string{
		"Authorization": "Bearer client-secret", "Cookie": "session=secret",
		"Proxy-Authorization": "Basic secret", "X-Api-Key": "client-api-key",
		"X-Goog-Api-Key": "google-secret", "Sec-WebSocket-Protocol": "responses, openai-insecure-api-key.secret",
		"Sec-WebSocket-Key": "handshake-secret", "Connection": "upgrade", "Upgrade": "websocket",
	}
	for name, value := range sensitive {
		c.Request.Header.Set(name, value)
	}
	c.Request.Header.Set("X-Trace-Id", "trace-123")

	sanitized := sanitizedResponsesWSHeaderContext(c)
	require.NotSame(t, c.Request, sanitized.Request)
	for name, value := range sensitive {
		assert.Empty(t, sanitized.Request.Header.Get(name), name)
		assert.Equal(t, value, c.Request.Header.Get(name), name+" live request mutated")
	}
	assert.Equal(t, "trace-123", sanitized.Request.Header.Get("X-Trace-Id"))
}

func TestResponsesWSHeaderOverrideCannotCopySensitiveClientValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Request.Header.Set("Authorization", "Bearer client-secret")
	c.Request.Header.Set("Cookie", "session=secret")
	c.Request.Header.Set("Sec-WebSocket-Protocol", "responses, openai-insecure-api-key.secret")
	c.Request.Header.Set("X-Trace-Id", "trace-123")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "operator-key", HeadersOverride: map[string]any{
		"X-Copied-Auth":     "{client_header:Authorization}",
		"X-Copied-Cookie":   "{client_header:Cookie}",
		"X-Copied-Protocol": "{client_header:Sec-WebSocket-Protocol}",
		"X-Trace":           "{client_header:X-Trace-Id}",
		"Authorization":     "Bearer {api_key}",
	}}}

	headers, err := relaychannel.ResolveHeaderOverride(info, sanitizedResponsesWSHeaderContext(c))
	require.NoError(t, err)
	assert.NotContains(t, headers, "x-copied-auth")
	assert.NotContains(t, headers, "x-copied-cookie")
	assert.NotContains(t, headers, "x-copied-protocol")
	assert.Equal(t, "trace-123", headers["x-trace"])
	assert.Equal(t, "Bearer operator-key", headers["authorization"])
}

func TestMergeResponsesWSBetaHeaderTokens(t *testing.T) {
	tests := []struct{ name, initial, want string }{
		{name: "openai", want: "responses_websockets=2026-02-06"},
		{name: "codex", initial: "responses=experimental", want: "responses=experimental, responses_websockets=2026-02-06"},
		{name: "deduplicate", initial: "responses=experimental, responses_websockets=2026-02-06, responses=experimental", want: "responses=experimental, responses_websockets=2026-02-06"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			if tt.initial != "" {
				header.Set("OpenAI-Beta", tt.initial)
			}
			mergeHeaderTokens(header, "OpenAI-Beta", "responses_websockets=2026-02-06")
			assert.Equal(t, tt.want, header.Get("OpenAI-Beta"))
		})
	}
}

func TestResponsesWSRequiredBetaTokens(t *testing.T) {
	openAI := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: appconstant.ChannelTypeOpenAI}}
	codex := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: appconstant.ChannelTypeCodex}}
	assert.Equal(t, []string{"responses_websockets=2026-02-06"}, responsesWSRequiredBetaTokens(openAI))
	assert.Equal(t, []string{"responses=experimental", "responses_websockets=2026-02-06"}, responsesWSRequiredBetaTokens(codex))

	header := http.Header{"Openai-Beta": []string{"operator=enabled"}}
	mergeHeaderTokens(header, "OpenAI-Beta", responsesWSRequiredBetaTokens(codex)...)
	assert.Equal(t, "operator=enabled, responses=experimental, responses_websockets=2026-02-06", header.Get("OpenAI-Beta"))
}

func TestResponsesWSRevalidatesEachCreateBeforeRateLimit(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	session := &responsesWSSession{c: c, revalidateAuth: func(*gin.Context) *types.NewAPIError {
		return types.NewErrorWithStatusCode(errors.New("token authentication failed"), types.ErrorCodeAccessDenied, http.StatusUnauthorized)
	}}
	apiErr := session.handleResponseCreate(responsesWSCreateRequest{Request: dto.OpenAIResponsesRequest{Model: "gpt-5"}})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	assert.Nil(t, session.getCurrent())
}

func TestLockedResponsesWSChannelNeverFallsBackToRandomSelection(t *testing.T) {
	previous := loadResponsesWSLockedChannel
	t.Cleanup(func() { loadResponsesWSLockedChannel = previous })
	loadResponsesWSLockedChannel = func(id int) (*appmodel.Channel, error) {
		return nil, errors.New("gone")
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, appconstant.ContextKeyUsingGroup, "default")
	retry := &service.RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-5", Retry: common.GetPointer(0)}
	channel, apiErr := selectResponsesWSChannelForSession(c, "gpt-5", retry, &appmodel.Channel{Id: 42})
	assert.Nil(t, channel)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "locked channel 42 is unavailable")
}

func TestResponsesWSExistingTargetRejectsRevokedModelAccess(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	reused := false
	session := &responsesWSSession{
		c:             c,
		target:        &websocket.Conn{},
		lockedModel:   "gpt-5",
		lockedChannel: &appmodel.Channel{Id: 42},
		revalidateAuth: func(c *gin.Context) *types.NewAPIError {
			common.SetContextKey(c, appconstant.ContextKeyTokenModelLimitEnabled, true)
			common.SetContextKey(c, appconstant.ContextKeyTokenModelLimit, map[string]bool{"gpt-4.1": true})
			return nil
		},
		reuseTarget: func(responsesWSCreateRequest, middleware.ModelRequestRateLimitCommit) *types.NewAPIError {
			reused = true
			return nil
		},
	}

	apiErr := session.handleResponseCreate(responsesWSCreateRequest{Request: dto.OpenAIResponsesRequest{Model: "gpt-5"}})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "token is not allowed to use model gpt-5")
	assert.False(t, reused)
}

func TestResponsesWSExistingTargetRejectsLockedChannelRemovedFromGroup(t *testing.T) {
	withResponsesWSChannelAuthorizationStubs(t, true, false)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, appconstant.ContextKeyChannelId, 99)
	reused := false
	session := existingTargetResponsesWSSession(c, &reused)
	session.revalidateAuth = func(c *gin.Context) *types.NewAPIError {
		common.SetContextKey(c, appconstant.ContextKeyTokenModelLimitEnabled, false)
		common.SetContextKey(c, appconstant.ContextKeyUsingGroup, "refreshed-group")
		return nil
	}

	apiErr := session.handleResponseCreate(responsesWSCreateRequest{Request: dto.OpenAIResponsesRequest{Model: "gpt-5"}})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "no longer available for group refreshed-group")
	assert.False(t, reused)
	assert.Equal(t, 99, common.GetContextKeyInt(c, appconstant.ContextKeyChannelId), "selected-channel context must not refresh before authorization succeeds")
}

func TestResponsesWSExistingTargetReusesWhenAuthorizationUnchanged(t *testing.T) {
	withResponsesWSChannelAuthorizationStubs(t, true, true)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	reused := false
	session := existingTargetResponsesWSSession(c, &reused)
	session.revalidateAuth = func(c *gin.Context) *types.NewAPIError {
		common.SetContextKey(c, appconstant.ContextKeyTokenModelLimitEnabled, false)
		common.SetContextKey(c, appconstant.ContextKeyUsingGroup, "default")
		return nil
	}

	apiErr := session.handleResponseCreate(responsesWSCreateRequest{Request: dto.OpenAIResponsesRequest{Model: "gpt-5"}})
	require.Nil(t, apiErr)
	assert.True(t, reused)
	assert.Equal(t, 42, common.GetContextKeyInt(c, appconstant.ContextKeyChannelId))
}

func existingTargetResponsesWSSession(c *gin.Context, reused *bool) *responsesWSSession {
	return &responsesWSSession{
		c:             c,
		target:        &websocket.Conn{},
		lockedModel:   "gpt-5",
		lockedChannel: &appmodel.Channel{Id: 42},
		reuseTarget: func(responsesWSCreateRequest, middleware.ModelRequestRateLimitCommit) *types.NewAPIError {
			*reused = true
			return nil
		},
	}
}

func withResponsesWSChannelAuthorizationStubs(t *testing.T, loadOK, available bool) {
	previousLoad := loadResponsesWSLockedChannel
	previousAvailable := isResponsesWSChannelAvailable
	t.Cleanup(func() {
		loadResponsesWSLockedChannel = previousLoad
		isResponsesWSChannelAvailable = previousAvailable
	})
	loadResponsesWSLockedChannel = func(id int) (*appmodel.Channel, error) {
		if !loadOK {
			return nil, errors.New("gone")
		}
		return &appmodel.Channel{Id: id, Type: appconstant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "operator-key"}, nil
	}
	isResponsesWSChannelAvailable = func(group, model string, channelID int) bool {
		return available
	}
}
