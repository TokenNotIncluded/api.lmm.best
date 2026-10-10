package testkit

import (
	"context"
	"strings"
	"sync"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func Token(letter string) string { return strings.Repeat(letter, 32) }

type Identity struct {
	mu      sync.Mutex
	users   map[string]*pb.AuthorizeResponse
	teams   map[string]*pb.ListTeamsResponse
	failure error
	Calls   int
}

func NewIdentity() *Identity {
	return &Identity{users: map[string]*pb.AuthorizeResponse{}, teams: map[string]*pb.ListTeamsResponse{}}
}
func (i *Identity) Set(token string, id int64, level int32, teams ...*pb.Team) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.users[token] = &pb.AuthorizeResponse{UserId: id, PlatformLevel: level, CredentialKind: pb.CredentialKind_CREDENTIAL_KIND_SESSION, Owner: &pb.Account{Kind: pb.AccountKind_ACCOUNT_KIND_PERSONAL, Id: id}}
	i.teams[token] = proto.Clone(&pb.ListTeamsResponse{Teams: teams}).(*pb.ListTeamsResponse)
}
func (i *Identity) Revoke(token string) { i.mu.Lock(); defer i.mu.Unlock(); delete(i.users, token) }
func (i *Identity) Fail(err error)      { i.mu.Lock(); defer i.mu.Unlock(); i.failure = err }
func (i *Identity) Authorize(_ context.Context, token string) (*pb.AuthorizeResponse, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.Calls++
	if i.failure != nil {
		return nil, i.failure
	}
	u, ok := i.users[token]
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "revoked")
	}
	return proto.Clone(u).(*pb.AuthorizeResponse), nil
}
func (i *Identity) ListTeams(_ context.Context, token string, after int64) (*pb.ListTeamsResponse, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.failure != nil {
		return nil, i.failure
	}
	if after != 0 {
		return &pb.ListTeamsResponse{}, nil
	}
	v := i.teams[token]
	if v == nil {
		return &pb.ListTeamsResponse{}, nil
	}
	return proto.Clone(v).(*pb.ListTeamsResponse), nil
}
