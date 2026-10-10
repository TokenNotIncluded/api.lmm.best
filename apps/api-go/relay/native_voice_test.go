package relay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

const nativeVoiceLiveStart = `{"type":"session.start","session":{"model":"gpt-live-1","delegation":{"type":"client"}}}`
const nativeVoiceASRStart = `{"type":"session.update","session":{"type":"transcription","audio":{"input":{"format":{"type":"audio/pcm","rate":24000},"turn_detection":null,"transcription":{"model":"gpt-realtime-whisper"}}}}}`

func nativeVoiceTestRead(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, raw, err := conn.ReadMessage()
	require.NoError(t, err)
	var event map[string]any
	require.NoError(t, json.Unmarshal(raw, &event))
	return event
}

func nativeVoiceTestWrite(t *testing.T, conn *websocket.Conn, raw string) {
	t.Helper()
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(raw)))
}

func nativeVoiceTestAudio(t *testing.T, conn *websocket.Conn, eventType string, seconds int) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"type": eventType, "audio": base64.StdEncoding.EncodeToString(make([]byte, seconds*48000))})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw))
}

func nativeVoiceTestResult(t *testing.T, result <-chan NativeVoiceResult) NativeVoiceResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(3 * time.Second):
		t.Fatal("native voice bridge did not join its readers")
		return NativeVoiceResult{}
	}
}

func nativeVoiceTestBridge(t *testing.T, kind dto.NativeVoiceKind, first string, hooks NativeVoiceHooks) (*websocket.Conn, *websocket.Conn, <-chan NativeVoiceResult, context.CancelFunc) {
	t.Helper()
	client, clientPeer := responsesWSTestPair(t)
	target, targetPeer := responsesWSTestPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	result := make(chan NativeVoiceResult, 1)
	if hooks.DrainTimeout == 0 {
		hooks.DrainTimeout = 30 * time.Millisecond
	}
	go func() { result <- NativeVoiceBridge(ctx, client, target, kind, []byte(first), hooks) }()
	if first != "" {
		nativeVoiceTestRead(t, targetPeer)
	}
	return clientPeer, targetPeer, result, cancel
}

func TestNativeVoiceLiveCumulativeUsageAndFinalization(t *testing.T) {
	client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceLive, nativeVoiceLiveStart, NativeVoiceHooks{UpstreamModel: "gpt-live-1"})
	for _, raw := range []string{
		`{"type":"session.started","session":{"model":"gpt-live-1"}}`,
		`{"type":"session.usage.updated","usage":{"seconds":2}}`,
		`{"type":"session.usage.updated","usage":{"seconds":4}}`,
		`{"type":"session.usage.updated","usage":{"seconds":3}}`,
		`{"type":"session.closed","reason":"close_requested","usage":{"seconds":6}}`,
	} {
		nativeVoiceTestWrite(t, target, raw)
		nativeVoiceTestRead(t, client)
	}
	got := nativeVoiceTestResult(t, done)
	require.NoError(t, got.Err)
	require.Equal(t, float64(6), got.Seconds)
	require.True(t, got.Finalized)
	require.False(t, got.Estimated)
}

func TestNativeVoiceLiveDisconnectDrainsFinalUsage(t *testing.T) {
	client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceLive, nativeVoiceLiveStart, NativeVoiceHooks{UpstreamModel: "gpt-live-1", DrainTimeout: time.Second})
	require.NoError(t, client.Close())
	require.Equal(t, "session.close", nativeVoiceTestRead(t, target)["type"])
	nativeVoiceTestWrite(t, target, `{"type":"session.closed","reason":"remote_hangup","usage":{"seconds":42}}`)
	got := nativeVoiceTestResult(t, done)
	require.NoError(t, got.Err)
	require.True(t, got.Finalized)
	require.False(t, got.Estimated)
	require.Equal(t, float64(42), got.Seconds)
}

func TestNativeVoiceLiveRejectedStartHasNoRuntime(t *testing.T) {
	client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceLive, nativeVoiceLiveStart, NativeVoiceHooks{UpstreamModel: "gpt-live-1"})
	nativeVoiceTestWrite(t, target, `{"type":"error","error":{"message":"not permitted"}}`)
	require.Equal(t, "error", nativeVoiceTestRead(t, client)["type"])
	got := nativeVoiceTestResult(t, done)
	require.Error(t, got.Err)
	require.False(t, got.Started)
	require.Equal(t, float64(0), got.Seconds)
}

func TestNativeVoiceLiveInvalidFinalCannotEraseObservedUsage(t *testing.T) {
	client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceLive, nativeVoiceLiveStart, NativeVoiceHooks{UpstreamModel: "gpt-live-1"})
	nativeVoiceTestWrite(t, target, `{"type":"session.usage.updated","usage":{"seconds":9}}`)
	nativeVoiceTestRead(t, client)
	nativeVoiceTestWrite(t, target, `{"type":"session.closed","usage":{"seconds":8}}`)
	require.Equal(t, "session.close", nativeVoiceTestRead(t, target)["type"])
	got := nativeVoiceTestResult(t, done)
	require.Error(t, got.Err)
	require.True(t, got.Estimated)
	require.False(t, got.Finalized)
	require.GreaterOrEqual(t, got.Seconds, float64(9))
}

func TestNativeVoiceLiveSidebandMinimumCountsOnce(t *testing.T) {
	target, targetPeer := responsesWSTestPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan NativeVoiceResult, 1)
	go func() {
		done <- NativeVoiceBridge(ctx, nil, target, dto.NativeVoiceLive, nil, NativeVoiceHooks{UpstreamModel: "gpt-live-1", InitiallyStarted: true, MinimumSeconds: 15})
	}()
	nativeVoiceTestWrite(t, targetPeer, `{"type":"session.usage.updated","usage":{"seconds":90}}`)
	nativeVoiceTestWrite(t, targetPeer, `{"type":"session.closed","usage":{"seconds":90}}`)
	got := nativeVoiceTestResult(t, done)
	require.NoError(t, got.Err)
	require.Equal(t, float64(90), got.Seconds, "WebRTC setup reservation is credited, never added to final duration")
	require.True(t, got.Finalized)
	require.False(t, got.Estimated)
}

func TestNativeVoiceTranslationUsesPCMInputDuration(t *testing.T) {
	client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceTranslation, "", NativeVoiceHooks{UpstreamModel: "gpt-realtime-translate"})
	nativeVoiceTestAudio(t, client, "session.input_audio_buffer.append", 2)
	require.Equal(t, "session.input_audio_buffer.append", nativeVoiceTestRead(t, target)["type"])
	nativeVoiceTestWrite(t, target, `{"type":"session.closed"}`)
	nativeVoiceTestRead(t, client)
	got := nativeVoiceTestResult(t, done)
	require.NoError(t, got.Err)
	require.Equal(t, float64(2), got.Seconds)
	require.True(t, got.Estimated)
	require.True(t, got.Finalized)
}

func TestNativeVoiceTranslationInitialAudioIsBudgeted(t *testing.T) {
	first, err := json.Marshal(map[string]any{"type": "session.input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(make([]byte, 48000))})
	require.NoError(t, err)
	budgeted := make(chan float64, 1)
	client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceTranslation, string(first), NativeVoiceHooks{
		UpstreamModel: "gpt-realtime-translate", EnsureBudget: func(seconds float64) error { budgeted <- seconds; return nil },
	})
	require.Equal(t, float64(1), <-budgeted)
	nativeVoiceTestWrite(t, target, `{"type":"session.closed"}`)
	nativeVoiceTestRead(t, client)
	got := nativeVoiceTestResult(t, done)
	require.Equal(t, float64(1), got.Seconds)
	require.True(t, got.Estimated)
}

func TestNativeVoiceLiveWaitsForStartedAck(t *testing.T) {
	client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceLive, nativeVoiceLiveStart, NativeVoiceHooks{UpstreamModel: "gpt-live-1"})
	nativeVoiceTestAudio(t, client, "session.input_audio.append", 1)
	require.Equal(t, "error", nativeVoiceTestRead(t, client)["type"])
	nativeVoiceTestWrite(t, target, `{"type":"session.started","session":{"model":"gpt-live-1"}}`)
	nativeVoiceTestRead(t, client)
	nativeVoiceTestAudio(t, client, "session.input_audio.append", 1)
	require.Equal(t, "session.input_audio.append", nativeVoiceTestRead(t, target)["type"])
	nativeVoiceTestWrite(t, target, `{"type":"session.closed","usage":{"seconds":1}}`)
	nativeVoiceTestRead(t, client)
	require.Equal(t, float64(1), nativeVoiceTestResult(t, done).Seconds)
}

func TestNativeVoiceLiveSidebandCancellationClosesVendor(t *testing.T) {
	target, targetPeer := responsesWSTestPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan NativeVoiceResult, 1)
	go func() {
		done <- NativeVoiceBridge(ctx, nil, target, dto.NativeVoiceLive, nil, NativeVoiceHooks{UpstreamModel: "gpt-live-1", InitiallyStarted: true, MinimumSeconds: 15, DrainTimeout: time.Second})
	}()
	cancel()
	require.Equal(t, "session.close", nativeVoiceTestRead(t, targetPeer)["type"])
	nativeVoiceTestWrite(t, targetPeer, `{"type":"session.closed","usage":{"seconds":18}}`)
	got := nativeVoiceTestResult(t, done)
	require.ErrorIs(t, got.Err, context.Canceled)
	require.Equal(t, float64(18), got.Seconds)
	require.True(t, got.Finalized)
	require.False(t, got.Estimated)
}

func TestNativeVoiceBudgetStopsBeforeAudioForward(t *testing.T) {
	client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceTranslation, "", NativeVoiceHooks{
		UpstreamModel: "gpt-realtime-translate",
		EnsureBudget: func(seconds float64) error {
			if seconds > 1 {
				return errors.New("balance exhausted")
			}
			return nil
		},
	})
	nativeVoiceTestAudio(t, client, "session.input_audio_buffer.append", 1)
	nativeVoiceTestRead(t, target)
	nativeVoiceTestAudio(t, client, "session.input_audio_buffer.append", 1)
	require.Equal(t, "error", nativeVoiceTestRead(t, client)["type"])
	require.Equal(t, "session.close", nativeVoiceTestRead(t, target)["type"], "rejected second audio frame must never reach vendor")
	nativeVoiceTestWrite(t, target, `{"type":"session.closed"}`)
	nativeVoiceTestRead(t, client)
	got := nativeVoiceTestResult(t, done)
	require.Equal(t, float64(1), got.Seconds)
	require.Equal(t, "budget_exhausted", got.Reason)
	require.ErrorContains(t, got.Err, "balance exhausted")
}

func TestNativeVoiceASROfficialDurationReplacesOnlyItsSegment(t *testing.T) {
	client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceTranscription, nativeVoiceASRStart, NativeVoiceHooks{UpstreamModel: "gpt-realtime-whisper"})
	for _, segment := range []struct {
		seconds int
		item    string
	}{{1, "item1"}, {2, "item2"}} {
		nativeVoiceTestAudio(t, client, "input_audio_buffer.append", segment.seconds)
		nativeVoiceTestRead(t, target)
		nativeVoiceTestWrite(t, client, `{"type":"input_audio_buffer.commit"}`)
		nativeVoiceTestRead(t, target)
		nativeVoiceTestWrite(t, target, `{"type":"input_audio_buffer.committed","item_id":"`+segment.item+`"}`)
		nativeVoiceTestRead(t, client)
	}
	for range 2 {
		nativeVoiceTestWrite(t, target, `{"type":"conversation.item.input_audio_transcription.completed","item_id":"item1","content_index":0,"usage":{"type":"duration","seconds":1.5}}`)
		nativeVoiceTestRead(t, client)
	}
	// Clearing uncommitted audio removes it from the missing-usage tail.
	nativeVoiceTestAudio(t, client, "input_audio_buffer.append", 3)
	nativeVoiceTestRead(t, target)
	nativeVoiceTestWrite(t, client, `{"type":"input_audio_buffer.clear"}`)
	nativeVoiceTestRead(t, target)
	require.NoError(t, target.Close())
	got := nativeVoiceTestResult(t, done)
	require.Equal(t, 3.5, got.Seconds, "official item1=1.5 + unreported item2 PCM=2; duplicate and cleared buffer add zero")
	require.True(t, got.Estimated)
	require.False(t, got.Finalized)
}

func TestNativeVoiceASROutOfOrderItemsDeduplicated(t *testing.T) {
	client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceTranscription, nativeVoiceASRStart, NativeVoiceHooks{UpstreamModel: "gpt-realtime-whisper"})
	for _, item := range []string{"item1", "item2"} {
		if item == "item2" {
			// An empty commit would be rejected by the vendor and must not steal
			// the next committed item's position in the usage FIFO.
			nativeVoiceTestWrite(t, client, `{"type":"input_audio_buffer.commit"}`)
			require.Equal(t, "error", nativeVoiceTestRead(t, client)["type"])
		}
		nativeVoiceTestAudio(t, client, "input_audio_buffer.append", 1)
		nativeVoiceTestRead(t, target)
		nativeVoiceTestWrite(t, client, `{"type":"input_audio_buffer.commit"}`)
		nativeVoiceTestRead(t, target)
		if item == "item2" {
			nativeVoiceTestWrite(t, target, `{"type":"input_audio_buffer.committed","item_id":"item1"}`)
			nativeVoiceTestRead(t, client)
		}
		nativeVoiceTestWrite(t, target, `{"type":"input_audio_buffer.committed","item_id":"`+item+`"}`)
		nativeVoiceTestRead(t, client)
	}
	for _, item := range []string{"item2", "item1", "item2"} {
		nativeVoiceTestWrite(t, target, `{"type":"conversation.item.input_audio_transcription.completed","item_id":"`+item+`","content_index":0,"usage":{"type":"duration","seconds":1.5}}`)
		nativeVoiceTestRead(t, client)
	}
	require.NoError(t, target.Close())
	got := nativeVoiceTestResult(t, done)
	require.Equal(t, float64(3), got.Seconds)
	require.True(t, got.Finalized)
	require.False(t, got.Estimated)
}

func TestNativeVoiceBlockedFramesNeverReachVendor(t *testing.T) {
	client, target, done, cancel := nativeVoiceTestBridge(t, dto.NativeVoiceTranscription, nativeVoiceASRStart, NativeVoiceHooks{
		UpstreamModel: "gpt-realtime-whisper",
		CheckClient: func(raw []byte) error {
			var event map[string]any
			_ = json.Unmarshal(raw, &event)
			if event["type"] == "forbidden" {
				return errors.New("security rule")
			}
			return nil
		},
	})
	for _, rejected := range []string{
		`{"type":"forbidden","text":"blocked"}`,
		`{"type":"session.update","session":{"audio":{"input":{"transcription":{"model":"gpt-live-transcribe"}}}}}`,
	} {
		nativeVoiceTestWrite(t, client, rejected)
		require.Equal(t, "error", nativeVoiceTestRead(t, client)["type"])
	}
	nativeVoiceTestWrite(t, client, `{"type":"input_audio_buffer.clear"}`)
	require.Equal(t, "input_audio_buffer.clear", nativeVoiceTestRead(t, target)["type"])
	cancel()
	got := nativeVoiceTestResult(t, done)
	require.ErrorIs(t, got.Err, context.Canceled)
	require.Equal(t, float64(0), got.Seconds)
}

func TestNativeVoiceRequestCancelClosesAndJoinsBothReaders(t *testing.T) {
	_, target, done, cancel := nativeVoiceTestBridge(t, dto.NativeVoiceLive, nativeVoiceLiveStart, NativeVoiceHooks{UpstreamModel: "gpt-live-1"})
	cancel()
	require.Equal(t, "session.close", nativeVoiceTestRead(t, target)["type"])
	nativeVoiceTestWrite(t, target, `{"type":"session.closed","usage":{"seconds":0}}`)
	got := nativeVoiceTestResult(t, done)
	require.ErrorIs(t, got.Err, context.Canceled)
	require.False(t, got.Estimated)
	require.True(t, got.Finalized)
	require.NoError(t, target.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err := target.ReadMessage()
	require.Error(t, err)
}

func TestNativeVoiceHookPanicRetainsUsageAndClosesReaders(t *testing.T) {
	for _, source := range []string{"budget", "security"} {
		t.Run(source, func(t *testing.T) {
			hooks := NativeVoiceHooks{UpstreamModel: "gpt-realtime-translate"}
			calls := 0
			panicOnSecond := func() {
				calls++
				if calls == 2 {
					panic("hook failure")
				}
			}
			if source == "budget" {
				hooks.EnsureBudget = func(float64) error { panicOnSecond(); return nil }
			} else {
				hooks.CheckClient = func([]byte) error { panicOnSecond(); return nil }
			}
			client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceTranslation, "", hooks)
			nativeVoiceTestAudio(t, client, "session.input_audio_buffer.append", 1)
			nativeVoiceTestRead(t, target)
			nativeVoiceTestAudio(t, client, "session.input_audio_buffer.append", 1)
			require.Equal(t, "session.close", nativeVoiceTestRead(t, target)["type"])
			got := nativeVoiceTestResult(t, done)
			require.ErrorContains(t, got.Err, "hook failure")
			require.Equal(t, "panic_safe_shutdown", got.Reason)
			require.Equal(t, float64(1), got.Seconds)
			require.True(t, got.Estimated)
		})
	}
}

func TestNativeVoiceLiveBudgetHookPanicRetainsCumulativeUsage(t *testing.T) {
	client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceLive, nativeVoiceLiveStart, NativeVoiceHooks{
		UpstreamModel: "gpt-live-1", EnsureBudget: func(seconds float64) error {
			if seconds >= 19 {
				panic("live budget failure")
			}
			return nil
		},
	})
	nativeVoiceTestWrite(t, target, `{"type":"session.started","session":{"model":"gpt-live-1"}}`)
	nativeVoiceTestRead(t, client)
	nativeVoiceTestWrite(t, target, `{"type":"session.usage.updated","usage":{"seconds":9}}`)
	require.Equal(t, "session.close", nativeVoiceTestRead(t, target)["type"])
	got := nativeVoiceTestResult(t, done)
	require.ErrorContains(t, got.Err, "live budget failure")
	require.Equal(t, "panic_safe_shutdown", got.Reason)
	require.GreaterOrEqual(t, got.Seconds, float64(9))
	require.True(t, got.Estimated)
}

func TestNativeVoiceASRDurationOverflowPreservesFiniteUsage(t *testing.T) {
	client, target, done, _ := nativeVoiceTestBridge(t, dto.NativeVoiceTranscription, nativeVoiceASRStart, NativeVoiceHooks{UpstreamModel: "gpt-realtime-whisper"})
	for _, item := range []string{"item1", "item2"} {
		nativeVoiceTestAudio(t, client, "input_audio_buffer.append", 1)
		nativeVoiceTestRead(t, target)
		nativeVoiceTestWrite(t, client, `{"type":"input_audio_buffer.commit"}`)
		nativeVoiceTestRead(t, target)
		nativeVoiceTestWrite(t, target, `{"type":"input_audio_buffer.committed","item_id":"`+item+`"}`)
		nativeVoiceTestRead(t, client)
	}
	nativeVoiceTestWrite(t, target, `{"type":"conversation.item.input_audio_transcription.completed","item_id":"item1","content_index":0,"usage":{"type":"duration","seconds":1e308}}`)
	nativeVoiceTestRead(t, client)
	nativeVoiceTestWrite(t, target, `{"type":"conversation.item.input_audio_transcription.completed","item_id":"item2","content_index":0,"usage":{"type":"duration","seconds":1e308}}`)
	got := nativeVoiceTestResult(t, done)
	require.ErrorContains(t, got.Err, "overflow")
	require.Equal(t, 1e308, got.Seconds)
	require.True(t, finiteVoiceSeconds(got.Seconds))
	_, err := json.Marshal(map[string]any{"audio_seconds": got.Seconds})
	require.NoError(t, err)
}
