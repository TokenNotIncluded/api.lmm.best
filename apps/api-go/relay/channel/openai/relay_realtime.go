package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relay/helper"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type realtimeFrame struct {
	fromClient bool
	message    []byte
	err        error
}

// Readers only move bytes. This collector owns all session state, quota
// reservations and socket writes, so a completed turn cannot race a reset or
// be settled a second time by a reader after this handler returns.
func OpenaiRealtimeHandler(c *gin.Context, info *relaycommon.RelayInfo) (apiError *types.NewAPIError, finalUsage *dto.RealtimeUsage) {
	if c == nil || c.Request == nil || info == nil || info.ClientWs == nil || info.TargetWs == nil {
		return types.NewError(fmt.Errorf("invalid websocket connection"), types.ErrorCodeBadResponse), nil
	}
	sumUsage := &dto.RealtimeUsage{}
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.LogError(c, fmt.Sprintf("realtime collector panic: %v", recovered))
			// Preserve the old reader panic shield after moving ownership into
			// this collector. The outer helper still settles incurred usage once.
			apiError, finalUsage = nil, sumUsage
		}
	}()

	info.IsStream = true
	clientConn, targetConn := info.ClientWs, info.TargetWs
	common.SetWebSocketReadLimit(clientConn)
	common.SetWebSocketReadLimit(targetConn)
	ctx, cancel := context.WithCancel(c.Request.Context())
	frames := make(chan realtimeFrame, 8)
	var workers sync.WaitGroup
	workers.Add(3)
	defer func() {
		cancel()
		_ = clientConn.Close()
		_ = targetConn.Close()
		workers.Wait()
	}()

	read := func(conn *websocket.Conn, fromClient bool) {
		defer workers.Done()
		send := func(frame realtimeFrame) {
			select {
			case frames <- frame:
			case <-ctx.Done():
			}
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				send(realtimeFrame{fromClient: fromClient, err: fmt.Errorf("realtime reader panic: %v", recovered)})
			}
		}()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				send(realtimeFrame{fromClient: fromClient, err: err})
				return
			}
			select {
			case frames <- realtimeFrame{fromClient: fromClient, message: message}:
			case <-ctx.Done():
				return
			}
		}
	}
	go read(clientConn, true)
	go read(targetConn, false)
	go func() {
		defer workers.Done()
		<-ctx.Done()
		// Closing a connection interrupts both ReadMessage and a blocked write.
		_ = clientConn.Close()
		_ = targetConn.Close()
	}()

	localUsage := &dto.RealtimeUsage{}
	completedResponses := make(map[string]struct{})
	localOutputDelivered := false
	for {
		select {
		case <-ctx.Done():
			return nil, finishRealtimeUsage(c, info, sumUsage, localUsage, localOutputDelivered)
		case frame := <-frames:
			if frame.err != nil {
				if !websocket.IsCloseError(frame.err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && ctx.Err() == nil {
					logger.LogError(c, "realtime transport ended: "+frame.err.Error())
				}
				return nil, finishRealtimeUsage(c, info, sumUsage, localUsage, localOutputDelivered)
			}
			event, responseID, err := decodeRealtimeEvent(frame.message)
			if err != nil {
				logger.LogError(c, "invalid realtime event: "+err.Error())
				return nil, finishRealtimeUsage(c, info, sumUsage, localUsage, localOutputDelivered)
			}
			if frame.fromClient {
				evaluation := service.EvaluateAdvancedSecurityText(c, info, dto.ModerationTextFromRealtimeJSON(frame.message))
				if len(evaluation.Matches) > 0 {
					matchIDs := make([]string, 0, len(evaluation.Matches))
					for _, match := range evaluation.Matches {
						matchIDs = append(matchIDs, match.RuleID)
					}
					logger.LogWarn(c, "advanced security rules matched in realtime event: "+strings.Join(matchIDs, ", "))
				}
				if evaluation.Blocked() {
					apiErr := service.NewAdvancedSecurityAPIError()
					apiErr.SetMessage(common.MessageWithRequestId(apiErr.Error(), info.RequestId))
					helper.WssError(c, clientConn, apiErr.ToOpenAIError())
					continue
				}
				if event.Type == dto.RealtimeEventTypeSessionUpdate && event.Session != nil {
					if event.Session.Tools != nil {
						info.RealtimeTools = event.Session.Tools
					}
				}
				textTokens, audioTokens, err := service.CountTokenRealtime(info, *event, info.UpstreamModelName)
				if err != nil {
					logger.LogError(c, "invalid realtime input: "+err.Error())
					return nil, finishRealtimeUsage(c, info, sumUsage, localUsage, localOutputDelivered)
				}
				if err := helper.WssString(c, targetConn, string(frame.message)); err != nil {
					return nil, finishRealtimeUsage(c, info, sumUsage, localUsage, localOutputDelivered)
				}
				addLocalRealtimeTokens(localUsage, textTokens, audioTokens, true)
				continue
			}

			info.SetFirstResponseTime()
			if event.Type == dto.RealtimeEventTypeSessionCreated || event.Type == dto.RealtimeEventTypeSessionUpdated {
				if event.Session != nil {
					info.InputAudioFormat = common.GetStringIfEmpty(event.Session.InputAudioFormat, info.InputAudioFormat)
					info.OutputAudioFormat = common.GetStringIfEmpty(event.Session.OutputAudioFormat, info.OutputAudioFormat)
				}
			}
			if event.Type == dto.RealtimeEventTypeResponseDone && event.Response != nil {
				_, duplicate := completedResponses[responseID]
				if responseID == "" || !duplicate {
					turnUsage := event.Response.Usage
					if turnUsage == nil {
						textTokens, audioTokens, err := service.CountTokenRealtime(info, *event, info.UpstreamModelName)
						if err != nil {
							logger.LogError(c, "invalid realtime usage: "+err.Error())
							return nil, finishRealtimeUsage(c, info, sumUsage, localUsage, localOutputDelivered)
						}
						addLocalRealtimeTokens(localUsage, textTokens, audioTokens, true)
						turnUsage = localUsage
					}
					if err := service.ValidateRealtimeUsage(turnUsage); err != nil {
						logger.LogError(c, "invalid realtime usage: "+err.Error())
						return nil, finishRealtimeUsage(c, info, sumUsage, localUsage, localOutputDelivered)
					}
					// Preserve the incurred usage even if extending the reservation
					// fails: the caller settles this one session exactly once.
					if err := preConsumeUsage(c, info, turnUsage, sumUsage); err != nil {
						logger.LogError(c, "realtime reservation failed: "+err.Error())
						localUsage = &dto.RealtimeUsage{}
						return nil, sumUsage
					}
					if responseID != "" {
						completedResponses[responseID] = struct{}{}
					}
					info.IsFirstRequest = false
					localUsage, localOutputDelivered = &dto.RealtimeUsage{}, false
				}
			} else {
				textTokens, audioTokens, err := service.CountTokenRealtime(info, *event, info.UpstreamModelName)
				if err != nil {
					logger.LogError(c, "invalid realtime output: "+err.Error())
					return nil, finishRealtimeUsage(c, info, sumUsage, localUsage, localOutputDelivered)
				}
				if err := helper.WssString(c, clientConn, string(frame.message)); err != nil {
					return nil, finishRealtimeUsage(c, info, sumUsage, localUsage, localOutputDelivered)
				}
				addLocalRealtimeTokens(localUsage, textTokens, audioTokens, false)
				localOutputDelivered = localOutputDelivered || textTokens > 0 || audioTokens > 0
				continue
			}
			if err := helper.WssString(c, clientConn, string(frame.message)); err != nil {
				return nil, sumUsage
			}
		}
	}
}

// No completed usage and no delivered output is an interrupted attempt, not a
// completed response. Do not turn buffered request audio into a final charge.
func finishRealtimeUsage(c *gin.Context, info *relaycommon.RelayInfo, total, pending *dto.RealtimeUsage, outputDelivered bool) *dto.RealtimeUsage {
	if outputDelivered {
		if err := preConsumeUsage(c, info, pending, total); err != nil {
			logger.LogError(c, "realtime final reservation failed: "+err.Error())
		}
	}
	return total
}

func addLocalRealtimeTokens(usage *dto.RealtimeUsage, textTokens, audioTokens int, input bool) {
	usage.TotalTokens += textTokens + audioTokens
	if input {
		usage.InputTokens += textTokens + audioTokens
		usage.InputTokenDetails.TextTokens += textTokens
		usage.InputTokenDetails.AudioTokens += audioTokens
	} else {
		usage.OutputTokens += textTokens + audioTokens
		usage.OutputTokenDetails.TextTokens += textTokens
		usage.OutputTokenDetails.AudioTokens += audioTokens
	}
}

func preConsumeUsage(ctx *gin.Context, info *relaycommon.RelayInfo, usage *dto.RealtimeUsage, totalUsage *dto.RealtimeUsage) error {
	if usage == nil || totalUsage == nil {
		return fmt.Errorf("invalid usage pointer")
	}
	if err := service.ValidateRealtimeUsage(usage); err != nil {
		return err
	}
	// Some compatible upstreams report modality counts without inclusive
	// totals. Normalize each validated turn before adding it to prior turns;
	// otherwise a later reported total could exclude an earlier valid turn.
	normalized := *usage
	if normalized.InputTokens == 0 {
		normalized.InputTokens = usage.InputTokenDetails.TextTokens + usage.InputTokenDetails.AudioTokens + usage.InputTokenDetails.ImageTokens
	}
	if normalized.OutputTokens == 0 {
		normalized.OutputTokens = usage.OutputTokenDetails.TextTokens + usage.OutputTokenDetails.AudioTokens + usage.OutputTokenDetails.ImageTokens
	}
	if normalized.TotalTokens == 0 {
		normalized.TotalTokens = normalized.InputTokens + normalized.OutputTokens
	}
	usage = &normalized
	merged := *totalUsage
	merged.InputTokenDetails = dto.CloneInputTokenDetails(totalUsage.InputTokenDetails)
	fields := []struct {
		target *int
		value  int
	}{
		{&merged.TotalTokens, usage.TotalTokens}, {&merged.InputTokens, usage.InputTokens}, {&merged.OutputTokens, usage.OutputTokens},
		{&merged.InputTokenDetails.CachedTokens, usage.InputTokenDetails.CachedTokens},
		{&merged.InputTokenDetails.TextTokens, usage.InputTokenDetails.TextTokens}, {&merged.InputTokenDetails.AudioTokens, usage.InputTokenDetails.AudioTokens},
		{&merged.InputTokenDetails.ImageTokens, usage.InputTokenDetails.ImageTokens},
		{&merged.OutputTokenDetails.TextTokens, usage.OutputTokenDetails.TextTokens}, {&merged.OutputTokenDetails.AudioTokens, usage.OutputTokenDetails.AudioTokens},
	}
	for _, field := range fields {
		if field.value < 0 || *field.target > math.MaxInt-field.value {
			return fmt.Errorf("invalid realtime token counter")
		}
		*field.target += field.value
	}
	if totalUsage.InputTokens == 0 {
		merged.InputTokenDetails.CachedTokensDetails = dto.CloneInputTokenDetails(usage.InputTokenDetails).CachedTokensDetails
	} else if usage.InputTokenDetails.CachedTokensDetails == nil || merged.InputTokenDetails.CachedTokensDetails == nil {
		merged.InputTokenDetails.CachedTokensDetails = nil
	} else {
		cached := merged.InputTokenDetails.CachedTokensDetails
		for _, field := range []struct {
			target *int
			value  int
		}{
			{&cached.TextTokens, usage.InputTokenDetails.CachedTokensDetails.TextTokens},
			{&cached.AudioTokens, usage.InputTokenDetails.CachedTokensDetails.AudioTokens},
			{&cached.ImageTokens, usage.InputTokenDetails.CachedTokensDetails.ImageTokens},
		} {
			if field.value < 0 || *field.target > math.MaxInt-field.value {
				return fmt.Errorf("invalid realtime cached token counter")
			}
			*field.target += field.value
		}
	}
	*totalUsage = merged
	return service.PreWssConsumeQuota(ctx, info, totalUsage)
}

// Decode additional GA session fields locally while forwarding the original
// bytes unchanged. The token estimator still accepts the legacy format names.
func decodeRealtimeEvent(message []byte) (*dto.RealtimeEvent, string, error) {
	event := &dto.RealtimeEvent{}
	if err := common.Unmarshal(message, event); err != nil {
		return nil, "", err
	}
	var extra struct {
		Response *struct {
			ID string `json:"id"`
		} `json:"response"`
		Session *struct {
			Audio struct {
				Input struct {
					Format json.RawMessage `json:"format"`
				} `json:"input"`
				Output struct {
					Format json.RawMessage `json:"format"`
				} `json:"output"`
			} `json:"audio"`
		} `json:"session"`
	}
	if err := common.Unmarshal(message, &extra); err != nil {
		return nil, "", err
	}
	if event.Session != nil && extra.Session != nil {
		if format := realtimeAudioFormat(extra.Session.Audio.Input.Format); format != "" {
			event.Session.InputAudioFormat = format
		}
		if format := realtimeAudioFormat(extra.Session.Audio.Output.Format); format != "" {
			event.Session.OutputAudioFormat = format
		}
	}
	switch event.Type {
	case "response.output_audio.delta":
		event.Type = dto.RealtimeEventResponseAudioDelta
	case "response.output_audio_transcript.delta", "response.output_text.delta":
		event.Type = dto.RealtimeEventResponseAudioTranscriptionDelta
	}
	responseID := ""
	if extra.Response != nil {
		responseID = extra.Response.ID
	}
	return event, responseID, nil
}

func realtimeAudioFormat(raw json.RawMessage) string {
	var format struct {
		Type string `json:"type"`
	}
	if len(raw) == 0 || common.Unmarshal(raw, &format) != nil {
		return ""
	}
	switch format.Type {
	case "audio/pcm":
		return "pcm16"
	case "audio/pcmu":
		return "g711_ulaw"
	case "audio/pcma":
		return "g711_alaw"
	}
	return ""
}
