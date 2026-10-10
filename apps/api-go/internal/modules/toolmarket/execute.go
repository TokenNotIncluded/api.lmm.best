package toolmarket

import (
	"context"
	"encoding/json"
)

func (m *Module) execute(ctx context.Context, p Principal, credential string, in ExecuteInput) (any, error) {
	if !validID.MatchString(in.IdempotencyKey) || len(in.Handle) != 64 || len(in.Quote) != 64 {
		return nil, ErrInvalid
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
	var canonical any
	if e = decodeStrict(in.Arguments, &canonical); e != nil {
		return nil, e
	}
	id := digest([]string{"toolmarket/v1", p.key(), in.IdempotencyKey})
	fingerprint := digest([]any{i.ID, r.ID, t.Name, canonical, q.ID})
	charge := Charge{ID: id, Installation: i.ID, Tool: t.Name, Quote: q.ID, Currency: q.Currency, Amount: q.Amount}
	var prior Execution
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
			if v.Owner != p || v.Fingerprint != fingerprint {
				return ErrConflict
			}
			prior = v
			return nil
		}
		if len(d.Executions) >= 50000 {
			return ErrLimit
		}
		d.Executions[id] = Execution{Owner: p, Fingerprint: fingerprint, State: "started", Charge: charge}
		d.audit(p, "execute", id, "started")
		created = true
		return nil
	})
	if e != nil {
		return nil, e
	}
	if !created {
		if prior.State == "done" {
			return prior.Result, nil
		}
		if prior.State == "settlement_pending" {
			return m.settle(ctx, p, credential, id, prior)
		}
		return nil, ErrPending
	}
	// Persist intent before either reserving funds or contacting the provider.
	// Ambiguous failures keep the intent. Retrying cannot execute/charge twice.
	if q.Amount > 0 {
		if e = m.funds.Reserve(ctx, credential, p, charge); e != nil {
			return nil, ErrPending
		}
	}
	// A revocation/disable that finished during reserve is observed before call.
	current, _, currentSecret, e := m.snapshot(p, i.ID)
	if e != nil || current.Generation != i.Generation {
		return nil, ErrPending
	}
	s = currentSecret
	c, e := m.connect(ctx, r.Server, s)
	if e != nil {
		return nil, ErrPending
	}
	defer c.close(ctx)
	result, e := c.rpc(ctx, "tools/call", map[string]any{"name": t.Name, "arguments": canonical})
	if e != nil {
		return nil, ErrPending
	}
	var check struct {
		Content []json.RawMessage `json:"content"`
		IsError bool              `json:"isError"`
	}
	if json.Unmarshal(result, &check) != nil || check.Content == nil {
		return nil, ErrPending
	}
	// Commit charges for an accepted fixed-price invocation, including a tool's
	// isError result. Transport uncertainty is held for reconciliation, not retried.
	state := "done"
	if q.Amount > 0 {
		state = "settlement_pending"
	}
	record := Execution{Owner: p, Fingerprint: fingerprint, State: state, Charge: charge, Result: result}
	e = m.store.update(func(d *database) error { d.Executions[id] = record; d.audit(p, "execute", id, state); return nil })
	if e != nil {
		return nil, ErrPending
	}
	if state == "settlement_pending" {
		return m.settle(ctx, p, credential, id, record)
	}
	return result, nil
}
func (m *Module) settle(ctx context.Context, p Principal, credential, id string, record Execution) (any, error) {
	if m.funds == nil {
		return nil, ErrFunds
	}
	if e := m.funds.Commit(ctx, credential, p, record.Charge); e != nil {
		return nil, ErrPending
	}
	e := m.store.update(func(d *database) error {
		v, ok := d.Executions[id]
		if !ok || v.Owner != p || v.Fingerprint != record.Fingerprint {
			return ErrConflict
		}
		v.State = "done"
		d.Executions[id] = v
		d.audit(p, "settle", id, "done")
		return nil
	})
	if e != nil {
		return nil, ErrPending
	}
	return record.Result, nil
}
