package model

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/account"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNativeAccountDenied         = errors.New("account operation is not permitted")
	ErrNativeAccountInput          = errors.New("invalid account operation")
	ErrNativeAccountExists         = errors.New("a team is already owned by this user")
	ErrNativeInvitationUnavailable = errors.New("invitation is unavailable")
)

// NativeAccountActor is copied from the verified browser session, never JSON.
// Both the user and session are rechecked under the mutation transaction.
type NativeAccountActor struct {
	UserID         int
	AuthVersion    int64
	SessionID      string
	SessionVersion int64
}

type NativeAccountSummary struct {
	Account       account.Ref      `json:"account"`
	DisplayName   string           `json:"display_name"`
	Enabled       bool             `json:"enabled"`
	TeamRole      account.TeamRole `json:"team_role,omitempty"`
	CanManageTeam bool             `json:"can_manage_team"`
	// This slice does not issue team keys or authorize financial operations.
	TeamBillingAvailable bool `json:"team_billing_available"`
}

type NativeAccountList struct {
	Personal   NativeAccountSummary   `json:"personal"`
	Teams      []NativeAccountSummary `json:"teams"`
	NextCursor int64                  `json:"next_cursor,omitempty"`
}

type NativeMemberList struct {
	OwnerUserID int                   `json:"owner_user_id"`
	Members     []NativeAccountMember `json:"members"`
	NextCursor  int                   `json:"next_cursor,omitempty"`
}

// NativeAccountStore uses the existing application database. It owns only
// account identity/membership, not another balance or subscription subsystem.
type NativeAccountStore struct{ db *gorm.DB }

func NewNativeAccountStore(db *gorm.DB) (*NativeAccountStore, error) {
	if db == nil {
		return nil, gorm.ErrInvalidDB
	}
	return &NativeAccountStore{db: db}, nil
}

func nativeRequestKeyValid(key string) bool {
	if len(key) < 16 || len(key) > 64 {
		return false
	}
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func nativeNameValid(name string) bool {
	if name == "" || name != strings.TrimSpace(name) || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

func nativeActorAllowed(user User) bool {
	// Do not mistake a natural L0 for an explicit restriction. An explicit
	// invalid/L0 override remains denied until its scope is migrated reliably.
	return user.Status == common.UserStatusEnabled && (user.TrustLevelOverride == nil ||
		*user.TrustLevelOverride >= TrustLevelMinUser+1 && *user.TrustLevelOverride <= TrustLevelMaxUser)
}

func nativeUsersAndSession(tx *gorm.DB, actor NativeAccountActor, otherIDs ...int) (map[int]User, error) {
	if actor.UserID <= 0 || actor.AuthVersion <= 0 || actor.SessionID == "" || actor.SessionVersion <= 0 {
		return nil, ErrNativeAccountDenied
	}
	ids := append([]int{actor.UserID}, otherIDs...)
	sort.Ints(ids)
	users := make(map[int]User, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrNativeAccountDenied
		}
		if _, ok := users[id]; ok {
			continue
		}
		var user User
		if err := lockForUpdate(tx).Select("id", "status", "auth_version", "trust_level_override", "username", "display_name").Where("id = ?", id).Take(&user).Error; err != nil {
			return nil, nativeLookupError(err)
		}
		users[id] = user
	}
	user := users[actor.UserID]
	if !nativeActorAllowed(user) || user.AuthVersion != actor.AuthVersion {
		return nil, ErrNativeAccountDenied
	}
	var session UserSession
	if err := lockForUpdate(tx).Select("sid", "user_id", "version", "user_auth_version", "status", "expires_at", "revoked_at").Where("sid = ?", actor.SessionID).Take(&session).Error; err != nil {
		return nil, nativeLookupError(err)
	}
	if session.UserID != actor.UserID || session.Version != actor.SessionVersion || session.UserAuthVersion != actor.AuthVersion || session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= time.Now().Unix() {
		return nil, ErrNativeAccountDenied
	}
	return users, nil
}

func nativeLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNativeAccountDenied
	}
	return err
}

func nativeMemberScope(tx *gorm.DB, a NativeAccountRecord, user User) (account.Scope, int64, error) {
	role, version := account.Owner, a.OwnershipVersion
	if version <= 0 {
		return account.Scope{}, 0, ErrNativeAccountDenied
	}
	if a.OwnerUserID != user.Id {
		var m NativeAccountMember
		if err := tx.Where("account_kind = ? AND account_id = ? AND user_id = ?", a.Kind, a.ID, user.Id).Take(&m).Error; err != nil {
			return account.Scope{}, 0, nativeLookupError(err)
		}
		if !m.Active || m.Version <= 0 {
			return account.Scope{}, 0, ErrNativeAccountDenied
		}
		role, version = m.Role, m.Version
	}
	membership := account.Membership{Account: a.Ref(), UserID: int64(user.Id), Role: role, Active: true, SpendGranted: false}
	scope, err := account.ResolveScope(account.Actor{UserID: int64(user.Id), Enabled: user.Status == common.UserStatusEnabled, Restricted: !nativeActorAllowed(user)}, account.State{Account: a.Ref(), Enabled: a.Enabled, DeveloperAccess: false}, &membership)
	return scope, version, err
}

// All team mutations acquire account -> sorted user rows -> actor session ->
// membership/invitation. A removed or demoted member cannot race the team lock.
// Account creation locks only its user/session; it never updates an existing team.
func (s *NativeAccountStore) teamTx(ctx context.Context, actor NativeAccountActor, teamID int64, others []int, fn func(*gorm.DB, NativeAccountRecord, map[int]User) error) error {
	if teamID <= 0 {
		return ErrNativeAccountInput
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var a NativeAccountRecord
		if err := lockForUpdate(tx).Where("kind = ? AND id = ?", account.Team, teamID).Take(&a).Error; err != nil {
			return nativeLookupError(err)
		}
		if !a.Enabled {
			return ErrNativeAccountDenied
		}
		users, err := nativeUsersAndSession(tx, actor, others...)
		if err != nil {
			return err
		}
		return fn(tx, a, users)
	})
}

func nativeAccountAudit(tx *gorm.DB, a NativeAccountRecord, actorID, targetID int, action string, role account.TeamRole) error {
	return tx.Create(&NativeAccountEvent{AccountKind: a.Kind, AccountID: a.ID, ActorUserID: actorID, TargetUserID: targetID, Action: action, Role: role}).Error
}

func nativeAccountSummary(a NativeAccountRecord, scope account.Scope) NativeAccountSummary {
	return NativeAccountSummary{Account: a.Ref(), DisplayName: a.DisplayName, Enabled: a.Enabled, TeamRole: scope.TeamRole(), CanManageTeam: scope.CanManageTeam()}
}

func (s *NativeAccountStore) CreateTeam(ctx context.Context, actor NativeAccountActor, name, requestKey string) (*NativeAccountSummary, error) {
	if !nativeNameValid(name) || !nativeRequestKeyValid(requestKey) {
		return nil, ErrNativeAccountInput
	}
	var result NativeAccountSummary
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		users, err := nativeUsersAndSession(tx, actor)
		if err != nil {
			return err
		}
		var a NativeAccountRecord
		err = tx.Where("kind = ? AND owner_user_id = ?", account.Team, actor.UserID).Take(&a).Error
		if err == nil {
			if a.CreationKey != requestKey {
				return ErrNativeAccountExists
			}
			if a.DisplayName != name {
				return ErrNativeAccountInput
			}
			if !a.Enabled {
				return ErrNativeAccountDenied
			}
			scope, _, scopeErr := nativeMemberScope(tx, a, users[actor.UserID])
			if scopeErr != nil {
				return scopeErr
			}
			result = nativeAccountSummary(a, scope)
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// Register the personal identity on this explicit write, not on GET or
		// every boot. No balances, roles, rewards or activation facts are copied.
		user := users[actor.UserID]
		personal := NativeAccountRecord{Kind: account.Personal, ID: int64(actor.UserID), OwnerUserID: actor.UserID, DisplayName: user.DisplayName, Enabled: true}
		if personal.DisplayName == "" {
			personal.DisplayName = user.Username
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&personal).Error; err != nil {
			return err
		}
		// Independent IDs avoid rewinding a shared SQL sequence when historical
		// personal user IDs are registered. They fit JavaScript's exact integers.
		id, err := rand.Int(rand.Reader, big.NewInt(1<<52))
		if err != nil {
			return err
		}
		a = NativeAccountRecord{Kind: account.Team, ID: id.Int64() + 1, OwnerUserID: actor.UserID, DisplayName: name, Enabled: true, CreationKey: requestKey}
		if err := tx.Create(&a).Error; err != nil {
			return err
		}
		if err := nativeAccountAudit(tx, a, actor.UserID, actor.UserID, "team.create", account.Owner); err != nil {
			return err
		}
		scope, _, err := nativeMemberScope(tx, a, user)
		if err != nil {
			return err
		}
		result = nativeAccountSummary(a, scope)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *NativeAccountStore) ListAccounts(ctx context.Context, actor NativeAccountActor, after int64) (*NativeAccountList, error) {
	if after < 0 {
		return nil, ErrNativeAccountInput
	}
	result := NativeAccountList{Teams: []NativeAccountSummary{}}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		users, err := nativeUsersAndSession(tx, actor)
		if err != nil {
			return err
		}
		u := users[actor.UserID]
		name := u.DisplayName
		if name == "" {
			name = u.Username
		}
		result.Personal = NativeAccountSummary{Account: account.Ref{Kind: account.Personal, ID: int64(u.Id)}, DisplayName: name, Enabled: true}
		var rows []NativeAccountRecord
		if err := tx.Where("kind = ? AND id > ? AND (owner_user_id = ? OR EXISTS (SELECT 1 FROM account_members m WHERE m.account_kind = accounts.kind AND m.account_id = accounts.id AND m.user_id = ? AND m.active = ?))", account.Team, after, actor.UserID, actor.UserID, true).Order("id ASC").Limit(51).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) > 50 {
			rows = rows[:50]
			result.NextCursor = rows[49].ID
		}
		for _, a := range rows {
			// Freeze does not hide a user's account from its account switcher.
			// It does remove all actions; no money is authorized by this catalog.
			if !a.Enabled {
				result.Teams = append(result.Teams, NativeAccountSummary{Account: a.Ref(), DisplayName: a.DisplayName})
				continue
			}
			scope, _, err := nativeMemberScope(tx, a, u)
			if err != nil {
				return err
			}
			result.Teams = append(result.Teams, nativeAccountSummary(a, scope))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *NativeAccountStore) GetTeam(ctx context.Context, actor NativeAccountActor, teamID int64) (*NativeAccountSummary, error) {
	var result NativeAccountSummary
	err := s.teamTx(ctx, actor, teamID, nil, func(tx *gorm.DB, a NativeAccountRecord, users map[int]User) error {
		scope, _, err := nativeMemberScope(tx, a, users[actor.UserID])
		if err != nil {
			return err
		}
		result = nativeAccountSummary(a, scope)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *NativeAccountStore) ListMembers(ctx context.Context, actor NativeAccountActor, teamID int64, after int) (*NativeMemberList, error) {
	if after < 0 {
		return nil, ErrNativeAccountInput
	}
	result := NativeMemberList{Members: []NativeAccountMember{}}
	err := s.teamTx(ctx, actor, teamID, nil, func(tx *gorm.DB, a NativeAccountRecord, users map[int]User) error {
		if _, _, err := nativeMemberScope(tx, a, users[actor.UserID]); err != nil {
			return err
		}
		result.OwnerUserID = a.OwnerUserID
		if err := tx.Where("account_kind = ? AND account_id = ? AND active = ? AND user_id > ?", account.Team, teamID, true, after).Order("user_id ASC").Limit(51).Find(&result.Members).Error; err != nil {
			return err
		}
		if len(result.Members) > 50 {
			result.Members = result.Members[:50]
			result.NextCursor = result.Members[49].UserID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *NativeAccountStore) Invite(ctx context.Context, actor NativeAccountActor, teamID int64, recipient int, role account.TeamRole, requestKey string) (*NativeAccountInvitation, error) {
	if recipient <= 0 || recipient == actor.UserID || !nativeRequestKeyValid(requestKey) {
		return nil, ErrNativeAccountInput
	}
	var result NativeAccountInvitation
	err := s.teamTx(ctx, actor, teamID, []int{recipient}, func(tx *gorm.DB, a NativeAccountRecord, users map[int]User) error {
		scope, version, err := nativeMemberScope(tx, a, users[actor.UserID])
		if err != nil {
			return err
		}
		if !scope.CanInviteRole(role) || recipient == a.OwnerUserID || !nativeActorAllowed(users[recipient]) {
			return ErrNativeAccountDenied
		}
		var existing NativeAccountInvitation
		err = tx.Where("account_kind = ? AND account_id = ? AND inviter_user_id = ? AND request_key = ?", account.Team, teamID, actor.UserID, requestKey).Take(&existing).Error
		if err == nil {
			if existing.RecipientUserID != recipient || existing.Role != role {
				return ErrNativeAccountInput
			}
			result = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var members int64
		if err := tx.Model(&NativeAccountMember{}).Where("account_kind = ? AND account_id = ? AND user_id = ? AND active = ?", account.Team, teamID, recipient, true).Count(&members).Error; err != nil {
			return err
		}
		if members != 0 {
			return ErrNativeAccountInput
		}
		var pending int64
		if err := tx.Model(&NativeAccountInvitation{}).Where("account_kind = ? AND account_id = ? AND status = ? AND expires_at > ?", account.Team, teamID, "pending", time.Now().Unix()).Count(&pending).Error; err != nil {
			return err
		}
		if pending >= 100 {
			return ErrNativeAccountInput
		}
		var secret [16]byte
		if _, err := rand.Read(secret[:]); err != nil {
			return err
		}
		result = NativeAccountInvitation{ID: hex.EncodeToString(secret[:]), AccountKind: account.Team, AccountID: teamID, InviterUserID: actor.UserID, RecipientUserID: recipient, Role: role, RequestKey: requestKey, InviterRole: scope.TeamRole(), InviterMemberVersion: version, InviterAuthVersion: actor.AuthVersion, Status: "pending", ExpiresAt: time.Now().Add(7 * 24 * time.Hour).Unix()}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		return nativeAccountAudit(tx, a, actor.UserID, recipient, "member.invite", role)
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// NativeAccountInvitationView adds only the team name needed before joining.
// Sensitive invitation fields keep their existing json:"-" tags.
type NativeAccountInvitationView struct {
	NativeAccountInvitation
	TeamDisplayName string `json:"team_display_name"`
}

func nativeInvitationViews(query *gorm.DB) ([]NativeAccountInvitationView, error) {
	rows := []NativeAccountInvitationView{}
	err := query.Model(&NativeAccountInvitation{}).
		Select("account_invitations.*, accounts.display_name AS team_display_name").
		Joins("JOIN accounts ON accounts.kind = account_invitations.account_kind AND accounts.id = account_invitations.account_id").
		Order("account_invitations.id ASC").Limit(50).Find(&rows).Error
	return rows, err
}

func (s *NativeAccountStore) ListInvitations(ctx context.Context, actor NativeAccountActor, after string) ([]NativeAccountInvitationView, error) {
	if after != "" && !nativeInvitationIDValid(after) {
		return nil, ErrNativeAccountInput
	}
	var result []NativeAccountInvitationView
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := nativeUsersAndSession(tx, actor); err != nil {
			return err
		}
		var err error
		result, err = nativeInvitationViews(tx.Where("recipient_user_id = ? AND status = ? AND expires_at > ? AND account_invitations.id > ? AND accounts.enabled = ?", actor.UserID, "pending", time.Now().Unix(), after, true))
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ListSentInvitations lets an owner manage all offers, while an administrator
// sees only their own member offers. Ordinary members cannot inspect this feed.
func (s *NativeAccountStore) ListSentInvitations(ctx context.Context, actor NativeAccountActor, teamID int64, after string) ([]NativeAccountInvitationView, error) {
	if after != "" && !nativeInvitationIDValid(after) {
		return nil, ErrNativeAccountInput
	}
	var result []NativeAccountInvitationView
	err := s.teamTx(ctx, actor, teamID, nil, func(tx *gorm.DB, a NativeAccountRecord, users map[int]User) error {
		scope, _, err := nativeMemberScope(tx, a, users[actor.UserID])
		if err != nil {
			return err
		}
		if !scope.CanManageTeam() {
			return ErrNativeAccountDenied
		}
		query := tx.Where("account_kind = ? AND account_id = ? AND account_invitations.id > ?", a.Kind, a.ID, after)
		if scope.TeamRole() != account.Owner {
			query = query.Where("inviter_user_id = ? AND role = ?", actor.UserID, account.Member)
		}
		result, err = nativeInvitationViews(query)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func nativeInvitationIDValid(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// recipientInvitationRoute reads only routing metadata. Every mutation reads
// the invitation again after the team and session locks, never trusting this as
// an authorization snapshot.
func (s *NativeAccountStore) recipientInvitationRoute(ctx context.Context, actor NativeAccountActor, id string) (NativeAccountInvitation, error) {
	var route NativeAccountInvitation
	if !nativeInvitationIDValid(id) {
		return route, ErrNativeAccountInput
	}
	err := s.db.WithContext(ctx).Select("id", "account_id", "inviter_user_id").
		Where("id = ? AND recipient_user_id = ? AND account_kind = ?", id, actor.UserID, account.Team).Take(&route).Error
	return route, nativeLookupError(err)
}

func (s *NativeAccountStore) AcceptInvitation(ctx context.Context, actor NativeAccountActor, id string) (*NativeAccountSummary, error) {
	route, err := s.recipientInvitationRoute(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	var result NativeAccountSummary
	err = s.teamTx(ctx, actor, route.AccountID, []int{route.InviterUserID}, func(tx *gorm.DB, a NativeAccountRecord, users map[int]User) error {
		var invite NativeAccountInvitation
		if err := lockForUpdate(tx).Where("id = ? AND account_kind = ? AND account_id = ? AND recipient_user_id = ?", id, account.Team, a.ID, actor.UserID).Take(&invite).Error; err != nil {
			return nativeLookupError(err)
		}
		if invite.InviterUserID != route.InviterUserID || actor.UserID == a.OwnerUserID {
			return ErrNativeInvitationUnavailable
		}
		if invite.Status == "accepted" {
			scope, _, err := nativeMemberScope(tx, a, users[actor.UserID])
			if err != nil {
				return ErrNativeInvitationUnavailable
			}
			result = nativeAccountSummary(a, scope)
			return nil
		}
		if invite.Status != "pending" || invite.ExpiresAt <= time.Now().Unix() {
			return ErrNativeInvitationUnavailable
		}
		issuer, version, err := nativeMemberScope(tx, a, users[invite.InviterUserID])
		if err != nil {
			return err
		}
		if users[invite.InviterUserID].AuthVersion != invite.InviterAuthVersion || issuer.TeamRole() != invite.InviterRole || version != invite.InviterMemberVersion || !issuer.CanInviteRole(invite.Role) {
			return ErrNativeInvitationUnavailable
		}
		var member NativeAccountMember
		err = tx.Where("account_kind = ? AND account_id = ? AND user_id = ?", account.Team, a.ID, actor.UserID).Take(&member).Error
		if err == nil {
			if member.Active {
				return ErrNativeInvitationUnavailable
			}
			if err := tx.Model(&member).Updates(map[string]interface{}{"active": true, "role": invite.Role, "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			member = NativeAccountMember{AccountKind: account.Team, AccountID: a.ID, UserID: actor.UserID, Role: invite.Role, Active: true, Version: 1}
			if err := tx.Create(&member).Error; err != nil {
				return err
			}
		} else {
			return err
		}
		if err := tx.Model(&invite).Update("status", "accepted").Error; err != nil {
			return err
		}
		// Other pending offers cannot be reused after this membership changes.
		if err := tx.Model(&NativeAccountInvitation{}).Where("account_kind = ? AND account_id = ? AND recipient_user_id = ? AND status = ?", account.Team, a.ID, actor.UserID, "pending").Update("status", "revoked").Error; err != nil {
			return err
		}
		if err := nativeAccountAudit(tx, a, actor.UserID, actor.UserID, "member.accept", invite.Role); err != nil {
			return err
		}
		scope, _, err := nativeMemberScope(tx, a, users[actor.UserID])
		if err != nil {
			return err
		}
		result = nativeAccountSummary(a, scope)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// SetMemberRole cannot promote the caller or grant ownership. RemoveMember may
// remove oneself, except the canonical owner. No ownership transfer is exposed.
func (s *NativeAccountStore) SetMemberRole(ctx context.Context, actor NativeAccountActor, teamID int64, target int, role account.TeamRole) error {
	return s.changeMember(ctx, actor, teamID, target, role, false)
}
func (s *NativeAccountStore) RemoveMember(ctx context.Context, actor NativeAccountActor, teamID int64, target int) error {
	return s.changeMember(ctx, actor, teamID, target, "", true)
}

func (s *NativeAccountStore) changeMember(ctx context.Context, actor NativeAccountActor, teamID int64, target int, role account.TeamRole, remove bool) error {
	if target <= 0 {
		return ErrNativeAccountInput
	}
	return s.teamTx(ctx, actor, teamID, nil, func(tx *gorm.DB, a NativeAccountRecord, users map[int]User) error {
		scope, _, err := nativeMemberScope(tx, a, users[actor.UserID])
		if err != nil {
			return err
		}
		if target == a.OwnerUserID {
			return ErrNativeAccountDenied
		}
		var member NativeAccountMember
		if err := tx.Where("account_kind = ? AND account_id = ? AND user_id = ? AND active = ?", account.Team, teamID, target, true).Take(&member).Error; err != nil {
			return nativeLookupError(err)
		}
		ref := account.Membership{Account: a.Ref(), UserID: int64(target), Role: member.Role, Active: member.Active}
		if !(remove && target == actor.UserID) && !scope.CanManageMember(ref) {
			return ErrNativeAccountDenied
		}
		if !remove && !scope.CanInviteRole(role) {
			return ErrNativeAccountDenied
		}
		if !remove && member.Role == role {
			return nil
		}
		values := map[string]interface{}{"version": gorm.Expr("version + 1")}
		action := "member.role"
		if remove {
			values["active"] = false
			action = "member.remove"
		} else {
			values["role"] = role
		}
		if err := tx.Model(&member).Updates(values).Error; err != nil {
			return err
		}
		if err := tx.Model(&NativeAccountInvitation{}).Where("account_kind = ? AND account_id = ? AND status = ? AND (inviter_user_id = ? OR recipient_user_id = ?)", account.Team, teamID, "pending", target, target).Update("status", "revoked").Error; err != nil {
			return err
		}
		return nativeAccountAudit(tx, a, actor.UserID, target, action, role)
	})
}

func (s *NativeAccountStore) RevokeInvitation(ctx context.Context, actor NativeAccountActor, teamID int64, id string) error {
	if !nativeInvitationIDValid(id) {
		return ErrNativeAccountInput
	}
	return s.teamTx(ctx, actor, teamID, nil, func(tx *gorm.DB, a NativeAccountRecord, users map[int]User) error {
		scope, _, err := nativeMemberScope(tx, a, users[actor.UserID])
		if err != nil {
			return err
		}
		var invite NativeAccountInvitation
		if err := tx.Where("id = ? AND account_kind = ? AND account_id = ?", id, account.Team, teamID).Take(&invite).Error; err != nil {
			return nativeLookupError(err)
		}
		// Admins may revoke their own member invitations; not another admin's.
		if scope.TeamRole() != account.Owner && !(scope.TeamRole() == account.Admin && invite.InviterUserID == actor.UserID && invite.Role == account.Member) {
			return ErrNativeAccountDenied
		}
		return closeNativeInvitation(tx, a, invite, actor.UserID, "invite.revoke")
	})
}

// DeclineInvitation uses the same revoked state as sender cancellation. The
// atomic audit event distinguishes the recipient action without a new status
// enum or migration. A retry cannot restore membership or append another event.
func (s *NativeAccountStore) DeclineInvitation(ctx context.Context, actor NativeAccountActor, id string) error {
	route, err := s.recipientInvitationRoute(ctx, actor, id)
	if err != nil {
		return err
	}
	return s.teamTx(ctx, actor, route.AccountID, nil, func(tx *gorm.DB, a NativeAccountRecord, _ map[int]User) error {
		var invite NativeAccountInvitation
		if err := lockForUpdate(tx).Where("id = ? AND account_kind = ? AND account_id = ? AND recipient_user_id = ?", id, a.Kind, a.ID, actor.UserID).Take(&invite).Error; err != nil {
			return nativeLookupError(err)
		}
		return closeNativeInvitation(tx, a, invite, actor.UserID, "invite.decline")
	})
}

func closeNativeInvitation(tx *gorm.DB, a NativeAccountRecord, invite NativeAccountInvitation, actorID int, action string) error {
	if invite.Status == "revoked" {
		return nil
	}
	if invite.Status != "pending" {
		return ErrNativeInvitationUnavailable
	}
	if err := tx.Model(&invite).Update("status", "revoked").Error; err != nil {
		return err
	}
	return nativeAccountAudit(tx, a, actorID, invite.RecipientUserID, action, invite.Role)
}
