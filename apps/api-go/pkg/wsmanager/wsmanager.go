package wsmanager

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gorilla/websocket"
)

const (
	KindRealtime  = "realtime"
	KindResponses = "responses"

	defaultCloseReason = "channel disabled or deleted"
	// ServiceRestartReason is sent with CloseServiceRestart during process drain.
	ServiceRestartReason = "service restarting"
	redisChannel         = "new-api:wsmanager:channel-close"
)

type CloseFunc func(code int, reason string)

type entry struct {
	id        uint64
	channelID int
	kind      string
	close     CloseFunc
	closeOnce sync.Once
}

func (e *entry) claimClose(code int, reason string) func() {
	var claimed bool
	e.closeOnce.Do(func() { claimed = true })
	if !claimed {
		return nil
	}
	return func() { e.close(code, reason) }
}

type closeEvent struct {
	ChannelIDs []int  `json:"channel_ids"`
	Reason     string `json:"reason"`
	Origin     string `json:"origin"`
}

var (
	mu            sync.Mutex
	nextID        uint64
	registry      = map[int]map[uint64]*entry{}
	draining      bool
	drainDone     = make(chan struct{})
	drainComplete bool

	originOnce sync.Once
	originID   string

	subscriberOnce sync.Once
)

// Register tracks a WebSocket session. Channel ID zero registers a session for
// process-wide draining only; positive IDs also participate in channel-policy
// closes. Once draining begins, registration is permanently rejected and the
// supplied close function is invoked with CloseServiceRestart.
func Register(channelID int, kind string, closeFn CloseFunc) (unregister func(), accepted bool) {
	if channelID < 0 || closeFn == nil {
		return func() {}, false
	}
	e := &entry{
		id:        atomic.AddUint64(&nextID, 1),
		channelID: channelID,
		kind:      kind,
		close:     closeFn,
	}

	mu.Lock()
	if draining {
		mu.Unlock()
		if close := e.claimClose(websocket.CloseServiceRestart, ServiceRestartReason); close != nil {
			close()
		}
		return func() {}, false
	}
	if registry[channelID] == nil {
		registry[channelID] = map[uint64]*entry{}
	}
	registry[channelID][e.id] = e
	mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			entries := registry[channelID]
			delete(entries, e.id)
			if len(entries) == 0 {
				delete(registry, channelID)
			}
			signalDrainedLocked()
			mu.Unlock()
		})
	}, true
}

// DrainAll atomically puts the manager into permanent draining mode, closes
// every tracked session with CloseServiceRestart, and waits for all of them to
// unregister or for ctx to expire. Repeated and concurrent calls are safe.
func DrainAll(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	mu.Lock()
	if !draining {
		draining = true
		for _, channelEntries := range registry {
			for _, e := range channelEntries {
				if close := e.claimClose(websocket.CloseServiceRestart, ServiceRestartReason); close != nil {
					// Start every close before publishing drain completion. The close
					// callback may unregister and will wait for mu without blocking us.
					go close()
				}
			}
		}
		signalDrainedLocked()
	}
	done := drainDone
	mu.Unlock()

	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func CloseChannel(channelID int, reason string) int {
	return CloseChannels([]int{channelID}, reason)
}

func CloseChannels(channelIDs []int, reason string) int {
	reason = normalizeReason(reason)
	entries, closes := takeEntries(channelIDs, websocket.ClosePolicyViolation, reason)
	for _, close := range closes {
		close()
	}
	if len(entries) > 0 {
		common.SysLog(fmt.Sprintf("closed %d active websocket connection(s), channels=%v, kinds=%v, reason=%s", len(entries), entryChannelIDs(entries), entryKindCounts(entries), reason))
	}
	return len(entries)
}

func CloseChannelsAndBroadcast(channelIDs []int, reason string) int {
	count := CloseChannels(channelIDs, reason)
	if err := PublishCloseChannels(context.Background(), channelIDs, reason); err != nil {
		common.SysLog(fmt.Sprintf("failed to publish websocket close event: %v", err))
	}
	return count
}

// StartSubscriber preserves the source-compatible detached startup API.
func StartSubscriber(ctx context.Context) {
	subscriberOnce.Do(func() { go RunSubscriber(ctx) })
}

// RunSubscriber owns the cross-instance close-event subscription until ctx is
// cancelled. Application lifecycle code should run this synchronously in its
// managed goroutine registry so Valkey cannot close before the subscriber.
func RunSubscriber(ctx context.Context) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	subscribe(ctx)
}

func PublishCloseChannels(ctx context.Context, channelIDs []int, reason string) error {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	ids := uniqueChannelIDs(channelIDs)
	if len(ids) == 0 {
		return nil
	}
	payload, err := common.Marshal(closeEvent{ChannelIDs: ids, Reason: normalizeReason(reason), Origin: getOriginID()})
	if err != nil {
		return err
	}
	return common.RDB.Publish(ctx, redisChannel, payload).Err()
}

func subscribe(ctx context.Context) {
	pubsub := common.RDB.Subscribe(ctx, redisChannel)
	defer pubsub.Close()
	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var event closeEvent
			if err := common.Unmarshal([]byte(msg.Payload), &event); err != nil {
				common.SysLog(fmt.Sprintf("failed to unmarshal websocket close event: %v", err))
				continue
			}
			if event.Origin != getOriginID() {
				CloseChannels(event.ChannelIDs, event.Reason)
			}
		}
	}
}

func takeEntries(channelIDs []int, code int, reason string) ([]*entry, []func()) {
	ids := uniqueChannelIDs(channelIDs)
	mu.Lock()
	defer mu.Unlock()
	var entries []*entry
	var closes []func()
	for _, channelID := range ids {
		for _, e := range registry[channelID] {
			if close := e.claimClose(code, reason); close != nil {
				entries = append(entries, e)
				closes = append(closes, close)
			}
		}
		if !draining {
			delete(registry, channelID)
		}
	}
	signalDrainedLocked()
	return entries, closes
}

func signalDrainedLocked() {
	if draining && !drainComplete && len(registry) == 0 {
		drainComplete = true
		close(drainDone)
	}
}

func entryChannelIDs(entries []*entry) []int {
	ids := make([]int, 0, len(entries))
	for _, e := range entries {
		if e != nil {
			ids = append(ids, e.channelID)
		}
	}
	return uniqueChannelIDs(ids)
}

func entryKindCounts(entries []*entry) map[string]int {
	counts := make(map[string]int)
	for _, e := range entries {
		if e != nil {
			counts[e.kind]++
		}
	}
	return counts
}

func uniqueChannelIDs(channelIDs []int) []int {
	seen := make(map[int]struct{}, len(channelIDs))
	ids := make([]int, 0, len(channelIDs))
	for _, id := range channelIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func normalizeReason(reason string) string {
	if reason == "" {
		return defaultCloseReason
	}
	return reason
}

func getOriginID() string {
	originOnce.Do(func() {
		name := common.NodeName
		if name == "" {
			name = "node"
		}
		originID = fmt.Sprintf("%s-%d-%d", name, os.Getpid(), time.Now().UnixNano())
	})
	return originID
}
