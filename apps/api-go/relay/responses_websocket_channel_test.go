package relay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	appconstant "github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	appmodel "github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const responsesWSChannelTestModel = "ws-channel-native-test"

func responsesWSNativeTestChannel(channelType int, baseURL string) *appmodel.Channel {
	channel := &appmodel.Channel{Id: 42, Type: channelType, Status: common.ChannelStatusEnabled, Key: "upstream-key", BaseURL: &baseURL, Group: "default", Models: responsesWSChannelTestModel}
	if !responsesWSChannelDefaultEnabled(channelType) {
		channel.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: common.GetPointer(true)})
	}
	if channelType == appconstant.ChannelTypeAdvancedCustom {
		channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{IncomingPath: "/v1/responses", UpstreamPath: "/v1/responses"}}}})
	}
	if channelType == appconstant.ChannelTypeCodex {
		channel.Key = `{"access_token":"upstream-key","account_id":"account-test"}`
	}
	return channel
}

func TestResponsesWSUnsupportedChannelsNeverPrepareBillOrDial(t *testing.T) {
	previousLoad, previousAvailable := loadResponsesWSLockedChannel, isResponsesWSChannelAvailable
	previousPost := postResponsesWSConsumeQuota
	t.Cleanup(func() {
		loadResponsesWSLockedChannel, isResponsesWSChannelAvailable = previousLoad, previousAvailable
		postResponsesWSConsumeQuota = previousPost
	})
	var dialed, posted atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { dialed.Add(1) }))
	t.Cleanup(upstream.Close)
	postResponsesWSConsumeQuota = func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) { posted.Add(1) }
	isResponsesWSChannelAvailable = func(string, string, int) bool { return true }
	for channelType := 0; channelType <= appconstant.ChannelTypeDummy; channelType++ {
		if responsesWSChannelSupportsType(channelType) {
			continue
		}
		t.Run(fmt.Sprint(channelType), func(t *testing.T) {
			channel := responsesWSNativeTestChannel(channelType, upstream.URL)
			loadResponsesWSLockedChannel = func(int) (*appmodel.Channel, error) { return channel, nil }
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			common.SetContextKey(c, appconstant.ContextKeyUsingGroup, "default")
			session := &responsesWSSession{c: c, lockedChannel: channel}
			commits := []bool{}
			apiErr := session.connectAndSendFirst(responsesWSCreateRequest{Request: dto.OpenAIResponsesRequest{Model: responsesWSChannelTestModel}}, func(success bool) { commits = append(commits, success) })
			require.NotNil(t, apiErr)
			assert.Equal(t, types.ErrorCode("responses_websocket_unsupported"), apiErr.GetErrorCode())
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.Equal(t, []bool{false}, commits)
			assert.Empty(t, c.GetStringSlice("use_channel"))
			_, prepared := common.GetContextKey(c, appconstant.ContextKeyRelayInfo)
			assert.False(t, prepared, "no billing call was even prepared")
		})
	}
	require.Zero(t, dialed.Load())
	require.Zero(t, posted.Load())
}

func TestResponsesWSIneligibleSettingsNeverPrepareBillOrDial(t *testing.T) {
	previousLoad, previousAvailable, previousPost := loadResponsesWSLockedChannel, isResponsesWSChannelAvailable, postResponsesWSConsumeQuota
	t.Cleanup(func() {
		loadResponsesWSLockedChannel, isResponsesWSChannelAvailable, postResponsesWSConsumeQuota = previousLoad, previousAvailable, previousPost
	})
	var dialed, posted atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { dialed.Add(1) }))
	t.Cleanup(upstream.Close)
	postResponsesWSConsumeQuota = func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage, []string) { posted.Add(1) }
	isResponsesWSChannelAvailable = func(string, string, int) bool { return true }
	type testCase struct {
		name        string
		channelType int
		change      func(*appmodel.Channel)
		code        types.ErrorCode
	}
	cases := []testCase{}
	for _, channelType := range []int{1, 57, 58, 59, 60} {
		cases = append(cases, testCase{fmt.Sprintf("disabled-%d", channelType), channelType, func(ch *appmodel.Channel) {
			ch.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: common.GetPointer(false)})
		}, "responses_websocket_disabled"})
	}
	for _, channelType := range []int{58, 59, 60} {
		cases = append(cases, testCase{fmt.Sprintf("unset-%d", channelType), channelType, func(ch *appmodel.Channel) { ch.SetSetting(dto.ChannelSettings{}) }, "responses_websocket_disabled"})
		cases = append(cases, testCase{fmt.Sprintf("unsupported-proxy-%d", channelType), channelType, func(ch *appmodel.Channel) {
			ch.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: common.GetPointer(true), Proxy: "http://proxy.example.com"})
		}, "responses_websocket_unsupported"})
	}
	for _, route := range []dto.AdvancedCustomRoute{
		{IncomingPath: "/v1/responses", UpstreamPath: "/v1/chat/completions", Converter: "openai_responses_to_openai_chat_completions"},
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/v1/responses"},
		{IncomingPath: "/v1/responses", UpstreamPath: "/v1/messages"},
		{IncomingPath: "/v1/responses", UpstreamPath: "https://user:secret@example.com/v1/responses"},
		{IncomingPath: "/v1/responses", UpstreamPath: "https://example.com/v1/responses#fragment"},
		{IncomingPath: "/v1/responses", UpstreamPath: "/v1/responses", Auth: &dto.AdvancedCustomRouteAuth{Type: " header ", Name: "Sec-WebSocket-Key", Value: "{api_key}"}},
		{IncomingPath: "/v1/responses", UpstreamPath: "/v1/responses", Auth: &dto.AdvancedCustomRouteAuth{Type: "header", Name: "X Invalid", Value: "{api_key}"}},
		{IncomingPath: "/v1/responses", UpstreamPath: "/v1/responses", Auth: &dto.AdvancedCustomRouteAuth{Type: "header", Name: "X-Custom-Key", Value: "invalid\r\nheader"}},
	} {
		cases = append(cases, testCase{fmt.Sprintf("invalid-route-%d", len(cases)), appconstant.ChannelTypeAdvancedCustom, func(ch *appmodel.Channel) {
			ch.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{route}}})
		}, "responses_websocket_unsupported"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channel := responsesWSNativeTestChannel(tc.channelType, upstream.URL)
			tc.change(channel)
			loadResponsesWSLockedChannel = func(int) (*appmodel.Channel, error) { return channel, nil }
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			common.SetContextKey(c, appconstant.ContextKeyUsingGroup, "default")
			session := &responsesWSSession{c: c, lockedChannel: channel}
			commits := []bool{}
			apiErr := session.connectAndSendFirst(responsesWSCreateRequest{Request: dto.OpenAIResponsesRequest{Model: responsesWSChannelTestModel}}, func(success bool) { commits = append(commits, success) })
			require.NotNil(t, apiErr)
			require.Equal(t, tc.code, apiErr.GetErrorCode())
			require.True(t, types.IsSkipRetryError(apiErr))
			require.Equal(t, []bool{false}, commits)
			require.Empty(t, c.GetStringSlice("use_channel"))
			_, prepared := common.GetContextKey(c, appconstant.ContextKeyRelayInfo)
			require.False(t, prepared)
		})
	}
	require.Zero(t, dialed.Load())
	require.Zero(t, posted.Load())
}

func TestResponsesWSChannelEligibilityDefaultsAndRouteRestrictions(t *testing.T) {
	for _, channelType := range []int{1, 57, 58, 59, 60} {
		for _, enabled := range []*bool{nil, common.GetPointer(false), common.GetPointer(true)} {
			channel := responsesWSNativeTestChannel(channelType, "https://example.com")
			channel.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: enabled})
			apiErr := responsesWSChannelEligibility(channel, "/v1/responses", responsesWSChannelTestModel)
			wantEnabled := responsesWSChannelDefaultEnabled(channelType)
			if enabled != nil {
				wantEnabled = *enabled
			}
			if wantEnabled {
				require.Nil(t, apiErr, "type=%d enabled=%v", channelType, enabled)
			} else {
				require.NotNil(t, apiErr)
				require.True(t, types.IsSkipRetryError(apiErr))
			}
		}
	}
	for _, route := range []dto.AdvancedCustomRoute{
		{IncomingPath: "/v1/chat/completions", UpstreamPath: "/v1/responses"},
		{IncomingPath: "/v1/responses", UpstreamPath: "/v1/chat/completions", Converter: "openai_responses_to_openai_chat_completions"},
		{IncomingPath: "/v1/responses", UpstreamPath: "/v1/messages"},
		{IncomingPath: "/v1/responses", UpstreamPath: "https://user:secret@example.com/v1/responses"},
		{IncomingPath: "/v1/responses", UpstreamPath: "/v1/responses", Auth: &dto.AdvancedCustomRouteAuth{Type: "header", Name: "Sec-WebSocket-Key", Value: "{key}"}},
	} {
		channel := responsesWSNativeTestChannel(appconstant.ChannelTypeAdvancedCustom, "https://example.com")
		channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{route}}})
		apiErr := responsesWSChannelEligibility(channel, "/v1/responses", responsesWSChannelTestModel)
		require.NotNil(t, apiErr, "%+v", route)
		require.True(t, types.IsSkipRetryError(apiErr))
	}
}

type responsesWSNativeHandshake struct {
	path, authorization, account, custom, query, organization string
}

func responsesWSNativeTestProvider(t *testing.T, terminalTypes ...string) (*httptest.Server, <-chan responsesWSNativeHandshake, *atomic.Int32) {
	t.Helper()
	handshakes := make(chan responsesWSNativeHandshake, 16)
	connections := &atomic.Int32{}
	turns := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		handshakes <- responsesWSNativeHandshake{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("chatgpt-account-id"), r.Header.Get("X-Custom-Key"), r.URL.Query().Get("api_key"), r.Header.Get("OpenAI-Organization")}
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var event map[string]any
			if common.Unmarshal(payload, &event) != nil || event["type"] != "response.create" || event["model"] != responsesWSChannelTestModel {
				return
			}
			turn := int(turns.Add(1))
			terminal := "response.completed"
			if turn <= len(terminalTypes) {
				terminal = terminalTypes[turn-1]
			}
			if conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":%q,"response":{"id":"response-%d","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`, terminal, turn))) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, handshakes, connections
}

func responsesWSNativeTestSession(t *testing.T, channel *appmodel.Channel) (*responsesWSSession, *websocket.Conn, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	previousDB, previousRedis := appmodel.DB, common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&appmodel.User{}, &appmodel.Channel{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	userID := int(time.Now().UnixNano())
	require.NoError(t, db.Create(&appmodel.User{Id: userID, Username: "ws-channel-test", Role: common.RoleAdminUser}).Error)
	require.NoError(t, db.Create(channel).Error)
	appmodel.DB, common.RedisEnabled = db, false
	t.Cleanup(func() { appmodel.DB, common.RedisEnabled = previousDB, previousRedis; _ = sqlDB.Close() })
	previousLoad, previousAvailable, previousPost := loadResponsesWSLockedChannel, isResponsesWSChannelAvailable, postResponsesWSConsumeQuota
	previousRatio := ratio_setting.ModelRatio2JSONString()
	ratios := ratio_setting.GetModelRatioCopy()
	ratios[responsesWSChannelTestModel] = 0
	raw, err := common.Marshal(ratios)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(raw)))
	quota := operation_setting.GetQuotaSetting()
	previousFreePreConsume, previousCountToken := quota.EnableFreeModelPreConsume, appconstant.CountToken
	previousRateEnabled := setting.ModelRequestRateLimitEnabled
	quota.EnableFreeModelPreConsume, appconstant.CountToken = false, false
	setting.ModelRequestRateLimitEnabled = false
	posted, authenticated := &atomic.Int32{}, &atomic.Int32{}
	postResponsesWSConsumeQuota = func(_ *gin.Context, _ *relaycommon.RelayInfo, usage *dto.Usage, _ []string) {
		if usage.TotalTokens == 2 {
			posted.Add(1)
		}
	}
	loadResponsesWSLockedChannel = func(int) (*appmodel.Channel, error) { return channel, nil }
	isResponsesWSChannelAvailable = func(string, string, int) bool { return true }
	t.Cleanup(func() {
		loadResponsesWSLockedChannel, isResponsesWSChannelAvailable, postResponsesWSConsumeQuota = previousLoad, previousAvailable, previousPost
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatio))
		quota.EnableFreeModelPreConsume, appconstant.CountToken = previousFreePreConsume, previousCountToken
		setting.ModelRequestRateLimitEnabled = previousRateEnabled
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Set("id", userID)
	common.SetContextKey(c, appconstant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, appconstant.ContextKeyTokenGroup, "default")
	client, peer := responsesWSTestPair(t)
	session := &responsesWSSession{c: c, client: client, lockedChannel: channel, revalidateAuth: func(*gin.Context) *types.NewAPIError { authenticated.Add(1); return nil }}
	t.Cleanup(func() { session.closeTarget(); session.targetReaders.Wait() })
	return session, peer, posted, authenticated
}

func responsesWSNativeTestTurn(t *testing.T, session *responsesWSSession, peer *websocket.Conn, eventID string) {
	t.Helper()
	require.Nil(t, session.handleResponseCreate(responsesWSCreateRequest{EventID: eventID, Request: dto.OpenAIResponsesRequest{Model: responsesWSChannelTestModel, Input: common.RawMessage(`"hi"`)}}))
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, payload, err := peer.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(payload), `"response"`)
	require.Nil(t, session.getCurrent())
}

func TestResponsesWSSupportedChannelsUseNativeProtocolAndCredentialPlacement(t *testing.T) {
	for _, channelType := range []int{1, 57, 58, 59, 60} {
		t.Run(strconv.Itoa(channelType), func(t *testing.T) {
			upstream, handshakes, connections := responsesWSNativeTestProvider(t)
			channel := responsesWSNativeTestChannel(channelType, upstream.URL)
			session, peer, posted, authenticated := responsesWSNativeTestSession(t, channel)
			responsesWSNativeTestTurn(t, session, peer, "first")
			responsesWSNativeTestTurn(t, session, peer, "second")
			handshake := <-handshakes
			path := "/v1/responses"
			if channelType == appconstant.ChannelTypeCodex {
				path = "/backend-api/codex/responses"
				require.Equal(t, "account-test", handshake.account)
			}
			require.Equal(t, path, handshake.path)
			require.Equal(t, "Bearer upstream-key", handshake.authorization)
			require.Equal(t, int32(1), connections.Load(), "unchanged turns reuse the physical connection")
			require.Equal(t, int32(2), authenticated.Load())
			require.Equal(t, int32(2), posted.Load(), "native terminal usage settles each turn once")
		})
	}
	for _, authType := range []string{"header", "query", "none"} {
		t.Run(authType, func(t *testing.T) {
			upstream, handshakes, _ := responsesWSNativeTestProvider(t)
			channel := responsesWSNativeTestChannel(appconstant.ChannelTypeAdvancedCustom, upstream.URL)
			config := channel.GetOtherSettings()
			name := "X-Custom-Key"
			if authType == "query" {
				name = "api_key"
			}
			config.AdvancedCustom.Routes[0].Auth = &dto.AdvancedCustomRouteAuth{Type: authType, Name: name, Value: "{api_key}"}
			channel.SetOtherSettings(config)
			session, peer, _, _ := responsesWSNativeTestSession(t, channel)
			responsesWSNativeTestTurn(t, session, peer, "auth")
			handshake := <-handshakes
			require.Empty(t, handshake.authorization)
			if authType == "header" {
				require.Equal(t, "upstream-key", handshake.custom)
			}
			if authType == "query" {
				require.Equal(t, "upstream-key", handshake.query)
			}
		})
	}
}

func TestResponsesWSAdvancedCustomReconnectsForConnectionChanges(t *testing.T) {
	changes := map[string]func(*appmodel.Channel){
		"path": func(channel *appmodel.Channel) {
			settings := channel.GetOtherSettings()
			settings.AdvancedCustom.Routes[0].UpstreamPath = "/new/v1/responses"
			channel.SetOtherSettings(settings)
		},
		"auth": func(channel *appmodel.Channel) {
			settings := channel.GetOtherSettings()
			settings.AdvancedCustom.Routes[0].Auth = &dto.AdvancedCustomRouteAuth{Type: "header", Name: "X-Custom-Key", Value: "{api_key}"}
			channel.SetOtherSettings(settings)
		},
		"credential": func(channel *appmodel.Channel) { channel.Key = "replacement-key" },
		"header": func(channel *appmodel.Channel) {
			channel.HeaderOverride = common.GetPointer(`{"X-Connection-Version":"2"}`)
		},
		"parameter header": func(channel *appmodel.Channel) {
			channel.ParamOverride = common.GetPointer(`{"operations":[{"mode":"set_header","path":"Authorization","value":"Bearer new"}]}`)
		},
		"base URL": func(channel *appmodel.Channel) { channel.BaseURL = common.GetPointer(*channel.BaseURL + "/gateway") },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			upstream, handshakes, connections := responsesWSNativeTestProvider(t)
			channel := responsesWSNativeTestChannel(appconstant.ChannelTypeAdvancedCustom, upstream.URL)
			if name == "parameter header" {
				channel.ParamOverride = common.GetPointer(`{"operations":[{"mode":"set_header","path":"Authorization","value":"Bearer old"}]}`)
			}
			session, peer, posted, authenticated := responsesWSNativeTestSession(t, channel)
			responsesWSNativeTestTurn(t, session, peer, "before")
			before := <-handshakes
			oldTarget := session.getTarget()
			change(channel)
			responsesWSNativeTestTurn(t, session, peer, "after")
			after := <-handshakes
			require.NotSame(t, oldTarget, session.getTarget())
			require.Equal(t, int32(2), connections.Load())
			require.Equal(t, channel.Id, session.lockedChannel.Id)
			require.Equal(t, responsesWSChannelTestModel, session.lockedModel)
			require.Equal(t, int32(2), posted.Load())
			require.Equal(t, int32(2), authenticated.Load())
			if name == "path" || name == "base URL" {
				require.NotEqual(t, before.path, after.path)
			}
			if name == "auth" {
				require.Equal(t, "upstream-key", after.custom)
			}
			if name == "credential" {
				require.Equal(t, "Bearer replacement-key", after.authorization)
			}
			if name == "parameter header" {
				require.Equal(t, "Bearer old", before.authorization)
				require.Equal(t, "Bearer new", after.authorization)
			}
		})
	}
}

func TestResponsesWSDisabledLockedChannelCannotDialOrSettleAnotherTurn(t *testing.T) {
	upstream, _, connections := responsesWSNativeTestProvider(t)
	channel := responsesWSNativeTestChannel(appconstant.ChannelTypeNewAPI, upstream.URL)
	session, peer, posted, authenticated := responsesWSNativeTestSession(t, channel)
	responsesWSNativeTestTurn(t, session, peer, "before-disable")
	channel.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: common.GetPointer(false)})
	apiErr := session.handleResponseCreate(responsesWSCreateRequest{Request: dto.OpenAIResponsesRequest{Model: responsesWSChannelTestModel}})
	require.NotNil(t, apiErr)
	require.Equal(t, types.ErrorCode("responses_websocket_disabled"), apiErr.GetErrorCode())
	require.True(t, types.IsSkipRetryError(apiErr))
	require.Equal(t, int32(1), connections.Load())
	require.Equal(t, int32(1), posted.Load())
	require.Equal(t, int32(2), authenticated.Load())
	require.Nil(t, session.getTarget(), "disabled channels cannot forward later controls over the old connection")
}

func TestResponsesWSRejectedCreateDoesNotOrphanAnAdmittedTurn(t *testing.T) {
	for _, reject := range []string{"disabled", "unsupported-model-route"} {
		t.Run(reject, func(t *testing.T) {
			release, received := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				close(received)
				<-release
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`))
				_, _, _ = conn.ReadMessage()
			}))
			t.Cleanup(upstream.Close)
			t.Cleanup(unblock)
			channel := responsesWSNativeTestChannel(appconstant.ChannelTypeAdvancedCustom, upstream.URL)
			channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{IncomingPath: "/v1/responses", UpstreamPath: "/v1/responses", Models: []string{responsesWSChannelTestModel}}}}})
			session, peer, posted, _ := responsesWSNativeTestSession(t, channel)
			create := responsesWSCreateRequest{EventID: "admitted", Request: dto.OpenAIResponsesRequest{Model: responsesWSChannelTestModel, Input: common.RawMessage(`"hi"`)}}
			require.Nil(t, session.handleResponseCreate(create))
			select {
			case <-received:
			case <-time.After(time.Second):
				t.Fatal("upstream did not receive the admitted create")
			}
			state, target := session.getCurrent(), session.getTarget()
			require.NotNil(t, state)
			committed := make(chan bool, 1)
			commitRate := state.commitRate
			state.commitRate = func(success bool) { commitRate(success); committed <- success }
			if reject == "disabled" {
				channel.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: common.GetPointer(false)})
			} else {
				create.Request.Model = "no-native-route"
			}
			apiErr := session.handleResponseCreate(create)
			require.NotNil(t, apiErr)
			require.True(t, types.IsSkipRetryError(apiErr))
			require.Same(t, state, session.getCurrent())
			require.Same(t, target, session.getTarget())
			require.Zero(t, posted.Load())
			unblock()
			require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
			_, _, err := peer.ReadMessage()
			require.NoError(t, err)
			require.Nil(t, session.getCurrent(), "terminal delivery must release the admitted reservation")
			require.Equal(t, int32(1), posted.Load())
			select {
			case successful := <-committed:
				require.True(t, successful, "the original turn keeps its own successful delivery")
			case <-time.After(time.Second):
				t.Fatal("the admitted rate reservation was not finalized")
			}
		})
	}
}

func TestResponsesWSOrganizationRemovalReconnectsWithoutTheOldHeader(t *testing.T) {
	upstream, handshakes, connections := responsesWSNativeTestProvider(t)
	channel := responsesWSNativeTestChannel(appconstant.ChannelTypeOpenAI, upstream.URL)
	channel.OpenAIOrganization = common.GetPointer("org-old")
	session, peer, _, _ := responsesWSNativeTestSession(t, channel)
	responsesWSNativeTestTurn(t, session, peer, "organization")
	require.Equal(t, "org-old", (<-handshakes).organization)
	channel.OpenAIOrganization = nil
	responsesWSNativeTestTurn(t, session, peer, "organization-cleared")
	require.Empty(t, (<-handshakes).organization)
	require.Equal(t, int32(2), connections.Load())
}

func TestResponsesWSMultiKeyConnectionReusesItsEnabledCredential(t *testing.T) {
	upstream, handshakes, connections := responsesWSNativeTestProvider(t)
	channel := responsesWSNativeTestChannel(appconstant.ChannelTypeSub2API, upstream.URL)
	channel.Key = "first-key\nsecond-key"
	channel.ChannelInfo = appmodel.ChannelInfo{IsMultiKey: true, MultiKeyMode: appconstant.MultiKeyModePolling, MultiKeyStatusList: map[int]int{}}
	session, peer, _, _ := responsesWSNativeTestSession(t, channel)
	responsesWSNativeTestTurn(t, session, peer, "first-key")
	first := <-handshakes
	responsesWSNativeTestTurn(t, session, peer, "same-key")
	require.Equal(t, int32(1), connections.Load(), "per-turn key rotation must not change an established connection's credential")
	channel.ChannelInfo.MultiKeyStatusList[session.connectionKeyIndex] = common.ChannelStatusManuallyDisabled
	responsesWSNativeTestTurn(t, session, peer, "replacement-key")
	require.NotEqual(t, first.authorization, (<-handshakes).authorization)
	require.Equal(t, int32(2), connections.Load())
}

func TestResponsesWSRetiredTargetCannotForwardOrSettleANewerTurn(t *testing.T) {
	client, peer := responsesWSTestPair(t)
	oldTarget, _ := responsesWSTestPair(t)
	newTarget, _ := responsesWSTestPair(t)
	session := &responsesWSSession{client: client, target: oldTarget}
	session.closeTarget()
	session.setTarget(newTarget)
	state := responsesWSCorrelationState("new-stream")
	commits := []bool{}
	state.commitRate = func(success bool) { commits = append(commits, success) }
	require.True(t, session.tryReserveCurrent(state))
	current, err := session.forwardTargetMessage(oldTarget, websocket.TextMessage, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":999}}}`))
	require.NoError(t, err)
	require.False(t, current)
	require.Same(t, state, session.getCurrent())
	require.Empty(t, commits)
	current, err = session.forwardTargetMessage(newTarget, websocket.TextMessage, []byte(`{"type":"response.cancelled"}`))
	require.NoError(t, err)
	require.True(t, current)
	require.Equal(t, []bool{false}, commits)
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
	_, payload, err := peer.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(payload), "response.cancelled")
	session.closeTarget()
}

func TestResponsesWSShutdownUnblocksAStalledClientWrite(t *testing.T) {
	client, _ := responsesWSTestPair(t)
	target, _ := responsesWSTestPair(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	session := &responsesWSSession{c: c, client: client, target: target}
	written := make(chan struct{})
	go func() {
		_, _ = session.forwardTargetMessage(target, websocket.TextMessage, []byte(strings.Repeat("x", 8*1024*1024)))
		close(written)
	}()
	// Observe the forwarding lock, without waiting for the client to read the
	// large frame. Shutdown must close the socket before waiting on this lock.
	require.Eventually(t, func() bool {
		if session.targetReadMu.TryLock() {
			session.targetReadMu.Unlock()
			return false
		}
		return true
	}, time.Second, time.Millisecond)
	closed := make(chan struct{})
	go func() { session.closeWithCode(websocket.CloseServiceRestart, "restart"); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown waited on the stalled client write")
	}
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("client write was not unblocked")
	}
}

func TestResponsesWSNewChannelsKeepPartialBillingSeparateFromSuccessfulRateSlots(t *testing.T) {
	for _, channelType := range []int{58, 59, 60} {
		t.Run(strconv.Itoa(channelType), func(t *testing.T) {
			upstream, _, connections := responsesWSNativeTestProvider(t, "response.failed", "response.completed")
			channel := responsesWSNativeTestChannel(channelType, upstream.URL)
			session, peer, posted, _ := responsesWSNativeTestSession(t, channel)
			previousRedis := common.RedisEnabled
			previousDuration, previousCount, previousSuccess := setting.ModelRequestRateLimitDurationMinutes, setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount
			previousGroups := setting.ModelRequestRateLimitGroup2JSONString()
			common.RedisEnabled = false
			setting.ModelRequestRateLimitEnabled = true
			setting.ModelRequestRateLimitDurationMinutes, setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = 1, 0, 1
			require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(`{}`))
			t.Cleanup(func() {
				session.closeTarget()
				session.targetReaders.Wait()
				common.RedisEnabled = previousRedis
				setting.ModelRequestRateLimitDurationMinutes, setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = previousDuration, previousCount, previousSuccess
				require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(previousGroups))
			})
			responsesWSNativeTestTurn(t, session, peer, "failed")
			responsesWSNativeTestTurn(t, session, peer, "successful")
			apiErr := session.handleResponseCreate(responsesWSCreateRequest{Request: dto.OpenAIResponsesRequest{Model: responsesWSChannelTestModel}})
			require.NotNil(t, apiErr)
			require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
			require.Equal(t, int32(2), posted.Load(), "failed billable output is charged without taking a success slot")
			require.Equal(t, int32(1), connections.Load())
		})
	}
}

func TestResponsesWSSelectionFiltersCapabilityBeforeUsedChannelTracking(t *testing.T) {
	previousDB, previousMemory := appmodel.DB, common.MemoryCacheEnabled
	previousRetryTimes := common.RetryTimes
	common.RetryTimes = 3
	t.Cleanup(func() { common.RetryTimes = previousRetryTimes })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&appmodel.Channel{}, &appmodel.Ability{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	appmodel.DB, common.MemoryCacheEnabled = db, true
	t.Cleanup(func() {
		// Clear this fixture's process-wide cache before restoring the database.
		_ = db.Where("1 = 1").Delete(&appmodel.Channel{}).Error
		_ = appmodel.InitChannelCache()
		appmodel.DB, common.MemoryCacheEnabled = previousDB, previousMemory
		if previousDB != nil && previousMemory {
			_ = appmodel.InitChannelCache()
		}
		_ = sqlDB.Close()
	})
	unsupported := responsesWSNativeTestChannel(appconstant.ChannelTypeAnthropic, "https://example.com")
	unsupported.Priority = common.GetPointer(int64(100))
	supported := responsesWSNativeTestChannel(appconstant.ChannelTypeNewAPI, "https://example.com")
	supported.Id, supported.Priority = 43, common.GetPointer(int64(1))
	require.NoError(t, unsupported.Insert())
	require.NoError(t, supported.Insert())
	require.NoError(t, appmodel.InitChannelCache())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	common.SetContextKey(c, appconstant.ContextKeyUsingGroup, "default")
	retry := &service.RetryParam{Ctx: c, TokenGroup: "default", ModelName: responsesWSChannelTestModel, RequestPath: "/v1/responses", Retry: common.GetPointer(0)}
	selected, apiErr := selectResponsesWSChannel(c, responsesWSChannelTestModel, retry)
	require.Nil(t, apiErr)
	require.Equal(t, supported.Id, selected.Id)
	require.Zero(t, retry.GetRetry(), "local filtering does not spend an upstream retry")
	require.Contains(t, retry.ExcludedChannelIDs, unsupported.Id)
	require.Empty(t, c.GetStringSlice("use_channel"))
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, selected, responsesWSChannelTestModel))
	common.SetContextKey(c, appconstant.ContextKeyTokenSpecificChannelId, strconv.Itoa(unsupported.Id))
	_, apiErr = selectResponsesWSChannel(c, responsesWSChannelTestModel, retry)
	require.NotNil(t, apiErr)
	require.True(t, types.IsSkipRetryError(apiErr))
	assert.Empty(t, c.GetStringSlice("use_channel"))
	// An unsupported selection at the retry limit used to leave a hidden
	// resetNextTry flag, which skipped B's next priority after moving from A.
	require.NoError(t, db.Model(supported).Update("group", "vip").Error)
	lower := responsesWSNativeTestChannel(appconstant.ChannelTypeSub2API, "https://example.com")
	lower.Id, lower.Group, lower.Priority = 44, "vip", common.GetPointer(int64(0))
	require.NoError(t, db.Create(lower).Error)
	require.NoError(t, appmodel.InitChannelCache())
	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	common.SetContextKey(c, appconstant.ContextKeyUsingGroup, "auto")
	common.SetContextKey(c, appconstant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, appconstant.ContextKeyTokenAutoGroups, []string{"default", "vip"})
	common.SetContextKey(c, appconstant.ContextKeyTokenCrossGroupRetry, true)
	retry = &service.RetryParam{Ctx: c, TokenGroup: "auto", ModelName: responsesWSChannelTestModel, RequestPath: "/v1/responses", Retry: common.GetPointer(common.RetryTimes)}
	selected, apiErr = selectResponsesWSChannel(c, responsesWSChannelTestModel, retry)
	require.Nil(t, apiErr)
	require.Equal(t, supported.Id, selected.Id)
	require.Equal(t, "vip", common.GetContextKeyString(c, appconstant.ContextKeyAutoGroup))
	retry.IncreaseRetry()
	require.Equal(t, 1, retry.GetRetry())
	selected, apiErr = selectResponsesWSChannel(c, responsesWSChannelTestModel, retry)
	require.Nil(t, apiErr)
	require.Equal(t, lower.Id, selected.Id)
}

func TestRetiredOpenHumanResponsesWSCannotOptIn(t *testing.T) {
	for _, enabled := range []*bool{nil, common.GetPointer(false), common.GetPointer(true)} {
		channel := responsesWSNativeTestChannel(appconstant.ChannelTypeOpenHuman, "https://example.com")
		channel.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: enabled})
		apiErr := responsesWSChannelEligibility(channel, "/v1/responses", responsesWSChannelTestModel)
		require.NotNil(t, apiErr)
		require.True(t, types.IsSkipRetryError(apiErr))
		require.False(t, responsesWSChannelSupportsType(channel.Type))
		require.False(t, responsesWSChannelDefaultEnabled(channel.Type))
	}
	require.Nil(t, GetTaskAdaptor(appconstant.TaskPlatform("61")))
}
