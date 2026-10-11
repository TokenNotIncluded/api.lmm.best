package humangrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"unicode/utf8"
)

const Version = "HumanGrant Protocol 0.1 Draft"
const BodyLimit = 16 << 10
const ServiceID = "lmm-api"
const ClientID = "lmm-agent"
const ProfileRead = "account.profile.read"
const BrowserPrefix = "/api/user/auth/humangrant"
const retention = int64(30 * 86400)

type Permission struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
}
type CreateRequest struct {
	ClientID      string       `json:"client_id"`
	TargetService string       `json:"target_service"`
	Purpose       string       `json:"purpose"`
	Permissions   []Permission `json:"requested_permissions"`
	ExpectedHuman *string      `json:"expected_human_id,omitempty"`
	SessionTTL    int64        `json:"session_ttl_seconds,omitempty"`
}
type Approval struct {
	Permissions []Permission `json:"permissions"`
	ExpiresAt   int64        `json:"expires_at"`
}
type EmailRequest struct {
	Email          string `json:"email"`
	IdempotencyKey string `json:"idempotency_key"`
}
type Settings struct {
	Enabled bool  `json:"enabled"`
	Epoch   int64 `json:"epoch"`
}
type ProtocolError struct {
	Code    string `json:"error"`
	Message string `json:"message"`
	Retry   int64  `json:"retry_after_seconds"`
	Status  int    `json:"-"`
}

func (e *ProtocolError) Error() string { return e.Code }
func failure(status int, code string) *ProtocolError {
	return &ProtocolError{Code: code, Message: "The request could not be completed.", Status: status}
}
func limited(seconds int64) *ProtocolError {
	e := failure(429, "slow_down")
	e.Retry = seconds
	return e
}

type Human struct {
	ID             string `json:"id"`
	SessionID      string `json:"session_id"`
	SessionVersion int64  `json:"session_version"`
	AuthVersion    int64  `json:"auth_version"`
}
type Account struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Root        bool   `json:"-"`
}

// Tx is a trusted host adapter, NOT a public identity API. All methods execute
// inside one transaction serialized with settings changes and grant revocation.
type Tx interface {
	Get(kind, id string, value any) (bool, error)
	Put(kind, id, owner string, expires int64, value any) error
	Delete(kind, id string) error
	List(kind, owner string, limit int) ([]json.RawMessage, error)
	Authenticate(*http.Request) (Human, Account, bool, error)
	CurrentHuman(Human) (Account, bool, error)
}
type Store interface {
	Transaction(context.Context, func(Tx) error) error
}

type Request struct {
	ID          string
	Link        string
	Actor       string
	PublicKey   string
	Input       CreateRequest
	Epoch       int64
	Created     int64
	Updated     int64
	Expires     int64
	Phase       string
	PollAfter   int64
	LastPoll    int64
	EmailStatus string
	LastEmail   int64
	GrantID     string
}
type Grant struct {
	ID        string       `json:"grant_id"`
	RequestID string       `json:"request_id"`
	Human     Human        `json:"-"`
	Actor     string       `json:"actor_id"`
	PublicKey string       `json:"-"`
	Client    string       `json:"client_id"`
	Service   string       `json:"target_service"`
	Requested []Permission `json:"-"`
	Approved  []Permission `json:"permissions"`
	Created   int64        `json:"created_at"`
	Expires   int64        `json:"expires_at"`
	Revoked   int64        `json:"revoked_at"`
	TokenHash string       `json:"-"`
	Epoch     int64        `json:"-"`
	AuditID   string       `json:"audit_id"`
}

// Persisted grants deliberately use a separate envelope. Public JSON tags must
// never accidentally drop private authorization bindings from durable storage.
type storedGrant struct {
	Grant     Grant        `json:"grant"`
	Human     Human        `json:"human"`
	PublicKey string       `json:"public_key"`
	Requested []Permission `json:"requested"`
	TokenHash string       `json:"token_hash"`
	Epoch     int64        `json:"epoch"`
}

func saveGrant(g Grant) storedGrant {
	return storedGrant{g, g.Human, g.PublicKey, g.Requested, g.TokenHash, g.Epoch}
}
func (s storedGrant) grant() Grant {
	g := s.Grant
	g.Human = s.Human
	g.PublicKey = s.PublicKey
	g.Requested = s.Requested
	g.TokenHash = s.TokenHash
	g.Epoch = s.Epoch
	return g
}

type Flow struct {
	CSRF     string
	Context  string
	Human    Human
	Verified bool
	Expires  int64
}
type State struct {
	ProtocolVersion  string       `json:"protocol_version"`
	RequestID        string       `json:"request_id"`
	Status           string       `json:"status"`
	StatusText       string       `json:"status_text"`
	Phase            string       `json:"phase"`
	Created          int64        `json:"created_at"`
	Updated          int64        `json:"status_updated_at"`
	Checked          int64        `json:"checked_at"`
	Expires          int64        `json:"expires_at"`
	PollAfter        int64        `json:"poll_after_seconds"`
	HumanURL         string       `json:"human_authorization_url"`
	Permissions      []Permission `json:"requested_permissions"`
	EmailStatus      string       `json:"email_request_status"`
	EmailRetry       int64        `json:"email_retry_after_seconds"`
	Service          string       `json:"target_service"`
	Client           string       `json:"client_id"`
	ForwardingPrompt string       `json:"forwarding_prompt"`
	GrantID          *string      `json:"grant_id"`
	GrantStatus      string       `json:"grant_status"`
	GrantExpires     *int64       `json:"grant_expires_at"`
}

type response struct {
	status      int
	value       any
	contentType string
	body        []byte
	cookies     []*http.Cookie
	redirect    string
}

func jsonResponse(status int, value any) response { return response{status: status, value: value} }
func denied(status int, code string) response     { return jsonResponse(status, failure(status, code)) }

// strictJSON rejects unknown fields, duplicate members, invalid UTF-8, null,
// non-object envelopes and trailing JSON instead of accepting ambiguous consent.
func strictJSON(body []byte, dst any) bool {
	if len(bytes.TrimSpace(body)) == 0 || len(body) > BodyLimit || !utf8.Valid(body) || bytes.TrimSpace(body)[0] != '{' {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if scanJSON(d, 0) != nil {
		return false
	}
	if _, err := d.Token(); err != io.EOF {
		return false
	}
	d = json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	return d.Decode(dst) == nil && d.Decode(new(any)) == io.EOF
}
func scanJSON(d *json.Decoder, depth int) error {
	if depth > 16 {
		return fmt.Errorf("JSON too deep")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			key, ok := k.(string)
			if !ok || keys[key] {
				return fmt.Errorf("duplicate member")
			}
			keys[key] = true
			if err := scanJSON(d, depth+1); err != nil {
				return err
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return fmt.Errorf("invalid object")
		}
	case '[':
		for d.More() {
			if err := scanJSON(d, depth+1); err != nil {
				return err
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid array")
		}
	default:
		return fmt.Errorf("invalid JSON")
	}
	return nil
}
func emptyJSON(body []byte) bool { var x struct{}; return strictJSON(body, &x) }
func includes(pairs []Permission, pair Permission) bool {
	for _, p := range pairs {
		if p == pair {
			return true
		}
	}
	return false
}
func validPairs(pairs []Permission) bool {
	if len(pairs) < 1 || len(pairs) > 16 {
		return false
	}
	seen := map[Permission]bool{}
	for _, p := range pairs {
		if p.Action != ProfileRead || !accountID.MatchString(p.Resource) || seen[p] {
			return false
		}
		seen[p] = true
	}
	return true
}
