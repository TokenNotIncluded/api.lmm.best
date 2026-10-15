// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package remotecontrol

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func cipher(size int) Ciphertext {
	return Ciphertext{base64.RawURLEncoding.EncodeToString(make([]byte, 12)), base64.RawURLEncoding.EncodeToString(make([]byte, size))}
}
func heartbeat(t *testing.T, r *relay, owner int64, id string) Snapshot {
	t.Helper()
	value, err := r.heartbeat(owner, id, "device_abcdefgh", cipher(16))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func TestRelayOwnerIsolation(t *testing.T) {
	r := newRelay()
	heartbeat(t, r, 1, "session_shared")
	if len(r.list(2)) != 0 {
		t.Fatal("cross-owner list")
	}
	if _, err := r.read(2, "session_shared", 0); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := r.append(2, "session_shared", "controller", cipher(16)); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	r.remove(2, "session_shared")
	if len(r.list(1)) != 1 {
		t.Fatal("cross-owner deletion")
	}
	heartbeat(t, r, 2, "session_shared")
	_, err := r.append(1, "session_shared", "plugin", cipher(16))
	if err != nil {
		t.Fatal(err)
	}
	if messages, err := r.read(2, "session_shared", 0); err != nil || len(messages) != 0 {
		t.Fatal("shared ID leaked messages")
	}
}
func TestRelayExpiryGenerationAndHeartbeat(t *testing.T) {
	r := newRelay()
	now := time.Unix(1000, 0)
	r.now = func() time.Time { return now }
	first := heartbeat(t, r, 1, "session_abcdefgh")
	now = now.Add(SessionTTL - time.Second)
	if _, err := r.append(1, first.SessionID, "controller", cipher(16)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := r.read(1, first.SessionID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("message traffic extended heartbeat", err)
	}
	if r.retained != 0 || len(r.owners) != 0 {
		t.Fatal("expiry retained storage")
	}
	second := heartbeat(t, r, 1, first.SessionID)
	if second.Generation == first.Generation {
		t.Fatal("generation reused after expiry")
	}
	message, err := r.append(1, first.SessionID, "plugin", cipher(16))
	if err != nil || message.Sequence != 1 {
		t.Fatal("cursor not reset", err)
	}
	stable := heartbeat(t, r, 1, first.SessionID)
	if stable.Generation != second.Generation {
		t.Fatal("heartbeat changed generation")
	}
	if _, err := r.heartbeat(1, first.SessionID, "device_different", cipher(16)); !errors.Is(err, ErrConflict) {
		t.Fatal("active device overwritten", err)
	}
	fresh := newRelay()
	restarted := heartbeat(t, fresh, 1, first.SessionID)
	if restarted.Generation == second.Generation || len(fresh.list(2)) != 0 {
		t.Fatal("restart retained state")
	}
}
func TestRelayRollingMessagesAndCopies(t *testing.T) {
	r := newRelay()
	heartbeat(t, r, 1, "session_abcdefgh")
	for i := 0; i < MaxMessages+2; i++ {
		if _, err := r.append(1, "session_abcdefgh", "plugin", cipher(16)); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := r.read(1, "session_abcdefgh", 0)
	if err != nil || len(messages) != MaxMessages || messages[0].Sequence != 3 || messages[len(messages)-1].Sequence != 130 {
		t.Fatal("rolling cursor", err)
	}
	messages[0].Sender = "mutated"
	reread, _ := r.read(1, "session_abcdefgh", 2)
	if reread[0].Sender != "plugin" {
		t.Fatal("mutable storage leaked")
	}
	snapshot := r.list(1)
	snapshot[0].DeviceID = "mutated"
	if r.list(1)[0].DeviceID != "device_abcdefgh" {
		t.Fatal("mutable snapshot leaked")
	}
	filtered, _ := r.read(1, "session_abcdefgh", 129)
	if len(filtered) != 1 || filtered[0].Sequence != 130 {
		t.Fatal("cursor filtering")
	}
	r.remove(1, "session_abcdefgh")
	if r.retained != 0 {
		t.Fatal("deletion leaked byte budget")
	}
}
func TestRelayByteBudgetAndAtomicRejection(t *testing.T) {
	r := newRelay()
	first := heartbeat(t, r, 1, "session_abcdefgh")
	r.maxRetained = r.retained
	if _, err := r.append(1, first.SessionID, "plugin", cipher(16)); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	before := r.retained
	if _, err := r.heartbeat(1, first.SessionID, first.DeviceID, cipher(17)); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if r.retained != before || r.list(1)[0].Metadata != first.Metadata {
		t.Fatal("rejection mutated records")
	}
	r.maxRetained = MaxRetainedBytes
	message, err := r.append(1, first.SessionID, "plugin", cipher(16))
	if err != nil || message.Sequence != 1 {
		t.Fatal("rejection consumed cursor", err)
	}
	// Large events roll by bytes before the count limit, rather than deadlocking
	// a live relay that needs to accept the next stop/ack event.
	for i := 0; i < 100; i++ {
		if _, err := r.append(1, first.SessionID, "plugin", cipher(64<<10)); err != nil {
			t.Fatal(err)
		}
	}
	messages, _ := r.read(1, first.SessionID, 0)
	if len(messages) >= MaxMessages || len(messages) == 0 || messages[len(messages)-1].Sequence != 101 || r.retained > MaxSessionBytes {
		t.Fatal("byte rolling limit")
	}
	total := 0
	for _, owned := range r.owners {
		for _, value := range owned {
			total += sessionBytes(value)
		}
	}
	if total != r.retained {
		t.Fatalf("budget drift %d != %d", total, r.retained)
	}
}
func TestRelayUserAndGlobalLimits(t *testing.T) {
	r := newRelay()
	for i := 0; i < MaxSessionsPerUser; i++ {
		heartbeat(t, r, 1, fmt.Sprintf("session_%03d", i))
	}
	if _, err := r.heartbeat(1, "session_overflow", "device_abcdefgh", cipher(16)); !errors.Is(err, ErrCapacity) {
		t.Fatal("per-user count", err)
	}
	for owner := int64(2); owner <= MaxSessions/MaxSessionsPerUser; owner++ {
		for i := 0; i < MaxSessionsPerUser; i++ {
			heartbeat(t, r, owner, fmt.Sprintf("session_%03d", i))
		}
	}
	if _, err := r.heartbeat(100, "session_overflow", "device_abcdefgh", cipher(16)); !errors.Is(err, ErrCapacity) {
		t.Fatal("global count", err)
	}
	heartbeat(t, r, 1, "session_000") // Existing heartbeat still succeeds at capacity.
	r = newRelay()
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("session_%03d", i)
		heartbeat(t, r, 1, id)
		for j := 0; j < 12; j++ {
			_, err := r.append(1, id, "plugin", cipher(64<<10))
			if errors.Is(err, ErrCapacity) {
				if r.ownerBytes(1) > MaxUserBytes {
					t.Fatal("owner byte limit")
				}
				heartbeat(t, r, 2, "session_independent")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Fatal("owner byte capacity not enforced")
}
func TestRelayConcurrentMonotonicCursor(t *testing.T) {
	r := newRelay()
	heartbeat(t, r, 1, "session_abcdefgh")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.append(1, "session_abcdefgh", "plugin", cipher(16)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	messages, _ := r.read(1, "session_abcdefgh", 0)
	if len(messages) != 100 {
		t.Fatal("lost concurrent messages")
	}
	for i, message := range messages {
		if message.Sequence != int64(i+1) {
			t.Fatal("nonmonotonic sequence")
		}
	}
}
