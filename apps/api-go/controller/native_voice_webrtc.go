package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/pkg/wsmanager"
	"github.com/LIghtJUNction/api.lmm.best/relay"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
)

// NativeVoiceWebRTC creates Live sessions only. The server attaches its own
// usage/control connection before returning an SDP answer to the browser.
func NativeVoiceWebRTC(c *gin.Context) {
	writeError := func(apiErr *types.NewAPIError) {
		c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		status := http.StatusBadRequest
		if common.IsRequestBodyTooLargeError(err) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(nativeVoiceError(err, status))
		return
	}
	raw, err := storage.Bytes()
	if err != nil {
		writeError(nativeVoiceError(err, http.StatusBadRequest))
		return
	}
	start, transport, err := parseNativeVoiceWebRTC(raw)
	if err != nil {
		writeError(nativeVoiceError(err, http.StatusBadRequest))
		return
	}
	info, apiErr := prepareNativeVoice(c, dto.NativeVoiceLive, start.Model, raw, 15)
	if apiErr != nil {
		writeError(apiErr)
		return
	}
	first, err := dto.RewriteNativeVoiceStart(start, info.UpstreamModelName)
	if err != nil {
		writeError(nativeVoiceError(err, http.StatusBadRequest))
		return
	}
	var event map[string]json.RawMessage
	_ = json.Unmarshal(first, &event)
	body, err := json.Marshal(map[string]json.RawMessage{"session": event["session"], "transport": transport})
	if err == nil && len(info.ParamOverride) > 0 {
		body, err = relaycommon.ApplyParamOverrideWithRelayInfo(body, info)
	}
	if err == nil {
		var checked *dto.NativeVoiceStart
		checked, _, err = parseNativeVoiceWebRTC(body)
		if err == nil && checked.Model != info.UpstreamModelName {
			err = errors.New("voice model overrides must use channel model mapping")
		}
	}
	if err != nil {
		writeError(nativeVoiceError(err, http.StatusBadRequest))
		return
	}
	body, err = restrictNativeVoiceWebRTCClient(body)
	if err != nil {
		writeError(nativeVoiceError(err, http.StatusBadRequest))
		return
	}
	if err = checkNativeVoiceSecurity(c, info, body); err != nil {
		writeError(nativeVoiceError(err, http.StatusForbidden))
		return
	}
	commitRate, apiErr := middleware.CheckModelRequestRateLimit(c)
	if apiErr != nil {
		writeError(apiErr)
		return
	}
	rateSucceeded := false
	defer func() {
		if commitRate != nil {
			commitRate(rateSucceeded)
		}
	}()
	// A background sideband must not retain Gin's pooled request context.
	background := c.Copy()
	background.Request = c.Request.Clone(context.Background())
	background.Request.Header = c.Request.Header.Clone()
	background.Request.Body = http.NoBody
	background.Set(common.KeyBodyStorage, nil)
	background.Set(common.KeyRequestBody, nil)
	billing, apiErr := service.NewNativeVoiceBilling(background, info, 15)
	if apiErr != nil {
		writeError(apiErr)
		return
	}
	creationPaid := false
	handedOff := false
	defer func() {
		if !handedOff {
			if creationPaid {
				_ = billing.Finalize(15, true, false, "initialization_incomplete")
			} else {
				_ = service.SettleBilling(background, info, 0)
			}
		}
	}()
	requestURL, err := nativeVoiceURL(info, "/v1/live/sessions", nil, false)
	if err != nil {
		writeError(nativeVoiceError(err, http.StatusBadRequest))
		return
	}
	headers, err := nativeVoiceHeaders(c, info)
	if err != nil {
		writeError(nativeVoiceError(err, http.StatusBadRequest))
		return
	}
	headers.Set("Content-Type", "application/json")
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		writeError(nativeVoiceError(err, http.StatusBadRequest))
		return
	}
	request.Header = headers
	client, err := service.GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
	if err != nil {
		writeError(nativeVoiceError(err, http.StatusBadRequest))
		return
	}
	response, err := client.Do(request)
	if err != nil {
		writeError(nativeVoiceError(errors.New("Live session creation failed"), http.StatusBadGateway))
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		apiErr := service.RelayErrorHandler(c.Request.Context(), response, false)
		service.ResetStatusCode(apiErr, c.GetString("status_code_mapping"))
		writeError(apiErr)
		return
	}
	creationPaid = true
	responseBody, err := common.ReadResponseBody(response)
	if err != nil {
		writeError(nativeVoiceError(err, http.StatusBadGateway))
		return
	}
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
		Transport struct {
			Type string `json:"type"`
			SDP  string `json:"sdp"`
		} `json:"transport"`
	}
	if json.Unmarshal(responseBody, &created) != nil || created.Session.ID == "" || strings.ContainsAny(created.Session.ID, "/?#\r\n") || created.Transport.Type != "webrtc" || strings.TrimSpace(created.Transport.SDP) == "" {
		writeError(nativeVoiceError(errors.New("invalid Live session creation response"), http.StatusBadGateway))
		return
	}
	// Sideband observation remains active independently of this HTTP request.
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	sideband, err := dialNativeVoice(ctx, background, info, "/v1/live/sessions/"+url.PathEscape(created.Session.ID)+"/attach", nil)
	if err != nil {
		cancel()
		closeUnobservedLiveSession(background, info, created.Session.ID)
		writeError(nativeVoiceError(errors.New("Live usage monitor could not attach; session termination was requested"), http.StatusBadGateway))
		return
	}
	unregister, accepted := wsmanager.Register(info.ChannelId, wsmanager.KindRealtime, func(int, string) { cancel() })
	if !accepted {
		_ = sideband.Close()
		cancel()
		unregister()
		closeUnobservedLiveSession(background, info, created.Session.ID)
		writeError(nativeVoiceError(errors.New("service is draining voice sessions"), http.StatusServiceUnavailable))
		return
	}
	handedOff = true
	go func() {
		defer cancel()
		defer unregister()
		result := relay.NativeVoiceBridge(ctx, nil, sideband, dto.NativeVoiceLive, nil, relay.NativeVoiceHooks{
			UpstreamModel:    info.UpstreamModelName,
			InitiallyStarted: true,
			MinimumSeconds:   15,
			EnsureBudget:     billing.EnsureBudget,
		})
		if err := billing.Finalize(result.Seconds, result.Estimated, result.Finalized, result.Reason); err != nil {
			logger.LogError(background, "Live WebRTC settlement failed: "+common.LocalLogPreview(err.Error()))
		}
	}()
	// Emit only the session ID and answer. No upstream credentials are exposed.
	c.JSON(response.StatusCode, created)
	rateSucceeded = c.Writer.Size() > 0
	if rateSucceeded {
		service.RecordChannelAffinity(c, info.ChannelId)
	} else {
		// A failed HTTP answer must not leave an unobserved media session alive.
		cancel()
	}
}

// WebRTC data-channel events bypass this gateway. Lock its permissions at
// startup so a browser cannot change the model or enable a separately billed
// Responses backend after the local price and authorization have been frozen.
func restrictNativeVoiceWebRTCClient(raw []byte) ([]byte, error) {
	var body, session, client, channel map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body["session"], &session); err != nil || session == nil {
		return nil, errors.New("Live session configuration required")
	}
	client = make(map[string]json.RawMessage)
	if value, exists := session["client"]; exists {
		if err := json.Unmarshal(value, &client); err != nil || client == nil {
			return nil, errors.New("Live client configuration must be an object")
		}
	}
	channel = make(map[string]json.RawMessage)
	if value, exists := client["data_channel"]; exists {
		if err := json.Unmarshal(value, &channel); err != nil || channel == nil {
			return nil, errors.New("Live data-channel configuration must be an object")
		}
	}
	allowed := []string{"session.input_audio.mute", "session.input_audio.unmute", "session.instructions.append", "session.thinking.append", "session.commentary.append", "session.close"}
	if value, exists := channel["allowed_client_events"]; exists && string(value) != `"all"` {
		var requested []string
		if err := json.Unmarshal(value, &requested); err != nil || requested == nil {
			return nil, errors.New("Live allowed_client_events must be all or an array of event names")
		}
		requestedSet := make(map[string]bool, len(requested))
		for _, event := range requested {
			requestedSet[event] = true
		}
		filtered := make([]string, 0, len(allowed))
		for _, event := range allowed {
			if requestedSet[event] {
				filtered = append(filtered, event)
			}
		}
		allowed = filtered
	}
	channel["allowed_client_events"], _ = json.Marshal(allowed)
	client["data_channel"], _ = json.Marshal(channel)
	session["client"], _ = json.Marshal(client)
	body["session"], _ = json.Marshal(session)
	return json.Marshal(body)
}

func parseNativeVoiceWebRTC(raw []byte) (*dto.NativeVoiceStart, json.RawMessage, error) {
	if err := dto.ValidateNativeVoiceJSON(raw); err != nil {
		return nil, nil, err
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, nil, err
	}
	for key := range data {
		if key != "session" && key != "transport" {
			return nil, nil, errors.New("Live creation accepts only session and transport")
		}
	}
	var transport struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	}
	if json.Unmarshal(data["transport"], &transport) != nil || transport.Type != "webrtc" || strings.TrimSpace(transport.SDP) == "" {
		return nil, nil, errors.New("native Live requires a WebRTC transport with an SDP offer")
	}
	first, err := json.Marshal(map[string]json.RawMessage{"type": json.RawMessage(`"session.start"`), "session": data["session"]})
	if err != nil {
		return nil, nil, err
	}
	start, err := dto.ParseNativeVoiceStart(dto.NativeVoiceLive, first, "")
	return start, data["transport"], err
}

func closeUnobservedLiveSession(c *gin.Context, info *relaycommon.RelayInfo, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requestURL, err := nativeVoiceURL(info, "/v1/live/sessions/"+url.PathEscape(sessionID)+"/hangup", nil, false)
	if err != nil {
		return
	}
	headers, err := nativeVoiceHeaders(c, info)
	if err != nil {
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, nil)
	if err != nil {
		return
	}
	request.Header = headers
	client, err := service.GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
	if err != nil {
		return
	}
	if response, err := client.Do(request); err == nil && response != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			logger.LogError(c, "Live orphan session termination request returned HTTP "+response.Status)
		}
	} else if err != nil {
		logger.LogError(c, "Live orphan session termination request failed: "+common.LocalLogPreview(err.Error()))
	}
}
