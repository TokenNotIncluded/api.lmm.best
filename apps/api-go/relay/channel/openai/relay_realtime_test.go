package openai

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type realtimeTestBilling struct {
	mu             sync.Mutex
	reserved       int
	calls          []int
	limit          int
	settled        int
	panicOnReserve bool
}

func (b *realtimeTestBilling) Reserve(target int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, target)
	if b.panicOnReserve {
		panic("reservation helper panic")
	}
	if b.limit > 0 && target > b.limit {
		return fmt.Errorf("budget exhausted")
	}
	if target > b.reserved {
		b.reserved = target
	}
	return nil
}
func (b *realtimeTestBilling) GetPreConsumedQuota() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reserved
}
func (b *realtimeTestBilling) Settle(int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.settled++
	return nil
}
func (*realtimeTestBilling) Refund(*gin.Context) {}
func (*realtimeTestBilling) NeedsRefund() bool   { return false }

func realtimeSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			accepted <- conn
		}
	}))
	t.Cleanup(server.Close)
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	serverConn := <-accepted
	t.Cleanup(func() { _ = peer.Close(); _ = serverConn.Close() })
	return serverConn, peer
}

type realtimeTestResult struct {
	usage *dto.RealtimeUsage
	err   *types.NewAPIError
}

func realtimeHandlerFixture(t *testing.T, billing *realtimeTestBilling) (*websocket.Conn, *websocket.Conn, context.CancelFunc, <-chan realtimeTestResult) {
	t.Helper()
	clientConn, client := realtimeSocketPair(t)
	upstream, targetConn := realtimeSocketPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil).WithContext(ctx)
	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "gpt-realtime-test"},
		OriginModelName: "gpt-realtime-test", ClientWs: clientConn, TargetWs: targetConn,
		InputAudioFormat: "pcm16", OutputAudioFormat: "pcm16", Billing: billing,
		PriceData: hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, AudioRatio: 1, AudioCompletionRatio: 1,
			GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}},
	}
	finished := make(chan realtimeTestResult, 1)
	go func() {
		err, usage := OpenaiRealtimeHandler(c, info)
		finished <- realtimeTestResult{usage: usage, err: err}
	}()
	return client, upstream, cancel, finished
}

func awaitRealtimeResult(t *testing.T, results <-chan realtimeTestResult) realtimeTestResult {
	t.Helper()
	select {
	case result := <-results:
		require.Nil(t, result.err)
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("realtime handler did not stop and join its readers")
		return realtimeTestResult{}
	}
}

func forwardRealtimeTurn(t *testing.T, client, upstream *websocket.Conn, id string, input, output int) {
	t.Helper()
	message := fmt.Sprintf(`{"type":"response.done","response":{"id":%q,"usage":{"total_tokens":%d,"input_tokens":%d,"output_tokens":%d,"input_token_details":{"text_tokens":%d},"output_token_details":{"text_tokens":%d}}}}`, id, input+output, input, output, input, output)
	require.NoError(t, upstream.WriteMessage(websocket.TextMessage, []byte(message)))
	require.NoError(t, client.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, forwarded, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, message, string(forwarded))
}

func TestRealtimeHandlerTwoTurnsReserveCumulativeOnce(t *testing.T) {
	billing := &realtimeTestBilling{}
	client, upstream, cancel, results := realtimeHandlerFixture(t, billing)
	forwardRealtimeTurn(t, client, upstream, "turn-1", 3, 1)
	// An upstream repeated response.done is still forwarded, but not billed twice.
	forwardRealtimeTurn(t, client, upstream, "turn-1", 3, 1)
	forwardRealtimeTurn(t, client, upstream, "turn-2", 5, 2)
	cancel()
	result := awaitRealtimeResult(t, results)
	require.Equal(t, 11, result.usage.TotalTokens)
	require.Equal(t, 8, result.usage.InputTokens)
	require.Equal(t, 3, result.usage.OutputTokens)
	billing.mu.Lock()
	defer billing.mu.Unlock()
	require.Equal(t, []int{4, 11}, billing.calls)
	require.Equal(t, 11, billing.reserved)
	require.Zero(t, billing.settled, "only the outer WssHelper owns final settlement")
}

func TestRealtimeHandlerRequestCancellationClosesBothSockets(t *testing.T) {
	billing := &realtimeTestBilling{}
	client, upstream, cancel, results := realtimeHandlerFixture(t, billing)
	cancel()
	result := awaitRealtimeResult(t, results)
	require.Zero(t, result.usage.TotalTokens)
	for _, socket := range []*websocket.Conn{client, upstream} {
		require.NoError(t, socket.SetReadDeadline(time.Now().Add(time.Second)))
		_, _, err := socket.ReadMessage()
		require.Error(t, err)
	}
	require.Empty(t, billing.calls)
}

func TestRealtimeHandlerClientDisconnectWithoutCompletionRefundsInputEstimate(t *testing.T) {
	billing := &realtimeTestBilling{}
	client, upstream, _, results := realtimeHandlerFixture(t, billing)
	audio := base64.StdEncoding.EncodeToString(make([]byte, 48000))
	message := []byte(fmt.Sprintf(`{"type":"input_audio_buffer.append","audio":%q}`, audio))
	require.NoError(t, client.WriteMessage(websocket.TextMessage, message))
	require.NoError(t, upstream.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, forwarded, err := upstream.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, message, forwarded)
	require.NoError(t, client.Close())
	result := awaitRealtimeResult(t, results)
	require.Zero(t, result.usage.TotalTokens)
	require.Empty(t, billing.calls)
}

func TestRealtimeHandlerMissingResponseDoesNotPanic(t *testing.T) {
	billing := &realtimeTestBilling{}
	client, upstream, cancel, results := realtimeHandlerFixture(t, billing)
	require.NoError(t, upstream.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.done"}`)))
	require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err := client.ReadMessage()
	require.NoError(t, err)
	cancel()
	result := awaitRealtimeResult(t, results)
	require.Zero(t, result.usage.TotalTokens)
}

func TestRealtimeHandlerReservationFailureKeepsIncurredUsageAndStops(t *testing.T) {
	billing := &realtimeTestBilling{limit: 6}
	client, upstream, _, results := realtimeHandlerFixture(t, billing)
	forwardRealtimeTurn(t, client, upstream, "turn-1", 3, 1)
	message := []byte(`{"type":"response.done","response":{"id":"turn-2","usage":{"total_tokens":7,"input_tokens":5,"output_tokens":2,"input_token_details":{"text_tokens":5},"output_token_details":{"text_tokens":2}}}}`)
	require.NoError(t, upstream.WriteMessage(websocket.TextMessage, message))
	result := awaitRealtimeResult(t, results)
	require.Equal(t, 11, result.usage.TotalTokens)
	require.Equal(t, []int{4, 11}, billing.calls)
	require.NoError(t, upstream.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err := upstream.ReadMessage()
	require.Error(t, err)
}

func TestRealtimeHandlerInvalidTurnDoesNotContaminateRecordedUsage(t *testing.T) {
	billing := &realtimeTestBilling{}
	client, upstream, _, results := realtimeHandlerFixture(t, billing)
	forwardRealtimeTurn(t, client, upstream, "turn-1", 3, 1)
	message := []byte(`{"type":"response.done","response":{"id":"invalid-turn","usage":{"total_tokens":100,"input_tokens":100,"input_token_details":{"text_tokens":100,"cached_tokens":-1}}}}`)
	require.NoError(t, upstream.WriteMessage(websocket.TextMessage, message))
	result := awaitRealtimeResult(t, results)
	require.Equal(t, 4, result.usage.TotalTokens)
	require.Equal(t, 3, result.usage.InputTokens)
	require.Equal(t, []int{4}, billing.calls)
}

func TestRealtimeCollectorPanicClosesWorkersAndKeepsRecordedCost(t *testing.T) {
	billing := &realtimeTestBilling{panicOnReserve: true}
	_, upstream, _, results := realtimeHandlerFixture(t, billing)
	message := []byte(`{"type":"response.done","response":{"id":"turn-1","usage":{"total_tokens":4,"input_tokens":3,"output_tokens":1,"input_token_details":{"text_tokens":3},"output_token_details":{"text_tokens":1}}}}`)
	require.NoError(t, upstream.WriteMessage(websocket.TextMessage, message))
	result := awaitRealtimeResult(t, results)
	require.Equal(t, 4, result.usage.TotalTokens)
	require.NoError(t, upstream.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err := upstream.ReadMessage()
	require.Error(t, err)
}

func TestDecodeRealtimeGAFormatsAndOutputAlias(t *testing.T) {
	event, _, err := decodeRealtimeEvent([]byte(`{"type":"session.updated","session":{"audio":{"input":{"format":{"type":"audio/pcmu"}},"output":{"format":{"type":"audio/pcm","rate":24000}}}}}`))
	require.NoError(t, err)
	require.Equal(t, "g711_ulaw", event.Session.InputAudioFormat)
	require.Equal(t, "pcm16", event.Session.OutputAudioFormat)
	event, _, err = decodeRealtimeEvent([]byte(`{"type":"response.output_audio.delta","delta":"AA=="}`))
	require.NoError(t, err)
	require.Equal(t, dto.RealtimeEventResponseAudioDelta, event.Type)
}

func TestRealtimeReportedModalityCountsAccumulateWithMissingTotals(t *testing.T) {
	billing := &realtimeTestBilling{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{Billing: billing, PriceData: hosttypes.PriceData{
		ModelRatio: 1, CacheRatio: 0.5, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
	}}
	total := &dto.RealtimeUsage{}
	turn := &dto.RealtimeUsage{InputTokenDetails: dto.InputTokenDetails{
		TextTokens: 3, CachedTokens: 1, CachedTokensDetails: &dto.CachedTokenDetails{TextTokens: 1},
	}}
	require.NoError(t, preConsumeUsage(c, info, turn, total))
	// Mix an explicit inclusive total with the preceding modality-only report.
	turn.InputTokens, turn.TotalTokens = 3, 3
	require.NoError(t, preConsumeUsage(c, info, turn, total))
	require.Equal(t, 6, total.InputTokens)
	require.Equal(t, 6, total.TotalTokens)
	require.Equal(t, 2, total.InputTokenDetails.CachedTokensDetails.TextTokens)
	require.Equal(t, 5, billing.GetPreConsumedQuota())
}
