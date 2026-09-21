package wsmanager

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetRegistryForTest() {
	mu.Lock()
	defer mu.Unlock()
	registry = map[int]map[uint64]*entry{}
	nextID = 0
	draining = false
	drainDone = make(chan struct{})
	drainComplete = false
}

func TestCloseChannelClosesOnlyMatchingRegistrationsOnce(t *testing.T) {
	resetRegistryForTest()
	var callsMu sync.Mutex
	calls := map[int]int{}
	_, accepted := Register(10, KindRealtime, func(code int, reason string) {
		callsMu.Lock()
		defer callsMu.Unlock()
		assert.Equal(t, websocket.ClosePolicyViolation, code)
		assert.Equal(t, "disabled", reason)
		calls[10]++
	})
	require.True(t, accepted)
	_, accepted = Register(10, KindResponses, func(int, string) {
		callsMu.Lock()
		defer callsMu.Unlock()
		calls[10]++
	})
	require.True(t, accepted)
	_, accepted = Register(20, KindResponses, func(int, string) {
		callsMu.Lock()
		defer callsMu.Unlock()
		calls[20]++
	})
	require.True(t, accepted)

	require.Equal(t, 2, CloseChannel(10, "disabled"))
	require.Equal(t, 0, CloseChannel(10, "disabled"))
	callsMu.Lock()
	defer callsMu.Unlock()
	assert.Equal(t, 2, calls[10])
	assert.Zero(t, calls[20])
}

func TestUnregisterPreventsChannelClose(t *testing.T) {
	resetRegistryForTest()
	calls := 0
	unregister, accepted := Register(10, KindResponses, func(int, string) { calls++ })
	require.True(t, accepted)
	unregister()
	assert.Zero(t, CloseChannel(10, "disabled"))
	assert.Zero(t, calls)
}

func TestDrainAllClosesTrackedSessionsAndWaitsForUnregister(t *testing.T) {
	resetRegistryForTest()

	closed := make(chan struct{}, 2)
	unregisters := make([]func(), 0, 2)
	for _, channelID := range []int{0, 42} {
		unregister, accepted := Register(channelID, KindResponses, func(code int, reason string) {
			assert.Equal(t, websocket.CloseServiceRestart, code)
			assert.Equal(t, ServiceRestartReason, reason)
			closed <- struct{}{}
		})
		require.True(t, accepted)
		unregisters = append(unregisters, unregister)
	}

	drainResult := make(chan error, 1)
	go func() {
		drainResult <- DrainAll(context.Background())
	}()

	for range unregisters {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for drain close callback")
		}
	}
	select {
	case err := <-drainResult:
		t.Fatalf("DrainAll returned before sessions unregistered: %v", err)
	default:
	}

	for _, unregister := range unregisters {
		unregister()
		unregister()
	}
	require.NoError(t, <-drainResult)
}

func TestDrainAllHonorsDeadlineAndRemainsDraining(t *testing.T) {
	resetRegistryForTest()

	var closeCalls atomic.Int32
	unregister, accepted := Register(7, KindRealtime, func(code int, reason string) {
		assert.Equal(t, websocket.CloseServiceRestart, code)
		assert.Equal(t, ServiceRestartReason, reason)
		closeCalls.Add(1)
	})
	require.True(t, accepted)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, DrainAll(ctx), context.DeadlineExceeded)
	require.Eventually(t, func() bool { return closeCalls.Load() == 1 }, time.Second, time.Millisecond)

	var rejectedCloseCalls atomic.Int32
	rejectedUnregister, accepted := Register(8, KindResponses, func(code int, reason string) {
		assert.Equal(t, websocket.CloseServiceRestart, code)
		assert.Equal(t, ServiceRestartReason, reason)
		rejectedCloseCalls.Add(1)
	})
	assert.False(t, accepted)
	rejectedUnregister()
	assert.Equal(t, int32(1), rejectedCloseCalls.Load())

	unregister()
	require.NoError(t, DrainAll(context.Background()))
	require.NoError(t, DrainAll(context.Background()))
	assert.Equal(t, int32(1), closeCalls.Load())
}

func TestRegisterRacingWithDrainIsAcceptedAndClosedOrRejectedAndClosed(t *testing.T) {
	resetRegistryForTest()

	const registrations = 200
	start := make(chan struct{})
	var closeCalls atomic.Int32
	var registrationsWG sync.WaitGroup
	var acceptedMu sync.Mutex
	acceptedUnregisters := make([]func(), 0, registrations)

	for i := 0; i < registrations; i++ {
		registrationsWG.Add(1)
		go func(channelID int) {
			defer registrationsWG.Done()
			<-start
			unregister, accepted := Register(channelID+1, KindRealtime, func(code int, reason string) {
				if code != websocket.CloseServiceRestart || reason != ServiceRestartReason {
					t.Errorf("unexpected drain close: code=%d reason=%q", code, reason)
				}
				closeCalls.Add(1)
			})
			if accepted {
				acceptedMu.Lock()
				acceptedUnregisters = append(acceptedUnregisters, unregister)
				acceptedMu.Unlock()
			}
		}(i)
	}

	drainResult := make(chan error, 1)
	go func() {
		<-start
		drainResult <- DrainAll(context.Background())
	}()
	close(start)
	registrationsWG.Wait()

	require.Eventually(t, func() bool {
		return closeCalls.Load() == registrations
	}, 2*time.Second, time.Millisecond)
	acceptedMu.Lock()
	for _, unregister := range acceptedUnregisters {
		unregister()
	}
	acceptedMu.Unlock()
	require.NoError(t, <-drainResult)
	assert.Equal(t, int32(registrations), closeCalls.Load())
}

func TestConcurrentDrainAllIsIdempotent(t *testing.T) {
	resetRegistryForTest()

	var closeCalls atomic.Int32
	unregister, accepted := Register(11, KindRealtime, func(int, string) { closeCalls.Add(1) })
	require.True(t, accepted)

	const drainers = 32
	results := make(chan error, drainers)
	for i := 0; i < drainers; i++ {
		go func() { results <- DrainAll(context.Background()) }()
	}
	require.Eventually(t, func() bool { return closeCalls.Load() == 1 }, time.Second, time.Millisecond)
	unregister()
	for i := 0; i < drainers; i++ {
		require.NoError(t, <-results)
	}
	assert.Equal(t, int32(1), closeCalls.Load())
}
