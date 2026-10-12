// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/storage"
)

func TestA08DirectToolResultIsFilteredBeforeGoAndHTTPResponses(t *testing.T) {
	tool := Tool{Definition: Definition{Summary: Summary{Name: "fixture.read", Description: "Read a synthetic record.", Permission: access.Read}, Parameters: objectSchema(map[string]Property{})},
		Execute: func(_ context.Context, in Invocation) (any, error) {
			return map[string]any{"password": "synthetic-private", "text": "echo " + in.Credential, "number": json.Number("9007199254740993")}, nil
		}}
	s, _, _ := setup(t, Options{Tools: []Tool{tool}})
	session := start(t, s, user, self)
	result := invoke(t, s, session, user, "direct", "fixture.read", `{}`)
	data, err := json.Marshal(result)
	if err != nil || strings.Contains(string(data), user) || strings.Contains(string(data), "synthetic-private") || !strings.Contains(string(data), "9007199254740993") {
		t.Fatal("direct call exposed a credential or changed a number")
	}
	body, err := json.Marshal(map[string]any{"team_id": 0, "session_id": session.ID, "call_id": "http", "name": "fixture.read", "arguments": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	response := request(s.Handler(), "POST", "/invoke", user, string(body))
	if response.Code != 200 || strings.Contains(response.Body.String(), user) || strings.Contains(response.Body.String(), "synthetic-private") {
		t.Fatal("HTTP invocation exposed a credential")
	}
}

func TestA08CallerCancellationDoesNotLoseReceivedToolOutcome(t *testing.T) {
	callCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	tool := Tool{Definition: Definition{Summary: Summary{Name: "fixture.effect", Description: "Synthetic completed effect.", Permission: access.Read}, Parameters: objectSchema(map[string]Property{})},
		Execute: func(context.Context, Invocation) (any, error) { calls.Add(1); cancel(); return "received result", nil }}
	s, _, _ := setup(t, Options{Tools: []Tool{tool}})
	session := start(t, s, user, self)
	if _, err := s.Invoke(callCtx, user, self, session.ID, "once", "fixture.effect", json.RawMessage(`{}`)); err != nil {
		t.Fatal("caller cancellation prevented the bounded receipt write", err)
	}
	p, err := s.check(ctx, user, self, access.Read)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.repo.Within(ctx, scope(p), func(tx storage.Tx) error {
		var record callRecord
		if err := tx.Get("calls", session.ID+":once", &record); err != nil {
			return err
		}
		current, err := sessionFor(tx, p, session.ID)
		if err != nil {
			return err
		}
		if record.Status != "done" || current.ActiveCall != "" || current.Calls != 1 {
			t.Fatal("received outcome was lost or the call lock remained")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	duplicate := invoke(t, s, session, user, "once", "fixture.effect", `{}`)
	if !duplicate.Duplicate || calls.Load() != 1 {
		t.Fatal("replay repeated the tool effect")
	}
}

func TestA08ToolErrorRetainsUnknownOutcomeWithoutPrivateError(t *testing.T) {
	var calls atomic.Int32
	tool := Tool{Definition: Definition{Summary: Summary{Name: "fixture.unknown", Description: "Synthetic ambiguous effect.", Permission: access.Read}, Parameters: objectSchema(map[string]Property{})},
		Execute: func(context.Context, Invocation) (any, error) {
			calls.Add(1)
			return nil, errors.New("synthetic-private-after-effect")
		}}
	s, _, _ := setup(t, Options{Tools: []Tool{tool}})
	session := start(t, s, user, self)
	_, err := s.Invoke(ctx, user, self, session.ID, "once", "fixture.unknown", json.RawMessage(`{}`))
	if !errors.Is(err, access.ErrUnavailable) || strings.Contains(err.Error(), "private") {
		t.Fatal("handler error text escaped the call boundary")
	}
	duplicate := invoke(t, s, session, user, "once", "fixture.unknown", `{}`)
	data, err := json.Marshal(duplicate)
	if err != nil || !strings.Contains(string(data), `"status":"unknown"`) || !duplicate.Duplicate || calls.Load() != 1 {
		t.Fatal("uncertain call was replayed or labeled a definite failure")
	}
	if _, err := s.Invoke(ctx, user, self, session.ID, "new-id", "fixture.unknown", json.RawMessage(`{}`)); !errors.Is(err, access.ErrConflict) {
		t.Fatal("new call ID reopened an uncertain session")
	}
}

type a08AuthorityFunc func(context.Context, string, access.Account, access.Permission) (access.Principal, error)

func (f a08AuthorityFunc) Check(ctx context.Context, token string, account access.Account, permission access.Permission) (access.Principal, error) {
	return f(ctx, token, account, permission)
}

func TestA08ServiceRejectsMismatchedAccountFromAuthority(t *testing.T) {
	s, _, _ := setup(t, Options{})
	for _, account := range []access.Account{{}, {Kind: "team", ID: 7}} {
		s.auth = a08AuthorityFunc(func(context.Context, string, access.Account, access.Permission) (access.Principal, error) {
			return access.Principal{UserID: 1, Level: 1, Account: access.Account{Kind: "team", ID: 8}}, nil
		})
		if _, err := s.Start(ctx, user, account); !errors.Is(err, access.ErrForbidden) {
			t.Fatal("authority changed the requested account")
		}
	}
}

func TestA08ChatDoesNotReleaseReplyAfterRevocationOrActorChange(t *testing.T) {
	for _, changeOwner := range []bool{false, true} {
		s, _, identity := setup(t, Options{})
		s.model = modelFunc(func(context.Context, string, ModelRequest) (ModelReply, error) {
			if changeOwner {
				identity.Set(user, 2, 1)
			} else {
				identity.Revoke(user)
			}
			return ModelReply{Text: "private reply"}, nil
		})
		session := start(t, s, user, self)
		owner, err := s.check(ctx, user, self, access.Read)
		if err != nil {
			t.Fatal(err)
		}
		reply, err := s.Chat(ctx, user, self, session.ID, "read my data")
		if err == nil || reply.Text != "" || len(reply.Results) != 0 {
			t.Fatal("revoked or changed principal received a model reply")
		}
		if err := s.repo.Within(ctx, scope(owner), func(tx storage.Tx) error {
			current, err := sessionFor(tx, owner, session.ID)
			if err == nil && current.ActiveTurn != "" {
				t.Fatal("revocation leaked the original owner turn lock")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestA08ChatMasksCredentialInModelText(t *testing.T) {
	model := modelFunc(func(context.Context, string, ModelRequest) (ModelReply, error) {
		return ModelReply{Text: "echo " + user + " password=synthetic-private"}, nil
	})
	s, _, _ := setup(t, Options{Model: model})
	session := start(t, s, user, self)
	reply, err := s.Chat(ctx, user, self, session.ID, "read data")
	if err != nil || strings.Contains(reply.Text, user) || strings.Contains(reply.Text, "synthetic-private") {
		t.Fatal("model text exposed a credential")
	}
}
