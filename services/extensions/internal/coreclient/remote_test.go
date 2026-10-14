package coreclient

import (
	"context"
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/grpc/metadata"
	"strings"
	"testing"
	"time"
)

func TestAuthorizeRemoteTypedWireAndRefusals(t *testing.T) {
	valid := func() *pb.AuthorizeRemoteResponse {
		return &pb.AuthorizeRemoteResponse{Source: &pb.AuthorizeRemoteResponse_NativePersonal{NativePersonal: &pb.NativePersonalRemoteSource{CredentialId: 9, NativeKind: pb.CredentialKind_CREDENTIAL_KIND_SESSION}}, UserId: 1, OwnerAccountId: 101, AuthVersion: 1, ExpiresAt: time.Now().Add(time.Hour).Unix(), RemotePermission: true}
	}
	reply := valid()
	client := testClient(t, &testCore{remote: func(ctx context.Context) (*pb.AuthorizeRemoteResponse, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if len(md.Get("authorization")) != 1 || md.Get("authorization")[0] != "Bearer "+testToken || len(md.Get("x-lmm-user-credential")) != 1 || md.Get("x-lmm-user-credential")[0] != testToken || md.Get("x-lmm-protocol")[0] != "1" {
			t.Error("caller metadata survived")
		}
		return reply, nil
	}})
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "forged", "x-lmm-user-credential", "forged"))
	a, err := client.AuthorizeRemote(ctx, testToken)
	if err != nil || a.OwnerAccountID != 101 || a.UserID != 1 || !a.Native() {
		t.Fatalf("native mapping: %v", err)
	}
	for _, change := range []func(*pb.AuthorizeRemoteResponse){
		func(p *pb.AuthorizeRemoteResponse) { p.Source = nil },
		func(p *pb.AuthorizeRemoteResponse) { p.RemotePermission = false },
		func(p *pb.AuthorizeRemoteResponse) { p.UserId = 0 },
		func(p *pb.AuthorizeRemoteResponse) { p.OwnerAccountId = 0 },
		func(p *pb.AuthorizeRemoteResponse) { p.AuthVersion = 0 },
		func(p *pb.AuthorizeRemoteResponse) { p.ExpiresAt = time.Now().Unix() },
		func(p *pb.AuthorizeRemoteResponse) { p.GetNativePersonal().CredentialId = 0 },
		func(p *pb.AuthorizeRemoteResponse) {
			p.GetNativePersonal().NativeKind = pb.CredentialKind_CREDENTIAL_KIND_API_KEY
		},
		func(p *pb.AuthorizeRemoteResponse) {
			p.Source = &pb.AuthorizeRemoteResponse_OauthGrant{OauthGrant: &pb.OAuthGrantRemoteSource{GrantId: "missing", AccessId: "missing"}}
		},
	} {
		reply = valid()
		change(reply)
		if _, err := client.AuthorizeRemote(ctx, testToken); err == nil {
			t.Fatal("malformed authority accepted")
		}
	}
	reply = valid()
	reply.Source = &pb.AuthorizeRemoteResponse_OauthGrant{OauthGrant: &pb.OAuthGrantRemoteSource{GrantId: "lmm_gr_" + strings.Repeat("a", 43), AccessId: "lmm_ac_" + strings.Repeat("b", 43)}}
	a, err = client.AuthorizeRemote(ctx, testToken)
	if err != nil || a.Native() || a.Source.NativeCredentialID != 0 {
		t.Fatalf("OAuth source reclassified: %v", err)
	}
}
