package coreclient

import (
	"context"
	"time"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RemoteSource is identity, not spending authority. Native and OAuth IDs never
// share a namespace or become general Authorize principals.
type RemoteSource struct {
	NativeCredentialID int64
	GrantID            string
	AccessID           string
}
type RemoteAuthority struct {
	Source         RemoteSource
	UserID         int64
	OwnerAccountID int64
	AuthVersion    int64
	ExpiresAt      int64
}

func (a *RemoteAuthority) Native() bool {
	return a != nil && a.Source.NativeCredentialID > 0 && a.Source.GrantID == "" && a.Source.AccessID == ""
}
func (a *RemoteAuthority) Valid() bool {
	return a != nil && a.UserID > 0 && a.OwnerAccountID > 0 && a.AuthVersion > 0 && a.ExpiresAt > time.Now().Unix() &&
		(a.Native() || (a.Source.NativeCredentialID == 0 && validRemoteID(a.Source.GrantID, "lmm_gr_") && validRemoteID(a.Source.AccessID, "lmm_ac_")))
}
func validRemoteID(id, prefix string) bool {
	return len(id) == len(prefix)+43 && id[:len(prefix)] == prefix && validUserCredential(id)
}

// AuthorizeRemote uses the same bounded RPC and replacement metadata as other
// calls. A malformed reply is a protocol failure, never cached authority.
func (c *Client) AuthorizeRemote(ctx context.Context, credential string) (*RemoteAuthority, error) {
	if !validUserCredential(credential) {
		return nil, status.Error(codes.Unauthenticated, "invalid user credential")
	}
	reply, err := c.rpc.AuthorizeRemote(c.context(ctx, credential), &pb.AuthorizeRemoteRequest{})
	if err != nil {
		return nil, err
	}
	if reply == nil || !reply.RemotePermission {
		return nil, status.Error(codes.PermissionDenied, "remote permission required")
	}
	a := &RemoteAuthority{UserID: reply.UserId, OwnerAccountID: reply.OwnerAccountId, AuthVersion: reply.AuthVersion, ExpiresAt: reply.ExpiresAt}
	switch source := reply.Source.(type) {
	case *pb.AuthorizeRemoteResponse_NativePersonal:
		if source.NativePersonal == nil || source.NativePersonal.NativeKind != pb.CredentialKind_CREDENTIAL_KIND_SESSION {
			return nil, status.Error(codes.PermissionDenied, "native session required")
		}
		a.Source.NativeCredentialID = source.NativePersonal.CredentialId
	case *pb.AuthorizeRemoteResponse_OauthGrant:
		if source.OauthGrant != nil {
			a.Source.GrantID = source.OauthGrant.GrantId
			a.Source.AccessID = source.OauthGrant.AccessId
		}
	default:
		return nil, status.Error(codes.FailedPrecondition, "remote source required")
	}
	if !a.Valid() {
		return nil, status.Error(codes.FailedPrecondition, "invalid remote authority")
	}
	return a, nil
}
