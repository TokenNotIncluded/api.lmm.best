// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package toolmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/redaction"
)

func (m *Module) bindExecutionStream(ctx context.Context, p Principal, credential, id string, expected Execution, i Installation, r Release, c *mcpSession) {
	c.beforeResume = func() error {
		if err := m.reauthorize(ctx, p, credential, "toolmarket.execute"); err != nil {
			return err
		}
		current, release, secret, err := m.snapshot(p, i.ID)
		if err != nil || current.Generation != i.Generation || release.ID != r.ID {
			return ErrPending
		}
		if _, err := m.connectionHeaders(release.Server, secret); err != nil {
			return ErrPending
		}
		return nil
	}
	c.checkpoint = func(requestID json.RawMessage, cursor string, retry time.Duration) error {
		// This implementation resumes only an explicit server session. A
		// stateless/unsupported stream remains uncertain, never a fresh POST.
		if !visibleStreamID(c.headers.Get("Mcp-Session-Id")) {
			return nil
		}
		err := m.store.update(func(d *database) error {
			v, ok := d.Executions[id]
			if !ok || v.Owner != p || v.Fingerprint != expected.Fingerprint || v.RequestHash != expected.RequestHash || v.State != "dispatched" {
				return ErrConflict
			}
			if cursor == "" {
				v.Resume = nil
			} else {
				expires := m.now().Add(5 * time.Minute).Unix()
				if v.Resume != nil {
					expires = v.Resume.Expires
				}
				point := &StreamResume{Session: c.headers.Get("Mcp-Session-Id"), Cursor: cursor, RequestID: string(requestID),
					Release: r.ID, Generation: i.Generation, RetryMS: retry.Milliseconds(), Expires: expires}
				if !validResume(point, m.now().Unix()) {
					return ErrPending
				}
				v.Resume = point
			}
			d.Executions[id] = v
			return nil
		})
		if err == nil && cursor == "" {
			c.preserve = false
		}
		return err
	}
}

func (m *Module) resumeExecution(ctx context.Context, p Principal, credential, id string, record Execution) (any, error) {
	point := record.Resume
	if !validResume(point, m.now().Unix()) {
		return nil, ErrPending
	}
	i, r, secret, err := m.snapshot(p, record.Charge.Installation)
	if err != nil || i.Generation != point.Generation || r.ID != point.Release {
		return nil, ErrPending
	}
	h, err := m.connectionHeaders(r.Server, secret)
	if err != nil {
		return nil, ErrPending
	}
	h.Set("Mcp-Session-Id", point.Session)
	c := &mcpSession{net: m.net, endpoint: r.Server.Endpoint, headers: h, preserve: true}
	defer func() {
		if !c.preserve {
			c.close(ctx)
		}
	}()
	m.bindExecutionStream(ctx, p, credential, id, record, i, r, c)
	stream := newMCPStream(json.RawMessage(point.RequestID), func(cursor string, retry time.Duration) error {
		return c.checkpoint(json.RawMessage(point.RequestID), cursor, retry)
	})
	stream.cursor, stream.retry = point.Cursor, time.Duration(point.RetryMS)*time.Millisecond
	result, err := c.resumeStream(ctx, stream)
	if err != nil {
		return nil, ErrPending
	}
	completed, err := m.saveExecutionResult(p, id, record, result, credential, secret.Access, secret.Refresh)
	if err != nil {
		return nil, ErrPending
	}
	c.preserve = false
	if completed.State == "settlement_pending" {
		return m.settle(ctx, p, credential, id, completed)
	}
	return completed.Result, nil
}

// A saved reply and the state transition are atomic. A final SSE event ID is
// deliberately not saved as a read cursor before this transaction succeeds.
func (m *Module) saveExecutionResult(p Principal, id string, expected Execution, raw json.RawMessage, secrets ...string) (Execution, error) {
	var check struct {
		Content []json.RawMessage `json:"content"`
		IsError bool              `json:"isError"`
	}
	if json.Unmarshal(raw, &check) != nil || check.Content == nil || expected.Owner != p || expected.Charge.Amount < 0 || expected.Charge.Currency != "USD" {
		return Execution{}, ErrPending
	}
	result, err := redaction.JSON(raw, secrets...)
	if err != nil {
		return Execution{}, ErrPending
	}
	record := expected
	record.State, record.Result, record.Resume = "done", result, nil
	if record.Charge.Amount > 0 {
		record.State = "settlement_pending"
	}
	err = m.store.update(func(d *database) error {
		v, ok := d.Executions[id]
		if !ok || v.Owner != p || v.Fingerprint != expected.Fingerprint || v.RequestHash != expected.RequestHash || v.Charge != expected.Charge {
			return ErrConflict
		}
		if v.State == "done" || v.State == "settlement_pending" {
			if !bytes.Equal(v.Result, result) {
				return ErrConflict
			}
			record = v
			return nil
		}
		if v.State != "dispatched" {
			return ErrConflict
		}
		d.Executions[id] = record
		d.audit(p, "execute", id, record.State)
		return nil
	})
	return record, err
}
