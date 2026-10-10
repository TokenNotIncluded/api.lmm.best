// Package toolmarket owns MCP marketplace data, never core identities or money.
package toolmarket

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid_request")
	ErrDenied      = errors.New("forbidden")
	ErrMissing     = errors.New("not_found")
	ErrConflict    = errors.New("conflict")
	ErrUnavailable = errors.New("core_unavailable")
	ErrFunds       = errors.New("funds_interface_unavailable")
	ErrPrice       = errors.New("price_unavailable")
	ErrAuth        = errors.New("authorization_required")
	ErrUpstream    = errors.New("upstream_error")
	ErrLimit       = errors.New("rate_limited")
	ErrPending     = errors.New("execution_in_doubt")
)

// Authority is implemented by the integration layer using the Rust control RPC.
// It MUST check the live credential, account membership and requested action.
// Neither the host service token nor a submitted account ID grants authority.
type Authority interface {
	Authorize(ctx context.Context, credential, account, action string) (Principal, error)
}
type Principal struct {
	User    string `json:"user"`
	Account string `json:"account"`
}

func (p Principal) valid() bool { return validID.MatchString(p.User) && validID.MatchString(p.Account) }
func (p Principal) key() string { return p.User + ":" + p.Account }

// Funds is an adapter contract, NOT a new public protobuf definition.
// Rust must independently validate the payer, server-side price, permission and
// idempotency key. Commit is idempotent. No adapter is provided until task 06
// supplies a real money RPC. A nil adapter ALWAYS rejects paid calls.
type Funds interface {
	Reserve(context.Context, string, Principal, Charge) error
	Commit(context.Context, string, Principal, Charge) error
}
type Charge struct {
	ID           string `json:"id"`
	Installation string `json:"installation"`
	Tool         string `json:"tool"`
	Quote        string `json:"quote"`
	Currency     string `json:"currency"`
	Amount       int64  `json:"amount"`
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
var validTool = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var validVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

const ProtocolVersion = "2025-11-25"
const maxBody = 1 << 20

type Price struct {
	// Amount is integer millionths of Currency per call, not floating point.
	Kind       string `json:"kind"` // unknown, free, fixed
	Currency   string `json:"currency,omitempty"`
	Amount     int64  `json:"amount"`
	Multiplier int64  `json:"multiplier_bps"`   // 10000 = 1x; cannot sell below source cost
	Source     string `json:"source,omitempty"` // operator supplied evidence URL, never inferred from MCP
	ValidUntil int64  `json:"valid_until,omitempty"`
}

func (p Price) total(now time.Time) (int64, error) {
	if p.Multiplier < 10000 || p.Multiplier > 1000000 {
		return 0, ErrInvalid
	}
	switch p.Kind {
	case "unknown":
		return 0, ErrPrice
	case "free":
		if p.Amount != 0 || p.Currency != "USD" || p.Source == "" || p.ValidUntil <= now.Unix() {
			return 0, ErrPrice
		}
		return 0, nil
	case "fixed":
		if p.Amount <= 0 || p.Amount > 9000000000000 || p.Currency != "USD" || p.Source == "" || p.ValidUntil <= now.Unix() {
			return 0, ErrPrice
		}
		return (p.Amount*p.Multiplier + 9999) / 10000, nil
	default:
		return 0, ErrInvalid
	}
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}
type Server struct {
	ID       string    `json:"id"`
	Owner    Principal `json:"owner"`
	Endpoint string    `json:"endpoint"`
	Auth     string    `json:"auth"`
	Header   string    `json:"header,omitempty"`
	ClientID string    `json:"client_id,omitempty"`
	Issuer   string    `json:"issuer,omitempty"`
	Scopes   string    `json:"scopes,omitempty"`
	Tools    []Tool    `json:"tools"`
	Revision int64     `json:"revision"`
}
type Release struct {
	ID        string           `json:"id"`
	Server    Server           `json:"server"`
	Version   string           `json:"version"`
	Prices    map[string]Price `json:"prices"`
	Published bool             `json:"published"`
}
type Installation struct {
	ID         string            `json:"id"`
	Owner      Principal         `json:"owner"`
	Release    string            `json:"release"`
	Server     string            `json:"server"`
	Enabled    bool              `json:"enabled"`
	Generation int64             `json:"generation"`
	Loaded     map[string]string `json:"loaded"`
}
type Secret struct {
	Refreshing bool      `json:"refreshing,omitempty"`
	Access     string    `json:"access"`
	Refresh    string    `json:"refresh,omitempty"`
	Expires    int64     `json:"expires,omitempty"`
	Meta       OAuthMeta `json:"meta"`
}
type OAuthState struct {
	Owner      Principal `json:"owner"`
	Install    string    `json:"install"`
	Generation int64     `json:"generation"`
	Verifier   string    `json:"verifier"`
	Binding    string    `json:"binding"`
	Expires    int64     `json:"expires"`
	Meta       OAuthMeta `json:"meta"`
}
type Execution struct {
	Owner       Principal       `json:"owner"`
	Fingerprint string          `json:"fingerprint"`
	State       string          `json:"state"`
	Charge      Charge          `json:"charge"`
	Result      json.RawMessage `json:"result,omitempty"`
}
type Audit struct {
	At      int64     `json:"at"`
	Owner   Principal `json:"owner"`
	Action  string    `json:"action"`
	Target  string    `json:"target"`
	Outcome string    `json:"outcome"`
}
type database struct {
	Version    int                     `json:"version"`
	Servers    map[string]Server       `json:"servers"`
	Releases   map[string]Release      `json:"releases"`
	Installs   map[string]Installation `json:"installs"`
	Secrets    map[string]Secret       `json:"secrets"`
	States     map[string]OAuthState   `json:"states"`
	Executions map[string]Execution    `json:"executions"`
	Audit      []Audit                 `json:"audit"`
}

func emptyDB() database {
	return database{1, map[string]Server{}, map[string]Release{}, map[string]Installation{}, map[string]Secret{}, map[string]OAuthState{}, map[string]Execution{}, []Audit{}}
}
func (d *database) audit(p Principal, action, target, outcome string) {
	d.Audit = append(d.Audit, Audit{time.Now().Unix(), p, action, target, outcome})
}
func randomID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func invalid(s string) error { return fmt.Errorf("%w: %s", ErrInvalid, s) }
