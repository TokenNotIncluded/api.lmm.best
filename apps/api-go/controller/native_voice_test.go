package controller

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const nativeVoiceControllerKey = "mock-native-voice-provider-key"

// Use the real token, distributor and billing owners. All vendor traffic stays
// inside this isolated SQLite fixture and the supplied loopback HTTP handler.
func newNativeVoiceControllerFixture(t *testing.T, models, mapping string, handler http.HandlerFunc) *systemOneRelayFixture {
	t.Helper()
	withSelfUseModeDisabled(t)
	previousRateLimit, previousQuotaUnit := setting.ModelRequestRateLimitEnabled, common.QuotaPerUnit
	setting.ModelRequestRateLimitEnabled, common.QuotaPerUnit = false, 500000
	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled, common.QuotaPerUnit = previousRateLimit, previousQuotaUnit
	})
	fixture := newSystemOneRelayFixture(t, http.StatusOK, `{}`)
	require.NoError(t, fixture.db.AutoMigrate(&model.PublicRelayContribution{}))
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	fixture.channel.Type = constant.ChannelTypeOpenAI
	fixture.channel.Key = nativeVoiceControllerKey
	fixture.channel.BaseURL = &upstream.URL
	fixture.channel.Models = models
	fixture.channel.ModelMapping = &mapping
	require.NoError(t, fixture.db.Save(&fixture.channel).Error)
	require.NoError(t, fixture.db.Where("channel_id = ?", fixture.channel.Id).Delete(&model.Ability{}).Error)
	require.NoError(t, fixture.channel.AddAbilities(nil))
	withTieredBillingConfig(t, map[string]string{
		"public-live": "tiered_expr", "gpt-live-1": "tiered_expr",
		"public-asr": "tiered_expr", "gpt-realtime-whisper": "tiered_expr",
	}, map[string]string{
		"public-live":          `tier("minute", audio_s * (50000.0 / 60))`,
		"gpt-live-1":           `tier("minute", audio_s * (50000.0 / 60))`,
		"public-asr":           `tier("minute", audio_s * (17000.0 / 60))`,
		"gpt-realtime-whisper": `tier("minute", audio_s * (17000.0 / 60))`,
	})
	model.InvalidatePricingCache()
	return fixture
}

func nativeVoiceControllerRouter() *gin.Engine {
	engine := gin.New()
	engine.Use(middleware.BodyStorageCleanup())
	protected := engine.Group("", middleware.TokenAuth(), middleware.RelayRequestAdmission(), middleware.ModelRequestRateLimit())
	protected.GET("/v1/live/sessions", NativeVoiceWebSocket)
	protected.GET("/v1/realtime", NativeVoiceWebSocket)
	protected.POST("/v1/live/sessions", NativeVoiceWebRTC)
	protected.POST("/v1/realtime/translations/calls", UnsupportedNativeVoiceWebRTC)
	protected.POST("/v1/realtime/translations/client_secrets", UnsupportedNativeVoiceWebRTC)
	return engine
}

func nativeVoiceControllerDial(t *testing.T, fixture *systemOneRelayFixture, server *httptest.Server, path, keySuffix string) *websocket.Conn {
	t.Helper()
	header := http.Header{"Authorization": []string{"Bearer " + fixture.token.Key + keySuffix}}
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+path, header)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(4*time.Second)))
	return conn
}

func nativeVoiceControllerRead(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_, raw, err := conn.ReadMessage()
	require.NoError(t, err)
	var event map[string]any
	require.NoError(t, json.Unmarshal(raw, &event))
	return event
}

func nativeVoiceControllerAwaitCharge(t *testing.T, fixture *systemOneRelayFixture, quota int) model.Log {
	t.Helper()
	require.Eventually(t, func() bool {
		var user model.User
		var token model.Token
		var count int64
		return fixture.db.First(&user, fixture.user.Id).Error == nil &&
			fixture.db.First(&token, fixture.token.Id).Error == nil &&
			fixture.db.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Count(&count).Error == nil &&
			user.UsedQuota == quota && token.UsedQuota == quota && count == 1
	}, 4*time.Second, 10*time.Millisecond, "native voice settlement must finish before fixture cleanup")
	return fixture.verifyCharge(t, quota)
}

func nativeVoiceProviderRead(t *testing.T, conn *websocket.Conn) (map[string]any, bool) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	_, raw, err := conn.ReadMessage()
	if !assert.NoError(t, err) {
		return nil, false
	}
	var event map[string]any
	ok := assert.NoError(t, json.Unmarshal(raw, &event))
	return event, ok
}

func nativeVoiceProviderWrite(t *testing.T, conn *websocket.Conn, raw string) bool {
	t.Helper()
	return assert.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(raw)))
}

func TestNativeVoiceControllerLiveAuthorizesFirstFrameAndSettlesOnce(t *testing.T) {
	var calls atomic.Int32
	upgrader := websocket.Upgrader{}
	fixture := newNativeVoiceControllerFixture(t, "public-live", `{"public-live":"gpt-live-1"}`, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/v1/live/sessions", r.URL.Path)
		assert.Empty(t, r.URL.RawQuery, "Live chooses its model in session.start, not a query")
		assert.Equal(t, "Bearer "+nativeVoiceControllerKey, r.Header.Get("Authorization"))
		conn, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		first, ok := nativeVoiceProviderRead(t, conn)
		if !ok {
			return
		}
		assert.Equal(t, "session.start", first["type"])
		assert.Equal(t, "gpt-live-1", first["session"].(map[string]any)["model"])
		for _, event := range []string{
			`{"type":"session.started","session":{"model":"gpt-live-1"}}`,
			`{"type":"session.usage.updated","usage":{"seconds":4}}`,
			`{"type":"session.usage.updated","usage":{"seconds":3}}`,
			`{"type":"session.closed","reason":"close_requested","usage":{"seconds":6}}`,
		} {
			if !nativeVoiceProviderWrite(t, conn, event) {
				return
			}
		}
	})
	require.NoError(t, fixture.db.Model(&fixture.token).Updates(map[string]any{
		"model_limits_enabled": true, "model_limits": "public-live",
	}).Error)
	server := httptest.NewServer(nativeVoiceControllerRouter())
	t.Cleanup(server.Close)
	client := nativeVoiceControllerDial(t, fixture, server, "/v1/live/sessions", "")
	assert.Zero(t, calls.Load(), "the authenticated handshake must wait for a model-bearing first frame")
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.start","session":{"model":"public-live","delegation":{"type":"client"}}}`)))
	for _, expected := range []string{"session.started", "session.usage.updated", "session.usage.updated", "session.closed"} {
		require.Equal(t, expected, nativeVoiceControllerRead(t, client)["type"])
	}
	log := nativeVoiceControllerAwaitCharge(t, fixture, 2500)
	assert.EqualValues(t, 1, calls.Load())
	assert.Equal(t, "public-live", log.ModelName, "logs and pricing retain the authorized public alias")
	assert.Zero(t, log.PromptTokens)
	assert.Zero(t, log.CompletionTokens)
	var other map[string]any
	require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
	assert.EqualValues(t, 6, other["audio_seconds"])
	assert.Equal(t, true, other["usage_finalized"])
	assert.Equal(t, false, other["usage_estimated"])
}

func TestNativeVoiceControllerSpecificChannelCannotBypassModelLimits(t *testing.T) {
	var calls atomic.Int32
	fixture := newNativeVoiceControllerFixture(t, "public-live", `{"public-live":"gpt-live-1"}`, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	require.NoError(t, fixture.db.Model(&fixture.user).Update("role", common.RoleAdminUser).Error)
	require.NoError(t, fixture.db.Model(&fixture.token).Updates(map[string]any{
		"model_limits_enabled": true, "model_limits": "gpt-live-1",
	}).Error)
	server := httptest.NewServer(nativeVoiceControllerRouter())
	t.Cleanup(server.Close)
	client := nativeVoiceControllerDial(t, fixture, server, "/v1/live/sessions", fmt.Sprintf("-%d", fixture.channel.Id))
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.start","session":{"model":"public-live"}}`)))
	event := nativeVoiceControllerRead(t, client)
	assert.Equal(t, "error", event["type"])
	assert.Contains(t, event["error"].(map[string]any)["message"], "not allowed")
	assert.Zero(t, calls.Load(), "mapped provider model access does not grant public alias access")
	var user model.User
	var token model.Token
	require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
	require.NoError(t, fixture.db.First(&token, fixture.token.Id).Error)
	assert.Equal(t, fixture.user.Quota, user.Quota)
	assert.Equal(t, fixture.token.RemainQuota, token.RemainQuota)
	var logs int64
	require.NoError(t, fixture.db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&logs).Error)
	assert.Zero(t, logs)
}

func TestNativeVoiceControllerTranscriptionUsesIntentAndDeduplicatesDuration(t *testing.T) {
	var calls atomic.Int32
	upgrader := websocket.Upgrader{}
	fixture := newNativeVoiceControllerFixture(t, "public-asr", `{"public-asr":"gpt-realtime-whisper"}`, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/v1/realtime", r.URL.Path)
		assert.Equal(t, "intent=transcription", r.URL.RawQuery)
		assert.Equal(t, "Bearer "+nativeVoiceControllerKey, r.Header.Get("Authorization"))
		conn, err := upgrader.Upgrade(w, r, nil)
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		first, ok := nativeVoiceProviderRead(t, conn)
		if !ok {
			return
		}
		session := first["session"].(map[string]any)
		input := session["audio"].(map[string]any)["input"].(map[string]any)
		assert.Equal(t, "transcription", session["type"])
		assert.Equal(t, "gpt-realtime-whisper", input["transcription"].(map[string]any)["model"])
		assert.EqualValues(t, 24000, input["format"].(map[string]any)["rate"])
		assert.Nil(t, input["turn_detection"])
		if !nativeVoiceProviderWrite(t, conn, `{"type":"session.updated"}`) {
			return
		}
		appendEvent, ok := nativeVoiceProviderRead(t, conn)
		if !ok || !assert.Equal(t, "input_audio_buffer.append", appendEvent["type"]) {
			return
		}
		commit, ok := nativeVoiceProviderRead(t, conn)
		if !ok || !assert.Equal(t, "input_audio_buffer.commit", commit["type"]) {
			return
		}
		for _, event := range []string{
			`{"type":"input_audio_buffer.committed","item_id":"item-one"}`,
			`{"type":"conversation.item.input_audio_transcription.completed","item_id":"item-one","content_index":0,"transcript":"hello","usage":{"type":"duration","seconds":6}}`,
			`{"type":"conversation.item.input_audio_transcription.completed","item_id":"item-one","content_index":0,"transcript":"hello","usage":{"type":"duration","seconds":6}}`,
		} {
			if !nativeVoiceProviderWrite(t, conn, event) {
				return
			}
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), time.Now().Add(time.Second))
	})
	server := httptest.NewServer(nativeVoiceControllerRouter())
	t.Cleanup(server.Close)
	client := nativeVoiceControllerDial(t, fixture, server, "/v1/realtime?intent=transcription", "")
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(`{"type":"session.update","session":{"type":"transcription","audio":{"input":{"transcription":{"model":"public-asr"}}}}}`)))
	require.Equal(t, "session.updated", nativeVoiceControllerRead(t, client)["type"])
	audio, err := json.Marshal(map[string]string{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 48000))})
	require.NoError(t, err)
	require.NoError(t, client.WriteMessage(websocket.TextMessage, audio))
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(`{"type":"input_audio_buffer.commit"}`)))
	for _, expected := range []string{"input_audio_buffer.committed", "conversation.item.input_audio_transcription.completed", "conversation.item.input_audio_transcription.completed"} {
		require.Equal(t, expected, nativeVoiceControllerRead(t, client)["type"])
	}
	log := nativeVoiceControllerAwaitCharge(t, fixture, 850)
	assert.EqualValues(t, 1, calls.Load())
	assert.Equal(t, "public-asr", log.ModelName)
	var other map[string]any
	require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
	assert.EqualValues(t, 6, other["audio_seconds"])
	assert.Equal(t, false, other["usage_estimated"])
	assert.Equal(t, true, other["usage_finalized"])
}

func TestNativeVoiceControllerWebRTCAttachesBeforeSDPAndCreditsMinimum(t *testing.T) {
	var createCalls, attachCalls atomic.Int32
	var attached atomic.Bool
	finish := make(chan struct{}, 1)
	t.Cleanup(func() { close(finish) })
	upgrader := websocket.Upgrader{}
	fixture := newNativeVoiceControllerFixture(t, "public-live", `{"public-live":"gpt-live-1"}`, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer "+nativeVoiceControllerKey, r.Header.Get("Authorization"))
		assert.Empty(t, r.URL.RawQuery)
		switch r.URL.Path {
		case "/v1/live/sessions":
			createCalls.Add(1)
			assert.Equal(t, http.MethodPost, r.Method)
			var body map[string]any
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			session := body["session"].(map[string]any)
			assert.Equal(t, "gpt-live-1", session["model"])
			dataChannel := session["client"].(map[string]any)["data_channel"].(map[string]any)
			assert.ElementsMatch(t, []string{"session.instructions.append", "session.close"}, dataChannel["allowed_client_events"], "browser events cannot switch models or enable a paid backend")
			assert.Equal(t, "all", dataChannel["allowed_server_events"], "the gateway preserves the browser's server-event subscription")
			assert.Equal(t, "webrtc", body["transport"].(map[string]any)["type"])
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"session":{"id":"session-mock"},"transport":{"type":"webrtc","sdp":"v=0\r\nmock-answer"},"client_secret":{"value":"never-return-this"}}`)
		case "/v1/live/sessions/session-mock/attach":
			attachCalls.Add(1)
			attached.Store(true)
			conn, err := upgrader.Upgrade(w, r, nil)
			if !assert.NoError(t, err) {
				return
			}
			defer conn.Close()
			select {
			case <-finish:
			case <-time.After(4 * time.Second):
				t.Error("SDP response did not complete after sideband attachment")
				return
			}
			nativeVoiceProviderWrite(t, conn, `{"type":"session.closed","reason":"remote_hangup","usage":{"seconds":0}}`)
		default:
			t.Errorf("unexpected native voice provider path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/live/sessions", strings.NewReader(`{"session":{"model":"public-live","delegation":{"type":"client"},"client":{"data_channel":{"allowed_client_events":["session.instructions.append","session.close","session.update","session.start","response.create"],"allowed_server_events":"all"}}},"transport":{"type":"webrtc","sdp":"v=0\r\nmock-offer"}}`))
	request.Header.Set("Authorization", "Bearer "+fixture.token.Key)
	request.Header.Set("Content-Type", "application/json")
	nativeVoiceControllerRouter().ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.True(t, attached.Load(), "the answer cannot be returned before the gateway attaches its usage monitor")
	assert.JSONEq(t, `{"session":{"id":"session-mock"},"transport":{"type":"webrtc","sdp":"v=0\r\nmock-answer"}}`, response.Body.String())
	assert.NotContains(t, response.Body.String(), "never-return-this")
	assert.NotContains(t, response.Body.String(), nativeVoiceControllerKey)
	finish <- struct{}{}
	log := nativeVoiceControllerAwaitCharge(t, fixture, 6250)
	assert.EqualValues(t, 1, createCalls.Load())
	assert.EqualValues(t, 1, attachCalls.Load())
	var other map[string]any
	require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
	assert.EqualValues(t, 15, other["audio_seconds"])
	assert.Equal(t, true, other["usage_finalized"])
}

func TestNativeVoiceControllerUnobservableWebRTCRejectsBeforeVendorWork(t *testing.T) {
	var calls atomic.Int32
	fixture := newNativeVoiceControllerFixture(t, "gpt-live-1", `{}`, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	engine := nativeVoiceControllerRouter()
	for _, path := range []string{"/v1/realtime/translations/calls", "/v1/realtime/translations/client_secrets"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-realtime-translate"}`))
		request.Header.Set("Authorization", "Bearer "+fixture.token.Key)
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(response, request)
		require.Equal(t, http.StatusNotImplemented, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), "unsupported_voice_transport")
	}
	assert.Zero(t, calls.Load())
	var user model.User
	var token model.Token
	require.NoError(t, fixture.db.First(&user, fixture.user.Id).Error)
	require.NoError(t, fixture.db.First(&token, fixture.token.Id).Error)
	assert.Equal(t, fixture.user.Quota, user.Quota)
	assert.Equal(t, fixture.token.RemainQuota, token.RemainQuota)
}
