package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestRefAndRoleValidation(t *testing.T) {
	for _, r := range []Ref{{Personal, 7}, {Team, 7}} {
		if !r.Valid() {
			t.Fatalf("valid reference rejected: %v", r)
		}
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf(`{"kind":%q,"id":7}`, r.Kind)
		if string(raw) != want {
			t.Fatalf("payer JSON changed: %s", raw)
		}
		var decoded Ref
		if err := json.Unmarshal(raw, &decoded); err != nil || decoded != r {
			t.Fatalf("round trip: %v %v", decoded, err)
		}
	}
	if (Ref{Personal, 7}) == (Ref{Team, 7}) {
		t.Fatal("account kinds collided")
	}
	for _, r := range []Ref{{}, {Personal, 0}, {Team, -1}, {"Team", 7}, {"root", 7}} {
		if r.Valid() {
			t.Fatalf("invalid reference accepted: %v", r)
		}
	}
	for _, r := range []TeamRole{Owner, Admin, Member} {
		if !r.Valid() {
			t.Fatalf("valid role rejected: %s", r)
		}
	}
	for _, r := range []TeamRole{"", "root", "superadmin", "5", "6", "10", "100"} {
		if r.Valid() {
			t.Fatalf("platform role accepted as team role: %s", r)
		}
	}
}

func TestResolveScope(t *testing.T) {
	actor := Actor{UserID: 7, Enabled: true}
	team := State{Account: Ref{Team, 42}, Enabled: true, DeveloperAccess: true}
	member := Membership{Account: team.Account, UserID: actor.UserID, Role: Member, Active: true, SpendGranted: true}
	for _, tc := range []struct {
		name                           string
		actor                          Actor
		state                          State
		membership                     *Membership
		denied, spend, develop, manage bool
		role                           TeamRole
	}{
		{name: "personal active", actor: actor, state: State{Ref{Personal, 7}, true, true}, spend: true, develop: true},
		{name: "personal L0", actor: actor, state: State{Ref{Personal, 7}, true, false}},
		{name: "natural personal L0 can use activated team", actor: actor, state: team, membership: &member, spend: true, develop: true, role: Member},
		{name: "owner of team L0 can configure but not spend", actor: actor, state: State{team.Account, true, false}, membership: &Membership{team.Account, 7, Owner, true, true}, manage: true, role: Owner},
		{name: "team admin", actor: actor, state: team, membership: &Membership{team.Account, 7, Admin, true, true}, spend: true, develop: true, manage: true, role: Admin},
		{name: "owner needs spending grant too", actor: actor, state: team, membership: &Membership{team.Account, 7, Owner, true, false}, develop: true, manage: true, role: Owner},
		{name: "view membership without spending grant", actor: actor, state: team, membership: &Membership{team.Account, 7, Member, true, false}, develop: true, role: Member},
		{name: "platform admin has no implicit membership", actor: actor, state: team, denied: true},
		{name: "absent actor", state: team, membership: &member, denied: true},
		{name: "negative actor", actor: Actor{-7, true, false}, state: team, membership: &member, denied: true},
		{name: "disabled actor", actor: Actor{7, false, false}, state: team, membership: &member, denied: true},
		{name: "restricted actor", actor: Actor{7, true, true}, state: team, membership: &member, denied: true},
		{name: "disabled team", actor: actor, state: State{team.Account, false, true}, membership: &member, denied: true},
		{name: "invalid account", actor: actor, state: State{Ref{"root", 42}, true, true}, membership: &member, denied: true},
		{name: "other personal account", actor: actor, state: State{Ref{Personal, 42}, true, true}, denied: true},
		{name: "personal with team membership", actor: actor, state: State{Ref{Personal, 7}, true, true}, membership: &member, denied: true},
		{name: "suspended membership", actor: actor, state: team, membership: &Membership{team.Account, 7, Owner, false, true}, denied: true},
		{name: "other user membership", actor: actor, state: team, membership: &Membership{team.Account, 8, Owner, true, true}, denied: true},
		{name: "other team membership", actor: actor, state: team, membership: &Membership{Ref{Team, 43}, 7, Owner, true, true}, denied: true},
		{name: "same ID wrong kind", actor: actor, state: team, membership: &Membership{Ref{Personal, 42}, 7, Owner, true, true}, denied: true},
		{name: "root is not team role", actor: actor, state: team, membership: &Membership{team.Account, 7, "root", true, true}, denied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := ResolveScope(tc.actor, tc.state, tc.membership)
			if tc.denied {
				if !errors.Is(err, ErrAccessDenied) || s != (Scope{}) {
					t.Fatalf("partial grant on denial: %+v, %v", s, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.Account() != tc.state.Account || s.ActorID() != tc.actor.UserID || s.TeamRole() != tc.role || s.CanSpend() != tc.spend || s.DeveloperAccess() != tc.develop || s.CanManageTeam() != tc.manage {
				t.Fatalf("wrong scope: %+v", s)
			}
		})
	}
}

func TestTeamHierarchyAndRevocation(t *testing.T) {
	a := Actor{UserID: 7, Enabled: true}
	state := State{Ref{Team, 42}, true, true}
	for _, sourceRole := range []TeamRole{Owner, Admin, Member} {
		m := Membership{state.Account, 7, sourceRole, true, true}
		s, err := ResolveScope(a, state, &m)
		if err != nil {
			t.Fatal(err)
		}
		for _, targetRole := range []TeamRole{Owner, Admin, Member} {
			want := sourceRole == Owner && targetRole != Owner || sourceRole == Admin && targetRole == Member
			if s.CanManageMember(Membership{state.Account, 8, targetRole, true, true}) != want {
				t.Fatalf("hierarchy %s -> %s", sourceRole, targetRole)
			}
		}
		for _, target := range []Membership{
			{state.Account, 7, Member, true, true}, // self is a separate operation
			{state.Account, 0, Member, true, true},
			{Ref{Team, 43}, 8, Member, true, true},
			{Ref{Personal, 42}, 8, Member, true, true},
			{state.Account, 8, Member, false, true},
			{state.Account, 8, "root", true, true},
		} {
			if s.CanManageMember(target) {
				t.Fatalf("invalid target accepted: %+v", target)
			}
		}
		m.Active = false
		if next, err := ResolveScope(a, state, &m); err == nil || next != (Scope{}) {
			t.Fatal("revoked membership retained authority")
		}
		// Scope copies values, but is not usable as a durable authorization.
		if s.TeamRole() != sourceRole {
			t.Fatal("scope aliases mutable membership")
		}
	}
	var zero Scope
	if zero.CanSpend() || zero.DeveloperAccess() || zero.CanManageTeam() || zero.CanManageMember(Membership{Ref{Team, 42}, 8, Member, true, true}) {
		t.Fatal("zero scope granted access")
	}
}

func FuzzScopeIsolation(f *testing.F) {
	f.Add("team", int64(42), int64(7), int64(42), int64(7), "owner", true, true, true)
	f.Add("personal", int64(7), int64(7), int64(42), int64(7), "member", true, true, true)
	f.Fuzz(func(t *testing.T, kind string, id, actorID, teamID, memberID int64, role string, enabled, active, spend bool) {
		state := State{Ref{Kind(kind), id}, enabled, true}
		member := Membership{Ref{Team, teamID}, memberID, TeamRole(role), active, spend}
		s, err := ResolveScope(Actor{UserID: actorID, Enabled: true}, state, &member)
		if err != nil {
			if s != (Scope{}) {
				t.Fatal("partial scope on denial")
			}
			return
		}
		if actorID <= 0 || !enabled || !active || !state.Account.Valid() || kind != "team" || id != teamID || actorID != memberID || !member.Role.Valid() || s.CanSpend() != spend {
			t.Fatalf("cross-account authority: %+v", s)
		}
	})
}
