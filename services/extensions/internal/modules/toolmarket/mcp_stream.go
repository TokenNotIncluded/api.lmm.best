// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package toolmarket

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var errStreamEnded = errors.New("mcp_stream_ended")

const maxStreamResumes = 2

// StreamResume contains only server-issued stream coordinates, not credentials.
// It is private encrypted operation state, never part of a public DTO.
type StreamResume struct {
	Session    string `json:"session"`
	Cursor     string `json:"cursor"`
	RequestID  string `json:"request_id"`
	Release    string `json:"release"`
	Generation int64  `json:"generation"`
	RetryMS    int64  `json:"retry_ms"`
	Expires    int64  `json:"expires"`
}

type mcpStream struct {
	id         json.RawMessage
	cursor     string
	retry      time.Duration
	remaining  int64
	checkpoint func(string, time.Duration) error
}

func visibleStreamID(s string) bool {
	if len(s) == 0 || len(s) > 512 {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func newMCPStream(id json.RawMessage, checkpoint func(string, time.Duration) error) *mcpStream {
	return &mcpStream{id: id, retry: time.Second, remaining: maxBody, checkpoint: checkpoint}
}

func (s *mcpStream) saveCursor(cursor string) error {
	if s.checkpoint != nil {
		if err := s.checkpoint(cursor, s.retry); err != nil {
			return err
		}
	}
	s.cursor = cursor
	return nil
}

// Only primers and notifications advance a durable cursor. The final response
// event is not acknowledged until its result has been saved by the caller.
// Thus a crash between parsing and saving a response cannot skip that response.
func (s *mcpStream) read(r *http.Response) (json.RawMessage, error) {
	reader := &io.LimitedReader{R: r.Body, N: s.remaining + 1}
	start := reader.N
	defer func() { s.remaining -= start - reader.N }()
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 4096), maxBody)
	var data []string
	cursor := s.cursor
	for scan.Scan() {
		if reader.N <= 0 {
			return nil, ErrLimit
		}
		line := scan.Text()
		if line == "" {
			payload := []byte(strings.Join(data, "\n"))
			data = nil
			if len(payload) > 0 {
				if uniqueJSON(payload) != nil {
					return nil, ErrUpstream
				}
				var env rpcEnvelope
				if json.Unmarshal(payload, &env) != nil {
					return nil, ErrUpstream
				}
				if len(env.ID) > 0 {
					return checkedRPCResult(env, s.id)
				}
				if env.JSONRPC != "2.0" || !strings.HasPrefix(env.Method, "notifications/") || len(env.Result) > 0 || len(env.Error) > 0 {
					return nil, ErrUpstream
				}
			}
			if cursor != s.cursor {
				if err := s.saveCursor(cursor); err != nil {
					return nil, err
				}
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		name, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch name {
		case "data":
			data = append(data, value)
		case "id":
			if value != "" && !visibleStreamID(value) {
				return nil, ErrUpstream
			}
			cursor = value
		case "retry":
			// SSE ignores non-decimal retry values, but an overflowing decimal
			// still asks for a long delay. Do not turn it into a shorter retry.
			if value == "" || strings.Trim(value, "0123456789") != "" {
				continue
			}
			ms, err := strconv.ParseUint(value, 10, 64)
			// Refuse a longer retry rather than polling earlier than requested.
			if err != nil || ms > 5000 {
				// Remove any older checkpoint as well. A new execute request
				// must not bypass this server delay with the previous cursor.
				if err := s.saveCursor(""); err != nil {
					return nil, err
				}
				return nil, ErrUpstream
			}
			s.retry = time.Duration(ms) * time.Millisecond
			if s.cursor != "" {
				if err := s.saveCursor(s.cursor); err != nil {
					return nil, err
				}
			}
		}
	}
	if reader.N <= 0 || s.remaining <= 0 {
		return nil, ErrLimit
	}
	if errors.Is(scan.Err(), bufio.ErrTooLong) {
		return nil, ErrLimit
	}
	// Truncation is resumable only from a previously stored cursor. Malformed
	// JSON/envelopes and oversized tokens returned above are not EOF.
	return nil, errStreamEnded
}

// Resumption is GET-only to the original endpoint, with the same session and
// request ID. 404, auth failure, malformed frames and missing cursors do not
// start a fresh session and never repeat a tools/call POST.
func (c *mcpSession) resumeStream(ctx context.Context, s *mcpStream) (json.RawMessage, error) {
	if !visibleStreamID(s.cursor) || !visibleStreamID(c.headers.Get("Mcp-Session-Id")) {
		return nil, ErrUpstream
	}
	for attempt := 0; attempt < maxStreamResumes; attempt++ {
		if s.remaining <= 0 {
			return nil, ErrLimit
		}
		timer := time.NewTimer(s.retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ErrPending
		case <-timer.C:
		}
		if c.beforeResume != nil {
			if err := c.beforeResume(); err != nil {
				return nil, err
			}
		}
		h := c.headers.Clone()
		h.Set("Accept", "text/event-stream")
		h.Set("Last-Event-ID", s.cursor)
		r, err := c.net.request(ctx, "GET", c.endpoint, nil, h)
		if err != nil {
			return nil, err
		}
		kind, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if r.StatusCode != http.StatusOK || mediaErr != nil || kind != "text/event-stream" {
			r.Body.Close()
			return nil, ErrUpstream
		}
		result, err := s.read(r)
		r.Body.Close()
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, errStreamEnded) || !visibleStreamID(s.cursor) {
			return nil, err
		}
	}
	return nil, ErrPending
}

func validResume(p *StreamResume, now int64) bool {
	if p == nil || !visibleStreamID(p.Session) || !visibleStreamID(p.Cursor) || p.Expires <= now ||
		!validID.MatchString(p.Release) || p.Generation <= 0 || p.RetryMS < 0 || p.RetryMS > 5000 {
		return false
	}
	id, err := strconv.ParseUint(p.RequestID, 10, 32)
	return err == nil && id > 0 && strconv.FormatUint(id, 10) == p.RequestID
}
