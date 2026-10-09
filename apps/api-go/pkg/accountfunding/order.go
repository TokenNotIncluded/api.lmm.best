// Package accountfunding validates an API key's ordered payer accounts.
// It does not authenticate callers, reserve money, or select subscriptions.
package accountfunding

import (
	"errors"
	"fmt"

	"github.com/LIghtJUNction/api.lmm.best/pkg/account"
)

// Aliases preserve the payer-order API and JSON while all account-scoped
// features share one identity definition.
type Kind = account.Kind
type Account = account.Ref

const (
	Personal = account.Personal
	Team     = account.Team
)

var (
	ErrInvalidOwner         = errors.New("invalid API key owner")
	ErrInvalidOrder         = errors.New("invalid funding account order")
	ErrAccountNotAuthorized = errors.New("funding account is not authorized")
)

// ResolveOrder validates the entire order before returning a detached copy.
// A nil order means owner-only; an explicit empty order is invalid. Nothing is
// appended automatically, including the personal account or newly joined teams.
//
// owner must come from the stored key, not the active browser account or request.
// teamGrants must come from trusted membership and spending-grant checks for the
// key's responsible user. Even a team-owned key needs its owning team's grant.
// This snapshot is not a reservation: callers must recheck authorization under
// the same transaction that reserves balances and budgets before upstream work.
// The package is an unconnected WIP foundation; existing relay behavior is intact.
func ResolveOrder(owner Account, configured []Account, teamGrants map[int64]bool) ([]Account, error) {
	if !owner.Valid() {
		return nil, ErrInvalidOwner
	}
	if configured == nil {
		configured = []Account{owner}
	}
	if len(configured) == 0 {
		return nil, ErrInvalidOrder
	}
	if owner.Kind == Team && (len(configured) != 1 || configured[0] != owner) {
		return nil, fmt.Errorf("%w: team keys must use only their owning account", ErrInvalidOrder)
	}

	seen := make(map[Account]struct{}, len(configured))
	for i, account := range configured {
		if !account.Valid() {
			return nil, fmt.Errorf("%w: invalid account at position %d", ErrInvalidOrder, i)
		}
		if _, duplicate := seen[account]; duplicate {
			return nil, fmt.Errorf("%w: duplicate account at position %d", ErrInvalidOrder, i)
		}
		seen[account] = struct{}{}
		if (account.Kind == Personal && account != owner) ||
			(account.Kind == Team && !teamGrants[account.ID]) {
			return nil, fmt.Errorf("%w: position %d", ErrAccountNotAuthorized, i)
		}
	}
	return append([]Account(nil), configured...), nil
}
