package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gorilla/websocket"
)

// NativeVoiceHooks keeps pricing and security in their existing request owners.
// EnsureBudget reserves for a cumulative duration; it must never settle a turn.
type NativeVoiceHooks struct {
	UpstreamModel    string
	InitiallyStarted bool
	StartedAt        time.Time
	MinimumSeconds   float64
	EnsureBudget     func(seconds float64) error
	CheckClient      func(raw []byte) error
	DrainTimeout     time.Duration
	TickInterval     time.Duration
	WriteTimeout     time.Duration
}

type NativeVoiceResult struct {
	Seconds   float64
	Estimated bool
	Finalized bool
	Started   bool
	Err       error
	Reason    string
}

type nativeVoiceFrame struct {
	client  bool
	control bool
	kind    int
	raw     []byte
	err     error
}

type nativeVoiceSegment struct {
	itemID   string
	bytes    int64
	reported bool
}

// NativeVoiceBridge has one collector for both writes and all usage mutation.
// Readers only enqueue frames. Closing both connections before joining readers
// also wakes a ReadMessage blocked when the HTTP request is canceled.
func NativeVoiceBridge(ctx context.Context, client, target *websocket.Conn, kind dto.NativeVoiceKind, first []byte, hooks NativeVoiceHooks) (result NativeVoiceResult) {
	if ctx == nil || target == nil {
		result.Err = errors.New("invalid native voice connection")
		return result
	}
	if err := ctx.Err(); err != nil && !hooks.InitiallyStarted {
		result.Err = err
		return result
	}
	if kind != dto.NativeVoiceLive && kind != dto.NativeVoiceTranscription && kind != dto.NativeVoiceTranslation {
		result.Err = errors.New("unsupported native voice protocol")
		return result
	}
	if hooks.DrainTimeout <= 0 {
		hooks.DrainTimeout = 2 * time.Second
	}
	if hooks.TickInterval <= 0 {
		hooks.TickInterval = time.Second
	}
	if hooks.WriteTimeout <= 0 {
		hooks.WriteTimeout = 2 * time.Second
	}
	lockedModel := hooks.UpstreamModel
	if len(first) > 0 {
		queryModel := ""
		if kind == dto.NativeVoiceTranslation {
			queryModel = lockedModel
		}
		start, err := dto.ParseNativeVoiceStart(kind, first, queryModel)
		if err != nil {
			result.Err = err
			return result
		}
		if lockedModel == "" {
			lockedModel = start.Model
		}
		if start.Model != lockedModel {
			result.Err = errors.New("initial native voice model differs from locked model")
			return result
		}
	} else if kind != dto.NativeVoiceTranslation && !hooks.InitiallyStarted {
		result.Err = errors.New("native voice initial session event required")
		return result
	}
	if lockedModel == "" {
		result.Err = errors.New("native voice model required")
		return result
	}
	if !finiteVoiceSeconds(hooks.MinimumSeconds) {
		result.Err = errors.New("invalid native voice minimum duration")
		return result
	}

	readerCtx, cancelReaders := context.WithCancel(context.Background())
	common.SetWebSocketReadLimit(client)
	common.SetWebSocketReadLimit(target)
	frames := make(chan nativeVoiceFrame, 2)
	var readers sync.WaitGroup
	enqueue := func(frame nativeVoiceFrame) bool {
		select {
		case frames <- frame:
			return true
		case <-readerCtx.Done():
			return false
		}
	}
	read := func(conn *websocket.Conn, fromClient bool) {
		defer readers.Done()
		for {
			messageType, raw, err := conn.ReadMessage()
			if !enqueue(nativeVoiceFrame{client: fromClient, kind: messageType, raw: raw, err: err}) || err != nil {
				return
			}
		}
	}
	for _, side := range []struct {
		conn   *websocket.Conn
		client bool
	}{{client, true}, {target, false}} {
		conn, fromClient := side.conn, side.client
		if conn == nil {
			continue
		}
		_ = conn.SetReadDeadline(time.Time{})
		// The default ping/close handlers write while reading. Queue their writes
		// instead, so the collector remains the sole writer even for controls.
		conn.SetPingHandler(func(data string) error {
			if !enqueue(nativeVoiceFrame{client: fromClient, control: true, kind: websocket.PongMessage, raw: []byte(data)}) {
				return context.Canceled
			}
			return nil
		})
		conn.SetCloseHandler(func(code int, text string) error { return nil })
		readers.Add(1)
		go read(conn, fromClient)
	}
	defer func() {
		cancelReaders()
		if client != nil {
			_ = client.Close()
		}
		_ = target.Close()
		readers.Wait()
	}()

	write := func(conn *websocket.Conn, messageType int, raw []byte) error {
		deadline := time.Now().Add(hooks.WriteTimeout)
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		if messageType == websocket.PongMessage || messageType == websocket.CloseMessage {
			return conn.WriteControl(messageType, raw, deadline)
		}
		return conn.WriteMessage(messageType, raw)
	}
	clientAlive := client != nil
	errorToClient := func(err error) {
		if !clientAlive {
			return
		}
		raw, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "code": "native_voice_rejected", "message": err.Error()}})
		if write(client, websocket.TextMessage, raw) != nil {
			clientAlive = false
		}
	}
	ensureBudget := func(seconds float64) error {
		if !finiteVoiceSeconds(seconds) {
			return errors.New("invalid native voice duration")
		}
		if hooks.EnsureBudget == nil {
			return nil
		}
		return hooks.EnsureBudget(seconds)
	}
	var startedAt time.Time
	var translatedBytes, firstAudioBytes int64
	var reportedSeconds float64
	liveAcknowledged := hooks.InitiallyStarted
	var bufferBytes int64
	segments := []*nativeVoiceSegment{}
	seen := map[string]struct{}{}
	pendingSeconds := func() float64 {
		bytes := bufferBytes
		for _, segment := range segments {
			if !segment.reported {
				bytes += segment.bytes
			}
		}
		return float64(bytes) / 48000
	}
	elapsed := func() float64 {
		if startedAt.IsZero() {
			return 0
		}
		return time.Since(startedAt).Seconds()
	}
	finish := func() NativeVoiceResult {
		switch kind {
		case dto.NativeVoiceLive:
			if !result.Finalized && result.Started {
				result.Seconds = math.Max(reportedSeconds, elapsed())
				result.Estimated = true
			}
			if result.Started {
				result.Seconds = math.Max(result.Seconds, hooks.MinimumSeconds)
			}
		case dto.NativeVoiceTranscription:
			pending := pendingSeconds()
			result.Seconds = reportedSeconds + pending
			result.Estimated = pending > 0
			result.Finalized = !result.Estimated
		case dto.NativeVoiceTranslation:
			result.Seconds = float64(translatedBytes) / 48000
			result.Estimated = translatedBytes > 0
		}
		return result
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result = finish()
			result.Err = fmt.Errorf("native voice collector panic: %v", recovered)
			result.Reason = "panic_safe_shutdown"
			// Sideband closure alone leaves WebRTC media alive. Request hangup
			// even on a hook panic; the caller still owns one final settlement.
			if result.Started && (kind == dto.NativeVoiceLive || kind == dto.NativeVoiceTranslation) {
				_ = write(target, websocket.TextMessage, []byte(`{"type":"session.close"}`))
			}
		}
	}()
	var initialBudgetErr error
	if hooks.InitiallyStarted {
		result.Started = true
		startedAt = hooks.StartedAt
		if startedAt.IsZero() {
			startedAt = time.Now()
		}
		if err := ensureBudget(math.Max(10, hooks.MinimumSeconds)); err != nil {
			initialBudgetErr = err
		}
	}
	if len(first) > 0 {
		if hooks.CheckClient != nil {
			if err := hooks.CheckClient(first); err != nil {
				errorToClient(err)
				result.Err = err
				return result
			}
		}
		if kind == dto.NativeVoiceTranslation {
			event, err := decodeNativeVoiceEvent(first)
			if err != nil {
				result.Err = err
				return result
			}
			eventType, _ := event["type"].(string)
			firstAudioBytes, err = nativeVoiceAudioBytes(eventType, event)
			if err != nil {
				result.Err = err
				return result
			}
			if firstAudioBytes > 0 {
				if err := ensureBudget(float64(firstAudioBytes) / 48000); err != nil {
					errorToClient(err)
					result.Err = err
					return result
				}
			}
		}
		if kind == dto.NativeVoiceLive {
			if err := ensureBudget(math.Max(10, hooks.MinimumSeconds)); err != nil {
				errorToClient(err)
				result.Err = err
				return result
			}
		}
		if err := write(target, websocket.TextMessage, first); err != nil {
			result.Err = err
			return result
		}
		// A successfully sent start may create vendor usage before its ack arrives.
		result.Started = true
		startedAt = time.Now()
		translatedBytes += firstAudioBytes
	}

	var draining bool
	var drainTimer *time.Timer
	var drainDone <-chan time.Time
	startDrain := func(reason string, err error, requestClose bool) {
		if draining {
			return
		}
		draining = true
		result.Reason = reason
		if err != nil {
			result.Err = err
		}
		if requestClose && (kind == dto.NativeVoiceLive || kind == dto.NativeVoiceTranslation) {
			_ = write(target, websocket.TextMessage, []byte(`{"type":"session.close"}`))
		}
		drainTimer = time.NewTimer(hooks.DrainTimeout)
		drainDone = drainTimer.C
	}
	defer func() {
		if drainTimer != nil {
			drainTimer.Stop()
		}
	}()
	ticker := time.NewTicker(hooks.TickInterval)
	defer ticker.Stop()
	ctxDone := ctx.Done()
	if initialBudgetErr != nil {
		startDrain("budget_exhausted", initialBudgetErr, true)
	}
	for {
		select {
		case <-ctxDone:
			// A Live sideband closing alone does not hang up WebRTC media. Ask
			// the vendor to close and keep collecting its final usage briefly.
			ctxDone = nil
			startDrain("request_canceled", ctx.Err(), true)
		case <-drainDone:
			return finish()
		case <-ticker.C:
			if kind == dto.NativeVoiceLive && result.Started && !draining {
				if err := ensureBudget(math.Max(reportedSeconds, elapsed()) + 10); err != nil {
					errorToClient(err)
					startDrain("budget_exhausted", err, true)
				}
			}
		case frame := <-frames:
			if frame.control {
				conn := target
				if frame.client {
					conn = client
				}
				if err := write(conn, frame.kind, frame.raw); err != nil {
					if frame.client {
						clientAlive = false
						startDrain("client_disconnected", err, true)
					} else {
						result.Err = err
						return finish()
					}
				}
				continue
			}
			if frame.err != nil {
				if !frame.client {
					if !websocket.IsCloseError(frame.err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
						result.Err = frame.err
					}
					return finish()
				}
				clientAlive = false
				startDrain("client_disconnected", nil, true)
				continue
			}
			if frame.client {
				if draining {
					continue
				}
				if kind == dto.NativeVoiceLive && !liveAcknowledged {
					errorToClient(errors.New("wait for session.started before sending Live commands"))
					continue
				}
				if frame.kind != websocket.TextMessage {
					errorToClient(errors.New("native voice requires JSON text events"))
					continue
				}
				if err := dto.ValidateNativeVoiceClientEvent(kind, frame.raw, lockedModel); err != nil {
					errorToClient(err)
					continue
				}
				if hooks.CheckClient != nil {
					if err := hooks.CheckClient(frame.raw); err != nil {
						errorToClient(err)
						continue
					}
				}
				event, err := decodeNativeVoiceEvent(frame.raw)
				if err != nil {
					errorToClient(err)
					continue
				}
				eventType, _ := event["type"].(string)
				if kind == dto.NativeVoiceTranscription && eventType == "input_audio_buffer.commit" && bufferBytes == 0 {
					errorToClient(errors.New("cannot commit an empty audio buffer"))
					continue
				}
				audioBytes, err := nativeVoiceAudioBytes(eventType, event)
				if err != nil {
					errorToClient(err)
					continue
				}
				if audioBytes > 0 {
					budget := reportedSeconds + pendingSeconds() + float64(audioBytes)/48000
					if kind == dto.NativeVoiceTranslation {
						budget = float64(translatedBytes+audioBytes) / 48000
					} else if kind == dto.NativeVoiceLive {
						budget = math.Max(reportedSeconds, elapsed()) + 10
					}
					if err := ensureBudget(budget); err != nil {
						errorToClient(err)
						startDrain("budget_exhausted", err, true)
						continue
					}
				}
				if err := write(target, frame.kind, frame.raw); err != nil {
					result.Err = err
					return finish()
				}
				if audioBytes > 0 {
					result.Started = true
					if kind == dto.NativeVoiceTranslation {
						translatedBytes += audioBytes
					} else if kind == dto.NativeVoiceTranscription {
						bufferBytes += audioBytes
					}
				}
				if kind == dto.NativeVoiceTranscription {
					switch eventType {
					case "input_audio_buffer.commit":
						segments = append(segments, &nativeVoiceSegment{bytes: bufferBytes})
						bufferBytes = 0
					case "input_audio_buffer.clear":
						bufferBytes = 0
					}
				}
				if eventType == "session.close" {
					startDrain("close_requested", nil, false)
				}
				continue
			}

			if frame.kind != websocket.TextMessage {
				result.Err = errors.New("invalid native voice server event")
				return finish()
			}
			event, err := decodeNativeVoiceEvent(frame.raw)
			if err != nil {
				startDrain("invalid_usage", err, true)
				continue
			}
			eventType, _ := event["type"].(string)
			switch kind {
			case dto.NativeVoiceLive:
				if eventType == "session.started" {
					liveAcknowledged = true
					result.Started = true
					if startedAt.IsZero() {
						startedAt = time.Now()
					}
				}
				if eventType == "error" && !liveAcknowledged && reportedSeconds == 0 {
					// An explicit rejection of the initial session creates no runtime.
					// An unexplained transport loss still uses the marked estimate.
					result.Started = false
					result.Reason = "start_rejected"
					result.Err = errors.New("upstream rejected native voice session start")
					if clientAlive {
						_ = write(client, frame.kind, frame.raw)
					}
					return finish()
				}
				if eventType == "session.usage.updated" || eventType == "session.closed" {
					seconds, err := nativeVoiceDuration(event, false)
					if err != nil || (eventType == "session.closed" && seconds < reportedSeconds) {
						if err == nil {
							err = errors.New("native voice cumulative usage decreased")
						}
						startDrain("invalid_usage", err, true)
						continue
					}
					reportedSeconds = math.Max(reportedSeconds, seconds)
					result.Seconds = reportedSeconds
					if eventType == "session.closed" {
						result.Finalized = true
						result.Reason, _ = event["reason"].(string)
					} else if !draining {
						if err := ensureBudget(reportedSeconds + 10); err != nil {
							errorToClient(err)
							startDrain("budget_exhausted", err, true)
						}
					}
				}
			case dto.NativeVoiceTranscription:
				if eventType == "input_audio_buffer.committed" {
					itemID, _ := event["item_id"].(string)
					alreadyAssigned := false
					for _, segment := range segments {
						if segment.itemID == itemID {
							alreadyAssigned = true
							break
						}
					}
					if itemID != "" && !alreadyAssigned {
						for _, segment := range segments {
							if segment.itemID == "" && !segment.reported {
								segment.itemID = itemID
								break
							}
						}
					}
				}
				if eventType == "conversation.item.input_audio_transcription.completed" {
					seconds, err := nativeVoiceDuration(event, true)
					itemID, _ := event["item_id"].(string)
					index, ok := event["content_index"].(float64)
					if err != nil || itemID == "" || !ok || !finiteVoiceSeconds(index) || index != math.Trunc(index) {
						if err == nil {
							err = errors.New("invalid transcription usage identity")
						}
						startDrain("invalid_usage", err, false)
						continue
					}
					identity := fmt.Sprintf("%s:%g", itemID, index)
					if _, duplicate := seen[identity]; !duplicate {
						if !finiteVoiceSeconds(reportedSeconds + seconds) {
							startDrain("invalid_usage", errors.New("transcription duration total overflow"), false)
							continue
						}
						seen[identity] = struct{}{}
						reportedSeconds += seconds
						var segment *nativeVoiceSegment
						for _, candidate := range segments {
							if candidate.itemID == itemID && !candidate.reported {
								segment = candidate
								break
							}
						}
						if segment == nil {
							for _, candidate := range segments {
								if candidate.itemID == "" && !candidate.reported {
									segment = candidate
									break
								}
							}
						}
						if segment != nil {
							segment.itemID, segment.reported = itemID, true
						}
						if !draining {
							if err := ensureBudget(reportedSeconds + pendingSeconds()); err != nil {
								errorToClient(err)
								startDrain("budget_exhausted", err, false)
							}
						}
					}
				}
			case dto.NativeVoiceTranslation:
				if eventType == "session.closed" {
					result.Finalized = true
				}
			}
			if clientAlive {
				if err := write(client, frame.kind, frame.raw); err != nil {
					clientAlive = false
					startDrain("client_disconnected", nil, true)
				}
			}
			if eventType == "session.closed" && result.Finalized {
				return finish()
			}
			if kind == dto.NativeVoiceTranscription && draining && pendingSeconds() == 0 {
				return finish()
			}
		}
	}
}

func finiteVoiceSeconds(seconds float64) bool {
	return seconds >= 0 && !math.IsNaN(seconds) && !math.IsInf(seconds, 0)
}

func decodeNativeVoiceEvent(raw []byte) (map[string]any, error) {
	var event map[string]any
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, fmt.Errorf("invalid native voice JSON: %w", err)
	}
	if event == nil {
		return nil, errors.New("native voice event must be an object")
	}
	if eventType, ok := event["type"].(string); !ok || eventType == "" {
		return nil, errors.New("native voice event type required")
	}
	return event, nil
}

func nativeVoiceDuration(event map[string]any, requireDuration bool) (float64, error) {
	usage, ok := event["usage"].(map[string]any)
	if !ok {
		return 0, errors.New("native voice usage required")
	}
	if requireDuration && usage["type"] != "duration" {
		return 0, errors.New("transcription duration usage required")
	}
	seconds, ok := usage["seconds"].(float64)
	if !ok || !finiteVoiceSeconds(seconds) {
		return 0, errors.New("invalid native voice usage seconds")
	}
	return seconds, nil
}

func nativeVoiceAudioBytes(eventType string, event map[string]any) (int64, error) {
	if eventType != "input_audio_buffer.append" && eventType != "session.input_audio_buffer.append" && eventType != "session.input_audio.append" {
		return 0, nil
	}
	audio, ok := event["audio"].(string)
	if !ok || audio == "" {
		return 0, errors.New("native voice audio required")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(audio)
	if err != nil || len(decoded)%2 != 0 {
		return 0, errors.New("native voice audio must contain base64 PCM16 samples")
	}
	return int64(len(decoded)), nil
}
