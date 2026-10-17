// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package toolmarket

import (
	"context"
	"errors"
	"time"
)

const ConfirmationAudience = "lmm-toolmarket/grant/v1"

// ConfirmationIntent is server-built from the stored preview, never from an
// agent's assertion about what a person approved. Monetary units are the native
// quote's USD millionths; these are not Core ledger credits.
type ConfirmationIntent struct {
	Audience      string    `json:"audience"`
	Grant         string    `json:"grant"`
	Owner         Principal `json:"owner"`
	Installation  string    `json:"installation"`
	Release       string    `json:"release"`
	Tool          string    `json:"tool"`
	Handle        string    `json:"handle"`
	Quote         string    `json:"quote"`
	ArgumentsHash string    `json:"arguments_hash"`
	Currency      string    `json:"currency"`
	Generation    int64     `json:"generation"`
	Amount        int64     `json:"amount"`
	MaxCalls      int64     `json:"max_calls"`
	MaxAmount     int64     `json:"max_amount"`
	Expires       int64     `json:"expires"`
}

// ConfirmationEvidence is a trusted adapter result, not a browser request DTO
// or a bearer credential. ID is an audit reference only. The adapter must
// independently verify the current direct-human session and exact intent.
type ConfirmationEvidence struct {
	ID         string    `json:"id"`
	Audience   string    `json:"audience"`
	IntentHash string    `json:"intent_hash"`
	Owner      Principal `json:"owner"`
	ActorKind  string    `json:"actor_kind"`
	ValidUntil int64     `json:"valid_until"`
}

// ConfirmationAuthority is a local integration port for A01/A02, NOT an
// implemented Core RPC. A plain Authority cannot confirm execution grants.
// Implementations must verify human-session kind (not API Key, service or
// delegated agent), live action permission, account and intent in Core. Never
// implement this with a role-name check, client flag, echo or unconditional
// approval. Retries for the same intent must refer to the same approval.
type ConfirmationAuthority interface {
	VerifyToolConfirmation(context.Context, string, ConfirmationIntent) (ConfirmationEvidence, error)
}

func confirmationIntent(g ExecutionGrant) ConfirmationIntent {
	return ConfirmationIntent{
		Audience: ConfirmationAudience, Grant: g.ID, Owner: g.Owner,
		Installation: g.Installation, Release: g.Release, Tool: g.Tool,
		Handle: g.Handle, Quote: g.Quote, ArgumentsHash: g.ArgumentsHash,
		Currency: g.Currency, Generation: g.Generation, Amount: g.Amount,
		MaxCalls: g.MaxCalls, MaxAmount: g.MaxAmount, Expires: g.Expires,
	}
}

func (m *Module) verifyConfirmation(ctx context.Context, credential string, g ExecutionGrant) (evidence ConfirmationEvidence, err error) {
	verifier, ok := m.authority.(ConfirmationAuthority)
	if !ok {
		return ConfirmationEvidence{}, ErrUnavailable
	}
	// A faulty integration adapter cannot turn a panic into approval or expose
	// credential-bearing error text. Do not detach an unbounded goroutine.
	defer func() {
		if recover() != nil {
			evidence, err = ConfirmationEvidence{}, ErrUnavailable
		}
	}()
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	intent := confirmationIntent(g)
	evidence, err = verifier.VerifyToolConfirmation(bounded, credential, intent)
	if err != nil {
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrAuth) {
			return ConfirmationEvidence{}, ErrDenied
		}
		return ConfirmationEvidence{}, ErrUnavailable
	}
	if bounded.Err() != nil {
		return ConfirmationEvidence{}, ErrUnavailable
	}
	now := m.now().Unix()
	if !validID.MatchString(evidence.ID) || evidence.Audience != ConfirmationAudience ||
		evidence.IntentHash != digest(intent) || evidence.Owner != g.Owner || evidence.ActorKind != "human_session" ||
		evidence.ValidUntil <= now || evidence.ValidUntil > now+300 || evidence.ValidUntil > g.Expires {
		return ConfirmationEvidence{}, ErrDenied
	}
	return evidence, nil
}
