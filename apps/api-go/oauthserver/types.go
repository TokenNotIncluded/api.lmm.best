// Package oauthserver implements an unmounted, deliberately narrow OAuth
// authorization-server core. Browser authentication, CSRF and HTTP routing are
// integration responsibilities; see README.md before exposing any method.
package oauthserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"gorm.io/gorm"
)

const (
	AuthorizationTTL = 5 * time.Minute
	CodeTTL          = 120 * time.Second
	AccessTTL        = 10 * time.Minute

	DefaultRefreshAbsoluteTTL = 30 * 24 * time.Hour
	DefaultRefreshIdleTTL     = 7 * 24 * time.Hour
)

// NativeClient is installed through trusted server configuration, not dynamic
// registration. RedirectURIs are portless http://127.0.0.1/<registered-path>
// templates. Only the port may vary in an authorization request.
type NativeClient struct {
	ID           string
	Name         string
	RedirectURIs []string
	Resources    []string
	Scopes       []string
}

type Config struct {
	// Issuer is a fixed HTTPS origin, never derived from Host/Forwarded headers.
	Issuer             string
	Clients            []NativeClient
	RefreshAbsoluteTTL time.Duration
	RefreshIdleTTL     time.Duration
}

// SenderBinding is a future extension point for VERIFIED sender constraints,
// not a claim accepted from an HTTP client. Phase one rejects nonempty values;
// no DPoP or mTLS enforcement is implemented or advertised.
type SenderBinding struct {
	Method     string
	Thumbprint string
}

// Grant is passed by value with a fresh Scopes slice. Policy must check current
// user status and permissions. It cannot add scopes or change the subject.
type Grant struct {
	FamilyID string
	UserID   int64
	ClientID string
	Resource string
	Scopes   []string
	Binding  SenderBinding
}

// Policy is mandatory and fail-closed. Calls occur at identity binding, consent,
// code exchange, refresh and EVERY access validation. Implementations must be
// concurrency-safe. All database reads MUST use the supplied transaction, not
// a root pool, replica, cache or nested transaction. The handle is valid only
// for this synchronous call; never retain it, commit it or mutate OAuth tables.
// This avoids a second connection checkout while the caller holds OAuth locks
// and keeps the permission check inside the issuance transaction. Access-only
// validation supplies a repeatable snapshot instead. Policies may lock their
// own permission facts; database changes must follow the same row-lock order.
type Policy interface {
	Authorize(context.Context, *gorm.DB, Grant) error
}

type PendingAuthorization struct {
	Transaction string
	ClientID    string
	ClientName  string
	RedirectURI string
	Resource    string
	Scopes      []string
	ExpiresAt   time.Time
}

type Consent struct {
	PendingAuthorization
	// Secret is returned once, only to the trusted browser-session adapter.
	Secret string
	UserID int64
}

// AuthorizationResponse contains a previously validated redirect URI with
// query-encoded code/state/iss (or access_denied/state/iss), never bearer tokens.
type AuthorizationResponse struct {
	RedirectURI string
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type AccessRequest struct {
	Token          string
	Resource       string
	RequiredScopes []string
	Binding        SenderBinding
}

// ProtocolError contains only safe public fields. Causes are kept for trusted
// diagnostics, never serialized as HTTP responses or appended to redirects.
type ProtocolError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
	Status      int    `json:"-"`
	// RedirectURI is set only for authorization errors AFTER validating the
	// registered client and redirect. Other errors must remain local.
	RedirectURI string `json:"-"`
	cause       error
}

func (e *ProtocolError) Error() string { return e.Code + ": " + e.Description }
func (e *ProtocolError) Unwrap() error { return e.cause }

func protocolError(code string) *ProtocolError {
	status := http.StatusBadRequest
	description := "The request is invalid."
	switch code {
	case "invalid_client":
		description = "The client is not registered."
	case "invalid_grant":
		description = "The grant is invalid, expired, consumed or revoked."
	case "invalid_scope":
		description = "The requested scope is not permitted."
	case "invalid_target":
		description = "The requested resource is not permitted."
	case "unsupported_grant_type":
		description = "Only authorization_code and refresh_token are supported."
	case "unsupported_response_type":
		description = "Only the code response type is supported."
	case "access_denied":
		status, description = http.StatusForbidden, "Authorization was denied."
	case "invalid_token":
		status, description = http.StatusUnauthorized, "The access token is invalid."
	case "server_error":
		status, description = http.StatusServiceUnavailable, "The authorization service is unavailable."
	}
	return &ProtocolError{Code: code, Description: description, Status: status}
}

func storageError(err error) error {
	if err == nil {
		return nil
	}
	var protocol *ProtocolError
	if errors.As(err, &protocol) {
		return err
	}
	protocol = protocolError("server_error")
	protocol.cause = err
	return protocol
}
