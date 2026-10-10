package rustauth

import (
	"context"
	"errors"
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

type coreProbe struct {
	self  *pb.AuthorizeResponse
	teams *pb.ListTeamsResponse
	err   error
	calls int
}

func (c *coreProbe) Authorize(context.Context, string) (*pb.AuthorizeResponse, error) {
	c.calls++
	return c.self, c.err
}
func (c *coreProbe) ListTeams(context.Context, string, int64) (*pb.ListTeamsResponse, error) {
	return c.teams, c.err
}
func session() *pb.AuthorizeResponse {
	return &pb.AuthorizeResponse{UserId: 1, PlatformLevel: 6, CredentialKind: pb.CredentialKind_CREDENTIAL_KIND_SESSION, Owner: &pb.Account{Kind: pb.AccountKind_ACCOUNT_KIND_PERSONAL, Id: 1}}
}
func TestNativeSessionAndTeamRoles(t *testing.T) {
	ctx := context.Background()
	c := &coreProbe{self: session()}
	a, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		role       pb.TeamRole
		spend      bool
		permission store.Permission
		allowed    bool
	}{
		{pb.TeamRole_TEAM_ROLE_OWNER, false, store.Manage, true},
		{pb.TeamRole_TEAM_ROLE_ADMIN, false, store.Manage, true},
		{pb.TeamRole_TEAM_ROLE_MEMBER, false, store.Manage, false},
		{pb.TeamRole_TEAM_ROLE_MEMBER, false, store.Read, true},
		{pb.TeamRole_TEAM_ROLE_MEMBER, false, store.Spend, false},
		{pb.TeamRole_TEAM_ROLE_MEMBER, true, store.Spend, true},
		{pb.TeamRole_TEAM_ROLE_UNSPECIFIED, true, store.Spend, false},
	} {
		c.teams = &pb.ListTeamsResponse{Teams: []*pb.Team{{Id: 10, Role: test.role, CanSpend: test.spend}}}
		id, err := a.Check(ctx, "credential", store.Account{Kind: "team", ID: 10}, test.permission)
		if test.allowed && (err != nil || id != 1) {
			t.Fatal(test, id, err)
		}
		if !test.allowed && !errors.Is(err, store.ErrForbidden) {
			t.Fatal(test, err)
		}
	}
	c.teams = &pb.ListTeamsResponse{}
	_, err = a.Check(ctx, "credential", store.Account{Kind: "team", ID: 10}, store.Manage)
	if !errors.Is(err, store.ErrForbidden) {
		t.Fatal(err)
	}
	if c.calls != 8 {
		t.Fatalf("authorization was cached: %d", c.calls)
	}
	_, err = a.Check(ctx, "credential", store.Account{Kind: "personal", ID: 2}, store.Manage)
	if !errors.Is(err, store.ErrForbidden) {
		t.Fatal("platform level bypass", err)
	}
	c.self.CredentialKind = pb.CredentialKind_CREDENTIAL_KIND_API_KEY
	_, err = a.Check(ctx, "credential", store.Account{Kind: "personal", ID: 1}, store.Manage)
	if !errors.Is(err, store.ErrForbidden) {
		t.Fatal("API key escalated", err)
	}
}
func TestCoreFailureAndInvalidCursorFailClosed(t *testing.T) {
	c := &coreProbe{self: session(), err: status.Error(codes.Unavailable, "offline")}
	a, _ := New(c)
	_, err := a.Check(context.Background(), "credential", store.Account{Kind: "personal", ID: 1}, store.Read)
	if !errors.Is(err, store.ErrUnavailable) {
		t.Fatal(err)
	}
	c.err = nil
	c.teams = &pb.ListTeamsResponse{NextAfterId: 99}
	_, err = a.Check(context.Background(), "credential", store.Account{Kind: "team", ID: 10}, store.Read)
	if !errors.Is(err, store.ErrUnavailable) {
		t.Fatal(err)
	}
}
