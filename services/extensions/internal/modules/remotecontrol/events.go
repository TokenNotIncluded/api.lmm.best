// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package remotecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
)

const (
	MaxStreamsPerUser   = 2
	MaxStreams          = 4 // Further reduced for small hosts to retain non-stream capacity.
	StreamLease         = 60 * time.Second
	StreamReauthorize   = 15 * time.Second
	StreamRPCDeadline   = 3 * time.Second
	StreamWriteDeadline = 5 * time.Second
)

// Internal test timings may shorten, never weaken, the production bounds.
type streamTiming struct{ lease, reauthorize, rpc, write time.Duration }

func boundedDuration(value, limit time.Duration) time.Duration {
	if value > 0 && value < limit {
		return value
	}
	return limit
}

// Reserve RPC and slow-write time inside the 15-second idle recheck window.
func (m *Module) streamTimings() streamTiming {
	return streamTiming{boundedDuration(m.timing.lease, StreamLease), boundedDuration(m.timing.reauthorize, StreamReauthorize-StreamRPCDeadline-StreamWriteDeadline), boundedDuration(m.timing.rpc, StreamRPCDeadline), boundedDuration(m.timing.write, StreamWriteDeadline)}
}

// @mark remote-control.events role=admission
func (m *Module) reserveStream(owner int64, capacity int) bool {
	m.streamsMu.Lock()
	defer m.streamsMu.Unlock()
	// At least one slot remains available to non-stream requests and workers.
	// A one-slot host can still serve commands but cannot retain an SSE stream.
	if m.streamCount >= min(MaxStreams, capacity-1) || m.streams[owner] >= MaxStreamsPerUser {
		return false
	}
	m.streamCount++
	m.streams[owner]++
	return true
}
func (m *Module) releaseStream(owner int64) {
	m.streamsMu.Lock()
	defer m.streamsMu.Unlock()
	m.streamCount--
	m.streams[owner]--
	if m.streams[owner] == 0 {
		delete(m.streams, owner)
	}
}

type eventBatch struct {
	Generation string    `json:"generation"`
	Messages   []Message `json:"messages"`
}

func eventQuery(raw string) (int64, string, bool) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return 0, "", false
	}
	var after int64
	var generation string
	for key, values := range query {
		if len(values) != 1 {
			return 0, "", false
		}
		switch key {
		case "after":
			if values[0] == "" {
				return 0, "", false
			}
			for _, c := range values[0] {
				if c < '0' || c > '9' {
					return 0, "", false
				}
			}
			after, err = strconv.ParseInt(values[0], 10, 64)
			if err != nil || after > 9007199254740991 {
				return 0, "", false
			}
		case "generation":
			generation = values[0]
			if !identifier.MatchString(generation) {
				return 0, "", false
			}
		default:
			return 0, "", false
		}
	}
	return after, generation, true
}

// @mark remote-control.events role=handler
func (m *Module) events(w http.ResponseWriter, r *http.Request) {
	timing := m.streamTimings()
	ctx, cancel := context.WithTimeout(r.Context(), timing.lease)
	defer cancel()
	stopDrain := context.AfterFunc(m.drain, cancel)
	defer stopDrain()
	authorize := func() (*coreclient.RemoteAuthority, *authFailure) {
		rpc, stop := context.WithTimeout(ctx, timing.rpc)
		defer stop()
		actor, denied := m.authorizePrincipal(rpc, r)
		if denied != nil {
			return nil, denied
		}
		return actor, nil
	}
	// Authenticate before inspecting existence, query or capacity.
	authority, denied := authorize()
	if denied != nil {
		failure(w, denied.status, denied.code)
		return
	}
	owner := authority.OwnerAccountID
	// A stream never outlives its original credential, even while idle.
	deadline := time.Unix(authority.ExpiresAt, 0)
	if lease, ok := ctx.Deadline(); ok && lease.Before(deadline) {
		deadline = lease
	}
	credentialCtx, stopCredential := context.WithDeadline(ctx, deadline)
	defer stopCredential()
	ctx = credentialCtx
	after, generation, valid := eventQuery(r.URL.RawQuery)
	if !valid {
		failure(w, 400, "invalid_cursor")
		return
	}
	id := r.PathValue("id")
	if !identifier.MatchString(id) {
		failure(w, 400, "invalid_request")
		return
	}
	batch, changed, remaining, err := m.relay.eventsSnapshot(owner, id, after, generation)
	if !relayError(w, err) {
		return
	}
	if !m.reserveStream(authority.UserID, modules.RequestCapacity(r.Context())) {
		relayError(w, ErrCapacity)
		return
	}
	defer m.releaseStream(authority.UserID)
	controller := http.NewResponseController(w)
	// Unsupported writers fail before SSE headers. No unbounded writes allowed.
	if err := controller.SetWriteDeadline(time.Now().Add(timing.write)); err != nil {
		failure(w, 503, "relay_unavailable")
		return
	}
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	write := func(event string, value any) bool {
		if ctx.Err() != nil {
			return false
		}
		deadline := time.Now().Add(timing.write)
		if lease, ok := ctx.Deadline(); ok && lease.Before(deadline) {
			deadline = lease
		}
		if controller.SetWriteDeadline(deadline) != nil {
			return false
		}
		var data []byte
		if value != nil {
			data, err = json.Marshal(value)
			if err != nil {
				return false
			}
		}
		if event == "" {
			_, err = fmt.Fprint(w, ": keepalive\n\n")
		} else {
			_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		}
		return err == nil && controller.Flush() == nil
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	opened := false
	keepalive := time.NewTimer(timing.reauthorize)
	defer keepalive.Stop()
	expiry := time.NewTimer(remaining)
	defer expiry.Stop()
	resetTimer := func(timer *time.Timer, delay time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
	}
	for {
		ready := !opened || batch.Generation != generation
		send := ready || len(batch.Messages) > 0
		comment := false
		if !send {
			select {
			case <-ctx.Done():
				return
			case <-changed:
			case <-expiry.C:
			case <-keepalive.C:
				comment = true
			}
		}
		if ctx.Err() != nil {
			return
		}
		// Every outgoing batch/comment rechecks live authority, outside the relay lock.
		// Wake-only metadata changes need no send or RPC until the next keepalive.
		if send || comment {
			fresh, freshDenied := authorize()
			if ctx.Err() != nil {
				return
			}
			if freshDenied != nil || fresh.UserID != authority.UserID || fresh.OwnerAccountID != owner || fresh.Source != authority.Source || fresh.AuthVersion != authority.AuthVersion {
				if !opened {
					if freshDenied != nil {
						denied = freshDenied
					} else {
						denied = &authFailure{401, "unauthorized"}
					}
					// Headers are not committed until the first write.
					w.Header().Set("Content-Type", "application/json")
					w.Header().Del("X-Accel-Buffering")
					failure(w, denied.status, denied.code)
					return
				}
				code := "unauthorized"
				if freshDenied != nil && freshDenied.status != 401 && freshDenied.status != 403 {
					code = "remote_unavailable"
				}
				write("error", map[string]string{"code": code})
				return
			}
		}
		// Recapture after RPC: do not send a pre-RPC snapshot of a now-deleted session.
		batch, changed, remaining, err = m.relay.eventsSnapshot(owner, id, after, generation)
		if err != nil {
			if opened {
				write("error", map[string]string{"code": "remote_unavailable"})
			} else {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Del("X-Accel-Buffering")
				relayError(w, err)
			}
			return
		}
		resetTimer(expiry, remaining)
		// A wake can expose new messages. Authenticate on the next iteration before sending.
		if !send && !comment {
			continue
		}
		ready = !opened || batch.Generation != generation
		if ready || len(batch.Messages) > 0 {
			event := "messages"
			if ready {
				event = "ready"
			}
			if !write(event, batch) {
				return
			}
			opened = true
			generation = batch.Generation
			if len(batch.Messages) > 0 {
				after = batch.Messages[len(batch.Messages)-1].Sequence
			} else if ready {
				after = 0
			}
		} else if comment {
			if !write("", nil) {
				return
			}
		}
		resetTimer(keepalive, timing.reauthorize)
		batch.Messages = nil
	}
}
