package controller

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	"github.com/LIghtJUNction/api.lmm.best/pkg/wsmanager"
	"github.com/LIghtJUNction/api.lmm.best/relay"
	relaychannel "github.com/LIghtJUNction/api.lmm.best/relay/channel"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func nativeVoiceError(err error, status int) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, types.ErrorCode("native_voice_error"), status, types.ErrOptionWithSkipRetry())
}

func nativeVoiceKind(c *gin.Context) dto.NativeVoiceKind {
	if c.Request.URL.Path == "/v1/live/sessions" {
		return dto.NativeVoiceLive
	}
	if c.Request.URL.Path == "/v1/realtime/translations" {
		return dto.NativeVoiceTranslation
	}
	return dto.NativeVoiceTranscription
}

// NativeVoiceWebSocket waits for the model-bearing startup event before using
// the normal distributor. No upstream connection or budget is created earlier.
func NativeVoiceWebSocket(c *gin.Context) {
	client, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer client.Close()
	common.SetWebSocketReadLimit(client)
	kind := nativeVoiceKind(c)
	var first []byte
	if kind != dto.NativeVoiceTranslation {
		_ = client.SetReadDeadline(time.Now().Add(30 * time.Second))
		stopWatch := make(chan struct{})
		watchDone := make(chan struct{})
		requestContext := c.Request.Context()
		go func() {
			defer close(watchDone)
			select {
			case <-requestContext.Done():
				_ = client.Close()
			case <-stopWatch:
			}
		}()
		messageType, message, readErr := client.ReadMessage()
		close(stopWatch)
		<-watchDone
		_ = client.SetReadDeadline(time.Time{})
		if readErr != nil {
			return
		}
		if messageType != websocket.TextMessage {
			helper.WssError(c, client, nativeVoiceError(errors.New("the first voice event must be JSON text"), http.StatusBadRequest).ToOpenAIError())
			return
		}
		first = message
	}
	start, err := dto.ParseNativeVoiceStart(kind, first, c.Query("model"))
	if err != nil {
		helper.WssError(c, client, nativeVoiceError(err, http.StatusBadRequest).ToOpenAIError())
		return
	}
	info, apiErr := prepareNativeVoice(c, kind, start.Model, first, 10)
	if apiErr != nil {
		helper.WssError(c, client, apiErr.ToOpenAIError())
		return
	}
	if kind != dto.NativeVoiceTranslation {
		first, err = dto.RewriteNativeVoiceStart(start, info.UpstreamModelName)
		if err == nil && len(info.ParamOverride) > 0 {
			first, err = relaycommon.ApplyParamOverrideWithRelayInfo(first, info)
		}
		if err == nil {
			var checked *dto.NativeVoiceStart
			checked, err = dto.ParseNativeVoiceStart(kind, first, "")
			if err == nil && checked.Model != info.UpstreamModelName {
				err = errors.New("voice model overrides must use channel model mapping")
			}
		}
		if err != nil {
			helper.WssError(c, client, nativeVoiceError(err, http.StatusBadRequest).ToOpenAIError())
			return
		}
	}
	if err := checkNativeVoiceSecurity(c, info, first); err != nil {
		helper.WssError(c, client, nativeVoiceError(err, http.StatusForbidden).ToOpenAIError())
		return
	}
	commitRate, apiErr := middleware.CheckModelRequestRateLimit(c)
	if apiErr != nil {
		helper.WssError(c, client, apiErr.ToOpenAIError())
		return
	}
	rateSucceeded := false
	defer func() {
		if commitRate != nil {
			commitRate(rateSucceeded)
		}
	}()
	billing, apiErr := service.NewNativeVoiceBilling(c, info, 10)
	if apiErr != nil {
		helper.WssError(c, client, apiErr.ToOpenAIError())
		return
	}
	target, err := dialNativeVoice(c.Request.Context(), c, info, nativeVoiceEndpoint(kind), nativeVoiceQuery(kind, info.UpstreamModelName))
	if err != nil {
		_ = service.SettleBilling(c, info, 0)
		helper.WssError(c, client, nativeVoiceError(err, http.StatusBadGateway).ToOpenAIError())
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	unregister, accepted := wsmanager.Register(info.ChannelId, wsmanager.KindRealtime, func(int, string) { cancel() })
	defer unregister()
	if !accepted {
		_ = target.Close()
		_ = service.SettleBilling(c, info, 0)
		return
	}
	result := relay.NativeVoiceBridge(ctx, client, target, kind, first, relay.NativeVoiceHooks{
		UpstreamModel: info.UpstreamModelName,
		EnsureBudget:  billing.EnsureBudget,
		CheckClient:   func(raw []byte) error { return checkNativeVoiceSecurity(c, info, raw) },
	})
	if err := billing.Finalize(result.Seconds, result.Estimated, result.Finalized, result.Reason); err != nil {
		logger.LogError(c, "native voice settlement failed: "+common.LocalLogPreview(err.Error()))
	}
	if result.Started {
		rateSucceeded = true
		service.RecordChannelAffinity(c, info.ChannelId)
	}
}

func nativeVoiceEndpoint(kind dto.NativeVoiceKind) string {
	switch kind {
	case dto.NativeVoiceLive:
		return "/v1/live/sessions"
	case dto.NativeVoiceTranslation:
		return "/v1/realtime/translations"
	default:
		return "/v1/realtime"
	}
}

func nativeVoiceQuery(kind dto.NativeVoiceKind, modelName string) url.Values {
	q := make(url.Values)
	switch kind {
	case dto.NativeVoiceTranscription:
		q.Set("intent", "transcription")
	case dto.NativeVoiceTranslation:
		q.Set("model", modelName)
	}
	return q
}

func prepareNativeVoice(c *gin.Context, kind dto.NativeVoiceKind, modelName string, raw []byte, reserveSeconds float64) (*relaycommon.RelayInfo, *types.NewAPIError) {
	if apiErr := selectNativeVoiceChannel(c, modelName, raw); apiErr != nil {
		return nil, apiErr
	}
	info := relaycommon.GenRelayInfoWs(c, nil)
	info.InitChannelMeta(c)
	info.OriginModelName = modelName
	if info.ChannelType != constant.ChannelTypeOpenAI && info.ChannelType != constant.ChannelTypeNewAPI {
		return nil, nativeVoiceError(errors.New("this voice protocol requires an OpenAI or Compatible Relay channel"), http.StatusBadRequest)
	}
	if err := helper.ModelMappedHelper(c, info, nil); err != nil {
		return nil, nativeVoiceError(err, http.StatusBadRequest)
	}
	valid := kind == dto.NativeVoiceLive && info.UpstreamModelName == "gpt-live-1" ||
		kind == dto.NativeVoiceTranscription && (info.UpstreamModelName == "gpt-live-transcribe" || info.UpstreamModelName == "gpt-realtime-whisper") ||
		kind == dto.NativeVoiceTranslation && info.UpstreamModelName == "gpt-realtime-translate"
	if !valid {
		return nil, nativeVoiceError(errors.New("the selected model does not support this voice protocol"), http.StatusBadRequest)
	}
	info.NativeVoiceReserveSeconds = reserveSeconds
	headers := make(map[string]string, len(c.Request.Header))
	for key := range c.Request.Header {
		headers[strings.ToLower(key)] = c.Request.Header.Get(key)
	}
	info.BillingRequestInput = &billingexpr.RequestInput{Body: append([]byte(nil), raw...), Headers: headers}
	if _, err := helper.ModelPriceHelper(c, info, 0, &types.TokenCountMeta{}); err != nil {
		return nil, nativeVoiceError(err, http.StatusBadRequest)
	}
	return info, nil
}

func checkNativeVoiceSecurity(c *gin.Context, info *relaycommon.RelayInfo, raw []byte) error {
	text := dto.SecurityTextFromRealtimeJSON(raw)
	if service.EvaluateAdvancedSecurityText(c, info, text).Blocked() {
		return errors.New("voice event blocked by security rules")
	}
	return nil
}

// The distributor writes HTTP errors. Capture them while selecting after a
// WebSocket upgrade and report the same failure as a WebSocket error event.
func selectNativeVoiceChannel(c *gin.Context, modelName string, raw []byte) *types.NewAPIError {
	if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		allowed, ok := common.GetContextKeyType[map[string]bool](c, constant.ContextKeyTokenModelLimit)
		if !ok || !allowed[ratio_setting.FormatMatchingModelName(modelName)] {
			return nativeVoiceError(errors.New("token is not allowed to use this model"), http.StatusForbidden)
		}
	}
	if len(raw) == 0 {
		raw, _ = json.Marshal(map[string]string{"model": modelName})
	}
	selected := c.Copy()
	writer := &nativeVoiceSelectionWriter{headers: make(http.Header), status: http.StatusOK}
	selected.Writer = writer
	selected.Request = c.Request.Clone(c.Request.Context())
	if nativeVoiceKind(c) == dto.NativeVoiceTranscription {
		// Channel capability filtering needs a path that identifies ASR, while
		// the actual vendor transport keeps /realtime?intent=transcription.
		selected.Request.URL.Path = "/v1/realtime/transcription_sessions"
		selected.Request.URL.RawPath = ""
	}
	selected.Request.Header = c.Request.Header.Clone()
	selected.Request.Header.Set("Content-Type", "application/json")
	selected.Request.Body = io.NopCloser(bytes.NewReader(raw))
	selected.Request.ContentLength = int64(len(raw))
	query := selected.Request.URL.Query()
	query.Set("model", modelName)
	selected.Request.URL.RawQuery = query.Encode()
	storage, err := common.CreateBodyStorage(raw)
	if err != nil {
		return nativeVoiceError(err, http.StatusBadRequest)
	}
	defer storage.Close()
	selected.Set(common.KeyBodyStorage, storage)
	selected.Set(common.KeyRequestBody, nil)
	middleware.Distribute()(selected)
	if writer.status >= http.StatusBadRequest || common.GetContextKeyInt(selected, constant.ContextKeyChannelId) == 0 {
		message := "no channel is available for this voice request"
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(writer.body.Bytes(), &body) == nil && body.Error.Message != "" {
			message = body.Error.Message
		}
		status := writer.status
		if status < http.StatusBadRequest {
			status = http.StatusServiceUnavailable
		}
		return nativeVoiceError(errors.New(message), status)
	}
	for key, value := range selected.Keys {
		if key != common.KeyBodyStorage && key != common.KeyRequestBody {
			c.Set(key, value)
		}
	}
	return nil
}

type nativeVoiceSelectionWriter struct {
	headers http.Header
	body    bytes.Buffer
	status  int
	written bool
}

func (w *nativeVoiceSelectionWriter) Header() http.Header { return w.headers }
func (w *nativeVoiceSelectionWriter) WriteHeader(status int) {
	if !w.written {
		w.status = status
	}
}
func (w *nativeVoiceSelectionWriter) Write(data []byte) (int, error) {
	w.written = true
	return w.body.Write(data)
}
func (w *nativeVoiceSelectionWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}
func (w *nativeVoiceSelectionWriter) Status() int              { return w.status }
func (w *nativeVoiceSelectionWriter) Size() int                { return w.body.Len() }
func (w *nativeVoiceSelectionWriter) Written() bool            { return w.written }
func (w *nativeVoiceSelectionWriter) WriteHeaderNow()          { w.written = true }
func (w *nativeVoiceSelectionWriter) Flush()                   {}
func (w *nativeVoiceSelectionWriter) CloseNotify() <-chan bool { return nil }
func (w *nativeVoiceSelectionWriter) Pusher() http.Pusher      { return nil }
func (w *nativeVoiceSelectionWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("voice selection cannot hijack a connection")
}

func nativeVoiceURL(info *relaycommon.RelayInfo, endpoint string, query url.Values, ws bool) (string, error) {
	base, err := url.Parse(info.ChannelBaseUrl)
	if err != nil || base.Host == "" || base.User != nil || base.Fragment != "" || base.RawQuery != "" || (base.Scheme != "http" && base.Scheme != "https") {
		return "", errors.New("voice channel requires an HTTP(S) base URL without credentials, query or fragment")
	}
	base.Path = strings.TrimSuffix(strings.TrimRight(base.Path, "/"), "/v1") + endpoint
	base.RawPath = ""
	base.RawQuery = query.Encode()
	if ws {
		if base.Scheme == "https" {
			base.Scheme = "wss"
		} else {
			base.Scheme = "ws"
		}
	}
	return base.String(), nil
}

func nativeVoiceHeaders(c *gin.Context, info *relaycommon.RelayInfo) (http.Header, error) {
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+info.ApiKey)
	if info.Organization != "" {
		header.Set("OpenAI-Organization", info.Organization)
	}
	if value := c.Request.Header.Get("OpenAI-Safety-Identifier"); value != "" {
		header.Set("OpenAI-Safety-Identifier", value)
	}
	sanitized := c.Copy()
	sanitized.Request = c.Request.Clone(c.Request.Context())
	sanitized.Request.Header = c.Request.Header.Clone()
	for name := range sanitized.Request.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "sec-websocket-") || lower == "authorization" || lower == "cookie" || lower == "proxy-authorization" || lower == "x-api-key" {
			sanitized.Request.Header.Del(name)
		}
	}
	override, err := relaychannel.ResolveHeaderOverride(info, sanitized)
	if err != nil {
		return nil, err
	}
	for key, value := range override {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "sec-websocket-") || lower == "connection" || lower == "upgrade" || lower == "content-length" || lower == "transfer-encoding" || lower == "host" {
			return nil, errors.New("voice headers cannot override transport handshake headers")
		}
		header.Set(key, value)
	}
	return header, nil
}

func dialNativeVoice(ctx context.Context, c *gin.Context, info *relaycommon.RelayInfo, endpoint string, query url.Values) (*websocket.Conn, error) {
	requestURL, err := nativeVoiceURL(info, endpoint, query, true)
	if err != nil {
		return nil, err
	}
	header, err := nativeVoiceHeaders(c, info)
	if err != nil {
		return nil, err
	}
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 30 * time.Second
	if proxyURL := strings.TrimSpace(info.ChannelSetting.Proxy); proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err != nil || proxy.Host == "" || (proxy.Scheme != "http" && proxy.Scheme != "https") {
			return nil, errors.New("native voice WebSocket requires an HTTP(S) channel proxy")
		}
		dialer.Proxy = http.ProxyURL(proxy)
	}
	conn, response, err := dialer.DialContext(ctx, requestURL, header)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("voice upstream connection failed: %w", err)
	}
	common.SetWebSocketReadLimit(conn)
	return conn, nil
}

func UnsupportedNativeVoiceWebRTC(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": gin.H{"type": "unsupported_voice_transport", "message": "this voice transport does not provide server-observable usage; use the metered WebSocket endpoint"}})
}
