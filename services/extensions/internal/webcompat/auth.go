// Package webcompat preserves public Web contracts without treating an extension
// service credential or a caller-supplied user ID as interactive user authority.
package webcompat

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Core must validate the live user credential against Rust on every request.
type Core interface {
	Authorize(context.Context, string) (*pb.AuthorizeResponse, error)
}

type Authenticator struct {
	core      Core
	hostToken []byte
}

// NewAuthenticator snapshots the separate host credential so a later mutation
// of caller-owned configuration cannot turn it into an accepted user token.
func NewAuthenticator(core Core, hostToken []byte) (*Authenticator, error) {
	if store.Missing(core) || len(hostToken) < 32 || len(hostToken) > 4096 {
		return nil, store.ErrInvalid
	}
	return &Authenticator{core: core, hostToken: append([]byte(nil), hostToken...)}, nil
}

// Verify accepts one standard Bearer header containing an opaque core session.
// Cookies, JWT claims, query parameters and identity/account headers are never
// read as credentials or as authority. Core remains the source of the actor.
func (a *Authenticator) Verify(ctx context.Context, r *http.Request) (*pb.AuthorizeResponse, error) {
	if a == nil || store.Missing(a.core) {
		return nil, store.ErrUnavailable
	}
	if ctx == nil || r == nil {
		return nil, store.ErrUnauthorized
	}
	var values []string
	for name, entries := range r.Header {
		if strings.EqualFold(name, "Authorization") {
			values = append(values, entries...)
		}
	}
	if len(values) != 1 {
		return nil, store.ErrUnauthorized
	}
	token, ok := strings.CutPrefix(values[0], "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(token), a.hostToken) == 1 || !sessionToken(token) {
		return nil, store.ErrUnauthorized
	}
	if ctx.Err() != nil {
		return nil, store.ErrUnavailable
	}
	self, err := a.core.Authorize(ctx, token)
	if err != nil {
		return nil, authError(err)
	}
	if self == nil || self.GetUserId() <= 0 || self.GetPlatformLevel() < 0 || self.GetPlatformLevel() > 6 {
		return nil, store.ErrUnavailable
	}
	if self.GetCredentialKind() != pb.CredentialKind_CREDENTIAL_KIND_SESSION ||
		self.GetOwner().GetKind() != pb.AccountKind_ACCOUNT_KIND_PERSONAL ||
		self.GetOwner().GetId() != self.GetUserId() {
		return nil, store.ErrForbidden
	}
	return self, nil
}

func sessionToken(token string) bool {
	if len(token) < 32 || len(token) > 256 || !strings.HasPrefix(token, "lmms_") {
		return false
	}
	for _, c := range token {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func authError(err error) error {
	switch status.Code(err) {
	case codes.Unauthenticated:
		return store.ErrUnauthorized
	case codes.PermissionDenied:
		return store.ErrForbidden
	case codes.InvalidArgument:
		return store.ErrInvalid
	default:
		return store.ErrUnavailable
	}
}
