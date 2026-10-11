// Package access translates fresh Rust identity checks for extension operations.
// It stores no identities, credentials, roles, sessions or money authority.
package access

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"time"
	"unicode/utf8"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrInvalid      = errors.New("invalid_request")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not_found")
	ErrConflict     = errors.New("state_conflict")
	ErrUnavailable  = errors.New("dependency_unavailable")
	ErrLimit        = errors.New("limit_reached")
)

// Account IDs use the identity protocol's user/team IDs, not ledger account IDs.
type Account struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
}

func (a Account) Valid() bool { return a.ID > 0 && (a.Kind == "personal" || a.Kind == "team") }
func (a Account) Key() string { b, _ := json.Marshal(a); return string(b) }

type Principal struct {
	UserID   int64   `json:"user_id"`
	Level    int32   `json:"level"`
	Account  Account `json:"account"`
	TeamRole string  `json:"team_role,omitempty"`
}

func (p Principal) LevelLabel() string {
	if p.Level < 0 || p.Level > 6 {
		return "unknown"
	}
	return [...]string{"L0", "L1", "L2", "L3", "L4", "L5 (administrator)", "L6 (super administrator)"}[p.Level]
}

type Permission string

const (
	Read   Permission = "read"
	Manage Permission = "manage"
)

type Authority interface {
	// A zero account selects the authenticated user's personal account.
	Check(context.Context, string, Account, Permission) (Principal, error)
}
type Core interface {
	Authorize(context.Context, string) (*pb.AuthorizeResponse, error)
	ListTeams(context.Context, string, int64) (*pb.ListTeamsResponse, error)
}
type Rust struct{ core Core }

func New(core Core) (*Rust, error) {
	if Missing(core) {
		return nil, ErrInvalid
	}
	return &Rust{core}, nil
}
func Missing(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface, reflect.Chan:
		return v.IsNil()
	}
	return false
}
func (a *Rust) Check(ctx context.Context, token string, account Account, permission Permission) (Principal, error) {
	if permission != Read && permission != Manage {
		return Principal{}, ErrInvalid
	}
	if account != (Account{}) && !account.Valid() {
		return Principal{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	self, err := a.core.Authorize(ctx, token)
	if err != nil {
		return Principal{}, Translate(err)
	}
	if self == nil || self.UserId <= 0 || self.PlatformLevel < 0 || self.PlatformLevel > 6 {
		return Principal{}, ErrUnavailable
	}
	// The current protocol gives API keys no assistant/operations scopes. A key
	// owned by an administrator is still not an interactive management session.
	if self.CredentialKind != pb.CredentialKind_CREDENTIAL_KIND_SESSION || self.GetOwner().GetKind() != pb.AccountKind_ACCOUNT_KIND_PERSONAL || self.GetOwner().GetId() != self.UserId {
		return Principal{}, ErrForbidden
	}
	if account == (Account{}) {
		account = Account{"personal", self.UserId}
	}
	p := Principal{UserID: self.UserId, Level: self.PlatformLevel, Account: account}
	if account.Kind == "personal" {
		if account.ID != self.UserId {
			return Principal{}, ErrForbidden
		}
		return p, nil
	}
	var after int64
	for page := 0; page < 100; page++ {
		teams, err := a.core.ListTeams(ctx, token, after)
		if err != nil {
			return Principal{}, Translate(err)
		}
		if teams == nil || len(teams.Teams) > 100 {
			return Principal{}, ErrUnavailable
		}
		last := after
		var match *pb.Team
		for _, team := range teams.Teams {
			if team == nil || team.Id <= last {
				return Principal{}, ErrUnavailable
			}
			last = team.Id
			if team.Id == account.ID {
				match = team
			}
		}
		if teams.NextAfterId != 0 && (teams.NextAfterId <= after || teams.NextAfterId != last) {
			return Principal{}, ErrUnavailable
		}
		if match != nil {
			switch match.Role {
			case pb.TeamRole_TEAM_ROLE_OWNER:
				p.TeamRole = "owner"
			case pb.TeamRole_TEAM_ROLE_ADMIN:
				p.TeamRole = "admin"
			case pb.TeamRole_TEAM_ROLE_MEMBER:
				p.TeamRole = "member"
			default:
				return Principal{}, ErrForbidden
			}
			if permission == Manage && p.TeamRole == "member" {
				return Principal{}, ErrForbidden
			}
			return p, nil
		}
		if teams.NextAfterId == 0 {
			return Principal{}, ErrForbidden
		}
		after = teams.NextAfterId
	}
	return Principal{}, ErrUnavailable
}
func Translate(err error) error {
	switch status.Code(err) {
	case codes.Unauthenticated:
		return ErrUnauthorized
	case codes.PermissionDenied:
		return ErrForbidden
	case codes.InvalidArgument:
		return ErrInvalid
	default:
		return ErrUnavailable
	}
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$`)

func ID(s string) bool { return identifier.MatchString(s) }
func Credential(r *http.Request) (string, error) {
	v := r.Header.Values("X-LMM-User-Credential")
	if len(v) != 1 || len(v[0]) < 32 || len(v[0]) > 256 {
		return "", ErrUnauthorized
	}
	for _, c := range v[0] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return "", ErrUnauthorized
		}
	}
	return v[0], nil
}

// Decode rejects duplicate keys, trailing documents, null, unknown fields and
// excessive nesting. Validation and execution therefore see the same document.
func Decode(data []byte, out any) error {
	if len(data) == 0 || len(data) > 16<<10 || !utf8.Valid(data) {
		return ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	first, err := dec.Token()
	if err != nil || first != json.Delim('{') {
		return ErrInvalid
	}
	if err = object(dec, 0); err != nil {
		return err
	}
	if _, err = dec.Token(); err != io.EOF {
		return ErrInvalid
	}
	dec = json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err = dec.Decode(out); err != nil {
		return ErrInvalid
	}
	return nil
}
func object(d *json.Decoder, depth int) error {
	if depth > 12 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return ErrInvalid
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return ErrInvalid
		}
		seen[name] = true
		if err = value(d, depth+1); err != nil {
			return err
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return ErrInvalid
	}
	return nil
}
func value(d *json.Decoder, depth int) error {
	if depth > 12 {
		return ErrInvalid
	}
	t, err := d.Token()
	if err != nil {
		return ErrInvalid
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			return object(d, depth+1)
		case '[':
			for d.More() {
				if err = value(d, depth+1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	return nil
}
func Respond(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		code, label := http.StatusServiceUnavailable, ErrUnavailable.Error()
		for _, pair := range []struct {
			err  error
			code int
		}{{ErrInvalid, 400}, {ErrUnauthorized, 401}, {ErrForbidden, 403}, {ErrNotFound, 404}, {ErrConflict, 409}, {ErrLimit, 429}} {
			if errors.Is(err, pair.err) {
				code, label = pair.code, pair.err.Error()
				break
			}
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": label})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}
func Body(w http.ResponseWriter, r *http.Request, out any) error {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		return ErrInvalid
	}
	return Decode(data, out)
}
