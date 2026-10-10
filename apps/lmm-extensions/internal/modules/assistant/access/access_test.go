package access_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/coreclient"
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/testkit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestLevelsAndRevocation(t *testing.T) {
	c := testkit.NewIdentity()
	a, _ := access.New(c)
	token := testkit.Token("a")
	for level := int32(0); level <= 6; level++ {
		c.Set(token, 1, level)
		p, e := a.Check(context.Background(), token, access.Account{}, access.Read)
		if e != nil || p.Level != level || p.LevelLabel() == "" {
			t.Fatal(p, e)
		}
	}
	c.Set(token, 1, 7)
	if _, e := a.Check(context.Background(), token, access.Account{}, access.Read); !errors.Is(e, access.ErrUnavailable) {
		t.Fatal(e)
	}
	c.Revoke(token)
	if _, e := a.Check(context.Background(), token, access.Account{}, access.Read); !errors.Is(e, access.ErrUnauthorized) {
		t.Fatal(e)
	}
	if c.Calls != 9 {
		t.Fatal("cached identity", c.Calls)
	}
}
func TestTeamRolesDoNotBecomePlatformAuthority(t *testing.T) {
	c := testkit.NewIdentity()
	a, _ := access.New(c)
	token := testkit.Token("a")
	team := access.Account{Kind: "team", ID: 10}
	for _, role := range []pb.TeamRole{pb.TeamRole_TEAM_ROLE_OWNER, pb.TeamRole_TEAM_ROLE_ADMIN, pb.TeamRole_TEAM_ROLE_MEMBER, pb.TeamRole_TEAM_ROLE_UNSPECIFIED} {
		c.Set(token, 1, 0, &pb.Team{Id: 10, Role: role, CanSpend: true})
		for _, permission := range []access.Permission{access.Read, access.Manage} {
			_, e := a.Check(context.Background(), token, team, permission)
			want := role != pb.TeamRole_TEAM_ROLE_UNSPECIFIED && (permission == access.Read || role != pb.TeamRole_TEAM_ROLE_MEMBER)
			if (e == nil) != want {
				t.Fatal(role, permission, e)
			}
		}
	}
	c.Set(token, 1, 6)
	for _, account := range []access.Account{team, {Kind: "personal", ID: 2}} {
		if _, e := a.Check(context.Background(), token, account, access.Manage); !errors.Is(e, access.ErrForbidden) {
			t.Fatal("L6 bypassed ownership", e)
		}
	}
}

type malformed struct {
	self *pb.AuthorizeResponse
	page *pb.ListTeamsResponse
}

func (c malformed) Authorize(context.Context, string) (*pb.AuthorizeResponse, error) {
	return c.self, nil
}
func (c malformed) ListTeams(context.Context, string, int64) (*pb.ListTeamsResponse, error) {
	return c.page, nil
}
func TestMalformedCoreAndUnscopedKeysFailClosed(t *testing.T) {
	user := &pb.AuthorizeResponse{UserId: 1, PlatformLevel: 6, CredentialKind: pb.CredentialKind_CREDENTIAL_KIND_SESSION, Owner: &pb.Account{Kind: pb.AccountKind_ACCOUNT_KIND_PERSONAL, Id: 1}}
	for _, page := range []*pb.ListTeamsResponse{nil, {NextAfterId: 5}, {Teams: []*pb.Team{{Id: 3}, {Id: 3}}}, {Teams: []*pb.Team{{Id: 3}}, NextAfterId: 4}} {
		a, _ := access.New(malformed{user, page})
		if _, e := a.Check(context.Background(), "token", access.Account{Kind: "team", ID: 10}, access.Read); !errors.Is(e, access.ErrUnavailable) {
			t.Fatal(page, e)
		}
	}
	user.CredentialKind = pb.CredentialKind_CREDENTIAL_KIND_API_KEY
	a, _ := access.New(malformed{self: user})
	if _, e := a.Check(context.Background(), "token", access.Account{}, access.Manage); !errors.Is(e, access.ErrForbidden) {
		t.Fatal("admin API key became a session", e)
	}
}
func TestStrictJSON(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}
	for _, data := range []string{`null`, `[]`, `{"name":"x","name":"y"}`, `{"name":"x","role":"admin"}`, `{"name":"x"} {}`, `{"name":"x","nested":{"a":1,"a":2}}`, strings.Repeat(" ", 16385)} {
		var in input
		if e := access.Decode([]byte(data), &in); !errors.Is(e, access.ErrInvalid) {
			t.Fatal(data, e)
		}
	}
	var in input
	if e := access.Decode([]byte(`{"name":"safe"}`), &in); e != nil || in.Name != "safe" {
		t.Fatal(in, e)
	}
}

type wire struct {
	pb.UnimplementedCoreControlServer
	t *testing.T
}

func (w *wire) Authorize(ctx context.Context, _ *pb.AuthorizeRequest) (*pb.AuthorizeResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if strings.Join(md.Get("authorization"), ",") != "Bearer "+testkit.Token("s") || strings.Join(md.Get("x-lmm-user-credential"), ",") != testkit.Token("u") || len(md.Get("attacker")) != 0 {
		w.t.Error("metadata was not separated", md)
	}
	return &pb.AuthorizeResponse{UserId: 1, PlatformLevel: 2, CredentialKind: pb.CredentialKind_CREDENTIAL_KIND_SESSION, Owner: &pb.Account{Kind: pb.AccountKind_ACCOUNT_KIND_PERSONAL, Id: 1}}, nil
}
func TestActualControlledUnixRPC(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "rpc.sock")
	listener, e := net.Listen("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	server := grpc.NewServer()
	pb.RegisterCoreControlServer(server, &wire{t: t})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	client, e := coreclient.New(socket, []byte(testkit.Token("s")))
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	a, _ := access.New(client)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "wrong", "attacker", "forged"))
	p, e := a.Check(ctx, testkit.Token("u"), access.Account{}, access.Read)
	if e != nil || p.Level != 2 {
		t.Fatal(p, e)
	}
	server.Stop()
	if _, e = a.Check(ctx, testkit.Token("u"), access.Account{}, access.Read); !errors.Is(e, access.ErrUnavailable) {
		t.Fatal(e)
	}
	if access.Translate(status.Error(codes.PermissionDenied, "secret")) != access.ErrForbidden {
		t.Fatal("error not sanitized")
	}
}
