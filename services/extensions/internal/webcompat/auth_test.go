package webcompat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type authCore struct {
	calls  int
	tokens []string
	fn     func(context.Context, string) (*pb.AuthorizeResponse, error)
}

func (c *authCore) Authorize(ctx context.Context, token string) (*pb.AuthorizeResponse, error) {
	c.calls++
	c.tokens = append(c.tokens, token)
	if c.fn != nil {
		return c.fn(ctx, token)
	}
	return sessionSelf(), nil
}

func sessionSelf() *pb.AuthorizeResponse {
	return &pb.AuthorizeResponse{UserId: 17, CredentialKind: pb.CredentialKind_CREDENTIAL_KIND_SESSION,
		Owner: &pb.Account{Kind: pb.AccountKind_ACCOUNT_KIND_PERSONAL, Id: 17}}
}

var webToken = "lmms_" + strings.Repeat("u", 43)
var hostToken = "lmms_" + strings.Repeat("h", 43)

func newAuth(t *testing.T, core Core) *Authenticator {
	t.Helper()
	a, err := NewAuthenticator(core, []byte(hostToken))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAuthenticatorRejectsAmbiguousOrUnsupportedCredentialsBeforeCore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
	}{
		{"missing", nil}, {"empty", []string{""}},
		{"basic", []string{"Basic " + webToken}},
		{"unknown-token-kind", []string{"Bearer lmmt_" + strings.Repeat("u", 43)}},
		{"jwt", []string{"Bearer eyJhbGciOiJIUzI1NiJ9.e30.signature"}},
		{"host-token", []string{"Bearer " + hostToken}},
		{"short", []string{"Bearer lmms_short"}},
		{"oversized", []string{"Bearer lmms_" + strings.Repeat("u", 252)}},
		{"extra-space", []string{"Bearer  " + webToken}},
		{"trailing-space", []string{"Bearer " + webToken + " "}},
		{"leading-space", []string{" Bearer " + webToken}},
		{"tab", []string{"Bearer\t" + webToken}},
		{"comma-joined", []string{"Bearer " + webToken + ", Bearer " + webToken}},
		{"duplicate", []string{"Bearer " + webToken, "Bearer " + webToken}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core := &authCore{}
			r := httptest.NewRequest(http.MethodGet, "/api/store?user_id=17", nil)
			r.Header["Authorization"] = tc.values
			r.Header.Set("X-LMM-User-Credential", webToken)
			r.Header.Set("Cookie", "session="+webToken)
			self, err := newAuth(t, core).Verify(r.Context(), r)
			if self != nil || !errors.Is(err, store.ErrUnauthorized) || core.calls != 0 {
				t.Fatalf("self=%v err=%v core calls=%d", self, err, core.calls)
			}
		})
	}
}

func TestAuthenticatorRejectsDuplicateHeaderNamesRegardlessOfCase(t *testing.T) {
	core := &authCore{}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header["Authorization"] = []string{"Bearer " + webToken}
	r.Header["authorization"] = []string{"Bearer " + webToken}
	_, err := newAuth(t, core).Verify(r.Context(), r)
	if !errors.Is(err, store.ErrUnauthorized) || core.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, core.calls)
	}
}

func TestAuthenticatorUsesOnlyTheLiveCoreSession(t *testing.T) {
	core := &authCore{}
	r := httptest.NewRequest(http.MethodGet, "/api/store?user_id=999&account_id=999", nil)
	r.Header.Set("Authorization", "Bearer "+webToken)
	r.Header.Set("Cookie", "session=not-the-bearer")
	r.Header.Set("X-LMM-User-Credential", hostToken)
	r.Header.Set("X-LMM-User-ID", "999")
	r.Header.Set("X-LMM-Account-ID", "999")
	self, err := newAuth(t, core).Verify(r.Context(), r)
	if err != nil || self.GetUserId() != 17 || core.calls != 1 || core.tokens[0] != webToken {
		t.Fatalf("self=%v err=%v calls=%d tokens=%v", self, err, core.calls, core.tokens)
	}
}

func TestAuthenticatorDoesNotCacheARevokedSession(t *testing.T) {
	core := &authCore{}
	core.fn = func(context.Context, string) (*pb.AuthorizeResponse, error) {
		if core.calls == 1 {
			return sessionSelf(), nil
		}
		return nil, status.Error(codes.Unauthenticated, "revoked synthetic credential")
	}
	a := newAuth(t, core)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+webToken)
	if _, err := a.Verify(r.Context(), r); err != nil {
		t.Fatal(err)
	}
	self, err := a.Verify(r.Context(), r)
	if self != nil || !errors.Is(err, store.ErrUnauthorized) || core.calls != 2 {
		t.Fatalf("self=%v err=%v calls=%d", self, err, core.calls)
	}
}

func TestAuthenticatorRejectsNonInteractiveOrInvalidCoreClaims(t *testing.T) {
	for _, tc := range []struct {
		name string
		self *pb.AuthorizeResponse
		want error
	}{
		{"nil", nil, store.ErrUnavailable},
		{"no-user", &pb.AuthorizeResponse{}, store.ErrUnavailable},
		{"api-key", &pb.AuthorizeResponse{UserId: 17, CredentialKind: pb.CredentialKind_CREDENTIAL_KIND_API_KEY, Owner: sessionSelf().Owner}, store.ErrForbidden},
		{"unknown-kind", &pb.AuthorizeResponse{UserId: 17, CredentialKind: pb.CredentialKind(100), Owner: sessionSelf().Owner}, store.ErrForbidden},
		{"missing-owner", &pb.AuthorizeResponse{UserId: 17, CredentialKind: pb.CredentialKind_CREDENTIAL_KIND_SESSION}, store.ErrForbidden},
		{"team-owner", &pb.AuthorizeResponse{UserId: 17, CredentialKind: pb.CredentialKind_CREDENTIAL_KIND_SESSION, Owner: &pb.Account{Kind: pb.AccountKind_ACCOUNT_KIND_TEAM, Id: 17}}, store.ErrForbidden},
		{"other-user", &pb.AuthorizeResponse{UserId: 17, CredentialKind: pb.CredentialKind_CREDENTIAL_KIND_SESSION, Owner: &pb.Account{Kind: pb.AccountKind_ACCOUNT_KIND_PERSONAL, Id: 999}}, store.ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core := &authCore{fn: func(context.Context, string) (*pb.AuthorizeResponse, error) { return tc.self, nil }}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("Authorization", "Bearer "+webToken)
			self, err := newAuth(t, core).Verify(r.Context(), r)
			if self != nil || !errors.Is(err, tc.want) || core.calls != 1 {
				t.Fatalf("self=%v err=%v calls=%d", self, err, core.calls)
			}
		})
	}
}

func TestAuthenticatorReturnsOnlySafeStoreErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want error
	}{
		{status.Error(codes.Unauthenticated, "synthetic-provider-secret"), store.ErrUnauthorized},
		{status.Error(codes.PermissionDenied, "synthetic-provider-secret"), store.ErrForbidden},
		{status.Error(codes.InvalidArgument, "synthetic-provider-secret"), store.ErrInvalid},
		{status.Error(codes.Unavailable, "synthetic-provider-secret"), store.ErrUnavailable},
		{fmt.Errorf("sql connection synthetic-provider-secret"), store.ErrUnavailable},
	} {
		core := &authCore{fn: func(context.Context, string) (*pb.AuthorizeResponse, error) { return nil, tc.err }}
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer "+webToken)
		self, err := newAuth(t, core).Verify(r.Context(), r)
		if self != nil || !errors.Is(err, tc.want) || strings.Contains(err.Error(), "synthetic-provider-secret") {
			t.Fatalf("self=%v err=%v", self, err)
		}
	}
}

func TestAuthenticatorSnapshotsHostTokenAndRejectsInvalidDependencies(t *testing.T) {
	original := []byte(hostToken)
	a, err := NewAuthenticator(&authCore{}, original)
	if err != nil {
		t.Fatal(err)
	}
	original[5] = 'x'
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+hostToken)
	if _, err := a.Verify(r.Context(), r); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal(err)
	}
	var typedNil *authCore
	for _, core := range []Core{nil, typedNil} {
		if _, err := NewAuthenticator(core, []byte(hostToken)); !errors.Is(err, store.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := NewAuthenticator(&authCore{}, nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestAuthenticatorStopsAtCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	core := &authCore{}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+webToken)
	if _, err := newAuth(t, core).Verify(ctx, r); !errors.Is(err, store.ErrUnavailable) || core.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, core.calls)
	}
}
