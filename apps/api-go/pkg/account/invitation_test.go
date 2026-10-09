package account

import "testing"

func TestNativeAccountInvitationRoles(t *testing.T) {
	for _, source := range []TeamRole{Owner, Admin, Member} {
		ref := Ref{Kind: Team, ID: 42}
		scope, err := ResolveScope(Actor{UserID: 7, Enabled: true}, State{Account: ref, Enabled: true}, &Membership{Account: ref, UserID: 7, Role: source, Active: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []TeamRole{Owner, Admin, Member, "root", "superadmin", "5", "6", "10", "100", ""} {
			want := source == Owner && (target == Admin || target == Member) || source == Admin && target == Member
			if got := scope.CanInviteRole(target); got != want {
				t.Fatalf("%s -> %s: got %v, want %v", source, target, got, want)
			}
		}
		if scope.CanSpend() {
			t.Fatal("invitation permission granted spending")
		}
	}
	personal, err := ResolveScope(Actor{UserID: 7, Enabled: true}, State{Account: Ref{Kind: Personal, ID: 7}, Enabled: true, DeveloperAccess: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []Scope{{}, personal} {
		for _, role := range []TeamRole{Owner, Admin, Member} {
			if scope.CanInviteRole(role) {
				t.Fatal("personal/zero scope acquired team permission")
			}
		}
	}
}

func FuzzNativeAccountInvitationScope(f *testing.F) {
	f.Add("owner", "admin", true)
	f.Add("admin", "owner", true)
	f.Add("member", "member", false)
	f.Fuzz(func(t *testing.T, source, target string, active bool) {
		ref := Ref{Kind: Team, ID: 42}
		scope, err := ResolveScope(Actor{UserID: 7, Enabled: true}, State{Account: ref, Enabled: true}, &Membership{Account: ref, UserID: 7, Role: TeamRole(source), Active: active})
		got := scope.CanInviteRole(TeamRole(target))
		want := err == nil && active && (source == "owner" && (target == "admin" || target == "member") || source == "admin" && target == "member")
		if got != want {
			t.Fatalf("unauthorized invitation: %q -> %q active=%v", source, target, active)
		}
	})
}
