package relay

import (
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// Split forwarding at its client-write boundary so admission assertions cannot
// race the sender's final callback. The real writer stays blocked on the mutex;
// no terminal frame can reach the peer until the test releases it.
func responsesWSBlockedTerminalWrite(t *testing.T, session *responsesWSSession, state *responsesWSCallState, message []byte, delivered bool) func() error {
	t.Helper()
	session.clientWriteMu.Lock()
	state.rateMu.Lock()
	state.rateDeliveryPending = true
	state.rateMu.Unlock()
	message = session.processUpstreamMessageForState(state, message)
	if !delivered {
		require.NoError(t, session.client.Close())
	}
	started, done := make(chan struct{}), make(chan struct{})
	var writeError error
	go func() {
		defer close(done)
		close(started)
		writeError = session.writeClient(websocket.TextMessage, message)
		state.completeRateDelivery(writeError == nil)
	}()
	<-started
	var unlock sync.Once
	finish := func() error {
		unlock.Do(session.clientWriteMu.Unlock)
		select {
		case <-done:
			return writeError
		case <-time.After(2 * time.Second):
			_ = session.client.Close()
			<-done
			t.Fatal("terminal write did not finish after releasing its mutex")
			return nil
		}
	}
	t.Cleanup(func() { _ = finish() })
	select {
	case <-done:
		t.Fatal("terminal write completed while its client-write mutex was held")
	default:
	}
	return finish
}

func TestResponsesWSFailedRateReservationReleasedBeforeTerminalDelivery(t *testing.T) {
	for _, delivered := range []bool{true, false} {
		name := "delivered"
		if !delivered {
			name = "write failure"
		}
		t.Run(name, func(t *testing.T) {
			previousPost := postResponsesWSConsumeQuota
			t.Cleanup(func() { postResponsesWSConsumeQuota = previousPost })
			posted := 0
			postResponsesWSConsumeQuota = func(_ *gin.Context, _ *relaycommon.RelayInfo, usage *dto.Usage, _ []string) {
				posted++
				require.Equal(t, 10, usage.TotalTokens)
			}
			client, peer := responsesWSTestPair(t)
			state := responsesWSCorrelationState("failed-rate-release")
			limiter := common.InMemoryRateLimiter{}
			reserved, admitted := limiter.Reserve("failed-terminal", 1, 60)
			require.True(t, admitted)
			completions := []bool{}
			state.commitRate = func(success bool) { completions = append(completions, success); reserved(success) }
			session := &responsesWSSession{client: client, current: state}
			finishWrite := responsesWSBlockedTerminalWrite(t, session, state, []byte(`{"type":"response.failed","response":{"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`), delivered)

			require.Nil(t, session.getCurrent())
			require.Equal(t, 1, posted, "partial output remains billable before terminal delivery")
			next, admitted := limiter.Reserve("failed-terminal", 1, 60)
			if admitted {
				next(false)
			}
			require.True(t, admitted, "clearing a failed turn must release its reservation even while its terminal write is blocked")
			require.Equal(t, []bool{false}, completions)

			err := finishWrite()
			if delivered {
				require.NoError(t, err)
				require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
				_, payload, err := peer.ReadMessage()
				require.NoError(t, err)
				require.Contains(t, string(payload), "response.failed")
			} else {
				require.Error(t, err)
			}
			state.completeRateDelivery(true)
			state.finishRate(true)
			session.failCurrent()
			require.Equal(t, []bool{false}, completions, "delivery and cleanup must not complete the reservation twice or turn a failure into success")
		})
	}
}

func TestResponsesWSSuccessRateReservationWaitsForTerminalDelivery(t *testing.T) {
	for _, delivered := range []bool{true, false} {
		name := "delivered"
		if !delivered {
			name = "write failure"
		}
		t.Run(name, func(t *testing.T) {
			previousPost := postResponsesWSConsumeQuota
			t.Cleanup(func() { postResponsesWSConsumeQuota = previousPost })
			posted := 0
			postResponsesWSConsumeQuota = func(_ *gin.Context, _ *relaycommon.RelayInfo, usage *dto.Usage, _ []string) {
				posted++
				require.Equal(t, 10, usage.TotalTokens)
			}
			client, peer := responsesWSTestPair(t)
			state := responsesWSCorrelationState("success-rate-delivery")
			limiter := common.InMemoryRateLimiter{}
			reserved, admitted := limiter.Reserve("successful-terminal", 1, 60)
			require.True(t, admitted)
			completions := []bool{}
			state.commitRate = func(success bool) { completions = append(completions, success); reserved(success) }
			session := &responsesWSSession{client: client, current: state}
			finishWrite := responsesWSBlockedTerminalWrite(t, session, state, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`), delivered)

			require.Nil(t, session.getCurrent())
			require.Equal(t, 1, posted)
			require.Empty(t, completions, "a successful outcome must remain pending until its terminal is actually delivered")
			competing, admitted := limiter.Reserve("successful-terminal", 1, 60)
			if admitted {
				competing(false)
			}
			require.False(t, admitted, "a blocked successful terminal must retain its in-flight reservation")

			err := finishWrite()
			if delivered {
				require.NoError(t, err)
				require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
				_, payload, err := peer.ReadMessage()
				require.NoError(t, err)
				require.Contains(t, string(payload), "response.completed")
			} else {
				require.Error(t, err)
			}
			require.Equal(t, []bool{delivered}, completions)
			next, admitted := limiter.Reserve("successful-terminal", 1, 60)
			if admitted {
				next(false)
			}
			require.Equal(t, !delivered, admitted, "only an actually delivered successful terminal consumes the success slot")
			state.completeRateDelivery(!delivered)
			state.finishRate(false)
			session.failCurrent()
			require.Equal(t, []bool{delivered}, completions, "the delivery callback completes the reservation exactly once")
		})
	}
}
