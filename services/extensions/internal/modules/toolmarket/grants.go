// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package toolmarket

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"time"
)

// ExecutionGrant records user consent, not wallet or platform spending authority.
// Core must still authorize and reserve every paid operation independently.
// Used counters include uncertain calls; they are never released by a timeout.
type ExecutionGrant struct {
	ID               string    `json:"id"`
	Owner            Principal `json:"owner"`
	Installation     string    `json:"installation"`
	Release          string    `json:"release"`
	Tool             string    `json:"tool"`
	Handle           string    `json:"handle"`
	Quote            string    `json:"quote"`
	Generation       int64     `json:"generation"`
	Amount           int64     `json:"amount"`
	Currency         string    `json:"currency"`
	MaxCalls         int64     `json:"max_calls"`
	MaxAmount        int64     `json:"max_amount"`
	UsedCalls        int64     `json:"used_calls"`
	UsedAmount       int64     `json:"used_amount"`
	Expires          int64     `json:"expires"`
	State            string    `json:"state"`
	ConfirmBefore    int64     `json:"confirm_before,omitempty"`
	ConfirmationHash string    `json:"confirmation_hash,omitempty"`
	ArgumentsHash    string    `json:"arguments_hash"`
	ApprovalID       string    `json:"approval_id,omitempty"`
	ApprovalHash     string    `json:"approval_hash,omitempty"`
}

type GrantPreviewInput struct {
	Installation string          `json:"installation"`
	Name         string          `json:"name"`
	Arguments    json.RawMessage `json:"arguments"`
	Handle       string          `json:"handle"`
	Quote        string          `json:"quote"`
	MaxCalls     int64           `json:"max_calls"`
	MaxAmount    int64           `json:"max_amount"` // USD millionths, same unit as the pinned native quote.
	Expires      int64           `json:"expires"`    // Unix seconds, at most one day and never past the quote.
}
type GrantConfirmInput struct {
	Grant        string `json:"grant"`
	Confirmation string `json:"confirmation"`
}
type GrantInput struct {
	Grant string `json:"grant"`
}
type GrantPreview struct {
	Grant        ExecutionGrant  `json:"grant"`
	Confirmation string          `json:"confirmation"`
	Arguments    json.RawMessage `json:"arguments"`
}

func publicGrant(g ExecutionGrant) ExecutionGrant {
	g.ConfirmationHash = ""
	return g
}

func (m *Module) reauthorize(ctx context.Context, p Principal, credential, action string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	actual, err := m.authority.Authorize(ctx, credential, p.Account, action)
	if err != nil {
		return err
	}
	if !p.valid() || actual != p {
		return ErrDenied
	}
	return nil
}

// Previews and confirmation are separate user endpoints, never MCP metatools.
// The confirmation contains no editable price, owner or budget fields.
func (m *Module) previewGrant(ctx context.Context, p Principal, credential string, in GrantPreviewInput) (GrantPreview, error) {
	if err := m.reauthorize(ctx, p, credential, "toolmarket.authorize"); err != nil {
		return GrantPreview{}, err
	}
	now := m.now().Unix()
	if in.MaxCalls < 1 || in.MaxCalls > 1024 || in.MaxAmount < 0 || in.MaxAmount > 9007199254740991 ||
		in.Expires <= now || in.Expires > now+int64((24*time.Hour)/time.Second) {
		return GrantPreview{}, ErrInvalid
	}
	i, r, _, err := m.snapshot(p, in.Installation)
	if err != nil {
		return GrantPreview{}, err
	}
	tool, err := findTool(r, in.Name)
	if err != nil {
		return GrantPreview{}, err
	}
	if err := validateArgs(tool.InputSchema, in.Arguments); err != nil {
		return GrantPreview{}, err
	}
	var arguments any
	if err := decodeStrict(in.Arguments, &arguments); err != nil {
		return GrantPreview{}, err
	}
	previewArguments, err := json.Marshal(arguments)
	if err != nil {
		return GrantPreview{}, ErrInvalid
	}
	q := quoteFor(r, tool, m.now())
	if !q.Available {
		return GrantPreview{}, ErrPrice
	}
	if i.Release == "" || i.Loaded[tool.Name] != in.Handle || len(in.Handle) != 64 || q.ID != in.Quote {
		return GrantPreview{}, ErrDenied
	}
	if in.MaxAmount < q.Amount || in.Expires > q.Expires {
		return GrantPreview{}, ErrInvalid
	}
	id, err := randomID()
	if err != nil {
		return GrantPreview{}, ErrUnavailable
	}
	nonce, err := randomID()
	if err != nil {
		return GrantPreview{}, ErrUnavailable
	}
	g := ExecutionGrant{ID: id, Owner: p, Installation: i.ID, Release: r.ID, Tool: tool.Name,
		Handle: in.Handle, Quote: q.ID, Generation: i.Generation, Amount: q.Amount, Currency: q.Currency,
		MaxCalls: in.MaxCalls, MaxAmount: in.MaxAmount, Expires: in.Expires, State: "preview",
		ConfirmBefore: min(now+300, in.Expires), ConfirmationHash: digest([]string{id, nonce}), ArgumentsHash: digest(arguments)}
	err = m.store.update(func(d *database) error {
		current, _, _, err := selected(*d, p, i.ID)
		if err != nil {
			return err
		}
		if current.Generation != g.Generation || current.Loaded[g.Tool] != g.Handle {
			return ErrConflict
		}
		if len(d.Grants) >= 50000 {
			return ErrLimit
		}
		if d.Grants == nil {
			d.Grants = map[string]ExecutionGrant{}
		}
		d.Grants[id] = g
		d.audit(p, "grant.preview", id, "preview")
		return nil
	})
	if err != nil {
		return GrantPreview{}, err
	}
	return GrantPreview{publicGrant(g), nonce, previewArguments}, nil
}

func (m *Module) confirmGrant(ctx context.Context, p Principal, credential string, in GrantConfirmInput) (ExecutionGrant, error) {
	if err := m.reauthorize(ctx, p, credential, "toolmarket.authorize"); err != nil {
		return ExecutionGrant{}, err
	}
	if !validID.MatchString(in.Grant) || len(in.Confirmation) != 43 {
		return ExecutionGrant{}, ErrInvalid
	}
	// Validate local proof before contacting Core; perform remote verification
	// outside the store lock, then compare the complete immutable intent again.
	var preview ExecutionGrant
	if err := m.store.view(func(d database) error {
		g, err := m.confirmableGrant(d, p, in)
		preview = g
		return err
	}); err != nil {
		return ExecutionGrant{}, err
	}
	evidence, err := m.verifyConfirmation(ctx, credential, preview)
	if err != nil {
		return ExecutionGrant{}, err
	}
	if err := m.reauthorize(ctx, p, credential, "toolmarket.authorize"); err != nil {
		return ExecutionGrant{}, err
	}
	var out ExecutionGrant
	err = m.store.update(func(d *database) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		g, err := m.confirmableGrant(*d, p, in)
		if err != nil {
			return err
		}
		if evidence.ValidUntil <= m.now().Unix() || evidence.IntentHash != digest(confirmationIntent(g)) {
			return ErrConflict
		}
		g.State, g.ConfirmationHash, g.ConfirmBefore = "active", "", 0
		g.ApprovalID, g.ApprovalHash = evidence.ID, evidence.IntentHash
		d.Grants[g.ID] = g
		d.audit(p, "grant.confirm", g.ID, "active")
		out = publicGrant(g)
		return nil
	})
	return out, err
}

func (m *Module) confirmableGrant(d database, p Principal, in GrantConfirmInput) (ExecutionGrant, error) {
	g, ok := d.Grants[in.Grant]
	if !ok || g.Owner != p {
		return ExecutionGrant{}, ErrMissing
	}
	if g.State != "preview" || m.now().Unix() >= g.ConfirmBefore {
		return ExecutionGrant{}, ErrConflict
	}
	if !hmac.Equal([]byte(g.ConfirmationHash), []byte(digest([]string{g.ID, in.Confirmation}))) {
		return ExecutionGrant{}, ErrDenied
	}
	if err := m.validateGrant(d, p, g, false); err != nil {
		return ExecutionGrant{}, err
	}
	return g, nil
}

func (m *Module) grantStatus(p Principal, id string) (ExecutionGrant, error) {
	if !validID.MatchString(id) {
		return ExecutionGrant{}, ErrInvalid
	}
	var out ExecutionGrant
	err := m.store.view(func(d database) error {
		g, ok := d.Grants[id]
		if !ok || g.Owner != p {
			return ErrMissing
		}
		out = publicGrant(g)
		return nil
	})
	return out, err
}

func (m *Module) revokeGrant(ctx context.Context, p Principal, credential, id string) error {
	if err := m.reauthorize(ctx, p, credential, "toolmarket.authorize"); err != nil {
		return err
	}
	if !validID.MatchString(id) {
		return ErrInvalid
	}
	return m.store.update(func(d *database) error {
		g, ok := d.Grants[id]
		if !ok || g.Owner != p {
			return ErrMissing
		}
		if g.State == "revoked" {
			return nil
		}
		g.State, g.ConfirmationHash, g.ConfirmBefore = "revoked", "", 0
		d.Grants[id] = g
		d.audit(p, "grant.revoke", id, "revoked")
		return nil
	})
}

func (m *Module) validateGrant(d database, p Principal, g ExecutionGrant, active bool) error {
	if g.Owner != p || (active && (g.State != "active" || !validID.MatchString(g.ApprovalID) || g.ApprovalHash != digest(confirmationIntent(g)))) || m.now().Unix() >= g.Expires {
		return ErrConsent
	}
	i, r, _, err := selected(d, p, g.Installation)
	if err != nil {
		return err
	}
	if i.Generation != g.Generation || i.Release != g.Release || i.Loaded[g.Tool] != g.Handle {
		return ErrConsent
	}
	t, err := findTool(r, g.Tool)
	if err != nil {
		return err
	}
	q := quoteFor(r, t, m.now())
	if !q.Available || q.ID != g.Quote || q.Amount != g.Amount || q.Currency != g.Currency {
		return ErrPrice
	}
	return nil
}

// Called in the SAME transaction as durable execution intent. Counters cannot
// race and a duplicate operation cannot consume consent a second time.
func (m *Module) consumeGrant(d *database, p Principal, in ExecuteInput, charge Charge, argumentsHash string) error {
	g, ok := d.Grants[in.Grant]
	if !ok || g.Owner != p {
		return ErrConsent
	}
	if err := m.validateGrant(*d, p, g, true); err != nil {
		return err
	}
	if g.Installation != in.Installation || g.Tool != in.Name || g.Handle != in.Handle || g.Quote != in.Quote ||
		g.Amount != charge.Amount || g.Currency != charge.Currency || g.ArgumentsHash != argumentsHash {
		return ErrConsent
	}
	if g.UsedCalls >= g.MaxCalls || g.UsedAmount < 0 || g.UsedAmount > g.MaxAmount || charge.Amount > g.MaxAmount-g.UsedAmount {
		return ErrLimit
	}
	g.UsedCalls++
	g.UsedAmount += charge.Amount
	d.Grants[g.ID] = g
	return nil
}
