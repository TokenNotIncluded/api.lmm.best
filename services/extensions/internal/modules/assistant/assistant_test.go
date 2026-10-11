package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/testkit"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/promotions"
)

var ctx = context.Background()
var self = access.Account{}
var user = testkit.Token("u")
var other = testkit.Token("v")

func setup(t *testing.T, options Options) (*Service, *testkit.Store, *testkit.Identity) {
	t.Helper()
	store := testkit.NewStore()
	identity := testkit.NewIdentity()
	identity.Set(user, 1, 1)
	identity.Set(other, 2, 6)
	auth, e := access.New(identity)
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(store, auth, nil, options)
	if e != nil {
		t.Fatal(e)
	}
	return s, store, identity
}
func start(t *testing.T, s *Service, token string, account access.Account) Session {
	t.Helper()
	v, e := s.Start(ctx, token, account)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func invoke(t *testing.T, s *Service, session Session, token, id, name, data string) Result {
	t.Helper()
	v, e := s.Invoke(ctx, token, session.Account, session.ID, id, name, json.RawMessage(data))
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func request(handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("X-LMM-User-Credential", token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}
func TestToolDiscoveryAndImmutableExactSchemas(t *testing.T) {
	s, _, _ := setup(t, Options{})
	p, e := s.check(ctx, user, self, access.Read)
	if e != nil {
		t.Fatal(e)
	}
	entries, next, e := s.registry.Discover(p, "", "", 3)
	if e != nil || len(entries) != 3 || next == "" {
		t.Fatalf("page: %v %s %v", entries, next, e)
	}
	b, _ := json.Marshal(entries)
	if strings.Contains(string(b), "parameters") || strings.Contains(string(b), "logs.operation") {
		t.Fatal(string(b))
	}
	second, _, _ := s.registry.Discover(p, "", next, 20)
	for _, v := range second {
		if v.Name <= next {
			t.Fatal("bad cursor")
		}
	}
	d, e := s.registry.Describe(p, "preferences.set")
	if e != nil || d.Parameters.AdditionalProperties || len(d.Parameters.Required) != 3 {
		t.Fatal(d, e)
	}
	d.Parameters.Properties["theme"] = textProperty(100)
	original, _ := s.registry.Describe(p, "preferences.set")
	if len(original.Parameters.Properties["theme"].Enum) == 0 {
		t.Fatal("description mutated registration")
	}
	def := Definition{Summary: Summary{Name: "custom.number", Description: "A bounded integer.", Permission: access.Read}, Parameters: objectSchema(map[string]Property{"n": numberProperty(1, 2)})}
	tool := Tool{Definition: def, Execute: func(context.Context, Invocation) (any, error) { return nil, nil }}
	registry, e := NewRegistry([]Tool{tool})
	if e != nil {
		t.Fatal(e)
	}
	prop := def.Parameters.Properties["n"]
	*prop.Maximum = 100
	def.Parameters.Required[0] = "role"
	frozen, _ := registry.Describe(p, "custom.number")
	if *frozen.Parameters.Properties["n"].Maximum != 2 || frozen.Parameters.Required[0] != "n" {
		t.Fatal("caller mutated registry")
	}
	for _, data := range []string{`{"n":3}`, `{"n":1.5}`, `{"n":1,"role":"admin"}`, `{"n":"1"}`, `{"n":1,"n":2}`} {
		if _, e := frozen.Parameters.decode(json.RawMessage(data)); !errors.Is(e, access.ErrInvalid) {
			t.Fatal(data, e)
		}
	}
	tool.Definition = frozen
	if _, e := NewRegistry([]Tool{tool, tool}); !errors.Is(e, access.ErrConflict) {
		t.Fatal(e)
	}
	if _, e := New(testkit.NewStore(), s.auth, nil, Options{Tools: []Tool{s.registry.tools["account.self"]}}); !errors.Is(e, access.ErrConflict) {
		t.Fatal("builtin override", e)
	}
}
func TestAssistantCannotEscalateOrChangeAnotherAccount(t *testing.T) {
	s, _, identity := setup(t, Options{})
	session := start(t, s, user, self)
	for _, data := range []string{`{"role":"admin"}`, `{"user_id":2}`, `{"level":6}`, `{"balance":9999}`} {
		if _, e := s.Invoke(ctx, user, self, session.ID, "inject", "account.self", json.RawMessage(data)); !errors.Is(e, access.ErrInvalid) {
			t.Fatal(data, e)
		}
	}
	if _, e := s.Invoke(ctx, user, self, session.ID, "ops", "logs.operation", json.RawMessage(`{"reference":"event-1","status":"completed"}`)); !errors.Is(e, access.ErrForbidden) {
		t.Fatal(e)
	}
	if _, e := s.Invoke(ctx, other, self, session.ID, "steal", "account.self", json.RawMessage(`{}`)); !errors.Is(e, access.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := s.Start(ctx, other, access.Account{Kind: "personal", ID: 1}); !errors.Is(e, access.ErrForbidden) {
		t.Fatal("L6 bypass", e)
	}
	result := invoke(t, s, session, user, "self", "account.self", `{}`)
	b, _ := json.Marshal(result)
	if !strings.Contains(string(b), `"level":1`) {
		t.Fatal(string(b))
	}
	identity.Revoke(user)
	if _, e := s.Invoke(ctx, user, self, session.ID, "self", "account.self", json.RawMessage(`{}`)); !errors.Is(e, access.ErrUnauthorized) {
		t.Fatal("cached authority on replay", e)
	}
}
func TestTeamPreferencesNeedOwnerOrAdminAndStayInScope(t *testing.T) {
	for _, role := range []pb.TeamRole{pb.TeamRole_TEAM_ROLE_OWNER, pb.TeamRole_TEAM_ROLE_ADMIN, pb.TeamRole_TEAM_ROLE_MEMBER} {
		t.Run(role.String(), func(t *testing.T) {
			s, _, identity := setup(t, Options{})
			team := access.Account{Kind: "team", ID: 7}
			identity.Set(user, 1, 6, &pb.Team{Id: 7, Role: role})
			identity.Set(other, 2, 1, &pb.Team{Id: 7, Role: pb.TeamRole_TEAM_ROLE_MEMBER})
			session := start(t, s, user, team)
			_, e := s.Invoke(ctx, user, team, session.ID, "set", "preferences.set", json.RawMessage(`{"theme":"dark","language":"zh-CN","currency":"CNY"}`))
			if role == pb.TeamRole_TEAM_ROLE_MEMBER {
				if !errors.Is(e, access.ErrForbidden) {
					t.Fatal(e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			readSession := start(t, s, other, team)
			v := invoke(t, s, readSession, other, "get", "preferences.get", `{}`)
			if v.Value.(Preferences).Currency != "CNY" {
				t.Fatal(v)
			}
			ownSession := start(t, s, other, self)
			own := invoke(t, s, ownSession, other, "own", "preferences.get", `{}`)
			if own.Value.(Preferences).Currency != "USD" {
				t.Fatal("team preference leaked", own)
			}
			identity.Set(user, 1, 6, &pb.Team{Id: 7, Role: pb.TeamRole_TEAM_ROLE_MEMBER})
			if _, e := s.Invoke(ctx, user, team, session.ID, "next", "preferences.set", json.RawMessage(`{"theme":"light","language":"en","currency":"USD"}`)); !errors.Is(e, access.ErrForbidden) {
				t.Fatal("stale team role", e)
			}
		})
	}
}
func TestSessionCallIdempotencyBudgetExpiryAndExplicitEnd(t *testing.T) {
	s, _, _ := setup(t, Options{MaxCalls: 2})
	session := start(t, s, user, self)
	invoke(t, s, session, user, "one", "logs.task", `{"reference":"task-1","status":"started"}`)
	duplicate := invoke(t, s, session, user, "one", "logs.task", `{"status":"started","reference":"task-1"}`)
	if !duplicate.Duplicate || duplicate.Continue {
		t.Fatal(duplicate)
	}
	if _, e := s.Invoke(ctx, user, self, session.ID, "one", "logs.task", json.RawMessage(`{"reference":"task-2","status":"started"}`)); !errors.Is(e, access.ErrConflict) {
		t.Fatal(e)
	}
	v := invoke(t, s, session, user, "two", "account.self", `{}`)
	if v.Continue || v.Reason != "call_limit" {
		t.Fatal(v)
	}
	if _, e := s.Invoke(ctx, user, self, session.ID, "three", "account.self", json.RawMessage(`{}`)); !errors.Is(e, access.ErrConflict) {
		t.Fatal(e)
	}
	entries, e := s.Entries(ctx, user, self, "", 100)
	if e != nil || len(entries) != 1 {
		t.Fatal(entries, e)
	}
	closed := start(t, s, user, self)
	v = invoke(t, s, closed, user, "end", "conversation.end", `{"reason":"completed"}`)
	if v.Continue || v.Reason != "completed" {
		t.Fatal(v)
	}
	expired := start(t, s, user, self)
	s.now = func() time.Time { return expired.ExpiresAt }
	if _, e := s.Invoke(ctx, user, self, expired.ID, "stale", "account.self", json.RawMessage(`{}`)); !errors.Is(e, access.ErrConflict) {
		t.Fatal(e)
	}
}

type modelFunc func(context.Context, string, ModelRequest) (ModelReply, error)

func (f modelFunc) Next(c context.Context, t string, r ModelRequest) (ModelReply, error) {
	return f(c, t, r)
}
func TestConversationEndStopsRemainingCallsAndModelRequests(t *testing.T) {
	calls := 0
	model := modelFunc(func(_ context.Context, token string, r ModelRequest) (ModelReply, error) {
		calls++
		if token != user || len(r.Tools) != 3 {
			t.Fatal("unscoped or heavy model context", r)
		}
		return ModelReply{Calls: []ToolCall{{"end", "conversation.end", json.RawMessage(`{"reason":"completed"}`)}, {"write", "logs.task", json.RawMessage(`{"reference":"must-not-run","status":"started"}`)}}}, nil
	})
	s, _, _ := setup(t, Options{Model: model})
	session := start(t, s, user, self)
	reply, e := s.Chat(ctx, user, self, session.ID, "Finish this conversation")
	if e != nil || reply.Reason != "completed" || calls != 1 || len(reply.Results) != 1 {
		t.Fatal(reply, calls, e)
	}
	entries, _ := s.Entries(ctx, user, self, "", 20)
	if len(entries) != 0 {
		t.Fatal("executed after terminal tool")
	}
	if _, e := s.Chat(ctx, user, self, session.ID, "again"); !errors.Is(e, access.ErrConflict) || calls != 1 {
		t.Fatal(calls, e)
	}
}
func TestConversationDiscoveryLoadsOnlySelectedSchemasAndDetectsLoops(t *testing.T) {
	calls := 0
	model := modelFunc(func(_ context.Context, _ string, r ModelRequest) (ModelReply, error) {
		calls++
		if calls == 1 {
			return ModelReply{Calls: []ToolCall{{"schema", "tools.describe", json.RawMessage(`{"name":"logs.task"}`)}}}, nil
		}
		if len(r.Tools) != 4 {
			t.Fatal("selected schema missing", r.Tools)
		}
		return ModelReply{Calls: []ToolCall{{"repeated", "logs.task", json.RawMessage(`{"reference":"task-one","status":"started"}`)}}}, nil
	})
	s, _, _ := setup(t, Options{Model: model})
	session := start(t, s, user, self)
	v, e := s.Chat(ctx, user, self, session.ID, "Create one task")
	if e != nil || v.Reason != "loop_detected" || calls != 3 {
		t.Fatal(v, calls, e)
	}
	entries, _ := s.Entries(ctx, user, self, "", 20)
	if len(entries) != 1 {
		t.Fatal(entries)
	}
}
func TestPrivacyEmptyRepliesAndModelFailures(t *testing.T) {
	for _, mode := range []string{"complete", "empty", "failure", "panic"} {
		t.Run(mode, func(t *testing.T) {
			model := modelFunc(func(context.Context, string, ModelRequest) (ModelReply, error) {
				switch mode {
				case "empty":
					return ModelReply{}, nil
				case "failure":
					return ModelReply{}, errors.New("secret-model-error")
				case "panic":
					panic("secret-model-error")
				}
				return ModelReply{Text: "private-response-sentinel"}, nil
			})
			s, store, _ := setup(t, Options{Model: model})
			session := start(t, s, user, self)
			out, e := s.Chat(ctx, user, self, session.ID, "private-chat-sentinel")
			if mode == "failure" || mode == "panic" {
				if !errors.Is(e, access.ErrUnavailable) {
					t.Fatal(e)
				}
			} else if e != nil {
				t.Fatal(out, e)
			}
			if mode == "empty" && out.Reason != "no_action" {
				t.Fatal(out)
			}
			b, _ := json.Marshal(store.Data)
			for _, secret := range []string{"private-chat-sentinel", "private-response-sentinel", user, "secret-model-error"} {
				if strings.Contains(string(b), secret) {
					t.Fatal("persisted private text", secret)
				}
			}
		})
	}
	s, _, _ := setup(t, Options{})
	session := start(t, s, user, self)
	if _, e := s.Chat(ctx, user, self, session.ID, "no production relay"); !errors.Is(e, access.ErrUnavailable) {
		t.Fatal(e)
	}
}
func TestTaskDrawingFeedbackAndOperationalAudit(t *testing.T) {
	s, store, _ := setup(t, Options{})
	session := start(t, s, user, self)
	invoke(t, s, session, user, "task", "logs.task", `{"reference":"task-1","status":"completed"}`)
	invoke(t, s, session, user, "drawing", "logs.drawing", `{"reference":"drawing-1","status":"failed"}`)
	invoke(t, s, session, user, "feedback", "feedback.submit", `{"reference":"feedback-1","category":"bug","description":"Explicit issue text"}`)
	admin := start(t, s, other, self)
	invoke(t, s, admin, other, "operation", "logs.operation", `{"reference":"operation-1","status":"completed"}`)
	entries, e := s.Entries(ctx, user, self, "", 2)
	if e != nil || len(entries) != 2 {
		t.Fatal(entries, e)
	}
	remaining, _ := s.Entries(ctx, user, self, entries[1].ID, 20)
	if len(remaining) != 1 || remaining[0].Kind != "feedback" {
		t.Fatal(remaining)
	}
	otherEntries, _ := s.Entries(ctx, other, self, "", 100)
	if len(otherEntries) != 1 || otherEntries[0].Kind != "operation" {
		t.Fatal(otherEntries)
	}
	for _, records := range store.Data {
		for key, b := range records {
			if strings.HasPrefix(key, "calls/") && (strings.Contains(string(b), "Explicit issue text") || strings.Contains(string(b), "arguments") || strings.Contains(string(b), "value")) {
				t.Fatal("audit contains payload", string(b))
			}
		}
	}
}
func TestConcurrentTurnExcludesDirectToolsAndAnotherTurn(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	model := modelFunc(func(context.Context, string, ModelRequest) (ModelReply, error) {
		close(entered)
		<-release
		return ModelReply{Text: "done"}, nil
	})
	s, _, _ := setup(t, Options{Model: model})
	session := start(t, s, user, self)
	go func() { _, e := s.Chat(ctx, user, self, session.ID, "one"); finished <- e }()
	<-entered
	if _, e := s.Chat(ctx, user, self, session.ID, "two"); !errors.Is(e, access.ErrConflict) {
		t.Fatal(e)
	}
	if _, e := s.Invoke(ctx, user, self, session.ID, "injected", "account.self", json.RawMessage(`{}`)); !errors.Is(e, access.ErrConflict) {
		t.Fatal(e)
	}
	close(release)
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
}
func TestToolFailuresAndConcurrentRetriesAreBounded(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	tool := Tool{Definition: Definition{Summary: Summary{Name: "test.block", Description: "Test controlled call.", Permission: access.Read}, Parameters: objectSchema(map[string]Property{})}, Execute: func(context.Context, Invocation) (any, error) {
		calls.Add(1)
		close(entered)
		<-release
		return "done", nil
	}}
	s, _, _ := setup(t, Options{Tools: []Tool{tool}})
	session := start(t, s, user, self)
	done := make(chan error, 1)
	go func() {
		_, e := s.Invoke(ctx, user, self, session.ID, "once", "test.block", json.RawMessage(`{}`))
		done <- e
	}()
	<-entered
	duplicate := invoke(t, s, session, user, "once", "test.block", `{}`)
	if !duplicate.Duplicate || duplicate.Continue {
		t.Fatal(duplicate)
	}
	if _, e := s.Invoke(ctx, user, self, session.ID, "another", "test.block", json.RawMessage(`{}`)); !errors.Is(e, access.ErrConflict) {
		t.Fatal(e)
	}
	close(release)
	if e := <-done; e != nil || calls.Load() != 1 {
		t.Fatal(e, calls.Load())
	}
	tool.Definition.Name = "test.panic"
	tool.Execute = func(context.Context, Invocation) (any, error) { panic("private handler error") }
	s, store, _ := setup(t, Options{Tools: []Tool{tool}})
	session = start(t, s, user, self)
	if _, e := s.Invoke(ctx, user, self, session.ID, "panic", "test.panic", json.RawMessage(`{}`)); !errors.Is(e, access.ErrUnavailable) {
		t.Fatal(e)
	}
	b, _ := json.Marshal(store.Data)
	if strings.Contains(string(b), "private handler error") {
		t.Fatal("leaked panic")
	}
	store.Fail = true
	if _, e := s.Start(ctx, user, self); !errors.Is(e, access.ErrUnavailable) {
		t.Fatal(e)
	}
}
func TestHTTPHostCredentialIsNotUserAuthorityAndRejectsForgedInputs(t *testing.T) {
	s, _, _ := setup(t, Options{})
	h := s.Handler()
	for _, path := range []string{"/tools", "/entries", "/tools/account.self"} {
		w := request(h, "GET", path, "", "")
		if w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
	for _, body := range []string{`{"user_id":2}`, `{"team_id":-1}`, `{"level":6}`, `{"team_id":0,"team_id":7}`, `{} {}`} {
		w := request(h, "POST", "/sessions", user, body)
		if w.Code != 400 {
			t.Fatal(body, w.Code)
		}
	}
	w := request(h, "GET", "/tools?limit=2", user, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "parameters") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/tools?team_id=7&team_id=8", user, "")
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	host, e := modules.New([]modules.Module{s}, []byte(strings.Repeat("s", 32)))
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "/extensions/v1/assistant/tools", nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 32))
	w = httptest.NewRecorder()
	host.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("host token granted user access", w.Code)
	}
	r.Header.Set("X-LMM-User-Credential", user)
	w = httptest.NewRecorder()
	host.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestPromotionToolsExposeNoAmountOrTeamGrant(t *testing.T) {
	s, _, identity := setup(t, Options{})
	promo, e := promotions.New(testkit.NewStore(), s.auth, promotions.UnavailableRewards{}, promotions.Policy{Limits: promotions.DefaultLimits()})
	if e != nil {
		t.Fatal(e)
	}
	s, e = New(testkit.NewStore(), s.auth, promo, Options{})
	if e != nil {
		t.Fatal(e)
	}
	session := start(t, s, user, self)
	for _, data := range []string{`{"campaign":"welcome","amount":9999}`, `{"campaign":"welcome","role":"admin"}`, `{"campaign":"welcome","user_id":2}`} {
		if _, e := s.Invoke(ctx, user, self, session.ID, "grant", "promotions.coupon.apply", json.RawMessage(data)); !errors.Is(e, access.ErrInvalid) {
			t.Fatal(e)
		}
	}
	identity.Set(user, 1, 6, &pb.Team{Id: 7, Role: pb.TeamRole_TEAM_ROLE_OWNER})
	team := access.Account{Kind: "team", ID: 7}
	session = start(t, s, user, team)
	if _, e := s.Invoke(ctx, user, team, session.ID, "grant", "promotions.coupon.apply", json.RawMessage(`{"campaign":"welcome"}`)); !errors.Is(e, access.ErrForbidden) {
		t.Fatal(e)
	}
}
