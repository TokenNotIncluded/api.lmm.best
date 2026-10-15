// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
// Package remotecontrol owns opaque, ephemeral extension records, never identity.
package remotecontrol

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sort"
	"sync"
	"time"
)

const (
	SessionTTL         = 2 * time.Minute
	MaxSessionsPerUser = 32
	MaxSessions        = 512
	MaxMessages        = 128
	MaxRetainedBytes   = 32 << 20 // Actual encoded payload bytes, not decoded estimates.
	MaxUserBytes       = 4 << 20
	MaxSessionBytes    = 1 << 20
)

var (
	ErrNotFound = errors.New("remote session unavailable")
	ErrCapacity = errors.New("remote relay capacity reached")
	ErrConflict = errors.New("remote session device changed")
)

type Ciphertext struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}
type Message struct {
	Ciphertext
	Sequence  int64  `json:"sequence"`
	Sender    string `json:"sender"`
	CreatedAt int64  `json:"created_at"`
}
type Snapshot struct {
	SessionID  string     `json:"session_id"`
	DeviceID   string     `json:"device_id"`
	Generation string     `json:"generation"`
	Metadata   Ciphertext `json:"metadata"`
	ExpiresAt  int64      `json:"expires_at"`
	UpdatedAt  int64      `json:"updated_at"`
}
type session struct {
	Snapshot
	expires  time.Time
	next     int64
	messages []Message
	changed  chan struct{}
}
type relay struct {
	mu          sync.Mutex
	owners      map[int64]map[string]*session
	now         func() time.Time
	retained    int
	maxRetained int
}

func newRelay() *relay {
	return &relay{owners: make(map[int64]map[string]*session), now: time.Now, maxRetained: MaxRetainedBytes}
}
func payloadBytes(c Ciphertext) int { return len(c.Nonce) + len(c.Ciphertext) }
func sessionBytes(s *session) int {
	bytes := payloadBytes(s.Metadata)
	for _, message := range s.messages {
		bytes += payloadBytes(message.Ciphertext)
	}
	return bytes
}

// Expiry is swept on every operation; no goroutine or socket starts in New.
func (r *relay) expire(now time.Time) {
	for owner, sessions := range r.owners {
		for id, value := range sessions {
			if !value.expires.After(now) {
				r.retained -= sessionBytes(value)
				close(value.changed)
				delete(sessions, id)
			}
		}
		if len(sessions) == 0 {
			delete(r.owners, owner)
		}
	}
}
func (r *relay) ownerBytes(owner int64) int {
	total := 0
	for _, value := range r.owners[owner] {
		total += sessionBytes(value)
	}
	return total
}
func (r *relay) heartbeat(owner int64, id, device string, metadata Ciphertext) (Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.expire(now)
	owned := r.owners[owner]
	value := owned[id]
	if value != nil && value.DeviceID != device {
		return Snapshot{}, ErrConflict
	}
	delta := payloadBytes(metadata)
	if value != nil {
		delta -= payloadBytes(value.Metadata)
	}
	if r.retained+delta > r.maxRetained || r.ownerBytes(owner)+delta > MaxUserBytes {
		return Snapshot{}, ErrCapacity
	}
	if value != nil && sessionBytes(value)+delta > MaxSessionBytes {
		return Snapshot{}, ErrCapacity
	}
	if value == nil {
		if len(owned) >= MaxSessionsPerUser {
			return Snapshot{}, ErrCapacity
		}
		count := 0
		for _, sessions := range r.owners {
			count += len(sessions)
		}
		if count >= MaxSessions {
			return Snapshot{}, ErrCapacity
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return Snapshot{}, err
		}
		value = &session{Snapshot: Snapshot{SessionID: id, DeviceID: device, Generation: base64.RawURLEncoding.EncodeToString(random[:])}, next: 1, changed: make(chan struct{})}
		if owned == nil {
			owned = make(map[string]*session)
			r.owners[owner] = owned
		}
		owned[id] = value
	}
	value.Metadata = metadata
	value.expires = now.Add(SessionTTL)
	value.ExpiresAt = value.expires.Unix()
	value.UpdatedAt = now.Unix()
	r.retained += delta
	value.notify()
	return value.Snapshot, nil
}
func (r *relay) list(owner int64) []Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expire(r.now())
	result := make([]Snapshot, 0, len(r.owners[owner]))
	for _, value := range r.owners[owner] {
		result = append(result, value.Snapshot)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SessionID < result[j].SessionID })
	return result
}
func (r *relay) append(owner int64, id, sender string, encrypted Ciphertext) (Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.expire(now)
	value := r.owners[owner][id]
	if value == nil {
		return Message{}, ErrNotFound
	}
	delta := payloadBytes(encrypted)
	drop := 0
	for len(value.messages)-drop >= MaxMessages || sessionBytes(value)+delta > MaxSessionBytes {
		if drop == len(value.messages) {
			return Message{}, ErrCapacity
		}
		delta -= payloadBytes(value.messages[drop].Ciphertext)
		drop++
	}
	if r.retained+delta > r.maxRetained || r.ownerBytes(owner)+delta > MaxUserBytes {
		return Message{}, ErrCapacity
	}
	// Bound the JSON-safe cursor and never wrap/reuse sequence IDs.
	if value.next > 9007199254740991 {
		return Message{}, ErrCapacity
	}
	message := Message{Ciphertext: encrypted, Sequence: value.next, Sender: sender, CreatedAt: now.UnixMilli()}
	value.next++
	if drop > 0 {
		copy(value.messages, value.messages[drop:])
		clear(value.messages[len(value.messages)-drop:]) // Release evicted ciphertext strings.
		value.messages = value.messages[:len(value.messages)-drop]
	}
	value.messages = append(value.messages, message)
	r.retained += delta
	value.notify()
	// Message traffic cannot extend the plugin heartbeat TTL.
	return message, nil
}
func (r *relay) read(owner int64, id string, after int64) ([]Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expire(r.now())
	value := r.owners[owner][id]
	if value == nil {
		return nil, ErrNotFound
	}
	result := make([]Message, 0)
	for _, message := range value.messages {
		if message.Sequence > after {
			result = append(result, message)
		}
	}
	return result, nil
}
func (r *relay) remove(owner int64, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expire(r.now())
	if value := r.owners[owner][id]; value != nil {
		r.retained -= sessionBytes(value)
		close(value.changed)
		delete(r.owners[owner], id)
	}
	if len(r.owners[owner]) == 0 {
		delete(r.owners, owner)
	}
}

// notify broadcasts under the relay mutex; there is no worker per message/session.
func (s *session) notify() { close(s.changed); s.changed = make(chan struct{}) }

// Capture the immutable message copy AND its wake channel under the same lock.
// A rolled-history gap returns all retained messages newer than the cursor; a
// different generation (or impossible future cursor) starts from retained zero.
func (r *relay) eventsSnapshot(owner int64, id string, after int64, generation string) (eventBatch, <-chan struct{}, time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.expire(now)
	value := r.owners[owner][id]
	if value == nil {
		return eventBatch{}, nil, 0, ErrNotFound
	}
	if generation != value.Generation || after >= value.next {
		after = 0
	}
	result := eventBatch{Generation: value.Generation, Messages: make([]Message, 0)}
	for _, message := range value.messages {
		if message.Sequence > after {
			result.Messages = append(result.Messages, message)
		}
	}
	return result, value.changed, value.expires.Sub(now), nil
}
