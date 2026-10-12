package toolmarket

import (
	"context"
)

func (m *Module) execute(ctx context.Context, p Principal, credential string, in ExecuteInput) (any, error) {
	if !validID.MatchString(in.IdempotencyKey) || len(in.Handle) != 64 || len(in.Quote) != 64 {
		return nil, ErrInvalid
	}
	if e := m.reauthorize(ctx, p, credential, "toolmarket.execute"); e != nil {
		return nil, e
	}
	var canonical any
	if e := decodeStrict(in.Arguments, &canonical); e != nil {
		return nil, e
	}
	id := digest([]string{"toolmarket/v1", p.key(), in.IdempotencyKey})
	requestHash := digest([]any{in.Installation, in.Name, in.Handle, in.Quote, in.Grant, canonical})
	// A saved operation owns its original quote and result. Expired prices,
	// revoked handles and upgrades cannot cause repricing or a second dispatch.
	var prior Execution
	found := false
	if e := m.store.view(func(d database) error {
		v, ok := d.Executions[id]
		if !ok || v.RequestHash == "" {
			return nil
		} // Older receipts use the legacy fingerprint check below.
		if v.Owner != p || v.RequestHash != requestHash {
			return ErrConflict
		}
		prior, found = v, true
		return nil
	}); e != nil {
		return nil, e
	}
	if found {
		return m.replay(ctx, p, credential, id, prior)
	}

	i, r, s, e := m.snapshot(p, in.Installation)
	if e != nil {
		return nil, e
	}
	if i.Release == "" {
		return nil, ErrConflict
	}
	t, e := findTool(r, in.Name)
	if e != nil {
		return nil, e
	}
	if i.Loaded[t.Name] != in.Handle {
		return nil, ErrDenied
	}
	if e = validateArgs(t.InputSchema, in.Arguments); e != nil {
		return nil, e
	}
	q := quoteFor(r, t, m.now())
	if !q.Available {
		return nil, ErrPrice
	}
	if q.ID != in.Quote {
		return nil, ErrConflict
	}
	if q.Amount > 0 && m.funds == nil {
		return nil, ErrFunds
	}
	if r.Server.Auth != "none" && (s.Access == "" || s.Expires > 0 && s.Expires <= m.now().Unix()) {
		return nil, ErrAuth
	}
	fingerprint := digest([]any{i.ID, r.ID, t.Name, canonical, q.ID})
	charge := Charge{ID: id, Installation: i.ID, Tool: t.Name, Quote: q.ID, Currency: q.Currency, Amount: q.Amount}
	created := false
	e = m.store.update(func(d *database) error {
		current, _, _, e := selected(*d, p, i.ID)
		if e != nil {
			return e
		}
		if current.Generation != i.Generation {
			return ErrConflict
		}
		if v, ok := d.Executions[id]; ok {
			if v.Owner != p || v.Fingerprint != fingerprint || v.RequestHash != "" && v.RequestHash != requestHash {
				return ErrConflict
			}
			prior = v
			return nil
		}
		if len(d.Executions) >= 50000 {
			return ErrLimit
		}
		if e := m.consumeGrant(d, p, in, charge, digest(canonical)); e != nil {
			return e
		}
		d.Executions[id] = Execution{Owner: p, Fingerprint: fingerprint, RequestHash: requestHash, Grant: in.Grant, State: "started", Charge: charge}
		d.audit(p, "execute", id, "started")
		created = true
		return nil
	})
	if e != nil {
		return nil, e
	}
	if !created {
		return m.replay(ctx, p, credential, id, prior)
	}
	// The same durable operation consumes consent and reserves Core funds.
	// Uncertainty retains both; no retry gets a fresh budget or idempotency key.
	if q.Amount > 0 {
		if e = m.funds.Reserve(ctx, credential, p, charge); e != nil {
			return nil, ErrPending
		}
	}
	if e = m.reauthorize(ctx, p, credential, "toolmarket.execute"); e != nil {
		return nil, ErrPending
	}
	current, _, currentSecret, e := m.snapshot(p, i.ID)
	if e != nil || current.Generation != i.Generation {
		return nil, ErrPending
	}
	s = currentSecret
	c, e := m.connect(ctx, r.Server, s)
	if e != nil {
		return nil, ErrPending
	}
	defer func() {
		if !c.preserve {
			c.close(ctx)
		}
	}()
	// Network initialization may be slow. Recheck live Core authority and local
	// consent after it, then persist the dispatch boundary before tools/call.
	if e = m.reauthorize(ctx, p, credential, "toolmarket.execute"); e != nil {
		return nil, ErrPending
	}
	e = m.store.update(func(d *database) error {
		g, ok := d.Grants[in.Grant]
		if !ok {
			return ErrConsent
		}
		if e := m.validateGrant(*d, p, g, true); e != nil {
			return e
		}
		v, ok := d.Executions[id]
		if !ok || v.Owner != p || v.RequestHash != requestHash || v.State != "started" {
			return ErrConflict
		}
		v.State = "dispatched"
		d.Executions[id] = v
		d.audit(p, "execute", id, "dispatched")
		return nil
	})
	if e != nil {
		return nil, ErrPending
	}
	expected := Execution{Owner: p, Fingerprint: fingerprint, RequestHash: requestHash, Grant: in.Grant, State: "dispatched", Charge: charge}
	m.bindExecutionStream(ctx, p, credential, id, expected, i, r, c)
	result, e := c.rpc(ctx, "tools/call", map[string]any{"name": t.Name, "arguments": canonical})
	if e != nil {
		return nil, ErrPending
	}
	// A valid isError result is still an accepted fixed-price invocation.
	record, e := m.saveExecutionResult(p, id, expected, result, credential, s.Access, s.Refresh)
	if e != nil {
		return nil, ErrPending
	}
	c.preserve = false
	if record.State == "settlement_pending" {
		return m.settle(ctx, p, credential, id, record)
	}
	return record.Result, nil
}

func (m *Module) replay(ctx context.Context, p Principal, credential, id string, record Execution) (any, error) {
	switch record.State {
	case "done":
		return m.executionResult(p, credential, record)
	case "settlement_pending":
		return m.settle(ctx, p, credential, id, record)
	case "dispatched":
		return m.resumeExecution(ctx, p, credential, id, record)
	default:
		return nil, ErrPending
	}
}

func (m *Module) settle(ctx context.Context, p Principal, credential, id string, record Execution) (any, error) {
	if m.funds == nil {
		return nil, ErrFunds
	}
	result, err := m.executionResult(p, credential, record)
	if err != nil {
		return nil, err
	}
	if e := m.funds.Commit(ctx, credential, p, record.Charge); e != nil {
		return nil, ErrPending
	}
	e := m.store.update(func(d *database) error {
		v, ok := d.Executions[id]
		if !ok || v.Owner != p || v.Fingerprint != record.Fingerprint || v.RequestHash != record.RequestHash {
			return ErrConflict
		}
		if v.State == "done" {
			return nil
		}
		if v.State != "settlement_pending" {
			return ErrConflict
		}
		v.State = "done"
		v.Result = result
		d.Executions[id] = v
		d.audit(p, "settle", id, "done")
		return nil
	})
	if e != nil {
		return nil, ErrPending
	}
	return result, nil
}
