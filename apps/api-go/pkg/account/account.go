// Package account defines account identity independently of login identity,
// platform administrator roles, billing, and public handles.
package account

import "errors"

type Kind string

const (
	Personal Kind = "personal"
	Team     Kind = "team"
)

// Ref is the shared resource-owner identity. Both fields belong in database
// keys, authorization checks, and caches. Personal IDs retain existing user IDs;
// personal:7 and team:7 are different accounts. Handles are never authority.
type Ref struct {
	Kind Kind  `json:"kind"`
	ID   int64 `json:"id"`
}

func (r Ref) Valid() bool {
	return r.ID > 0 && (r.Kind == Personal || r.Kind == Team)
}

type TeamRole string

const (
	Owner  TeamRole = "owner"
	Admin  TeamRole = "admin"
	Member TeamRole = "member"
)

func (r TeamRole) Valid() bool {
	return r == Owner || r == Admin || r == Member
}

// Actor is the authenticated human (or a key's responsible user), never the
// selected payer. Restricted includes platform-wide security restrictions.
// A natural personal L0 is not a restriction. Platform roles are deliberately
// absent: platform administration must not imply team membership or spending.
type Actor struct {
	UserID     int64
	Enabled    bool
	Restricted bool
}

// State is a server-loaded account snapshot. DeveloperAccess is the existing
// authoritative access decision for THIS account, not the actor's level badge.
type State struct {
	Account         Ref
	Enabled         bool
	DeveloperAccess bool
}

// Membership and State must be loaded by the server, not decoded from a client
// claim. Spending grants do not replace budget and balance reservations.
type Membership struct {
	Account      Ref
	UserID       int64
	Role         TeamRole
	Active       bool
	SpendGranted bool
}

var ErrAccessDenied = errors.New("account access denied")

// Scope is a resolved self-service view. It is not a signed or durable grant.
// Its zero value grants nothing. Re-resolve inside the write/reservation
// transaction; do not retain this snapshot across requests or revocations.
type Scope struct {
	actorID         int64
	account         Ref
	role            TeamRole
	developerAccess bool
	spendGranted    bool
}

func ResolveScope(actor Actor, state State, membership *Membership) (Scope, error) {
	if actor.UserID <= 0 || !actor.Enabled || actor.Restricted || !state.Enabled || !state.Account.Valid() {
		return Scope{}, ErrAccessDenied
	}
	scope := Scope{actorID: actor.UserID, account: state.Account, developerAccess: state.DeveloperAccess}
	if state.Account.Kind == Personal {
		if state.Account.ID != actor.UserID || membership != nil {
			return Scope{}, ErrAccessDenied
		}
		scope.spendGranted = true
		return scope, nil
	}
	if membership == nil || !membership.Active || membership.UserID != actor.UserID ||
		membership.Account != state.Account || !membership.Role.Valid() {
		return Scope{}, ErrAccessDenied
	}
	scope.role, scope.spendGranted = membership.Role, membership.SpendGranted
	return scope, nil
}

func (s Scope) Account() Ref          { return s.account }
func (s Scope) ActorID() int64        { return s.actorID }
func (s Scope) TeamRole() TeamRole    { return s.role }
func (s Scope) DeveloperAccess() bool { return s.developerAccess }
func (s Scope) CanSpend() bool        { return s.developerAccess && s.spendGranted }

// CanManageTeam opens the management area; sensitive actions still need their
// own checks. It does not grant all team settings or platform permissions.
func (s Scope) CanManageTeam() bool { return s.role == Owner || s.role == Admin }

// CanManageMember is only the hierarchy check for an existing OTHER member.
// It does not authorize role promotion, ownership transfer, reading prompts,
// or exporting funds. Those operations need their own action checks.
// Self-imposed budgets use a separate path and cannot replace superior limits.
func (s Scope) CanManageMember(target Membership) bool {
	if !s.CanManageTeam() || !target.Active || target.UserID <= 0 || target.UserID == s.actorID ||
		target.Account != s.account || !target.Role.Valid() || target.Role == Owner {
		return false
	}
	return s.role == Owner || target.Role == Member
}
