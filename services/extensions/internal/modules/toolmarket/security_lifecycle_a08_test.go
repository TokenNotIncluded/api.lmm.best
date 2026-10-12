// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package toolmarket

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The normal action authority alone says nothing about direct human approval.
type actionOnlyAuthority struct{ Authority }
type humanCheckAuthority struct {
	Authority
	check func(context.Context, string, ConfirmationIntent) (ConfirmationEvidence, error)
}

func (a humanCheckAuthority) VerifyToolConfirmation(ctx context.Context, token string, in ConfirmationIntent) (ConfirmationEvidence, error) {
	return a.check(ctx, token, in)
}

func TestA08ConfirmationRequiresDirectHumanCheck(t *testing.T) {
	for _, mode := range []string{"missing", "agent", "revoked", "cancelled", "panic"} {
		t.Run(mode, func(t *testing.T) {
			mock := newMock(t)
			m := testModule(t, mock)
			_, i, _ := prepared(t, m, mock, "none", "free")
			in := invocation(t, m, alice, i.ID, "human-check")
			preview := grantPreviewFor(t, m, in, 1, 0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "missing" {
				m.authority = actionOnlyAuthority{m.authority}
			} else {
				m.authority = humanCheckAuthority{m.authority, func(_ context.Context, _ string, _ ConfirmationIntent) (ConfirmationEvidence, error) {
					switch mode {
					case "agent":
						return ConfirmationEvidence{}, ErrDenied
					case "revoked":
						return ConfirmationEvidence{}, ErrAuth
					case "cancelled":
						cancel()
						return ConfirmationEvidence{}, nil
					default:
						panic("synthetic-private-error")
					}
				}}
			}
			_, err := m.confirmGrant(ctx, alice, "alice", GrantConfirmInput{preview.Grant.ID, preview.Confirmation})
			if err == nil {
				t.Fatal("non-human or unavailable approval was accepted")
			}
			g, err := m.grantStatus(alice, preview.Grant.ID)
			if err != nil || g.State != "preview" || mock.calls.Load() != 0 {
				t.Fatal("rejected approval changed grant or contacted tool")
			}
		})
	}
}

func TestA08LegacyUnprovenGrantCannotDispatch(t *testing.T) {
	mock := newMock(t)
	m := testModule(t, mock)
	_, i, _ := prepared(t, m, mock, "none", "free")
	in := invocation(t, m, alice, i.ID, "legacy-consent")
	if err := m.store.update(func(d *database) error {
		data, _ := json.Marshal(d.Grants[in.Grant])
		var old map[string]json.RawMessage
		_ = json.Unmarshal(data, &old)
		delete(old, "approval_id")
		delete(old, "approval_hash")
		data, _ = json.Marshal(old)
		var g ExecutionGrant
		_ = json.Unmarshal(data, &g)
		d.Grants[in.Grant] = g
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.execute(context.Background(), alice, "alice", in); !errors.Is(err, ErrConsent) {
		t.Fatal("old approval without human evidence was dispatched", err)
	}
	if mock.calls.Load() != 0 {
		t.Fatal("unproved consent reached provider")
	}
}

func TestA08SavedResultsAreFilteredOnEveryRead(t *testing.T) {
	for _, route := range []string{"replay", "legacy-replay", "status", "settlement"} {
		t.Run(route, func(t *testing.T) {
			mock := newMock(t)
			m := testModule(t, mock)
			_, i, _ := prepared(t, m, mock, "api_key", "free")
			in := invocation(t, m, alice, i.ID, "old-result")
			if _, err := m.execute(context.Background(), alice, "alice", in); err != nil {
				t.Fatal(err)
			}
			id := digest([]string{"toolmarket/v1", alice.key(), in.IdempotencyKey})
			if err := m.store.update(func(d *database) error {
				x := d.Executions[id]
				x.Result = json.RawMessage(`{"content":[{"type":"text","text":"echo secret-alice"}],"password":"old-private-value","id":9007199254740993}`)
				if route == "legacy-replay" {
					x.RequestHash = ""
				}
				if route == "settlement" {
					x.State = "settlement_pending"
				}
				d.Executions[id] = x
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var data []byte
			if route == "status" {
				w := post(t, m, "/executions/status", "alice", `{"idempotency_key":"old-result"}`)
				if w.Code != 200 {
					t.Fatal(w.Code)
				}
				data = w.Body.Bytes()
			} else {
				if route == "settlement" {
					f := newFunds()
					var charge Charge
					_ = m.store.view(func(d database) error { charge = d.Executions[id].Charge; return nil })
					if err := f.Reserve(context.Background(), "alice", alice, charge); err != nil {
						t.Fatal(err)
					}
					m.funds = f
				}
				out, err := m.execute(context.Background(), alice, "alice", in)
				if err != nil {
					t.Fatal(err)
				}
				data, _ = json.Marshal(out)
			}
			if strings.Contains(string(data), "old-private-value") || strings.Contains(string(data), "secret-alice") || !strings.Contains(string(data), "9007199254740993") {
				t.Fatal("saved result leaked a secret or changed exact numbers")
			}
			if mock.calls.Load() != 1 {
				t.Fatal("read replayed provider invocation")
			}
		})
	}
}

type nilFunds struct{}

func (*nilFunds) Reserve(context.Context, string, Principal, Charge) error { panic("nil funds") }
func (*nilFunds) Commit(context.Context, string, Principal, Charge) error  { panic("nil funds") }
func TestA08TypedNilDependenciesFailAtConstruction(t *testing.T) {
	var authority *testAuthority
	if _, err := New(&Store{data: emptyDB()}, authority, Config{CallbackURL: "https://app.example/callback"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("typed nil authority accepted")
	}
	var funds *nilFunds
	if _, err := New(&Store{data: emptyDB()}, &testAuthority{}, Config{CallbackURL: "https://app.example/callback", Funds: funds}); !errors.Is(err, ErrInvalid) {
		t.Fatal("typed nil funds accepted")
	}
}

func lifecycleMCP(t *testing.T, badVersion, duplicateSession bool, list string) (*Module, Server, *atomic.Int32) {
	t.Helper()
	deleted := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleted.Add(1)
			w.WriteHeader(204)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any
		if req.Method == "initialize" {
			w.Header().Add("Mcp-Session-Id", "synthetic-session")
			if duplicateSession {
				w.Header().Add("Mcp-Session-Id", "other-session")
			}
			version := ProtocolVersion
			if badVersion {
				version = "unsupported-fixture"
			}
			result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}}
		} else {
			result = json.RawMessage(list)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	m, err := New(&Store{data: emptyDB()}, &testAuthority{}, Config{CallbackURL: "https://app.example/callback"})
	if err != nil {
		t.Fatal(err)
	}
	m.net = &network{test: true, client: server.Client()}
	return m, Server{Endpoint: server.URL, Auth: "none"}, deleted
}
func TestA08FailedMCPHandshakeClosesSession(t *testing.T) {
	m, server, deleted := lifecycleMCP(t, true, false, `{"tools":[]}`)
	if _, err := m.connect(context.Background(), server, Secret{}); err == nil {
		t.Fatal("invalid version accepted")
	}
	if deleted.Load() != 1 {
		t.Fatal("failed handshake leaked allocated session")
	}
}
func TestA08MCPRejectsDuplicateSessionHeaders(t *testing.T) {
	m, server, _ := lifecycleMCP(t, false, true, `{"tools":[]}`)
	c, err := m.connect(context.Background(), server, Secret{})
	if err == nil {
		c.close(context.Background())
		t.Fatal("ambiguous session was accepted")
	}
}
func TestA08CancelledMCPCallStillClosesSession(t *testing.T) {
	m, server, deleted := lifecycleMCP(t, false, false, `{"tools":[]}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := m.connect(ctx, server, Secret{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	c.close(ctx)
	if deleted.Load() != 1 {
		t.Fatal("cancelled context prevented bounded session cleanup")
	}
}
func TestA08MCPListRequiresAnArray(t *testing.T) {
	for _, input := range []string{`{}`, `{"tools":null}`, `{"tools":{}}`, `{"tools":[]}`} {
		t.Run(input, func(t *testing.T) {
			m, server, _ := lifecycleMCP(t, false, false, input)
			c, err := m.connect(context.Background(), server, Secret{})
			if err != nil {
				t.Fatal(err)
			}
			defer c.close(context.Background())
			_, err = c.tools(context.Background())
			if (err == nil) != (input == `{"tools":[]}`) {
				t.Fatal("invalid tool-list response was treated as empty success", err)
			}
		})
	}
}
