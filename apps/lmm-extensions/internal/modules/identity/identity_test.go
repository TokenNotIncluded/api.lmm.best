package identity

import (
	"context"
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeCore struct {
	calls int
	err   error
}

func (f *fakeCore) Capabilities(context.Context) (*pb.CapabilitiesResponse, error) {
	f.calls++
	return &pb.CapabilitiesResponse{ProtocolMajor: 1}, f.err
}
func (f *fakeCore) Authorize(context.Context, string) (*pb.AuthorizeResponse, error) {
	f.calls++
	return &pb.AuthorizeResponse{UserId: 9007199254740993}, f.err
}
func (f *fakeCore) ListTeams(context.Context, string, int64) (*pb.ListTeamsResponse, error) {
	f.calls++
	return &pb.ListTeamsResponse{}, f.err
}
func TestUserCredentialAndCursorAreRequiredBeforeRPC(t *testing.T) {
	f := &fakeCore{}
	handler := New(f).Handler()
	for _, path := range []string{"/self", "/teams"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 401 || f.calls != 0 {
			t.Fatalf("missing user reached core: %d", w.Code)
		}
	}
	for _, path := range []string{"/teams?after_id=-1", "/teams?after_id=1&after_id=2", "/teams?after_id=no"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-LMM-User-Credential", strings.Repeat("a", 32))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 || f.calls != 0 {
			t.Fatalf("invalid cursor reached core: %d", w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/self", nil)
	r.Header.Add("X-LMM-User-Credential", strings.Repeat("a", 32))
	r.Header.Add("X-LMM-User-Credential", strings.Repeat("b", 32))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 || f.calls != 0 {
		t.Fatal("accepted duplicate credential")
	}
}
func TestRPCStatusMappingDoesNotLeakBackendDetails(t *testing.T) {
	for _, tc := range []struct {
		code codes.Code
		http int
	}{
		{codes.Unauthenticated, 401}, {codes.PermissionDenied, 403}, {codes.InvalidArgument, 400},
		{codes.ResourceExhausted, 429}, {codes.Unavailable, 503}, {codes.DeadlineExceeded, 504},
		{codes.Canceled, 408}, {codes.Internal, 502}, {codes.FailedPrecondition, 409},
	} {
		f := &fakeCore{err: status.Error(tc.code, "SECRET database error")}
		r := httptest.NewRequest("GET", "/self", nil)
		r.Header.Set("X-LMM-User-Credential", strings.Repeat("a", 32))
		w := httptest.NewRecorder()
		New(f).Handler().ServeHTTP(w, r)
		if w.Code != tc.http || strings.Contains(w.Body.String(), "SECRET") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unsafe error mapping: %d %s", w.Code, w.Body.String())
		}
	}
}
func TestJSONDoesNotRoundLargeIDs(t *testing.T) {
	f := &fakeCore{}
	r := httptest.NewRequest("GET", "/self", nil)
	r.Header.Set("X-LMM-User-Credential", strings.Repeat("a", 32))
	w := httptest.NewRecorder()
	New(f).Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"9007199254740993"`) {
		t.Fatal(w.Body.String())
	}
}
