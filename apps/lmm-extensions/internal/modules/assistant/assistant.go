// Package assistant provides scoped tools and bounded conversations outside core.
// Conversation text and complete tool payloads are not retained. Display
// preferences and explicitly submitted feedback have their own stored records.
package assistant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/storage"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/promotions"
)

type Session struct {
	ID         string         `json:"id"`
	UserID     int64          `json:"user_id"`
	Account    access.Account `json:"account"`
	Calls      int            `json:"calls"`
	ActiveCall string         `json:"active_call,omitempty"`
	ActiveTurn string         `json:"active_turn,omitempty"`
	Closed     bool           `json:"closed"`
	Reason     string         `json:"reason,omitempty"`
	ExpiresAt  time.Time      `json:"expires_at"`
}
type callRecord struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Tool      string    `json:"tool"`
	Hash      string    `json:"hash"`
	Status    string    `json:"status"`
	At        time.Time `json:"at"`
}
type Preferences struct {
	Theme    string `json:"theme"`
	Language string `json:"language"`
	Currency string `json:"currency"`
}
type Entry struct {
	ID          string    `json:"id"`
	UserID      int64     `json:"user_id"`
	Kind        string    `json:"kind"`
	Reference   string    `json:"reference"`
	Status      string    `json:"status,omitempty"`
	Category    string    `json:"category,omitempty"`
	Description string    `json:"description,omitempty"` // Explicit feedback only, not chat.
	At          time.Time `json:"at"`
}
type Result struct {
	Tool      string `json:"tool"`
	Value     any    `json:"value,omitempty"`
	Continue  bool   `json:"continue"`
	Reason    string `json:"reason,omitempty"`
	Duplicate bool   `json:"duplicate,omitempty"`
}
type Options struct {
	MaxCalls   int
	SessionTTL time.Duration
	Model      Model
	Tools      []Tool
}
type Service struct {
	repo       storage.Repository
	auth       access.Authority
	promotions *promotions.Service
	registry   *Registry
	maxCalls   int
	ttl        time.Duration
	model      Model
	now        func() time.Time
}

func New(repo storage.Repository, auth access.Authority, promos *promotions.Service, options Options) (*Service, error) {
	if access.Missing(repo) || access.Missing(auth) {
		return nil, access.ErrInvalid
	}
	if options.MaxCalls == 0 {
		options.MaxCalls = 12
	}
	if options.SessionTTL == 0 {
		options.SessionTTL = 30 * time.Minute
	}
	if options.MaxCalls < 1 || options.MaxCalls > 32 || options.SessionTTL < time.Minute || options.SessionTTL > time.Hour {
		return nil, access.ErrInvalid
	}
	s := &Service{repo: repo, auth: auth, promotions: promos, maxCalls: options.MaxCalls, ttl: options.SessionTTL, model: options.Model, now: time.Now}
	registry, e := NewRegistry(append(s.builtins(), options.Tools...))
	if e != nil {
		return nil, e
	}
	s.registry = registry
	return s, nil
}
func freshID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func scope(p access.Principal) string { return fmt.Sprintf("%d:%s", p.UserID, p.Account.Key()) }
func (s *Service) check(ctx context.Context, token string, account access.Account, permission access.Permission) (access.Principal, error) {
	p, e := s.auth.Check(ctx, token, account, permission)
	if e != nil {
		return p, e
	}
	if p.UserID <= 0 || p.Level < 0 || p.Level > 6 || !p.Account.Valid() {
		return p, access.ErrUnavailable
	}
	return p, nil
}
func (s *Service) Start(ctx context.Context, token string, account access.Account) (Session, error) {
	p, e := s.check(ctx, token, account, access.Read)
	if e != nil {
		return Session{}, e
	}
	session := Session{ID: freshID(), UserID: p.UserID, Account: p.Account, ExpiresAt: s.now().UTC().Add(s.ttl)}
	if session.ID == "" {
		return Session{}, access.ErrUnavailable
	}
	e = s.repo.Within(ctx, scope(p), func(tx storage.Tx) error { return tx.Put("sessions", session.ID, session) })
	return session, e
}
func sessionFor(tx storage.Tx, p access.Principal, id string) (Session, error) {
	var v Session
	e := tx.Get("sessions", id, &v)
	if e != nil {
		return v, e
	}
	if v.UserID != p.UserID || v.Account != p.Account {
		return v, access.ErrNotFound
	}
	return v, nil
}
func (s *Service) Invoke(ctx context.Context, token string, account access.Account, sessionID, callID, name string, data json.RawMessage) (Result, error) {
	if !access.ID(sessionID) || !access.ID(callID) {
		return Result{}, access.ErrInvalid
	}
	t, exists := s.registry.tools[name]
	permission := access.Read
	if exists {
		permission = t.Definition.Permission
	}
	p, e := s.check(ctx, token, account, permission)
	if e != nil {
		return Result{}, e
	}
	if !exists || !allowed(p, t.Definition) {
		return Result{}, access.ErrForbidden
	}
	args, e := t.Definition.Parameters.decode(data)
	if e != nil {
		return Result{}, e
	}
	id := sessionID + ":" + callID
	fingerprint := digest([]any{name, args})
	var session Session
	var record callRecord
	duplicate := false
	e = s.repo.Within(ctx, scope(p), func(tx storage.Tx) error {
		var e error
		session, e = sessionFor(tx, p, sessionID)
		if e != nil {
			return e
		}
		e = tx.Get("calls", id, &record)
		if e == nil {
			if record.Hash != fingerprint || record.Tool != name {
				return access.ErrConflict
			}
			duplicate = true
			return nil
		}
		if !errors.Is(e, access.ErrNotFound) {
			return e
		}
		if session.Closed || !s.now().Before(session.ExpiresAt) || session.ActiveCall != "" || (session.ActiveTurn != "" && session.ActiveTurn != turnFrom(ctx)) {
			return access.ErrConflict
		}
		if session.Calls >= s.maxCalls {
			return access.ErrLimit
		}
		session.Calls++
		session.ActiveCall = id
		if name == "conversation.end" {
			session.Closed = true
			session.Reason = args["reason"].(string)
		}
		record = callRecord{ID: id, SessionID: session.ID, Tool: name, Hash: fingerprint, Status: "pending", At: s.now().UTC()}
		if e = tx.Put("sessions", session.ID, session); e != nil {
			return e
		}
		return tx.Put("calls", id, record)
	})
	if e != nil {
		return Result{}, e
	}
	if duplicate {
		return Result{Tool: name, Value: map[string]string{"status": record.Status}, Duplicate: true, Continue: false, Reason: "duplicate_call"}, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	value, runErr := execute(bounded, t, Invocation{token, p, args})
	resultBytes, marshalErr := json.Marshal(value)
	if marshalErr != nil || len(resultBytes) > 8<<10 {
		value = nil
		runErr = access.ErrLimit
	}
	// Do not expose arbitrary handler error text (which may contain secrets).
	if runErr != nil {
		value = nil
	}
	saveErr := s.repo.Within(ctx, scope(p), func(tx storage.Tx) error {
		current, e := sessionFor(tx, p, sessionID)
		if e != nil {
			return e
		}
		if current.ActiveCall != id {
			return access.ErrConflict
		}
		current.ActiveCall = ""
		record.Status = "done"
		if runErr != nil {
			record.Status = "failed"
			current.Closed = true
			current.Reason = "tool_error"
		}
		if _, pending := value.(promotions.PendingClaim); pending && !current.Closed {
			current.Closed = true
			current.Reason = "reward_pending"
		}
		if current.Calls >= s.maxCalls && !current.Closed {
			current.Closed = true
			current.Reason = "call_limit"
		}
		if e = tx.Put("calls", id, record); e != nil {
			return e
		}
		if e = tx.Put("sessions", sessionID, current); e != nil {
			return e
		}
		session = current
		return nil
	})
	if saveErr != nil {
		return Result{}, saveErr
	}
	if runErr != nil {
		return Result{Tool: name, Continue: false, Reason: "tool_error"}, runErr
	}
	return Result{Tool: name, Value: value, Continue: !session.Closed, Reason: session.Reason}, nil
}
func execute(ctx context.Context, t Tool, in Invocation) (out any, err error) {
	defer func() {
		if recover() != nil {
			out = nil
			err = access.ErrUnavailable
		}
	}()
	return t.Execute(ctx, in)
}
func (s *Service) Close(ctx context.Context, token string, account access.Account, id, reason string) error {
	if reason != "completed" && reason != "no_action" && reason != "loop_detected" && reason != "model_error" && reason != "turn_limit" {
		return access.ErrInvalid
	}
	p, e := s.check(ctx, token, account, access.Read)
	if e != nil {
		return e
	}
	return s.repo.Within(ctx, scope(p), func(tx storage.Tx) error {
		v, e := sessionFor(tx, p, id)
		if e != nil {
			return e
		}
		if v.ActiveCall != "" || (v.ActiveTurn != "" && v.ActiveTurn != turnFrom(ctx)) {
			return access.ErrConflict
		}
		if !v.Closed {
			v.Closed = true
			v.Reason = reason
		}
		return tx.Put("sessions", id, v)
	})
}
func (s *Service) Entries(ctx context.Context, token string, account access.Account, after string, limit int) ([]Entry, error) {
	p, e := s.check(ctx, token, account, access.Read)
	if e != nil {
		return nil, e
	}
	if len(after) > 128 || limit < 1 || limit > 100 {
		return nil, access.ErrInvalid
	}
	out := []Entry{}
	e = s.repo.Within(ctx, scope(p), func(tx storage.Tx) error {
		rows, e := tx.List("entries", after, limit)
		if e != nil {
			return e
		}
		for _, b := range rows {
			var entry Entry
			if json.Unmarshal(b, &entry) != nil || entry.UserID != p.UserID {
				return access.ErrUnavailable
			}
			out = append(out, entry)
		}
		return nil
	})
	return out, e
}
func (s *Service) log(ctx context.Context, in Invocation, kind string) (any, error) {
	ref := in.Arguments["reference"].(string)
	if !access.ID(ref) {
		return nil, access.ErrInvalid
	}
	random := freshID()
	if random == "" {
		return nil, access.ErrUnavailable
	}
	entry := Entry{ID: fmt.Sprintf("%020d-%s", s.now().UnixMicro(), random), UserID: in.Principal.UserID, Kind: kind, Reference: ref, At: s.now().UTC()}
	if kind == "feedback" {
		entry.Category = in.Arguments["category"].(string)
		entry.Description = strings.TrimSpace(in.Arguments["description"].(string))
		if entry.Description == "" {
			return nil, access.ErrInvalid
		}
	} else {
		entry.Status = in.Arguments["status"].(string)
	}
	e := s.repo.Within(ctx, scope(in.Principal), func(tx storage.Tx) error { return tx.Put("entries", entry.ID, entry) })
	return map[string]string{"id": entry.ID}, e
}
func (s *Service) builtins() []Tool {
	makeTool := func(name, description string, permission access.Permission, personal bool, minLevel int32, properties map[string]Property, fn func(context.Context, Invocation) (any, error)) Tool {
		return Tool{Definition: Definition{Summary: Summary{name, description, permission, minLevel, personal}, Parameters: objectSchema(properties)}, Execute: fn}
	}
	tools := []Tool{
		makeTool("account.self", "Read current Rust identity and account scope.", access.Read, false, 0, map[string]Property{}, func(_ context.Context, in Invocation) (any, error) {
			return struct {
				access.Principal
				LevelLabel string `json:"level_label"`
			}{in.Principal, in.Principal.LevelLabel()}, nil
		}),
		makeTool("tools.search", "Find permitted tools; returns summaries, not full parameter schemas.", access.Read, false, 0, map[string]Property{"query": {Type: "string", MaxLength: 64}, "after": {Type: "string", MaxLength: 128}, "limit": numberProperty(1, 20)}, func(_ context.Context, in Invocation) (any, error) {
			tools, next, err := s.registry.Discover(in.Principal, in.Arguments["query"].(string), in.Arguments["after"].(string), int(in.Arguments["limit"].(int64)))
			return struct {
				Tools []Summary `json:"tools"`
				Next  string    `json:"next,omitempty"`
			}{tools, next}, err
		}),
		makeTool("tools.describe", "Load one permitted tool's exact input schema.", access.Read, false, 0, map[string]Property{"name": textProperty(128)}, func(_ context.Context, in Invocation) (any, error) {
			return s.registry.Describe(in.Principal, in.Arguments["name"].(string))
		}),
		makeTool("preferences.get", "Read account display preferences.", access.Read, false, 0, map[string]Property{}, func(ctx context.Context, in Invocation) (any, error) {
			v := Preferences{"system", "en", "USD"}
			e := s.repo.Within(ctx, in.Principal.Account.Key(), func(tx storage.Tx) error {
				e := tx.Get("preferences", "display", &v)
				if errors.Is(e, access.ErrNotFound) {
					return nil
				}
				return e
			})
			return v, e
		}),
		makeTool("preferences.set", "Set display options. Team writes require owner or admin.", access.Manage, false, 0, map[string]Property{"theme": enumProperty("system", "light", "dark"), "language": enumProperty("en", "zh-CN", "zh-TW", "ja", "ko", "fr", "de", "es"), "currency": enumProperty("USD", "CNY", "POINTS")}, func(ctx context.Context, in Invocation) (any, error) {
			v := Preferences{in.Arguments["theme"].(string), in.Arguments["language"].(string), in.Arguments["currency"].(string)}
			e := s.repo.Within(ctx, in.Principal.Account.Key(), func(tx storage.Tx) error { return tx.Put("preferences", "display", v) })
			return v, e
		}),
		makeTool("conversation.end", "End this conversation without another model request.", access.Read, false, 0, map[string]Property{"reason": enumProperty("completed", "no_action", "user_request")}, func(_ context.Context, in Invocation) (any, error) {
			return map[string]string{"reason": in.Arguments["reason"].(string)}, nil
		}),
	}
	for _, kind := range []string{"task", "drawing", "operation"} {
		minLevel := int32(0)
		permission := access.Read
		if kind == "operation" {
			minLevel = 5
			permission = access.Manage
		}
		tools = append(tools, makeTool("logs."+kind, "Record status and an opaque reference, never a prompt or image.", permission, false, minLevel, map[string]Property{"reference": textProperty(128), "status": enumProperty("started", "completed", "failed", "cancelled")}, func(ctx context.Context, in Invocation) (any, error) { return s.log(ctx, in, kind) }))
	}
	tools = append(tools, makeTool("feedback.submit", "Save explicitly submitted feedback, not the conversation.", access.Read, false, 0, map[string]Property{"reference": textProperty(128), "category": enumProperty("bug", "experience", "feature"), "description": textProperty(2048)}, func(ctx context.Context, in Invocation) (any, error) { return s.log(ctx, in, "feedback") }))
	if s.promotions != nil {
		tools = append(tools, makeTool("promotions.limits", "Read reward limits for your current level.", access.Read, true, 0, map[string]Property{}, func(ctx context.Context, in Invocation) (any, error) { return s.promotions.Limits(ctx, in.Credential) }))
		for _, action := range []string{"read", "reconcile"} {
			tools = append(tools, makeTool("promotions.claim."+action, "Read or resume your exact saved claim; never start a replacement reward.", access.Read, true, 0, map[string]Property{"key": textProperty(128)}, func(ctx context.Context, in Invocation) (any, error) {
				key := in.Arguments["key"].(string)
				if action == "read" {
					return s.promotions.Read(ctx, in.Credential, key)
				}
				claim, err := s.promotions.Reconcile(ctx, in.Credential, key)
				return promotions.Result(claim, err)
			}))
		}
		for _, kind := range []string{promotions.Coupon, promotions.Referral} {
			properties := map[string]Property{"campaign": textProperty(128)}
			if kind == promotions.Referral {
				properties["evidence_ref"] = textProperty(128)
			}
			for _, action := range []string{"eligibility", "apply"} {
				tools = append(tools, makeTool("promotions."+kind+"."+action, "Request a core-verified campaign benefit; no caller-set amount.", access.Read, true, 0, properties, func(ctx context.Context, in Invocation) (any, error) {
					evidence, _ := in.Arguments["evidence_ref"].(string)
					campaign := in.Arguments["campaign"].(string)
					if action == "eligibility" {
						return s.promotions.Eligibility(ctx, in.Credential, campaign, evidence, kind)
					}
					claim, err := s.promotions.Apply(ctx, in.Credential, campaign, evidence, kind)
					return promotions.Result(claim, err)
				}))
			}
		}
	}
	return tools
}
