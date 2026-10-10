package assistant

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/storage"
)

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}
type ModelRequest struct {
	Input   string       `json:"input"`
	Tools   []Definition `json:"tools"` // Only the entry tools and explicitly loaded schemas.
	Results []Result     `json:"results"`
}
type ModelReply struct {
	Text  string     `json:"text"`
	Calls []ToolCall `json:"calls"`
}

// Model is supplied by composition and must use Rust's authenticated model
// request path. This package has no upstream URLs, provider keys or free relay.
// Next must honor cancellation and bound its wire response before decoding.
// No production adapter is installed until the core model path is available.
type Model interface {
	Next(context.Context, string, ModelRequest) (ModelReply, error)
}
type Reply struct {
	Text    string   `json:"text"`
	Reason  string   `json:"reason"`
	Results []Result `json:"results"`
}
type turnKey struct{}

func turnFrom(ctx context.Context) string { v, _ := ctx.Value(turnKey{}).(string); return v }
func (s *Service) Chat(ctx context.Context, token string, account access.Account, sessionID, input string) (Reply, error) {
	if access.Missing(s.model) {
		return Reply{}, access.ErrUnavailable
	}
	if len(input) == 0 || len(input) > 8192 || !access.ID(sessionID) {
		return Reply{}, access.ErrInvalid
	}
	p, e := s.check(ctx, token, account, access.Read)
	if e != nil {
		return Reply{}, e
	}
	turn := freshID()
	if turn == "" {
		return Reply{}, access.ErrUnavailable
	}
	ctx = context.WithValue(ctx, turnKey{}, turn)
	e = s.repo.Within(ctx, scope(p), func(tx storage.Tx) error {
		session, e := sessionFor(tx, p, sessionID)
		if e != nil {
			return e
		}
		if session.Closed || session.ActiveCall != "" || session.ActiveTurn != "" || !s.now().Before(session.ExpiresAt) {
			return access.ErrConflict
		}
		session.ActiveTurn = turn
		return tx.Put("sessions", sessionID, session)
	})
	if e != nil {
		return Reply{}, e
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = s.repo.Within(cleanup, scope(p), func(tx storage.Tx) error {
			session, e := sessionFor(tx, p, sessionID)
			if e != nil {
				return e
			}
			if session.ActiveTurn == turn {
				session.ActiveTurn = ""
				return tx.Put("sessions", sessionID, session)
			}
			return nil
		})
	}()
	out := Reply{Results: []Result{}}
	seen := map[string]bool{}
	loaded := map[string]bool{"tools.search": true, "tools.describe": true, "conversation.end": true}
	stop := func(reason string) (Reply, error) {
		out.Reason = reason
		return out, s.Close(ctx, token, account, sessionID, reason)
	}
	for round := 0; round < s.maxCalls; round++ {
		// No identity or role caching across model steps or tool calls.
		p, e = s.check(ctx, token, account, access.Read)
		if e != nil {
			return out, e
		}
		names := make([]string, 0, len(loaded))
		for name := range loaded {
			names = append(names, name)
		}
		sort.Strings(names)
		catalog := make([]Definition, 0, len(names))
		for _, name := range names {
			if d, err := s.registry.Describe(p, name); err == nil {
				catalog = append(catalog, d)
			}
		}
		request := ModelRequest{Input: input, Tools: catalog, Results: append([]Result(nil), out.Results...)}
		encoded, err := json.Marshal(request)
		if err != nil || len(encoded) > 48<<10 {
			_, _ = stop("turn_limit")
			return out, access.ErrLimit
		}
		bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
		response, e := nextModel(bounded, s.model, token, request)
		cancel()
		if e != nil {
			_, _ = stop("model_error")
			return out, access.ErrUnavailable
		}
		if len(response.Text) > 8192 || len(response.Calls) > 4 {
			_, _ = stop("model_error")
			return out, access.ErrLimit
		}
		out.Text = response.Text
		if len(response.Calls) == 0 {
			if response.Text == "" {
				return stop("no_action")
			}
			return stop("completed")
		}
		for _, call := range response.Calls {
			if !access.ID(call.ID) || len(call.ID) > 64 {
				_, _ = stop("model_error")
				return out, access.ErrInvalid
			}
			var args map[string]any
			if e = access.Decode(call.Arguments, &args); e != nil {
				_, _ = stop("model_error")
				return out, e
			}
			signature := digest([]any{call.Name, args})
			if seen[signature] {
				return stop("loop_detected")
			}
			seen[signature] = true
			result, e := s.Invoke(ctx, token, account, sessionID, turn+":"+call.ID, call.Name, call.Arguments)
			if e != nil {
				_, _ = stop("model_error")
				return out, e
			}
			if call.Name == "tools.describe" {
				if d, ok := result.Value.(Definition); ok {
					if !loaded[d.Name] && len(loaded) >= 11 {
						_, _ = stop("turn_limit")
						return out, access.ErrLimit
					}
					loaded[d.Name] = true
				}
			}
			out.Results = append(out.Results, result)
			b, _ := json.Marshal(out.Results)
			if len(b) > 32<<10 {
				_, _ = stop("turn_limit")
				return out, access.ErrLimit
			}
			// A terminal tool also cancels the remaining calls in this model batch.
			if !result.Continue {
				out.Reason = result.Reason
				return out, nil
			}
		}
	}
	return stop("turn_limit")
}

// A faulty extension model adapter must not expose panic text or reopen a turn.
func nextModel(ctx context.Context, model Model, token string, request ModelRequest) (reply ModelReply, err error) {
	defer func() {
		if recover() != nil {
			reply = ModelReply{}
			err = access.ErrUnavailable
		}
	}()
	return model.Next(ctx, token, request)
}
