// Package rustauth adapts the existing read-only Rust identity contract.
// It does not invent a money RPC or treat funding candidates as spend approval.
package rustauth

import (
	"context"
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
)

type Core interface {
	Authorize(context.Context, string) (*pb.AuthorizeResponse, error)
	ListTeams(context.Context, string, int64) (*pb.ListTeamsResponse, error)
}
type Adapter struct{ core Core }

func New(core Core) (*Adapter, error) {
	if store.Missing(core) {
		return nil, store.ErrInvalid
	}
	return &Adapter{core}, nil
}
func (a *Adapter) Check(ctx context.Context, token string, account store.Account, p store.Permission) (int64, error) {
	if !account.Valid() || (p != store.Read && p != store.Manage && p != store.Spend) {
		return 0, store.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	self, err := a.core.Authorize(ctx, token)
	if err != nil {
		return 0, translate(err)
	}
	if self == nil || self.GetUserId() <= 0 {
		return 0, store.ErrUnauthorized
	}
	// The current protocol has no commerce scopes on API keys. Reject all such
	// keys until scoped authorization exists, including keys owned by a team admin.
	if self.GetCredentialKind() != pb.CredentialKind_CREDENTIAL_KIND_SESSION {
		return 0, store.ErrForbidden
	}
	if self.GetOwner().GetKind() != pb.AccountKind_ACCOUNT_KIND_PERSONAL || self.GetOwner().GetId() != self.GetUserId() {
		return 0, store.ErrForbidden
	}
	if account.Kind == "personal" {
		if account.ID == self.GetUserId() {
			return self.GetUserId(), nil
		}
		return 0, store.ErrForbidden
	}
	var after int64
	for page := 0; page < 100; page++ {
		teams, err := a.core.ListTeams(ctx, token, after)
		if err != nil {
			return 0, translate(err)
		}
		if teams == nil || len(teams.GetTeams()) > 100 {
			return 0, store.ErrUnavailable
		}
		last := after
		for _, team := range teams.GetTeams() {
			if team == nil || team.GetId() <= last {
				return 0, store.ErrUnavailable
			}
			last = team.GetId()
			if team.GetId() != account.ID {
				continue
			}
			role := team.GetRole()
			known := role == pb.TeamRole_TEAM_ROLE_OWNER || role == pb.TeamRole_TEAM_ROLE_ADMIN || role == pb.TeamRole_TEAM_ROLE_MEMBER
			if !known {
				return 0, store.ErrForbidden
			}
			allowed := p == store.Read || (p == store.Manage && (role == pb.TeamRole_TEAM_ROLE_OWNER || role == pb.TeamRole_TEAM_ROLE_ADMIN)) || (p == store.Spend && team.GetCanSpend())
			if !allowed {
				return 0, store.ErrForbidden
			}
			return self.GetUserId(), nil
		}
		next := teams.GetNextAfterId()
		if next == 0 {
			return 0, store.ErrForbidden
		}
		if next <= after || next != last {
			return 0, store.ErrUnavailable
		}
		after = next
	}
	return 0, store.ErrUnavailable
}
func translate(err error) error {
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
